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

package api

import (
	"encoding/json"
	"testing"

	"github.com/project-astron/astron/internal/agent"
)

func TestAgentResultToDTO(t *testing.T) {
	res := agent.Result{
		Answer: "the answer",
		Steps: []agent.Step{
			{Tool: "search_cluster_graph", Args: json.RawMessage(`{"query":"web"}`), Summary: "search_cluster_graph: 2 hits"},
		},
		Agentic:             true,
		StepBudgetExhausted: false,
	}

	got := agentResultToDTO("why?", res)
	if got.Question != "why?" || got.Answer != "the answer" || !got.Agentic || got.StepBudgetExhausted {
		t.Fatalf("unexpected DTO: %+v", got)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("Steps = %+v, want 1", got.Steps)
	}
	step := got.Steps[0]
	if step.Tool != "search_cluster_graph" || step.Summary != "search_cluster_graph: 2 hits" {
		t.Errorf("unexpected step: %+v", step)
	}
	if string(step.Args) != `{"query":"web"}` {
		t.Errorf("Args = %s", step.Args)
	}

	// Round-trips through JSON with the documented field names.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"question", "answer", "steps", "agentic"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("JSON missing field %q: %s", field, b)
		}
	}
	if _, ok := decoded["stepBudgetExhausted"]; ok {
		t.Errorf("stepBudgetExhausted should be omitted when false: %s", b)
	}
}

// TestAgentResultToDTONoSteps verifies the fallback (non-agentic) path, which
// produces an empty (not nil) Steps slice.
func TestAgentResultToDTONoSteps(t *testing.T) {
	got := agentResultToDTO("why?", agent.Result{Answer: "plain answer"})
	if got.Agentic {
		t.Error("expected Agentic = false")
	}
	if got.Steps == nil || len(got.Steps) != 0 {
		t.Errorf("Steps = %+v, want empty non-nil slice", got.Steps)
	}
}
