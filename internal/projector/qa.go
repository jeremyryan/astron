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
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

// ErrModelNotAllowed indicates a per-request chat model override that the
// projection's allowedModels policy does not permit.
var ErrModelNotAllowed = errors.New("chat model is not allowed for this projection")

// chatModelsCacheTTL bounds how often the provider is asked to enumerate its
// models; the list changes rarely.
const chatModelsCacheTTL = 5 * time.Minute

// ChatModelList is the set of chat models a user may choose from for a
// projection, plus the configured default.
type ChatModelList struct {
	Default string   `json:"default"`
	Models  []string `json:"models"`
}

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

// allowsAnyModel reports whether the policy opts into every provider model.
func allowsAnyModel(allowed []string) bool { return slices.Contains(allowed, "*") }

// ChatModels returns the chat models a user may select for this projection.
// With an empty allowedModels policy only the configured model is offered;
// "*" offers everything the provider lists (cached briefly); otherwise the
// explicit allow-list (plus the configured model) is offered.
func (p *Projector) ChatModels(ctx context.Context) (ChatModelList, error) {
	if !p.chatEnabled() {
		return ChatModelList{}, ErrChatNotEnabled
	}
	def := p.opts.Chat.Model()
	allowed := p.opts.ChatSettings.AllowedModels

	var models []string
	switch {
	case len(allowed) == 0:
		models = []string{def}
	case allowsAnyModel(allowed):
		provided, err := p.providerModels(ctx)
		if err != nil {
			return ChatModelList{}, err
		}
		models = provided
	default:
		models = slices.Clone(allowed)
	}
	if !slices.Contains(models, def) {
		models = append(models, def)
	}
	sort.Strings(models)
	return ChatModelList{Default: def, Models: models}, nil
}

// providerModels enumerates the provider's models, caching the result briefly.
func (p *Projector) providerModels(ctx context.Context) ([]string, error) {
	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()
	if p.cachedModels != nil && time.Since(p.cachedModelsAt) < chatModelsCacheTTL {
		return slices.Clone(p.cachedModels), nil
	}
	models, err := rag.ListModels(ctx, p.opts.ChatSettings)
	if err != nil {
		return nil, fmt.Errorf("listing chat models: %w", err)
	}
	p.cachedModels = models
	p.cachedModelsAt = time.Now()
	return slices.Clone(models), nil
}

// chatFor resolves the Chat to use for a request: the projection's configured
// chat when model is empty (or names the default), otherwise a variant
// targeting the requested model — provided the allowedModels policy permits it
// and the backend supports model overrides. Under a "*" policy the model name
// is passed through and validated by the provider itself.
func (p *Projector) chatFor(model string) (rag.Chat, error) {
	chat := p.opts.Chat
	if model == "" || model == chat.Model() {
		return chat, nil
	}
	allowed := p.opts.ChatSettings.AllowedModels
	if !allowsAnyModel(allowed) && !slices.Contains(allowed, model) {
		return nil, fmt.Errorf("%w: %q", ErrModelNotAllowed, model)
	}
	selector, ok := chat.(rag.ModelSelector)
	if !ok {
		return nil, fmt.Errorf("%w: backend does not support model overrides", ErrModelNotAllowed)
	}
	return selector.WithModel(model), nil
}

// Query answers a natural-language question by generating a guarded, read-only
// Cypher statement from the projection's live schema, executing it, and
// returning the rows. It requires a chat model and a query-capable store.
// model optionally overrides the configured chat model (subject to the
// projection's allowedModels policy); empty uses the default.
func (p *Projector) Query(ctx context.Context, question, model string) (QueryResult, error) {
	if !p.chatEnabled() || p.opts.QueryStore == nil {
		return QueryResult{}, ErrChatNotEnabled
	}
	chat, err := p.chatFor(model)
	if err != nil {
		return QueryResult{}, err
	}

	data, err := p.opts.Store.ReadGraph(ctx, p.opts.ID)
	if err != nil {
		return QueryResult{}, fmt.Errorf("reading graph: %w", err)
	}
	schema := rag.SchemaSummary(data)

	reply, err := chat.Complete(ctx, rag.CypherMessages(schema, question))
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
// requires both embedding (for retrieval) and a chat model. model optionally
// overrides the configured chat model (subject to the projection's
// allowedModels policy); empty uses the default.
func (p *Projector) Answer(ctx context.Context, question, model string, opts SearchOptions) (AnswerResult, error) {
	if !p.chatEnabled() {
		return AnswerResult{}, ErrChatNotEnabled
	}
	chat, err := p.chatFor(model)
	if err != nil {
		return AnswerResult{}, err
	}

	retrieval, err := p.Search(ctx, question, opts)
	if err != nil {
		return AnswerResult{}, err
	}

	answer, err := chat.Complete(ctx, rag.AnswerMessages(question, retrieval.Cards, relationshipSentences(retrieval.Subgraph)))
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
