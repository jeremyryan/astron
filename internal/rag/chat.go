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
	"fmt"
	"strings"
)

// Role identifies the author of a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single chat message.
type Message struct {
	Role    Role
	Content string
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
