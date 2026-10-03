package mcpserver

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"neohome/internal/core"
	"neohome/internal/shell"
)

const (
	protocolVersion = "2025-11-25"
	maxRequestBytes = 1 << 20
	maxOutputBytes  = 64 << 10
	maxCommandBytes = 4096
)

var supportedVersions = map[string]bool{
	protocolVersion: true,
	"2025-06-18":    true,
	"2025-03-26":    true,
	"2024-11-05":    true,
}

type Server struct {
	world  *core.World
	device *core.Device
	user   *core.User
	token  string
	origin string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type boundedOutput struct {
	buf       bytes.Buffer
	remaining int
	truncated bool
}

func New(w *core.World, token, allowedOrigin string) (*Server, error) {
	if w == nil {
		return nil, errors.New("MCP world must not be nil")
	}
	if err := validateBearerToken(token); err != nil {
		return nil, err
	}
	w.Lock()
	d, u, err := w.EnsureMCPPlayer()
	w.Unlock()
	if err != nil {
		return nil, fmt.Errorf("provision MCP character: %w", err)
	}
	return &Server{world: w, device: d, user: u, token: token, origin: allowedOrigin}, nil
}

func validateBearerToken(token string) error {
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("MCP bearer token must contain at least 32 characters and no whitespace")
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "MCP endpoint accepts POST only", http.StatusMethodNotAllowed)
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && (s.origin == "" || origin != s.origin) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if !acceptsMCPJSON(r.Header.Get("Accept")) {
		http.Error(w, "Accept must include application/json and text/event-stream", http.StatusNotAcceptable)
		return
	}
	if !s.authorized(r.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", `Bearer`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(data, &req); err != nil {
		s.writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "Parse error"}})
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" || (len(req.ID) > 0 && !validRequestID(req.ID)) {
		s.writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32600, Message: "Invalid Request"}})
		return
	}
	if len(req.ID) == 0 {
		if req.Method == "notifications/initialized" || strings.HasPrefix(req.Method, "notifications/") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rpcErr := s.handle(req)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
	s.writeRPC(w, resp)
}

func validRequestID(id json.RawMessage) bool {
	var stringID string
	if json.Unmarshal(id, &stringID) == nil {
		return true
	}
	var numberID json.Number
	decoder := json.NewDecoder(bytes.NewReader(id))
	decoder.UseNumber()
	if decoder.Decode(&numberID) == nil {
		return true
	}
	return false
}

func (s *Server) authorized(header string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := strings.TrimPrefix(header, prefix)
	return len(got) == len(s.token) && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func acceptsMCPJSON(header string) bool {
	var jsonOK, eventStreamOK bool
	for _, item := range strings.Split(header, ",") {
		mediaType := strings.TrimSpace(strings.SplitN(item, ";", 2)[0])
		switch mediaType {
		case "application/json", "*/*":
			jsonOK = true
		case "text/event-stream":
			eventStreamOK = true
		}
	}
	return jsonOK && eventStreamOK
}

func (s *Server) writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// The client may have disconnected. There is no useful recovery response.
		return
	}
}

func (s *Server) handle(req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || !supportedVersions[params.ProtocolVersion] {
			return nil, &rpcError{Code: -32602, Message: "unsupported or missing protocolVersion"}
		}
		return map[string]any{
			"protocolVersion": params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]string{"name": "neohome", "version": "0.1.0"},
			"instructions":    "You are the unprivileged mcp-agent character. All actions run only in NeoHome's simulated world. Do not assume host commands execute.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": []any{
			map[string]any{
				"name":        "world_status",
				"description": "Inspect the simulated world clock and your own simulated device. Does not expose credentials or other players' private files.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
				"annotations": map[string]bool{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false},
			},
			map[string]any{
				"name":        "run_shell",
				"description": "Run one command as the unprivileged mcp-agent user on its simulated NeoHome device. Commands affect persistent game state. No host executable or host shell is invoked.",
				"annotations": map[string]bool{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": true},
				"inputSchema": map[string]any{
					"type":                 "object",
					"properties":           map[string]any{"command": map[string]any{"type": "string", "maxLength": maxCommandBytes, "description": "A NeoHome virtual shell command, not a host command."}},
					"required":             []string{"command"},
					"additionalProperties": false,
				},
			},
		}}, nil
	case "tools/call":
		var call toolCall
		if err := json.Unmarshal(req.Params, &call); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid tools/call params"}
		}
		return s.callTool(call)
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func (s *Server) callTool(call toolCall) (any, *rpcError) {
	switch call.Name {
	case "world_status":
		s.world.Lock()
		status := fmt.Sprintf("sim_time=%s\ntick=%d\ndevice=%s\nhostname=%s\nprofile=%s\npowered=%t\nnetwork_up=%t\n",
			s.world.Sim.Format(time.RFC3339), s.world.TickCount, s.device.ID, s.device.Hostname,
			s.device.Profile, s.device.Powered(), s.device.NetUp)
		s.world.Unlock()
		return toolResult(status, false), nil
	case "run_shell":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.Command) == "" || len(args.Command) > maxCommandBytes {
			return toolResult("command must be non-empty and no longer than 4096 bytes", true), nil
		}
		s.world.Lock()
		out := &boundedOutput{remaining: maxOutputBytes}
		sh := shell.NewShell(s.world, s.device, s.user, out, "10.77.1.41", "mcp")
		exitCode := sh.ExecLineStatus(args.Command)
		s.device.Logf("info", "mcp", "mcp-agent executed command (status %d): %s", exitCode, args.Command)
		s.world.AddEvent(s.device.ID, "info", "mcp", "mcp-agent executed command (status %d): %s", exitCode, args.Command)
		s.world.Unlock()
		text := out.buf.String()
		if out.truncated {
			text += "\n[output truncated at 65536 bytes]"
		}
		return toolResult(text, exitCode != 0), nil
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown tool"}
	}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []any{map[string]string{"type": "text", "text": text}},
		"isError": isError,
	}
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.remaining == 0 {
		b.truncated = b.truncated || len(p) > 0
		return len(p), nil
	}
	n := len(p)
	if n > b.remaining {
		n = b.remaining
		b.truncated = true
	}
	_, _ = b.buf.Write(p[:n])
	b.remaining -= n
	return len(p), nil
}
