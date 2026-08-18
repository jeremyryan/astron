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
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/project-astron/astron/internal/rag"
)

// TestNewRunnerDefaults verifies zero RunnerOptions fields are replaced with
// the documented defaults.
func TestNewRunnerDefaults(t *testing.T) {
	r := NewRunner(&rag.FakeChat{}, nil, RunnerOptions{})
	if r.maxSteps != defaultMaxSteps {
		t.Errorf("maxSteps = %d, want %d", r.maxSteps, defaultMaxSteps)
	}
	if r.toolTimeout != defaultToolTimeout {
		t.Errorf("toolTimeout = %v, want %v", r.toolTimeout, defaultToolTimeout)
	}
	if r.maxObservationBytes != defaultMaxObservationBytes {
		t.Errorf("maxObservationBytes = %d, want %d", r.maxObservationBytes, defaultMaxObservationBytes)
	}
	if r.systemPrompt != defaultSystemPrompt {
		t.Errorf("systemPrompt not defaulted")
	}
}

// TestNewRunnerHonoursOverrides verifies explicit RunnerOptions are used as-is.
func TestNewRunnerHonoursOverrides(t *testing.T) {
	r := NewRunner(&rag.FakeChat{}, nil, RunnerOptions{
		MaxSteps: 3, ToolTimeout: 5 * time.Second, MaxObservationBytes: 100, SystemPrompt: "custom",
	})
	if r.maxSteps != 3 || r.toolTimeout != 5*time.Second || r.maxObservationBytes != 100 || r.systemPrompt != "custom" {
		t.Fatalf("overrides not honoured: %+v", r)
	}
}

// TestRunnerFinalAnswerWithoutTools verifies a model that answers immediately
// (no tool calls) short-circuits the loop with no steps.
func TestRunnerFinalAnswerWithoutTools(t *testing.T) {
	chat := &rag.FakeChat{Reply: "the answer"}
	r := NewRunner(chat, nil, RunnerOptions{})

	res, err := r.Run(context.Background(), "why?", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "the answer" || len(res.Steps) != 0 || res.StepBudgetExhausted {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// TestRunnerExecutesToolThenAnswers verifies a tool call is executed against
// the ToolSet, its result fed back as an observation, and the loop continues
// to the model's subsequent final answer.
func TestRunnerExecutesToolThenAnswers(t *testing.T) {
	chat := &rag.FakeChat{
		ToolScript: []rag.Reply{
			{ToolCalls: []rag.ToolCall{{ID: "call_1", Name: ToolSearchClusterGraph, Arguments: json.RawMessage(`{"query":"web pods"}`)}}},
			{Content: "found 2 web pods"},
		},
	}
	var gotArgs json.RawMessage
	tools := ToolSet{{
		Spec: rag.ToolSpec{Name: ToolSearchClusterGraph},
		Invoke: func(_ context.Context, args json.RawMessage) (string, error) {
			gotArgs = args
			return `{"hits":2}`, nil
		},
	}}
	r := NewRunner(chat, tools, RunnerOptions{})

	res, err := r.Run(context.Background(), "how many web pods?", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "found 2 web pods" {
		t.Fatalf("Answer = %q", res.Answer)
	}
	if res.StepBudgetExhausted {
		t.Fatal("did not expect step budget exhaustion")
	}
	if len(res.Steps) != 1 {
		t.Fatalf("Steps = %+v, want 1", res.Steps)
	}
	step := res.Steps[0]
	if step.Tool != ToolSearchClusterGraph {
		t.Errorf("Steps[0].Tool = %q", step.Tool)
	}
	if !strings.Contains(step.Summary, "hits") {
		t.Errorf("Steps[0].Summary = %q, want it to reflect the tool result", step.Summary)
	}
	if string(gotArgs) != `{"query":"web pods"}` {
		t.Errorf("tool invoked with args %s", gotArgs)
	}
}

// TestRunnerUnknownToolProducesObservation verifies a model requesting a tool
// that isn't in the ToolSet gets an error observation back (not a Go error
// that aborts the run), and the loop continues.
func TestRunnerUnknownToolProducesObservation(t *testing.T) {
	var observed string
	chat := &rag.FakeChat{
		ToolCallFunc: func(messages []rag.Message, _ []rag.ToolSpec) rag.Reply {
			// First call: request a tool that doesn't exist.
			for _, m := range messages {
				if m.Role == rag.RoleTool {
					observed = m.Content
					return rag.Reply{Content: "recovered"}
				}
			}
			return rag.Reply{ToolCalls: []rag.ToolCall{{ID: "call_1", Name: "no_such_tool", Arguments: json.RawMessage(`{}`)}}}
		},
	}
	r := NewRunner(chat, nil, RunnerOptions{})

	res, err := r.Run(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "recovered" {
		t.Fatalf("Answer = %q", res.Answer)
	}
	if !strings.Contains(observed, "unknown tool") {
		t.Fatalf("observation = %q, want it to mention the unknown tool", observed)
	}
	if len(res.Steps) != 1 || !strings.Contains(res.Steps[0].Summary, "unknown tool") {
		t.Fatalf("unexpected steps: %+v", res.Steps)
	}
}

// TestRunnerToolInvokeErrorBecomesObservation verifies a tool execution error
// is surfaced to the model as an observation rather than aborting the run.
func TestRunnerToolInvokeErrorBecomesObservation(t *testing.T) {
	boom := errors.New("boom")
	tools := ToolSet{{
		Spec:   rag.ToolSpec{Name: ToolQueryGraph},
		Invoke: func(context.Context, json.RawMessage) (string, error) { return "", boom },
	}}
	chat := &rag.FakeChat{
		ToolScript: []rag.Reply{
			{ToolCalls: []rag.ToolCall{{ID: "call_1", Name: ToolQueryGraph, Arguments: json.RawMessage(`{}`)}}},
			{Content: "handled the failure"},
		},
	}
	r := NewRunner(chat, tools, RunnerOptions{})

	res, err := r.Run(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "handled the failure" {
		t.Fatalf("Answer = %q", res.Answer)
	}
	if len(res.Steps) != 1 || !strings.Contains(res.Steps[0].Summary, "boom") {
		t.Fatalf("unexpected steps: %+v", res.Steps)
	}
}

// TestRunnerStepBudgetExhausted verifies a model that never stops requesting
// tools is cut off at MaxSteps and forced to a final, tool-less answer.
func TestRunnerStepBudgetExhausted(t *testing.T) {
	tools := ToolSet{{
		Spec:   rag.ToolSpec{Name: ToolGetGraphSchema},
		Invoke: func(context.Context, json.RawMessage) (string, error) { return "schema", nil },
	}}
	chat := &rag.FakeChat{
		ToolCallFunc: func(_ []rag.Message, specs []rag.ToolSpec) rag.Reply {
			if len(specs) == 0 {
				// The forced final call passes no tools.
				return rag.Reply{Content: "forced final answer"}
			}
			return rag.Reply{ToolCalls: []rag.ToolCall{{ID: "call", Name: ToolGetGraphSchema, Arguments: json.RawMessage(`{}`)}}}
		},
	}
	r := NewRunner(chat, tools, RunnerOptions{MaxSteps: 2})

	res, err := r.Run(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.StepBudgetExhausted {
		t.Fatal("expected StepBudgetExhausted")
	}
	if res.Answer != "forced final answer" {
		t.Fatalf("Answer = %q", res.Answer)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("Steps = %+v, want 2 (MaxSteps)", res.Steps)
	}
}

// TestRunnerSeedsSystemHistoryAndQuestion verifies the message transcript
// sent to the model is [system, history..., question].
func TestRunnerSeedsSystemHistoryAndQuestion(t *testing.T) {
	var seen []rag.Message
	chat := &rag.FakeChat{
		ToolCallFunc: func(messages []rag.Message, _ []rag.ToolSpec) rag.Reply {
			seen = messages
			return rag.Reply{Content: "ok"}
		},
	}
	r := NewRunner(chat, nil, RunnerOptions{SystemPrompt: "SYSTEM"})
	history := []rag.Message{
		{Role: rag.RoleUser, Content: "earlier question"},
		{Role: rag.RoleAssistant, Content: "earlier answer"},
	}

	if _, err := r.Run(context.Background(), "new question", history); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen) != 4 {
		t.Fatalf("messages = %+v, want 4", seen)
	}
	if seen[0].Role != rag.RoleSystem || seen[0].Content != "SYSTEM" {
		t.Errorf("messages[0] = %+v", seen[0])
	}
	if seen[1].Role != history[0].Role || seen[1].Content != history[0].Content ||
		seen[2].Role != history[1].Role || seen[2].Content != history[1].Content {
		t.Errorf("history not carried through verbatim: %+v", seen[1:3])
	}
	if seen[3].Role != rag.RoleUser || seen[3].Content != "new question" {
		t.Errorf("messages[3] = %+v", seen[3])
	}
}

// TestRunnerCompletionErrorAborts verifies a chat completion error aborts the
// run (unlike a tool error, this is not recoverable by the model).
func TestRunnerCompletionErrorAborts(t *testing.T) {
	boom := errors.New("provider unavailable")
	chat := &rag.FakeChat{
		ToolCallFunc: func([]rag.Message, []rag.ToolSpec) rag.Reply {
			panic("should not be reached")
		},
	}
	// Wrap to inject a completion error on the first call.
	failing := &failingToolCaller{err: boom, delegate: chat}
	r := NewRunner(failing, nil, RunnerOptions{})

	_, err := r.Run(context.Background(), "q", nil)
	if !errors.Is(err, boom) {
		t.Fatalf("Run error = %v, want it to wrap %v", err, boom)
	}
}

// failingToolCaller returns err from its first CompleteWithTools call.
type failingToolCaller struct {
	err      error
	delegate rag.ToolCaller
}

func (f *failingToolCaller) CompleteWithTools(ctx context.Context, messages []rag.Message, tools []rag.ToolSpec) (rag.Reply, error) {
	if f.err != nil {
		err := f.err
		f.err = nil
		return rag.Reply{}, err
	}
	return f.delegate.CompleteWithTools(ctx, messages, tools)
}

// TestRunnerObservationTruncated verifies long tool results are truncated to
// MaxObservationBytes before being fed back to the model.
func TestRunnerObservationTruncated(t *testing.T) {
	long := strings.Repeat("x", 1000)
	tools := ToolSet{{
		Spec:   rag.ToolSpec{Name: ToolGetResourceYAML},
		Invoke: func(context.Context, json.RawMessage) (string, error) { return long, nil },
	}}
	var observed string
	chat := &rag.FakeChat{
		ToolCallFunc: func(messages []rag.Message, _ []rag.ToolSpec) rag.Reply {
			for _, m := range messages {
				if m.Role == rag.RoleTool {
					observed = m.Content
					return rag.Reply{Content: "done"}
				}
			}
			return rag.Reply{ToolCalls: []rag.ToolCall{{ID: "call", Name: ToolGetResourceYAML, Arguments: json.RawMessage(`{}`)}}}
		},
	}
	r := NewRunner(chat, tools, RunnerOptions{MaxObservationBytes: 50})

	if _, err := r.Run(context.Background(), "q", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(observed) > 50+len("…(truncated)") {
		t.Fatalf("observation not truncated: %d bytes", len(observed))
	}
	if !strings.HasSuffix(observed, "…(truncated)") {
		t.Fatalf("observation missing truncation marker: %q", observed)
	}
}

// TestRunnerToolInvocationHasDeadline verifies each tool call is given a
// context with a deadline derived from ToolTimeout.
func TestRunnerToolInvocationHasDeadline(t *testing.T) {
	var hadDeadline bool
	tools := ToolSet{{
		Spec: rag.ToolSpec{Name: ToolGetGraphSchema},
		Invoke: func(ctx context.Context, _ json.RawMessage) (string, error) {
			_, hadDeadline = ctx.Deadline()
			return "ok", nil
		},
	}}
	chat := &rag.FakeChat{
		ToolScript: []rag.Reply{
			{ToolCalls: []rag.ToolCall{{ID: "call", Name: ToolGetGraphSchema, Arguments: json.RawMessage(`{}`)}}},
			{Content: "done"},
		},
	}
	r := NewRunner(chat, tools, RunnerOptions{ToolTimeout: time.Second})

	if _, err := r.Run(context.Background(), "q", nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hadDeadline {
		t.Fatal("expected the tool's context to carry a deadline")
	}
}

func TestSummarize(t *testing.T) {
	cases := []struct {
		name, observation, want string
	}{
		{"t", "", "t"},
		{"t", "  a   b  ", "t: a b"},
		{"t", strings.Repeat("z", 200), fmt.Sprintf("t: %s…", strings.Repeat("z", 160))},
	}
	for _, c := range cases {
		if got := summarize(c.name, c.observation); got != c.want {
			t.Errorf("summarize(%q, %q) = %q, want %q", c.name, c.observation, got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc…(truncated)" {
		t.Errorf("truncate long = %q", got)
	}
	if got := truncate("abcdef", 0); got != "abcdef" {
		t.Errorf("truncate with maxBytes<=0 should be a no-op, got %q", got)
	}
}
