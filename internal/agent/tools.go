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

package agent

import (
	"context"
	"encoding/json"

	"github.com/project-astron/astron/internal/rag"
)

// Tool is one invocable capability offered to the model: its advertised
// rag.ToolSpec plus a handler that executes it and returns a textual (JSON)
// observation. Invoke is expected to be read-only and should return an error
// only for genuine failures — the Runner turns those into an observation
// telling the model what went wrong, rather than aborting the request, so the
// model has a chance to recover (e.g. try different arguments).
type Tool struct {
	Spec   rag.ToolSpec
	Invoke func(ctx context.Context, args json.RawMessage) (string, error)
}

// ToolSet is the set of tools offered to the model for one agent run, keyed
// by their advertised name (Spec.Name).
type ToolSet []Tool

// Specs returns the rag.ToolSpecs to advertise to the model, in ToolSet order.
func (ts ToolSet) Specs() []rag.ToolSpec {
	specs := make([]rag.ToolSpec, len(ts))
	for i, t := range ts {
		specs[i] = t.Spec
	}
	return specs
}

// find returns the Tool registered under name, if any.
func (ts ToolSet) find(name string) (Tool, bool) {
	for _, t := range ts {
		if t.Spec.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}
