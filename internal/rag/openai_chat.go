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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// OpenAIChatConfig configures an OpenAIChat.
type OpenAIChatConfig struct {
	APIKey      string
	Model       string
	BaseURL     string
	Temperature float64
	HTTPClient  *http.Client
}

// OpenAIChat is a Chat backed by an OpenAI-compatible chat-completions API
// (OpenAI, Azure OpenAI, or Ollama's /v1 endpoint).
type OpenAIChat struct {
	cfg    OpenAIChatConfig
	client *http.Client
	// omitTemperature is set once the model has rejected an explicit
	// temperature (some newer OpenAI models only allow the default), so
	// subsequent requests skip the field instead of retrying every time.
	omitTemperature atomic.Bool
	// forceNoReasoningEffort is set once a tool-calling request has been
	// rejected because the model's default (non-"none") reasoning effort can't
	// be combined with tools on this endpoint (some reasoning models require
	// /v1/responses for that combination unless reasoning is turned off).
	// Once set, subsequent CompleteWithTools calls send
	// reasoning_effort:"none" up front instead of retrying every time.
	// Complete (no tools) is unaffected, since the error only occurs when
	// tools are present.
	forceNoReasoningEffort atomic.Bool
}

// compile-time assertion that OpenAIChat satisfies Chat.
var _ Chat = (*OpenAIChat)(nil)

// NewOpenAIChat constructs an OpenAIChat, validating required fields without
// performing network I/O.
func NewOpenAIChat(cfg OpenAIChatConfig) (*OpenAIChat, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai chat: APIKey is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("openai chat: Model is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultOpenAIBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &OpenAIChat{cfg: cfg, client: client}, nil
}

// Model returns the configured model name.
func (c *OpenAIChat) Model() string { return c.cfg.Model }

// compile-time assertion that OpenAIChat supports model overrides.
var _ ModelSelector = (*OpenAIChat)(nil)

// WithModel returns a copy of the chat targeting a different model with the
// same credentials, endpoint and settings. The temperature-support and
// reasoning-effort decisions are not carried over, since both are
// model-specific.
func (c *OpenAIChat) WithModel(model string) Chat {
	cfg := c.cfg
	cfg.Model = model
	return &OpenAIChat{cfg: cfg, client: c.client}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	// ToolCalls is set on an assistant message that requested tool calls (a
	// response we parse) or that we're replaying back to the API as history.
	ToolCalls []wireToolCall `json:"tool_calls,omitempty"`
	// ToolCallID identifies which tool call a role:"tool" message answers.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// wireToolCall and wireFunctionCall mirror the OpenAI chat-completions
// "tool_calls" shape: a call is typed "function" and names the function plus
// its JSON-encoded (string, not object) arguments.
type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireFunctionCall `json:"function"`
}

type wireFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// wireTool advertises one callable tool to the model, mirroring the OpenAI
// chat-completions "tools" request shape.
type wireTool struct {
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	// Temperature is a pointer so it can be omitted entirely for models that
	// only accept the default value.
	Temperature *float64   `json:"temperature,omitempty"`
	Tools       []wireTool `json:"tools,omitempty"`
	// ReasoningEffort, when set, is sent as "none" to let a reasoning model's
	// tool calls work on this (classic chat-completions) endpoint; omitted
	// otherwise, leaving the model/gateway's own default in effect.
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
}

// chatError is the error object returned by the chat-completions API.
type chatError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	// Param names the offending request field (e.g. "temperature") when the
	// API can attribute the error to one.
	Param string `json:"param"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *chatError `json:"error"`
}

// requestIssue reports which specific request field, if any, a
// chat-completions request was rejected for, so retry logic can adjust
// exactly that field rather than guessing.
type requestIssue struct {
	// TemperatureRejected reports whether the model rejected an explicit
	// temperature value.
	TemperatureRejected bool
	// ReasoningRejected reports whether the model rejected combining tools
	// with its default (non-"none") reasoning effort.
	ReasoningRejected bool
}

// Complete calls the chat-completions endpoint and returns the assistant reply.
//
// Some newer OpenAI models reject any explicit temperature other than the
// default (e.g. "'temperature' does not support 0 with this model"). When that
// happens, Complete transparently retries once without the temperature field
// and remembers the model's preference for future calls.
func (c *OpenAIChat) Complete(ctx context.Context, messages []Message) (string, error) {
	msgs := toWireMessages(messages)

	content, issue, err := c.complete(ctx, msgs, !c.omitTemperature.Load())
	if err != nil && issue.TemperatureRejected {
		// The model only supports the default temperature; drop the field,
		// remember that for next time, and retry once.
		c.omitTemperature.Store(true)
		content, _, err = c.complete(ctx, msgs, false)
	}
	return content, err
}

// complete performs a single, tool-less chat-completions request.
// includeTemperature controls whether the configured temperature is sent.
func (c *OpenAIChat) complete(ctx context.Context, msgs []chatMessage, includeTemperature bool) (string, requestIssue, error) {
	reqBody := chatRequest{Model: c.cfg.Model, Messages: msgs}
	if includeTemperature {
		temp := c.cfg.Temperature
		reqBody.Temperature = &temp
	}
	parsed, issue, err := c.doRequest(ctx, reqBody)
	if err != nil {
		return "", issue, err
	}
	return parsed.Choices[0].Message.Content, requestIssue{}, nil
}

// compile-time assertion that OpenAIChat supports tool calling.
var _ ToolCaller = (*OpenAIChat)(nil)

// CompleteWithTools calls the chat-completions endpoint with the given tools
// advertised, returning either the model's final answer (Reply.Content) or
// the tool calls it wants executed (Reply.ToolCalls). Like Complete, it
// transparently retries once without an explicit temperature if the model
// rejects it.
//
// Some reasoning models (e.g. gpt-5-style ones) reject combining tools with
// their default reasoning effort on this endpoint ("Function tools with
// reasoning_effort are not supported ... set reasoning_effort to 'none'" —
// full reasoning+tools support requires OpenAI's newer /v1/responses API,
// which isn't implemented by every OpenAI-compatible provider Astron
// targets, e.g. Ollama). When that happens, CompleteWithTools transparently
// retries once with reasoning_effort:"none" and remembers the model's
// preference for future calls, the same way it already handles temperature.
func (c *OpenAIChat) CompleteWithTools(ctx context.Context, messages []Message, tools []ToolSpec) (Reply, error) {
	msgs := toWireMessages(messages)
	wireTools := make([]wireTool, len(tools))
	for i, t := range tools {
		wireTools[i] = wireTool{Type: "function", Function: wireFunction(t)}
	}

	includeTemperature := !c.omitTemperature.Load()
	forceNoReasoning := c.forceNoReasoningEffort.Load()
	reply, issue, err := c.completeWithTools(ctx, msgs, wireTools, includeTemperature, forceNoReasoning)
	if err != nil && (issue.TemperatureRejected || issue.ReasoningRejected) {
		if issue.TemperatureRejected {
			c.omitTemperature.Store(true)
			includeTemperature = false
		}
		if issue.ReasoningRejected {
			c.forceNoReasoningEffort.Store(true)
			forceNoReasoning = true
		}
		reply, _, err = c.completeWithTools(ctx, msgs, wireTools, includeTemperature, forceNoReasoning)
	}
	return reply, err
}

// completeWithTools performs a single tool-advertising chat-completions
// request, mirroring complete's temperature-retry contract and additionally
// forcing reasoning_effort:"none" when forceNoReasoning is set.
func (c *OpenAIChat) completeWithTools(
	ctx context.Context, msgs []chatMessage, tools []wireTool, includeTemperature, forceNoReasoning bool,
) (Reply, requestIssue, error) {
	reqBody := chatRequest{Model: c.cfg.Model, Messages: msgs, Tools: tools}
	if includeTemperature {
		temp := c.cfg.Temperature
		reqBody.Temperature = &temp
	}
	if forceNoReasoning {
		none := "none"
		reqBody.ReasoningEffort = &none
	}
	parsed, issue, err := c.doRequest(ctx, reqBody)
	if err != nil {
		return Reply{}, issue, err
	}
	msg := parsed.Choices[0].Message
	reply := Reply{Content: msg.Content}
	for _, tc := range msg.ToolCalls {
		reply.ToolCalls = append(reply.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}
	return reply, requestIssue{}, nil
}

// doRequest sends a chat-completions request and returns the parsed response.
// The returned requestIssue reports which specific field, if any, the request
// was rejected for, so callers can retry with just that field adjusted.
func (c *OpenAIChat) doRequest(ctx context.Context, reqBody chatRequest) (chatResponse, requestIssue, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return chatResponse{}, requestIssue{}, fmt.Errorf("openai chat: marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return chatResponse{}, requestIssue{}, fmt.Errorf("openai chat: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return chatResponse{}, requestIssue{}, fmt.Errorf("openai chat: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return chatResponse{}, requestIssue{}, fmt.Errorf("openai chat: reading response: %w", err)
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return chatResponse{}, requestIssue{}, fmt.Errorf("openai chat: decoding response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil && parsed.Error.Message != "" {
			issue := requestIssue{
				TemperatureRejected: isTemperatureError(reqBody.Temperature != nil, parsed.Error),
				ReasoningRejected:   isReasoningEffortToolsError(len(reqBody.Tools) > 0, parsed.Error),
			}
			return chatResponse{}, issue, fmt.Errorf("openai chat: api error (status %d): %s", resp.StatusCode, parsed.Error.Message)
		}
		return chatResponse{}, requestIssue{}, fmt.Errorf("openai chat: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(parsed.Choices) == 0 {
		return chatResponse{}, requestIssue{}, fmt.Errorf("openai chat: response contained no choices")
	}
	return parsed, requestIssue{}, nil
}

// toWireMessages converts Messages to the wire chatMessage shape, carrying
// over tool-calling fields (ToolCalls, ToolCallID) used on the tool-calling
// path; Complete's callers simply leave those fields zero.
func toWireMessages(messages []Message) []chatMessage {
	msgs := make([]chatMessage, len(messages))
	for i, m := range messages {
		wm := chatMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: wireFunctionCall{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			})
		}
		msgs[i] = wm
	}
	return msgs
}

// isTemperatureError reports whether an API error is a rejection of the
// temperature parameter (only meaningful when temperature was actually sent),
// e.g. "Unsupported value: 'temperature' does not support 0 with this model.
// Only the default (1) value is supported."
func isTemperatureError(temperatureSent bool, apiErr *chatError) bool {
	if !temperatureSent || apiErr == nil {
		return false
	}
	if apiErr.Param == "temperature" {
		return true
	}
	return strings.Contains(strings.ToLower(apiErr.Message), "temperature")
}

// isReasoningEffortToolsError reports whether an API error is a rejection of
// combining tools with the model's default reasoning effort (only meaningful
// when tools were actually sent), e.g. "Function tools with reasoning_effort
// are not supported for gpt-5.6-sol in /v1/chat/completions. To use function
// tools, use /v1/responses or set reasoning_effort to 'none'."
func isReasoningEffortToolsError(toolsSent bool, apiErr *chatError) bool {
	if !toolsSent || apiErr == nil {
		return false
	}
	if apiErr.Param == "reasoning_effort" {
		return true
	}
	return strings.Contains(strings.ToLower(apiErr.Message), "reasoning_effort")
}
