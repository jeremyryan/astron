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

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServer returns an MCP server wired to a fake Gamera API plus a handle
// to the requests it received.
func newTestServer(t *testing.T) (*Server, *[]string) {
	t.Helper()
	var seen []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/projections":
			_, _ = w.Write([]byte(`[{"uid":"u1","namespace":"gamera","name":"default","nodeCount":3}]`))
		case strings.HasSuffix(r.URL.Path, "/rag/search"):
			_, _ = w.Write([]byte(`{"query":"web","seeds":[{"id":"u-pod","kind":"Pod","name":"web","score":0.9}]}`))
		case strings.HasSuffix(r.URL.Path, "/rag/neighborhood"):
			_, _ = w.Write([]byte(`{"query":"Pod/web","subgraph":{"nodes":[],"edges":[]}}`))
		case strings.HasSuffix(r.URL.Path, "/rag/query"):
			_, _ = w.Write([]byte(`{"question":"how many pods?","cypher":"MATCH (p:Pod) RETURN count(p)","rows":[{"count(p)":3}]}`))
		case strings.HasSuffix(r.URL.Path, "/rag/answer"):
			_, _ = w.Write([]byte(`{"question":"why?","answer":"Because the ConfigMap is missing.","retrieval":{"seeds":[]}}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(api.Close)
	return NewServer(NewAPIClient(api.URL, api.Client())), &seen
}

// run feeds the given newline-delimited requests through Serve and returns the
// decoded response objects (one per line of output).
func run(t *testing.T, s *Server, requests ...string) []rpcResponse {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var resps []rpcResponse
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("decoding response %q: %v", line, err)
		}
		resps = append(resps, r)
	}
	return resps
}

// TestServeReturnsOnContextCancelWhileBlockedOnRead verifies that Serve reacts
// to context cancellation (e.g. Ctrl+C) even while blocked waiting for the next
// request, rather than hanging until more input or EOF arrives.
func TestServeReturnsOnContextCancelWhileBlockedOnRead(t *testing.T) {
	s, _ := newTestServer(t)

	// A pipe whose writer never writes: the scanner blocks indefinitely on read.
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, pr, &out) }()

	// Let Serve reach the blocking read, then cancel as Ctrl+C would.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return promptly after context cancellation")
	}
}

// resultText extracts the first text content from a tools/call result.
func resultText(t *testing.T, r rpcResponse) (string, bool) {
	t.Helper()
	b, err := json.Marshal(r.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	return res.Content[0].Text, res.IsError
}

func TestInitialize(t *testing.T) {
	s, _ := newTestServer(t)
	resps := run(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	b, _ := json.Marshal(resps[0].Result)
	if !strings.Contains(string(b), protocolVersion) || !strings.Contains(string(b), serverName) {
		t.Errorf("unexpected initialize result: %s", b)
	}
}

func TestNotificationProducesNoResponse(t *testing.T) {
	s, _ := newTestServer(t)
	resps := run(t, s, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if len(resps) != 0 {
		t.Fatalf("notifications must not be answered, got %d responses", len(resps))
	}
}

func TestToolsListAdvertisesAllTools(t *testing.T) {
	s, _ := newTestServer(t)
	resps := run(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	b, _ := json.Marshal(resps[0].Result)
	for _, name := range []string{"list_projections", "search_cluster_graph", "get_resource_neighborhood", "get_resource_yaml", "answer_question", "query_cluster"} {
		if !strings.Contains(string(b), `"`+name+`"`) {
			t.Errorf("tools/list missing %q:\n%s", name, b)
		}
	}
}

func TestToolCallQueryAndAnswer(t *testing.T) {
	s, seen := newTestServer(t)

	qReq := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"query_cluster","arguments":{"projectionNamespace":"gamera","projectionName":"default","question":"how many pods?"}}}`
	aReq := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"answer_question","arguments":{"projectionNamespace":"gamera","projectionName":"default","question":"why?"}}}`
	resps := run(t, s, qReq, aReq)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}

	qText, qErr := resultText(t, resps[0])
	if qErr || !strings.Contains(qText, `"cypher"`) {
		t.Errorf("unexpected query_cluster result: %s", qText)
	}
	aText, aErr := resultText(t, resps[1])
	if aErr || !strings.Contains(aText, "ConfigMap is missing") {
		t.Errorf("unexpected answer_question result: %s", aText)
	}

	var sawQuery, sawAnswer bool
	for _, path := range *seen {
		if strings.HasSuffix(path, "/rag/query") {
			sawQuery = true
		}
		if strings.HasSuffix(path, "/rag/answer") {
			sawAnswer = true
		}
	}
	if !sawQuery || !sawAnswer {
		t.Errorf("expected query and answer endpoints to be called, saw: %v", *seen)
	}
}

func TestToolCallSearch(t *testing.T) {
	s, seen := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_cluster_graph","arguments":{"projectionNamespace":"gamera","projectionName":"default","query":"web"}}}`
	resps := run(t, s, req)
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("unexpected response: %+v", resps)
	}
	text, isErr := resultText(t, resps[0])
	if isErr {
		t.Fatalf("tool reported error: %s", text)
	}
	if !strings.Contains(text, `"seeds"`) {
		t.Errorf("expected search result JSON, got: %s", text)
	}
	found := false
	for _, s := range *seen {
		if strings.HasSuffix(s, "/rag/search") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the API search endpoint to be called, saw: %v", *seen)
	}
}

func TestToolCallMissingRequiredArgIsToolError(t *testing.T) {
	s, _ := newTestServer(t)
	// Missing query: the handler should return a tool error (isError=true), not
	// a protocol error.
	req := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_cluster_graph","arguments":{"projectionNamespace":"gamera","projectionName":"default"}}}`
	resps := run(t, s, req)
	if resps[0].Error != nil {
		t.Fatalf("expected a tool result, got protocol error: %+v", resps[0].Error)
	}
	text, isErr := resultText(t, resps[0])
	if !isErr || !strings.Contains(text, "required") {
		t.Errorf("expected isError with a 'required' message, got isError=%v text=%q", isErr, text)
	}
}

func TestUnknownToolIsToolError(t *testing.T) {
	s, _ := newTestServer(t)
	req := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`
	resps := run(t, s, req)
	text, isErr := resultText(t, resps[0])
	if !isErr || !strings.Contains(text, "unknown tool") {
		t.Errorf("expected unknown-tool error result, got isError=%v text=%q", isErr, text)
	}
}

func TestUnknownMethodIsProtocolError(t *testing.T) {
	s, _ := newTestServer(t)
	resps := run(t, s, `{"jsonrpc":"2.0","id":6,"method":"frobnicate"}`)
	if resps[0].Error == nil || resps[0].Error.Code != codeMethodNotFnd {
		t.Errorf("expected method-not-found error, got %+v", resps[0])
	}
}

func TestParseErrorOnGarbageLine(t *testing.T) {
	s, _ := newTestServer(t)
	resps := run(t, s, `{not valid json`)
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != codeParseError {
		t.Fatalf("expected a parse error response, got %+v", resps)
	}
}

func TestMultipleRequestsInSequence(t *testing.T) {
	s, _ := newTestServer(t)
	resps := run(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_projections","arguments":{}}}`,
	)
	// initialize + tools/call answered; the notification is not.
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}
	text, isErr := resultText(t, resps[1])
	if isErr || !strings.Contains(text, "nodeCount") {
		t.Errorf("unexpected list_projections result: %s", text)
	}
}
