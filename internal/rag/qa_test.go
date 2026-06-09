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
	"strings"
	"testing"

	"github.com/project-gamera/gamera/internal/graph"
)

func TestSchemaSummaryIsGroundedAndDeterministic(t *testing.T) {
	data := graph.GraphData{
		Nodes: []graph.Node{
			{Ref: graph.Ref{Kind: "Pod", Namespace: "shop", Name: "web-1", UID: "u-pod"}, Properties: map[string]any{"phase": "Running", "ready": "1/1"}},
			{Ref: graph.Ref{Kind: "Deployment", Namespace: "shop", Name: "web", UID: "u-dep"}},
		},
		Relationships: []graph.Relationship{
			{Type: "OWNS", From: graph.Ref{UID: "u-dep"}, To: graph.Ref{UID: "u-pod"}},
		},
	}
	got := SchemaSummary(data)

	for _, want := range []string{
		":Pod — phase, ready", // properties sorted, identity excluded
		":Deployment",
		"(:Deployment)-[:OWNS]->(:Pod)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("schema summary missing %q:\n%s", want, got)
		}
	}

	if SchemaSummary(data) != got {
		t.Error("schema summary is not deterministic")
	}
}

func TestCypherMessagesIncludeSchemaAndGuardrails(t *testing.T) {
	msgs := CypherMessages("SCHEMA-HERE", "how many pods?")
	if len(msgs) != 2 || msgs[0].Role != RoleSystem || msgs[1].Role != RoleUser {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "SCHEMA-HERE") {
		t.Error("system prompt should embed the schema")
	}
	if !strings.Contains(msgs[0].Content, "$projection") || !strings.Contains(msgs[0].Content, "read-only") {
		t.Error("system prompt should instruct projection scoping and read-only")
	}
	if !strings.Contains(msgs[1].Content, "how many pods?") {
		t.Error("user prompt should contain the question")
	}
}

func TestExtractCypher(t *testing.T) {
	cases := map[string]string{
		"MATCH (n) RETURN n":                      "MATCH (n) RETURN n",
		"```cypher\nMATCH (n) RETURN n\n```":      "MATCH (n) RETURN n",
		"```\nMATCH (n) RETURN n\n```":            "MATCH (n) RETURN n",
		"  ```cypher\nMATCH (n)\nRETURN n\n```  ": "MATCH (n)\nRETURN n",
		"```cypher\nMATCH (n) RETURN n":           "MATCH (n) RETURN n", // no closing fence
	}
	for in, want := range cases {
		if got := ExtractCypher(in); got != want {
			t.Errorf("ExtractCypher(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnswerMessagesIncludeContextAndCitationsInstruction(t *testing.T) {
	cards := []Card{{Ref: graph.Ref{Kind: "Pod", Name: "web-1"}, Text: "Pod web-1 is Running."}}
	msgs := AnswerMessages("why is it down?", cards, []string{"Deployment web OWNS Pod web-1"})
	if !strings.Contains(msgs[0].Content, "Cite") {
		t.Error("system prompt should ask for citations")
	}
	if !strings.Contains(msgs[1].Content, "Pod web-1 is Running.") {
		t.Error("user prompt should contain the card text")
	}
	if !strings.Contains(msgs[1].Content, "Deployment web OWNS Pod web-1") {
		t.Error("user prompt should contain the relationship lines")
	}
	if !strings.Contains(msgs[1].Content, "why is it down?") {
		t.Error("user prompt should contain the question")
	}
}

func TestFakeChat(t *testing.T) {
	echo := NewFakeChat("")
	got, err := echo.Complete(context.Background(), []Message{{Role: RoleSystem, Content: "sys"}, {Role: RoleUser, Content: "hello"}})
	if err != nil || got != "hello" {
		t.Fatalf("echo fake = %q, err=%v", got, err)
	}

	fixed := NewFakeChat("MATCH (n) RETURN n")
	if got, _ := fixed.Complete(context.Background(), nil); got != "MATCH (n) RETURN n" {
		t.Errorf("fixed fake = %q", got)
	}

	scripted := &FakeChat{ReplyFunc: func(m []Message) string { return "saw " + string(rune('0'+len(m))) }}
	if got, _ := scripted.Complete(context.Background(), []Message{{}, {}}); got != "saw 2" {
		t.Errorf("scripted fake = %q", got)
	}
}

func TestNewChatFactory(t *testing.T) {
	if _, err := NewChat(ChatConfig{}); err != nil {
		t.Errorf("empty config should default to fake: %v", err)
	}
	if _, err := NewChat(ChatConfig{Provider: ProviderOpenAI, Model: "gpt", APIKey: "k"}); err != nil {
		t.Errorf("openai chat: %v", err)
	}
	if _, err := NewChat(ChatConfig{Provider: ProviderOpenAI, Model: "gpt"}); err == nil {
		t.Error("openai chat without key should error")
	}
	if _, err := NewChat(ChatConfig{Provider: "bogus"}); err == nil {
		t.Error("unknown provider should error")
	}
}

func TestOpenAIChatHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing auth header")
		}
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Messages) == 0 {
			t.Error("expected messages in request")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "MATCH (n) RETURN n"}}},
		})
	}))
	defer srv.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "gpt", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewOpenAIChat: %v", err)
	}
	out, err := chat.Complete(context.Background(), []Message{{Role: RoleUser, Content: "q"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "MATCH (n) RETURN n" {
		t.Errorf("unexpected completion: %q", out)
	}
}

func TestOpenAIChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "rate limited"}})
	}))
	defer srv.Close()

	chat, _ := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "gpt", BaseURL: srv.URL})
	if _, err := chat.Complete(context.Background(), []Message{{Role: RoleUser, Content: "q"}}); err == nil {
		t.Fatal("expected an error for a 429 response")
	}
}
