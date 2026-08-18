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
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Role identifies the author of a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleTool identifies a message carrying the result of a tool call,
	// correlated to the request via Message.ToolCallID. Used only on the
	// tool-calling path (see ToolCaller).
	RoleTool Role = "tool"
)

// Message is a single chat message.
type Message struct {
	Role    Role
	Content string

	// ToolCalls is set on an assistant message that requested tool
	// invocations. Used only on the tool-calling path (see ToolCaller).
	ToolCalls []ToolCall
	// ToolCallID identifies which ToolCall a RoleTool message is the result
	// of. Used only on the tool-calling path (see ToolCaller).
	ToolCallID string
}

// ToolSpec advertises a callable tool to the model: its name, a
// natural-language description of when and how to use it, and its arguments
// as a JSON Schema object (e.g. {"type":"object","properties":{...}}).
type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall is the model's request to invoke one tool, as returned in a Reply.
// Arguments is the raw JSON object the model produced for the tool's
// parameters.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// Reply is one turn of a tool-using completion. Exactly one of Content or
// ToolCalls is meaningful: a final answer has Content set and ToolCalls empty;
// a request to use tools has ToolCalls set (Content is typically empty).
type Reply struct {
	Content   string
	ToolCalls []ToolCall
}

// ToolCaller is implemented by Chat backends that support native tool /
// function calling. It is a separate, optional interface (like ModelSelector)
// so backends and callers that don't need tool use are unaffected.
type ToolCaller interface {
	// CompleteWithTools behaves like Complete, but additionally advertises
	// tools the model may call. The caller is responsible for executing any
	// requested ToolCalls and continuing the conversation with RoleTool
	// result messages (each carrying the originating ToolCallID) until the
	// model returns a Reply with Content and no ToolCalls.
	CompleteWithTools(ctx context.Context, messages []Message, tools []ToolSpec) (Reply, error)
}

// ModelSelector is implemented by Chat backends that can produce a variant of
// themselves targeting a different model with the same provider, credentials
// and settings. It is used to honour per-request model overrides.
type ModelSelector interface {
	// WithModel returns a Chat identical to the receiver except for the model.
	WithModel(model string) Chat
}

// Chat is a provider-agnostic chat-completion model used for text-to-Cypher and
// answer synthesis. Like Embedder, it sits behind an interface so the backend
// (OpenAI, Azure, Ollama, or a test fake) is swappable.
type Chat interface {
	// Complete returns the assistant's reply to the given messages.
	Complete(ctx context.Context, messages []Message) (string, error)
	// Model returns a stable identifier for the model in use.
	Model() string
}

// ChatConfig is the resolved configuration for constructing a Chat (the
// in-process equivalent of the CRD's graphRAG.chat block after secrets are
// read).
type ChatConfig struct {
	// Provider selects the backend. Defaults to ProviderFake when empty.
	Provider Provider
	// Model is the chat model name (required for real providers).
	Model string
	// APIKey authenticates to the provider, when applicable.
	APIKey string
	// BaseURL overrides the provider endpoint (required for azure/ollama).
	BaseURL string
	// Temperature controls sampling. Defaults to 0 for deterministic,
	// instruction-following output (well suited to Cypher generation).
	Temperature float64
	// AllowedModels is the admin policy for per-request model selection: empty
	// allows only Model; a single "*" allows anything the provider offers;
	// otherwise the listed names (plus Model) are allowed. It does not affect
	// the Chat constructed by NewChat, only callers implementing selection.
	AllowedModels []string
}

// NewChat constructs a Chat from resolved configuration. It performs no network
// I/O.
func NewChat(cfg ChatConfig) (Chat, error) {
	switch Provider(strings.ToLower(string(cfg.Provider))) {
	case "", ProviderFake:
		return NewFakeChat(""), nil
	case ProviderLiteLLM:
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("chat provider %q requires a baseURL", cfg.Provider)
		}
		fallthrough
	case ProviderOpenAI, ProviderAzureOpenAI, ProviderOllama:
		return NewOpenAIChat(OpenAIChatConfig{
			APIKey:      cfg.APIKey,
			Model:       cfg.Model,
			BaseURL:     cfg.BaseURL,
			Temperature: cfg.Temperature,
		})
	default:
		return nil, fmt.Errorf("unknown chat provider %q", cfg.Provider)
	}
}
