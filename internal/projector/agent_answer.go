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

	"github.com/project-astron/astron/internal/agent"
	"github.com/project-astron/astron/internal/rag"
)

// AnswerWithTools answers question using a bounded, tool-using agent loop
// (see internal/agent) that can call this projection's own retrieval
// capabilities (search, neighborhood, guarded Cypher, schema, live resource
// reads) as it works out the answer, rather than grounding on a single
// pre-baked retrieval like Answer does.
//
// history carries the prior conversation turns, if any, so follow-up
// questions have context. model optionally overrides the configured chat
// model, exactly as for Answer and Query (subject to the same
// allowedModels policy and controller-wide provider routing via chatFor).
//
// When the resolved chat backend does not support tool calling (it doesn't
// implement rag.ToolCaller — e.g. a plain fake chat in tests, or a backend
// that lacks function-calling support), AnswerWithTools falls back to the
// fixed Answer pipeline rather than failing the request; the returned
// Result.Agentic reports which path was taken.
func (p *Projector) AnswerWithTools(
	ctx context.Context, question, model string, history []rag.Message, opts SearchOptions,
) (agent.Result, error) {
	if !p.chatEnabled() {
		return agent.Result{}, ErrChatNotEnabled
	}
	chat, err := p.chatFor(model)
	if err != nil {
		return agent.Result{}, err
	}

	caller, ok := chat.(rag.ToolCaller)
	if !ok {
		res, err := p.Answer(ctx, question, model, opts)
		if err != nil {
			return agent.Result{}, err
		}
		return agent.Result{Answer: res.Answer}, nil
	}

	runner := agent.NewRunner(caller, p.toolSet(model), agent.RunnerOptions{})
	return runner.Run(ctx, question, history)
}
