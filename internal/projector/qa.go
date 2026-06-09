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
	"fmt"

	"github.com/project-gamera/gamera/internal/graph"
	"github.com/project-gamera/gamera/internal/rag"
)

// QueryResult is the outcome of a text-to-Cypher query: the generated Cypher
// and the rows it produced.
type QueryResult struct {
	Question string           `json:"question"`
	Cypher   string           `json:"cypher"`
	Rows     []map[string]any `json:"rows"`
}

// AnswerResult is the outcome of natural-language question answering: the
// answer text plus the retrieval context that grounded it.
type AnswerResult struct {
	Question  string    `json:"question"`
	Answer    string    `json:"answer"`
	Retrieval Retrieval `json:"-"`
}

// chatEnabled reports whether natural-language answering is configured.
func (p *Projector) chatEnabled() bool { return p.opts.Chat != nil }

// Query answers a natural-language question by generating a guarded, read-only
// Cypher statement from the projection's live schema, executing it, and
// returning the rows. It requires a chat model and a query-capable store.
func (p *Projector) Query(ctx context.Context, question string) (QueryResult, error) {
	if !p.chatEnabled() || p.opts.QueryStore == nil {
		return QueryResult{}, ErrChatNotEnabled
	}

	data, err := p.opts.Store.ReadGraph(ctx, p.opts.ID)
	if err != nil {
		return QueryResult{}, fmt.Errorf("reading graph: %w", err)
	}
	schema := rag.SchemaSummary(data)

	reply, err := p.opts.Chat.Complete(ctx, rag.CypherMessages(schema, question))
	if err != nil {
		return QueryResult{}, fmt.Errorf("generating cypher: %w", err)
	}
	cypher := rag.ExtractCypher(reply)

	// Validate before execution so an obviously unsafe generation is reported
	// clearly (the store re-validates as defense-in-depth).
	if err := graph.ValidateReadOnlyCypher(cypher); err != nil {
		return QueryResult{Question: question, Cypher: cypher}, fmt.Errorf("generated query rejected: %w", err)
	}

	rows, err := p.opts.QueryStore.ReadOnlyQuery(ctx, p.opts.ID, cypher, nil)
	if err != nil {
		return QueryResult{Question: question, Cypher: cypher}, err
	}
	return QueryResult{Question: question, Cypher: cypher, Rows: rows}, nil
}

// Answer answers a natural-language question by retrieving relevant context
// (hybrid vector + graph) and asking the chat model to answer from it. It
// requires both embedding (for retrieval) and a chat model.
func (p *Projector) Answer(ctx context.Context, question string, opts SearchOptions) (AnswerResult, error) {
	if !p.chatEnabled() {
		return AnswerResult{}, ErrChatNotEnabled
	}

	retrieval, err := p.Search(ctx, question, opts)
	if err != nil {
		return AnswerResult{}, err
	}

	answer, err := p.opts.Chat.Complete(ctx, rag.AnswerMessages(question, retrieval.Cards, relationshipSentences(retrieval.Subgraph)))
	if err != nil {
		return AnswerResult{}, fmt.Errorf("generating answer: %w", err)
	}
	return AnswerResult{Question: question, Answer: answer, Retrieval: retrieval}, nil
}

// relationshipSentences renders a subgraph's edges as short readable lines for
// the answer prompt, e.g. "Deployment web OWNS Pod web-1".
func relationshipSentences(data graph.GraphData) []string {
	kindName := make(map[string]string, len(data.Nodes))
	for _, n := range data.Nodes {
		label := n.Ref.Kind + " " + n.Ref.Name
		kindName[n.Ref.ID()] = label
	}
	lines := make([]string, 0, len(data.Relationships))
	for _, r := range data.Relationships {
		from := kindName[r.From.ID()]
		to := kindName[r.To.ID()]
		if from == "" {
			from = r.From.Name
		}
		if to == "" {
			to = r.To.Name
		}
		lines = append(lines, fmt.Sprintf("%s %s %s", from, r.Type, to))
	}
	return lines
}
