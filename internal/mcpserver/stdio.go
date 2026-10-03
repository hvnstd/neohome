package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RunStdio exposes a running Streamable HTTP MCP server over stdio. This keeps
// stdio clients attached to the same authoritative world instead of forking a
// second copy of the simulation.
func RunStdio(in io.Reader, out io.Writer, endpoint, token string) error {
	if endpoint == "" || token == "" {
		return fmt.Errorf("NEOHOME_MCP_URL and NEOHOME_MCP_TOKEN are required for stdio mode")
	}
	if err := validateBearerToken(token); err != nil {
		return err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), maxRequestBytes)
	for scanner.Scan() {
		line := bytes.Clone(scanner.Bytes())
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			return fmt.Errorf("invalid stdio JSON-RPC message: %w", err)
		}
		httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(line))
		if err != nil {
			return fmt.Errorf("create MCP HTTP request: %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := client.Do(httpReq)
		if err != nil {
			return fmt.Errorf("forward MCP request: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxRequestBytes))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read MCP response: %w", readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close MCP response: %w", closeErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("MCP HTTP server returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		if len(req.ID) > 0 {
			if len(body) == 0 {
				return fmt.Errorf("MCP HTTP server returned an empty response for request %s", req.Method)
			}
			if _, err := out.Write(append(bytes.TrimSpace(body), '\n')); err != nil {
				return fmt.Errorf("write stdio MCP response: %w", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdio MCP input: %w", err)
	}
	return nil
}
