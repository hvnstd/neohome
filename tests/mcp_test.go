package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"neohome/internal/core"
	"neohome/internal/mcpserver"
)

const testMCPToken = "test-token-with-more-than-thirty-two-characters"

type mcpTestResponse struct {
	Result map[string]json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func postMCP(t *testing.T, handler http.Handler, token string, message any, origin string) (*httptest.ResponseRecorder, mcpTestResponse) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var response mcpTestResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return rec, response
}

func TestMCPToolsOperateOnUnprivilegedPersistentSimulatedCharacter(t *testing.T) {
	w := core.NewWorld()
	server, err := mcpserver.New(w, testMCPToken, "")
	if err != nil {
		t.Fatal(err)
	}
	player := w.Players["mcp-agent"]
	if player == nil || !player.MCPOnly {
		t.Fatal("MCP character should exist without a password-based login")
	}
	d := w.Devices[player.PC]
	if d == nil || d.Owner != "mcp-agent" || d.FindUser("mcp-agent").UID == 0 {
		t.Fatal("MCP character must use its own unprivileged device account")
	}
	if main, assistant := w.WalletBalance("mcp-agent"); main != 0 || assistant != 0 {
		t.Fatalf("MCP character unexpectedly shares household funds: main=%d assistant=%d", main, assistant)
	}

	_, init := postMCP(t, server.Handler(), testMCPToken, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-11-25"},
	}, "")
	if init.Error != nil {
		t.Fatalf("initialize failed: %+v", init.Error)
	}
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(init.Result["protocolVersion"], &initResult.ProtocolVersion); err != nil || initResult.ProtocolVersion != "2025-11-25" {
		t.Fatalf("unexpected initialize result: %s", init.Result["protocolVersion"])
	}

	_, wrote := postMCP(t, server.Handler(), testMCPToken, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "run_shell", "arguments": map[string]string{
			"command": "echo mcp-world-state > /home/mcp-agent/proof.txt",
		}},
	}, "")
	if wrote.Error != nil {
		t.Fatalf("run_shell protocol error: %+v", wrote.Error)
	}
	if got, ok := d.FS.Read("/home/mcp-agent/proof.txt"); !ok || string(got) != "mcp-world-state\n" {
		t.Fatalf("run_shell did not change simulated filesystem state: %q, exists=%t", got, ok)
	}
	if len(w.Events) == 0 || !strings.Contains(w.Events[len(w.Events)-1].Message, "echo mcp-world-state") {
		t.Fatal("MCP shell command was not recorded as a world event")
	}

	_, denied := postMCP(t, server.Handler(), testMCPToken, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "run_shell", "arguments": map[string]string{
			"command": "touch /etc/mcp-forbidden",
		}},
	}, "")
	var deniedResult struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(denied.Result["isError"], &deniedResult.IsError); err != nil || !deniedResult.IsError {
		t.Fatalf("run_shell should report permission errors: %s", denied.Result["isError"])
	}
	if d.FS.Exists("/etc/mcp-forbidden") {
		t.Fatal("unprivileged MCP command changed a protected file")
	}

	protectedCommands := []string{
		"mkdir /etc/mcp-dir",
		"rm /etc/shadow",
		"chmod 0777 /etc/shadow",
		"chown mcp-agent /etc/shadow",
		"ln -s /etc/shadow /etc/mcp-link",
		"mv /etc/hostname /home/mcp-agent/hostname",
		"set HISTFILE=/etc/shadow; history -w",
	}
	for _, command := range protectedCommands {
		_, result := postMCP(t, server.Handler(), testMCPToken, map[string]any{
			"jsonrpc": "2.0", "id": 4, "method": "tools/call",
			"params": map[string]any{"name": "run_shell", "arguments": map[string]string{"command": command}},
		}, "")
		var tool struct {
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(result.Result["isError"], &tool.IsError); err != nil || !tool.IsError {
			t.Errorf("%q should fail as the unprivileged MCP account: %s", command, result.Result["isError"])
		}
	}
	if !d.FS.Exists("/etc/shadow") || d.FS.Exists("/etc/mcp-dir") || d.FS.Exists("/etc/mcp-link") {
		t.Fatal("an unprivileged MCP command changed protected filesystem state")
	}
	shadow, _ := d.FS.Get("/etc/shadow")
	if shadow.Owner != "root" || shadow.Mode.Perm() != 0640 {
		t.Fatal("an unprivileged MCP command changed shadow ownership or permissions")
	}

	worldFile := filepath.Join(t.TempDir(), "mcp-world.gob")
	if err := w.Save(worldFile); err != nil {
		t.Fatal(err)
	}
	reloaded, err := core.LoadWorld(worldFile)
	if err != nil {
		t.Fatal(err)
	}
	devicesBefore := len(reloaded.Devices)
	if _, err := mcpserver.New(reloaded, testMCPToken, ""); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Devices) != devicesBefore || !reloaded.Players["mcp-agent"].MCPOnly {
		t.Fatal("MCP character did not persist or was duplicated on reconnect")
	}
}

func TestMCPHTTPRequiresBearerTokenAndRejectsUnapprovedOrigin(t *testing.T) {
	server, err := mcpserver.New(core.NewWorld(), testMCPToken, "")
	if err != nil {
		t.Fatal(err)
	}
	message := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}

	rec, _ := postMCP(t, server.Handler(), "", message, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer token status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	rec, _ = postMCP(t, server.Handler(), testMCPToken, message, "https://attacker.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unapproved Origin status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestMCPStdioBridgeUsesSameHTTPWorld(t *testing.T) {
	server, err := mcpserver.New(core.NewWorld(), testMCPToken, "")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := mcpserver.RunStdio(strings.NewReader(input), &output, httpServer.URL, testMCPToken); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"protocolVersion":"2025-11-25"`) {
		t.Fatalf("stdio bridge returned unexpected protocol output: %q", output.String())
	}
}
