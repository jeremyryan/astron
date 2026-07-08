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
	"fmt"
	"strings"
)

// Provider enumerates the supported embedding backends. It mirrors the
// graphRAG.embedding.provider field of a GraphProjection.
type Provider string

const (
	// ProviderFake is the deterministic, dependency-free embedder used for tests
	// and offline development.
	ProviderFake Provider = "fake"
	// ProviderOpenAI targets the public OpenAI embeddings API.
	ProviderOpenAI Provider = "openai"
	// ProviderAzureOpenAI targets an Azure OpenAI deployment (OpenAI-compatible).
	ProviderAzureOpenAI Provider = "azure"
	// ProviderOllama targets a local Ollama server's OpenAI-compatible endpoint.
	ProviderOllama Provider = "ollama"
	// ProviderLiteLLM targets a LiteLLM proxy (OpenAI-compatible). LiteLLM
	// aggregates many upstream vendors behind one endpoint, so it pairs well
	// with per-request model selection (allowedModels: ["*"]).
	ProviderLiteLLM Provider = "litellm"
)

// EmbedderConfig is the resolved configuration for constructing an Embedder. It
// is the in-process equivalent of the CRD's graphRAG.embedding block once any
// referenced Secret has been read.
type EmbedderConfig struct {
	// Provider selects the backend. Defaults to ProviderFake when empty.
	Provider Provider
	// Model is the embedding model name (required for real providers).
	Model string
	// APIKey authenticates to the provider, when applicable.
	APIKey string
	// BaseURL overrides the provider endpoint (required for Azure/Ollama).
	BaseURL string
	// Dimensions optionally pins the produced vector length.
	Dimensions int
}

// NewEmbedder constructs an Embedder from resolved configuration. It performs no
// network I/O; connectivity is validated elsewhere (e.g. during reconciliation
// or a health check).
func NewEmbedder(cfg EmbedderConfig) (Embedder, error) {
	switch Provider(strings.ToLower(string(cfg.Provider))) {
	case "", ProviderFake:
		return NewFakeEmbedder(cfg.Dimensions), nil

	case ProviderLiteLLM:
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("embedding provider %q requires a baseURL", cfg.Provider)
		}
		fallthrough
	case ProviderOpenAI, ProviderAzureOpenAI, ProviderOllama:
		return NewOpenAIEmbedder(OpenAIConfig{
			APIKey:     cfg.APIKey,
			Model:      cfg.Model,
			BaseURL:    cfg.BaseURL,
			Dimensions: cfg.Dimensions,
		})

	default:
		return nil, fmt.Errorf("unknown embedding provider %q", cfg.Provider)
	}
}
