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
	"errors"
	"strings"
	"testing"

	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

func newQAProjector(store *retrievalStore, chat rag.Chat, embed bool) *Projector {
	opts := Options{ID: "proj-qa", Store: store}
	if chat != nil {
		opts.Chat = chat
		opts.QueryStore = store
	}
	if embed {
		opts.Embedder = rag.NewFakeEmbedder(8)
		opts.VectorStore = store
	}
	return New(opts)
}

func TestQueryRequiresChat(t *testing.T) {
	p := newQAProjector(&retrievalStore{data: sampleGraph()}, nil, false)
	if _, err := p.Query(context.Background(), "how many pods?"); !errors.Is(err, ErrChatNotEnabled) {
		t.Fatalf("expected ErrChatNotEnabled, got %v", err)
	}
}

func TestQueryGeneratesValidatesAndExecutes(t *testing.T) {
	store := &retrievalStore{
		data:      sampleGraph(),
		queryRows: []map[string]any{{"n": int64(3)}},
	}
	chat := rag.NewFakeChat("```cypher\nMATCH (p:Pod {_projection: $projection}) RETURN count(p) AS n\n```")
	p := newQAProjector(store, chat, false)

	res, err := p.Query(context.Background(), "how many pods?")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// The fenced reply must be unwrapped before execution.
	if strings.Contains(res.Cypher, "```") {
		t.Errorf("cypher not unwrapped: %q", res.Cypher)
	}
	if store.lastCypher != res.Cypher {
		t.Errorf("executed cypher %q != returned %q", store.lastCypher, res.Cypher)
	}
	if len(res.Rows) != 1 || res.Rows[0]["n"] != int64(3) {
		t.Errorf("unexpected rows: %+v", res.Rows)
	}
}

func TestQueryRejectsUnsafeGeneratedCypher(t *testing.T) {
	store := &retrievalStore{data: sampleGraph()}
	chat := rag.NewFakeChat("MATCH (p:Pod) DETACH DELETE p RETURN 1")
	p := newQAProjector(store, chat, false)

	_, err := p.Query(context.Background(), "delete the pods")
	if err == nil {
		t.Fatal("expected unsafe generated cypher to be rejected")
	}
	if store.lastCypher != "" {
		t.Errorf("unsafe cypher must not be executed, but store saw: %q", store.lastCypher)
	}
}

func TestAnswerRequiresChat(t *testing.T) {
	p := newQAProjector(&retrievalStore{data: sampleGraph()}, nil, true)
	if _, err := p.Answer(context.Background(), "why?", SearchOptions{}); !errors.Is(err, ErrChatNotEnabled) {
		t.Fatalf("expected ErrChatNotEnabled, got %v", err)
	}
}

func TestAnswerRetrievesAndSynthesizes(t *testing.T) {
	store := &retrievalStore{
		data: sampleGraph(),
		hits: []graph.VectorHit{hit("u-pod", 0.9)},
	}
	// Echoing fake returns the user prompt, which embeds the retrieved context.
	chat := rag.NewFakeChat("")
	p := newQAProjector(store, chat, true)

	res, err := p.Answer(context.Background(), "what owns the pod?", SearchOptions{TopK: 1, Hops: 1})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if res.Question != "what owns the pod?" {
		t.Errorf("question not echoed: %q", res.Question)
	}
	// The answer prompt (echoed back) should include retrieved cards and edges.
	if !strings.Contains(res.Answer, "Pod `web-1`") {
		t.Errorf("answer context missing pod card: %q", res.Answer)
	}
	if !strings.Contains(res.Answer, "OWNS") {
		t.Errorf("answer context missing relationship lines: %q", res.Answer)
	}
	// Retrieval context is attached for provenance.
	if len(res.Retrieval.Seeds) != 1 || res.Retrieval.Seeds[0].Ref.UID != "u-pod" {
		t.Errorf("unexpected retrieval seeds: %+v", res.Retrieval.Seeds)
	}
}

func TestAnswerRequiresEmbeddingForRetrieval(t *testing.T) {
	store := &retrievalStore{data: sampleGraph()}
	// chat enabled but embedding disabled: Search inside Answer should fail.
	p := newQAProjector(store, rag.NewFakeChat("x"), false)
	if _, err := p.Answer(context.Background(), "why?", SearchOptions{}); !errors.Is(err, ErrRAGNotEnabled) {
		t.Fatalf("expected ErrRAGNotEnabled, got %v", err)
	}
}
