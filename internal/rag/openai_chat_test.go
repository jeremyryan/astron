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

package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// temperatureSent reads a chat-completions request body, reporting whether the
// temperature field was present (nil pointer => omitted).
func temperatureSent(t *testing.T, r *http.Request) bool {
	t.Helper()
	var body struct {
		Temperature *float64 `json:"temperature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}
	return body.Temperature != nil
}

// TestOpenAIChatRetriesWithoutTemperature verifies that when a model rejects an
// explicit temperature, Complete transparently retries without it and returns
// the successful reply.
func TestOpenAIChatRetriesWithoutTemperature(t *testing.T) {
	var sentTemperature []bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hasTemp := temperatureSent(t, r)
		sentTemperature = append(sentTemperature, hasTemp)
		if hasTemp {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "Unsupported value: 'temperature' does not support 0 with this model. Only the default (1) value is supported.",
					"type":    "invalid_request_error",
					"param":   "temperature",
					"code":    "unsupported_value",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ok"}}},
		})
	}))
	defer ts.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "o3", BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}

	out, err := chat.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "ok" {
		t.Fatalf("Complete = %q, want ok", out)
	}
	if len(sentTemperature) != 2 || !sentTemperature[0] || sentTemperature[1] {
		t.Fatalf("expected [with-temp, without-temp] requests, got %v", sentTemperature)
	}

	// A subsequent call must skip temperature outright (no wasted retry),
	// having remembered the model's preference.
	sentTemperature = nil
	if _, err := chat.Complete(context.Background(), []Message{{Role: RoleUser, Content: "again"}}); err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	if len(sentTemperature) != 1 || sentTemperature[0] {
		t.Fatalf("expected a single without-temp request, got %v", sentTemperature)
	}
}

// TestOpenAIChatSendsTemperatureByDefault verifies the temperature is included
// on the happy path (models that accept it), and that non-temperature errors
// are not retried.
func TestOpenAIChatSendsTemperatureByDefault(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !temperatureSent(t, r) {
			t.Errorf("expected temperature to be sent")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "hi"}}},
		})
	}))
	defer ts.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "gpt-4o-mini", BaseURL: ts.URL, Temperature: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one request, got %d", calls)
	}
}

// TestOpenAIChatDoesNotRetryUnrelatedError verifies a non-temperature API error
// surfaces immediately without a second request.
func TestOpenAIChatDoesNotRetryUnrelatedError(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "invalid api key", "type": "invalid_request_error"},
		})
	}))
	defer ts.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "gpt-4o-mini", BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Fatalf("expected exactly one request (no retry), got %d", calls)
	}
}

// requestBody decodes a chat-completions request body into a generic map for
// inspecting wire-shape details tests care about.
func requestBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}
	return body
}

// toolNameSearchGraph is the tool name used across the tool-calling tests.
const toolNameSearchGraph = "search_cluster_graph"

// TestOpenAIChatCompleteWithToolsSendsToolSpecs verifies ToolSpecs are
// translated into the OpenAI "tools" request shape and that a response with
// tool_calls (no content) is parsed into Reply.ToolCalls.
func TestOpenAIChatCompleteWithToolsSendsToolSpecs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := requestBody(t, r)
		tools, _ := body["tools"].([]any)
		if len(tools) != 1 {
			t.Fatalf("expected 1 tool in request, got %+v", body["tools"])
		}
		fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
		if fn["name"] != toolNameSearchGraph {
			t.Errorf("tool name = %v, want %s", fn["name"], toolNameSearchGraph)
		}
		if fn["description"] != "search the graph" {
			t.Errorf("tool description = %v", fn["description"])
		}
		params, _ := fn["parameters"].(map[string]any)
		if params["type"] != "object" {
			t.Errorf("tool parameters = %+v, want type object", params)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": nil,
					"tool_calls": []map[string]any{{
						"id":   "call_1",
						"type": "function",
						"function": map[string]any{
							"name":      toolNameSearchGraph,
							"arguments": `{"query":"web pods"}`,
						},
					}},
				},
			}},
		})
	}))
	defer ts.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "gpt-4o-mini", BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}

	tools := []ToolSpec{{
		Name:        toolNameSearchGraph,
		Description: "search the graph",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": map[string]any{"type": "string"}},
		},
	}}
	reply, err := chat.CompleteWithTools(context.Background(), []Message{{Role: RoleUser, Content: "find web pods"}}, tools)
	if err != nil {
		t.Fatalf("CompleteWithTools: %v", err)
	}
	if reply.Content != "" {
		t.Errorf("Content = %q, want empty", reply.Content)
	}
	if len(reply.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v, want 1", reply.ToolCalls)
	}
	got := reply.ToolCalls[0]
	if got.ID != "call_1" || got.Name != toolNameSearchGraph {
		t.Errorf("unexpected tool call: %+v", got)
	}
	if string(got.Arguments) != `{"query":"web pods"}` {
		t.Errorf("Arguments = %s", got.Arguments)
	}
}

// TestOpenAIChatCompleteWithToolsFinalAnswer verifies a response with content
// and no tool_calls is parsed as a final Reply.
func TestOpenAIChatCompleteWithToolsFinalAnswer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "the answer"}}},
		})
	}))
	defer ts.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "gpt-4o-mini", BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := chat.CompleteWithTools(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("CompleteWithTools: %v", err)
	}
	if reply.Content != "the answer" || len(reply.ToolCalls) != 0 {
		t.Fatalf("unexpected reply: %+v", reply)
	}
}

// TestOpenAIChatCompleteWithToolsRoundTripsToolResult verifies an assistant
// tool-call message and its RoleTool result round-trip onto the wire in the
// shape the chat-completions API expects (tool_calls on the assistant
// message, tool_call_id on the tool result message).
func TestOpenAIChatCompleteWithToolsRoundTripsToolResult(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := requestBody(t, r)
		msgs, _ := body["messages"].([]any)
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d: %+v", len(msgs), msgs)
		}
		assistant, _ := msgs[1].(map[string]any)
		if assistant["role"] != "assistant" {
			t.Errorf("messages[1].role = %v, want assistant", assistant["role"])
		}
		toolCalls, _ := assistant["tool_calls"].([]any)
		if len(toolCalls) != 1 {
			t.Fatalf("expected 1 tool_calls on assistant message, got %+v", assistant["tool_calls"])
		}
		toolMsg, _ := msgs[2].(map[string]any)
		if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" || toolMsg["content"] != `{"pods":1}` {
			t.Errorf("unexpected tool result message: %+v", toolMsg)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "there is 1 pod"}}},
		})
	}))
	defer ts.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "gpt-4o-mini", BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}

	messages := []Message{
		{Role: RoleUser, Content: "how many pods?"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "query_graph", Arguments: json.RawMessage(`{}`)}}},
		{Role: RoleTool, ToolCallID: "call_1", Content: `{"pods":1}`},
	}
	reply, err := chat.CompleteWithTools(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("CompleteWithTools: %v", err)
	}
	if reply.Content != "there is 1 pod" {
		t.Fatalf("Content = %q", reply.Content)
	}
}

// TestOpenAIChatCompleteWithToolsRetriesWithoutTemperature verifies the
// temperature-rejection retry logic also applies to the tool-calling path.
func TestOpenAIChatCompleteWithToolsRetriesWithoutTemperature(t *testing.T) {
	var sentTemperature []bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hasTemp := temperatureSent(t, r)
		sentTemperature = append(sentTemperature, hasTemp)
		if hasTemp {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "Unsupported value: 'temperature' does not support 0 with this model.",
					"param":   "temperature",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}}},
		})
	}))
	defer ts.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "o3", BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := chat.CompleteWithTools(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("CompleteWithTools: %v", err)
	}
	if reply.Content != "ok" {
		t.Fatalf("Content = %q, want ok", reply.Content)
	}
	if len(sentTemperature) != 2 || !sentTemperature[0] || sentTemperature[1] {
		t.Fatalf("expected [with-temp, without-temp] requests, got %v", sentTemperature)
	}
}
