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
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

// EnvProvidersConfigFile names the environment variable holding the path to the
// controller-wide providers configuration file. It mirrors the
// --providers-config-file flag.
const EnvProvidersConfigFile = "ASTRON_PROVIDERS_CONFIG_FILE"

// DefaultProviderAPIKeyKey is the Secret data key assumed to hold a provider's
// API key when a SecretKeyRef does not name one.
const DefaultProviderAPIKeyKey = "apiKey"

// SecretKeyRef references a key within a Kubernetes Secret that holds a
// provider's API key. Credentials are never inlined in the providers config
// file (which is mounted from a ConfigMap and therefore not secret); they are
// referenced here and resolved from Secrets by callers.
type SecretKeyRef struct {
	// Name is the Secret name.
	Name string `json:"name"`
	// Namespace is the Secret namespace. When empty, the controller's own
	// namespace is assumed by the resolver.
	Namespace string `json:"namespace,omitempty"`
	// Key is the Secret data key holding the API key. Defaults to
	// DefaultProviderAPIKeyKey ("apiKey") when empty.
	Key string `json:"key,omitempty"`
}

// EmbeddingProviderConfig is one named embedding provider made available to
// every projection by the controller-wide providers configuration.
type EmbeddingProviderConfig struct {
	// Name uniquely identifies this provider so projections (and future agents)
	// can select it by name. Required and unique among embedding providers.
	Name string `json:"name"`
	// Provider selects the backend (openai, azure, ollama, litellm, fake).
	// Defaults to openai when empty.
	Provider Provider `json:"provider,omitempty"`
	// Model is the embedding model name (required for non-fake providers).
	Model string `json:"model,omitempty"`
	// BaseURL overrides the provider endpoint (required for azure, ollama and
	// litellm).
	BaseURL string `json:"baseURL,omitempty"`
	// Dimensions optionally pins the produced vector length.
	Dimensions int `json:"dimensions,omitempty"`
	// APIKeySecret references the Secret key holding this provider's API key.
	// Optional for the fake and (typically) ollama providers.
	APIKeySecret *SecretKeyRef `json:"apiKeySecret,omitempty"`
}

// ChatProviderConfig is one named chat provider made available to every
// projection by the controller-wide providers configuration.
type ChatProviderConfig struct {
	// Name uniquely identifies this provider so projections (and future agents)
	// can select it by name. Required and unique among chat providers.
	Name string `json:"name"`
	// Provider selects the backend (openai, azure, ollama, litellm, fake).
	// Defaults to openai when empty.
	Provider Provider `json:"provider,omitempty"`
	// Model is the chat model name (required for non-fake providers).
	Model string `json:"model,omitempty"`
	// BaseURL overrides the provider endpoint (required for azure, ollama and
	// litellm).
	BaseURL string `json:"baseURL,omitempty"`
	// AllowedModels controls which chat models may be selected per request.
	// Empty allows only Model; a single "*" allows any model the provider
	// offers; otherwise the listed names (plus Model) are allowed.
	AllowedModels []string `json:"allowedModels,omitempty"`
	// APIKeySecret references the Secret key holding this provider's API key.
	// Optional for the fake and (typically) ollama providers.
	APIKeySecret *SecretKeyRef `json:"apiKeySecret,omitempty"`
}

// ProvidersConfig is the controller-wide set of agentic model providers, shared
// by every projection. It is loaded from a YAML file typically mounted from a
// ConfigMap.
type ProvidersConfig struct {
	// EmbeddingProviders are the named embedding backends available cluster-wide.
	EmbeddingProviders []EmbeddingProviderConfig `json:"embeddingProviders,omitempty"`
	// ChatProviders are the named chat backends available cluster-wide.
	ChatProviders []ChatProviderConfig `json:"chatProviders,omitempty"`
}

// knownProviders is the set of provider backends accepted in the providers
// configuration. It mirrors the CRD's provider enum.
var knownProviders = map[Provider]bool{
	ProviderFake:        true,
	ProviderOpenAI:      true,
	ProviderAzureOpenAI: true,
	ProviderOllama:      true,
	ProviderLiteLLM:     true,
}

// baseURLRequiredProviders lists providers whose endpoint must be given
// explicitly (there is no sensible public default).
var baseURLRequiredProviders = map[Provider]bool{
	ProviderAzureOpenAI: true,
	ProviderOllama:      true,
	ProviderLiteLLM:     true,
}

// ProviderRegistry holds the resolved controller-wide providers, indexed by
// name for lookup and preserving declaration order for stable listing. It is
// immutable once built.
type ProviderRegistry struct {
	embedding      map[string]EmbeddingProviderConfig
	chat           map[string]ChatProviderConfig
	embeddingOrder []string
	chatOrder      []string
}

// LoadProvidersConfig reads and validates the controller-wide providers
// configuration from a YAML file (typically mounted from a ConfigMap) and
// returns a registry of the declared embedding and chat providers.
//
// An empty configFile yields an empty registry and no error (the feature is
// simply off). A file that cannot be read, cannot be parsed, or fails
// validation is an error. Provider API keys are never read here: only their
// Secret references are recorded, to be resolved by callers.
func LoadProvidersConfig(configFile string) (*ProviderRegistry, error) {
	reg := &ProviderRegistry{
		embedding: map[string]EmbeddingProviderConfig{},
		chat:      map[string]ChatProviderConfig{},
	}
	if configFile == "" {
		return reg, nil
	}

	raw, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("reading providers config file %q: %w", configFile, err)
	}
	var cfg ProvidersConfig
	if err := yaml.UnmarshalStrict(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parsing providers config file %q: %w", configFile, err)
	}

	for i := range cfg.EmbeddingProviders {
		p := cfg.EmbeddingProviders[i]
		if p.Provider == "" {
			p.Provider = ProviderOpenAI
		}
		if err := validateProvider("embedding", p.Name, p.Provider, p.Model, p.BaseURL); err != nil {
			return nil, err
		}
		if _, dup := reg.embedding[p.Name]; dup {
			return nil, fmt.Errorf("duplicate embedding provider name %q", p.Name)
		}
		reg.embedding[p.Name] = p
		reg.embeddingOrder = append(reg.embeddingOrder, p.Name)
	}

	for i := range cfg.ChatProviders {
		p := cfg.ChatProviders[i]
		if p.Provider == "" {
			p.Provider = ProviderOpenAI
		}
		if err := validateProvider("chat", p.Name, p.Provider, p.Model, p.BaseURL); err != nil {
			return nil, err
		}
		if _, dup := reg.chat[p.Name]; dup {
			return nil, fmt.Errorf("duplicate chat provider name %q", p.Name)
		}
		reg.chat[p.Name] = p
		reg.chatOrder = append(reg.chatOrder, p.Name)
	}

	return reg, nil
}

// validateProvider checks the shared fields of an embedding or chat provider
// entry. kind is "embedding" or "chat" for error messages.
func validateProvider(kind, name string, provider Provider, model, baseURL string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s provider is missing a name", kind)
	}
	if !knownProviders[provider] {
		return fmt.Errorf("%s provider %q has unknown provider %q", kind, name, provider)
	}
	if provider != ProviderFake && strings.TrimSpace(model) == "" {
		return fmt.Errorf("%s provider %q is missing a model", kind, name)
	}
	if baseURLRequiredProviders[provider] && strings.TrimSpace(baseURL) == "" {
		return fmt.Errorf("%s provider %q (%s) requires a baseURL", kind, name, provider)
	}
	return nil
}

// EmbeddingProvider returns the named embedding provider and whether it exists.
func (r *ProviderRegistry) EmbeddingProvider(name string) (EmbeddingProviderConfig, bool) {
	p, ok := r.embedding[name]
	return p, ok
}

// ChatProvider returns the named chat provider and whether it exists.
func (r *ProviderRegistry) ChatProvider(name string) (ChatProviderConfig, bool) {
	p, ok := r.chat[name]
	return p, ok
}

// EmbeddingProviderNames lists the embedding provider names in declaration
// order.
func (r *ProviderRegistry) EmbeddingProviderNames() []string {
	return append([]string(nil), r.embeddingOrder...)
}

// ChatProviderNames lists the chat provider names in declaration order.
func (r *ProviderRegistry) ChatProviderNames() []string {
	return append([]string(nil), r.chatOrder...)
}

// Empty reports whether no providers are configured.
func (r *ProviderRegistry) Empty() bool {
	return len(r.embedding) == 0 && len(r.chat) == 0
}
