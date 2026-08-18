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
	"fmt"
	"strings"
	"time"

	"github.com/project-astron/astron/internal/rag"
)

// Defaults applied by NewRunner when a RunnerOptions field is left zero.
const (
	defaultMaxSteps            = 6
	defaultToolTimeout         = 20 * time.Second
	defaultMaxObservationBytes = 8 * 1024
)

// defaultSystemPrompt instructs the model on its role, scope and how to use
// the catalog's tools.
const defaultSystemPrompt = "You are a Kubernetes cluster assistant with tools to inspect a " +
	"single, already-selected cluster resource graph (a \"projection\"). Use the tools to " +
	"gather the information you need before answering; do not guess about resources you have " +
	"not looked up. Prefer search_cluster_graph for open-ended questions, " +
	"get_resource_neighborhood when you already know the exact resource, query_graph for " +
	"precise counts and filters, get_graph_schema to check what resource kinds and " +
	"relationships exist, and get_resource_yaml for full manifest detail. Cite the specific " +
	"resources (kind, namespace, name) that ground your answer."

// budgetExhaustedPrompt is appended when the step budget runs out, asking the
// model to answer from whatever it has already gathered instead of failing
// the request outright.
const budgetExhaustedPrompt = "You have used all available tool calls. Answer the original " +
	"question now, as best you can, using only the information already gathered above. Do not " +
	"request further tools."

// Step records one tool invocation the agent made, for transparency and
// debugging (surfaced to the UI as "tool activity").
type Step struct {
	// Tool is the invoked tool's name.
	Tool string `json:"tool"`
	// Args is the raw JSON arguments the model supplied.
	Args json.RawMessage `json:"args"`
	// Summary is a short, human-readable rendering of the tool's result (or
	// error), truncated for display.
	Summary string `json:"summary"`
}

// Result is the outcome of a Runner.Run call.
type Result struct {
	// Answer is the model's final natural-language answer.
	Answer string
	// Steps records every tool call the agent made, in order.
	Steps []Step
	// StepBudgetExhausted reports whether the run hit RunnerOptions.MaxSteps
	// before the model volunteered a final answer; Answer is still populated
	// (via one last, tool-less completion) but may be less complete than an
	// unbounded run would have produced.
	StepBudgetExhausted bool
	// Agentic reports whether the tool-using loop actually ran. Run always sets
	// it true; a caller that falls back to a non-tool-calling pipeline when no
	// ToolCaller is available (e.g. Projector.AnswerWithTools) should construct
	// its own Result with this left false, so callers can tell the two apart.
	Agentic bool
}

// RunnerOptions configures a Runner. Zero values are replaced with sensible
// defaults by NewRunner.
type RunnerOptions struct {
	// MaxSteps bounds how many rounds of tool calls the agent may make before
	// it is forced to answer with whatever it has gathered. Default 6.
	MaxSteps int
	// ToolTimeout bounds each individual tool invocation. Default 20s.
	ToolTimeout time.Duration
	// MaxObservationBytes truncates each tool result fed back to the model,
	// bounding token cost and latency. Default 8 KiB.
	MaxObservationBytes int
	// SystemPrompt overrides the default system message describing the
	// agent's role and tool-use guidance.
	SystemPrompt string
}

// Runner drives the bounded tool-calling loop described in the package doc.
type Runner struct {
	chat  rag.ToolCaller
	tools ToolSet

	maxSteps            int
	toolTimeout         time.Duration
	maxObservationBytes int
	systemPrompt        string
}

// NewRunner builds a Runner over chat (a tool-calling-capable model) and
// tools (the capabilities offered to it), applying defaults for any zero
// RunnerOptions fields.
func NewRunner(chat rag.ToolCaller, tools ToolSet, opts RunnerOptions) *Runner {
	r := &Runner{
		chat:                chat,
		tools:               tools,
		maxSteps:            opts.MaxSteps,
		toolTimeout:         opts.ToolTimeout,
		maxObservationBytes: opts.MaxObservationBytes,
		systemPrompt:        opts.SystemPrompt,
	}
	if r.maxSteps <= 0 {
		r.maxSteps = defaultMaxSteps
	}
	if r.toolTimeout <= 0 {
		r.toolTimeout = defaultToolTimeout
	}
	if r.maxObservationBytes <= 0 {
		r.maxObservationBytes = defaultMaxObservationBytes
	}
	if r.systemPrompt == "" {
		r.systemPrompt = defaultSystemPrompt
	}
	return r
}

// Run answers question, optionally continuing a prior conversation (history),
// by repeatedly offering the Runner's tools to the model and executing what
// it requests. It returns once the model produces a final, tool-call-free
// reply, or once MaxSteps rounds of tool calls have been made — in which case
// it asks the model for one last, tool-less answer and sets
// Result.StepBudgetExhausted.
func (r *Runner) Run(ctx context.Context, question string, history []rag.Message) (Result, error) {
	msgs := make([]rag.Message, 0, len(history)+2)
	msgs = append(msgs, rag.Message{Role: rag.RoleSystem, Content: r.systemPrompt})
	msgs = append(msgs, history...)
	msgs = append(msgs, rag.Message{Role: rag.RoleUser, Content: question})

	specs := r.tools.Specs()
	var steps []Step

	for i := 0; i < r.maxSteps; i++ {
		reply, err := r.chat.CompleteWithTools(ctx, msgs, specs)
		if err != nil {
			return Result{Steps: steps}, fmt.Errorf("agent: completion failed: %w", err)
		}
		if len(reply.ToolCalls) == 0 {
			return Result{Answer: reply.Content, Steps: steps, Agentic: true}, nil
		}

		msgs = append(msgs, rag.Message{Role: rag.RoleAssistant, Content: reply.Content, ToolCalls: reply.ToolCalls})
		for _, call := range reply.ToolCalls {
			observation, summary := r.invoke(ctx, call)
			steps = append(steps, Step{Tool: call.Name, Args: call.Arguments, Summary: summary})
			msgs = append(msgs, rag.Message{Role: rag.RoleTool, ToolCallID: call.ID, Content: observation})
		}
	}

	// Step budget exhausted: force one final, tool-less answer from whatever
	// was gathered rather than failing the request outright.
	msgs = append(msgs, rag.Message{Role: rag.RoleUser, Content: budgetExhaustedPrompt})
	final, err := r.chat.CompleteWithTools(ctx, msgs, nil)
	if err != nil {
		return Result{Steps: steps, StepBudgetExhausted: true, Agentic: true}, fmt.Errorf("agent: final completion failed: %w", err)
	}
	return Result{Answer: final.Content, Steps: steps, StepBudgetExhausted: true, Agentic: true}, nil
}

// invoke executes one requested tool call, bounding it by ToolTimeout and
// MaxObservationBytes. Both an unknown tool name and a tool execution error
// are turned into an observation describing the failure (rather than
// propagated as a Go error), so the model can see what went wrong and
// recover — e.g. by retrying with different arguments or using another tool.
func (r *Runner) invoke(ctx context.Context, call rag.ToolCall) (observation, summary string) {
	tool, ok := r.tools.find(call.Name)
	if !ok {
		msg := fmt.Sprintf("error: unknown tool %q", call.Name)
		return msg, msg
	}

	callCtx := ctx
	if r.toolTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, r.toolTimeout)
		defer cancel()
	}

	out, err := tool.Invoke(callCtx, call.Arguments)
	if err != nil {
		msg := fmt.Sprintf("error: %v", err)
		return msg, summarize(call.Name, msg)
	}
	out = truncate(out, r.maxObservationBytes)
	return out, summarize(call.Name, out)
}

// summarize renders a short, single-line description of a tool's result for
// Step.Summary: whitespace-collapsed and capped in length.
func summarize(name, observation string) string {
	const maxLen = 160
	s := strings.Join(strings.Fields(observation), " ")
	if len(s) > maxLen {
		s = s[:maxLen] + "…"
	}
	if s == "" {
		return name
	}
	return name + ": " + s
}

// truncate caps s at max bytes, appending a marker when it does.
func truncate(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "…(truncated)"
}
