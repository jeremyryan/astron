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

package projector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/project-astron/astron/internal/agent"
	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

// TestAnswerWithToolsRequiresChat verifies AnswerWithTools reports
// ErrChatNotEnabled exactly like Answer/Query when no chat is configured.
func TestAnswerWithToolsRequiresChat(t *testing.T) {
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, false)
	if _, err := p.AnswerWithTools(context.Background(), "why?", "", nil, SearchOptions{}); !errors.Is(err, ErrChatNotEnabled) {
		t.Fatalf("expected ErrChatNotEnabled, got %v", err)
	}
}

// TestAnswerWithToolsFallsBackWithoutToolCaller verifies a chat backend that
// doesn't implement rag.ToolCaller (the plain, unscripted FakeChat used
// elsewhere in this package's tests) falls back to the fixed Answer pipeline,
// and Result.Agentic reports that.
func TestAnswerWithToolsFallsBackWithoutToolCaller(t *testing.T) {
	// A bare, un-embedded chat that satisfies rag.Chat but not rag.ToolCaller
	// by construction: wrap FakeChat so the type assertion fails.
	store := &retrievalStore{data: sampleGraph(), hits: []graph.VectorHit{hit(uidPod, 0.9)}}
	// Echo mode (empty Reply): Answer's grounded prompt is reflected back,
	// letting the assertion below confirm the fixed pipeline actually ran.
	chat := &chatOnly{rag.NewFakeChat("")}
	p := New(Options{
		ID: "proj-fallback", Store: store, QueryStore: store,
		Chat: chat, Embedder: rag.NewFakeEmbedder(8), VectorStore: store,
	})

	res, err := p.AnswerWithTools(context.Background(), "what owns the pod?", "", nil, SearchOptions{TopK: 1})
	if err != nil {
		t.Fatalf("AnswerWithTools: %v", err)
	}
	if res.Agentic {
		t.Fatal("expected the fallback (non-agentic) path")
	}
	if !strings.Contains(res.Answer, "Pod `web-1`") {
		t.Errorf("expected the fallback Answer pipeline's grounded output, got %q", res.Answer)
	}
	if len(res.Steps) != 0 {
		t.Errorf("fallback path should report no tool steps, got %+v", res.Steps)
	}
}

// chatOnly adapts a rag.Chat to satisfy only rag.Chat, deliberately hiding any
// rag.ToolCaller the embedded value might implement, so tests can exercise
// AnswerWithTools' fallback path.
type chatOnly struct{ rag.Chat }

// TestAnswerWithToolsRunsAgentLoop verifies a rag.ToolCaller-capable chat
// drives the tool-using loop: a tool call is executed against this
// projection's real Search, and the final answer comes from the model.
func TestAnswerWithToolsRunsAgentLoop(t *testing.T) {
	store := &retrievalStore{data: sampleGraph(), hits: []graph.VectorHit{hit(uidPod, 0.9)}}
	chat := &rag.FakeChat{
		ToolScript: []rag.Reply{
			{ToolCalls: []rag.ToolCall{{
				ID: "call_1", Name: agent.ToolSearchClusterGraph,
				Arguments: json.RawMessage(`{"query":"web pod"}`),
			}}},
			{Content: "the web pod is owned by the web Deployment"},
		},
	}
	p := New(Options{
		ID: "proj-agent", Store: store, QueryStore: store,
		Chat: chat, Embedder: rag.NewFakeEmbedder(8), VectorStore: store,
	})

	res, err := p.AnswerWithTools(context.Background(), "what owns the web pod?", "", nil, SearchOptions{})
	if err != nil {
		t.Fatalf("AnswerWithTools: %v", err)
	}
	if !res.Agentic {
		t.Fatal("expected the tool-using (agentic) path")
	}
	if res.Answer != "the web pod is owned by the web Deployment" {
		t.Fatalf("Answer = %q", res.Answer)
	}
	if len(res.Steps) != 1 || res.Steps[0].Tool != agent.ToolSearchClusterGraph {
		t.Fatalf("unexpected steps: %+v", res.Steps)
	}
}

// TestAnswerWithToolsRoutesToProviderChat verifies model overrides route to a
// controller-wide provider chat exactly as chatFor already does for
// Answer/Query, and that provider chat drives the agent loop too.
func TestAnswerWithToolsRoutesToProviderChat(t *testing.T) {
	store := &retrievalStore{data: sampleGraph()}
	provider := &rag.FakeChat{Reply: "provider answer", ModelName: "fake"}
	p := New(Options{
		ID: "proj-provider", Store: store, QueryStore: store,
		ProviderChats: map[string]rag.Chat{"fake": provider},
	})

	res, err := p.AnswerWithTools(context.Background(), "why?", "fake", nil, SearchOptions{})
	if err != nil {
		t.Fatalf("AnswerWithTools: %v", err)
	}
	if !res.Agentic || res.Answer != "provider answer" {
		t.Fatalf("unexpected result: %+v", res)
	}
}
