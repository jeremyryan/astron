/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// runMCP executes the root command's mcp-server subcommand with the given stdin,
// returning stdout (the JSON-RPC stream) and stderr.
func runMCP(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"mcp-server"}, args...))
	err := cmd.Execute()
	return out.String(), errb.String(), err
}

func TestMCPServerInitializeOverStdio(t *testing.T) {
	// A single initialize request; EOF then ends the stdio session cleanly.
	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	out, errOut, err := runMCP(t, req)
	if err != nil {
		t.Fatalf("mcp-server failed: %v", err)
	}
	if !strings.Contains(errOut, "serving over stdio") {
		t.Errorf("expected startup log on stderr, got: %q", errOut)
	}
	// stdout must carry a valid JSON-RPC response (and nothing else).
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	line := strings.TrimSpace(out)
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("stdout is not a JSON-RPC response: %v\n%s", err, out)
	}
	if resp.JSONRPC != "2.0" || resp.Error != nil || resp.Result == nil {
		t.Fatalf("unexpected initialize response: %s", line)
	}
}

func TestMCPServerEmptyStdinExitsCleanly(t *testing.T) {
	out, _, err := runMCP(t, "")
	if err != nil {
		t.Fatalf("expected clean exit on EOF, got: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no protocol output for empty stdin, got: %q", out)
	}
}

func TestMCPServerAPIBaseURLFlag(t *testing.T) {
	// --api-base-url is reflected in the startup log (and used for the client).
	_, errOut, err := runMCP(t, "", "--api-base-url", "http://custom:1234")
	if err != nil {
		t.Fatalf("mcp-server failed: %v", err)
	}
	if !strings.Contains(errOut, "API at http://custom:1234") {
		t.Errorf("expected --api-base-url to be used, got: %q", errOut)
	}
}

func TestResolveMCPBaseURL(t *testing.T) {
	// Explicit --server wins over the env var.
	t.Setenv("GAMERA_API_URL", "http://env:9999")
	if got := resolveMCPBaseURL(&options{server: "http://explicit:1234"}); got != "http://explicit:1234" {
		t.Errorf("explicit server should win, got %q", got)
	}
	// Default --server falls back to the env var.
	if got := resolveMCPBaseURL(&options{server: defaultServer}); got != "http://env:9999" {
		t.Errorf("expected env fallback, got %q", got)
	}
	// Default --server with no env var keeps the default.
	t.Setenv("GAMERA_API_URL", "")
	if got := resolveMCPBaseURL(&options{server: defaultServer}); got != defaultServer {
		t.Errorf("expected default, got %q", got)
	}
}
