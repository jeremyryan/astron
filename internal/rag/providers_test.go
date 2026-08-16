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
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeProviders writes content to a temp file and returns its path.
func writeProviders(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "providers.yaml")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatalf("writing providers file: %v", err)
	}
	return file
}

func TestLoadProvidersConfigEmpty(t *testing.T) {
	reg, err := LoadProvidersConfig("")
	if err != nil {
		t.Fatalf("LoadProvidersConfig(\"\"): %v", err)
	}
	if !reg.Empty() {
		t.Fatalf("expected empty registry, got %d embedding / %d chat",
			len(reg.EmbeddingProviderNames()), len(reg.ChatProviderNames()))
	}
}

func TestLoadProvidersConfigParsesAndIndexes(t *testing.T) {
	file := writeProviders(t, `
embeddingProviders:
  - name: openai-small
    provider: openai
    model: text-embedding-3-small
    dimensions: 1536
    apiKeySecret:
      name: openai-key
      key: apiKey
  - name: local
    provider: ollama
    model: nomic-embed-text
    baseURL: http://ollama.astron.svc:11434/v1
chatProviders:
  - name: gpt4o
    provider: openai
    model: gpt-4o-mini
    allowedModels: ["*"]
    apiKeySecret:
      name: openai-key
      namespace: astron
`)
	reg, err := LoadProvidersConfig(file)
	if err != nil {
		t.Fatalf("LoadProvidersConfig: %v", err)
	}
	if reg.Empty() {
		t.Fatal("expected a non-empty registry")
	}

	if got, want := reg.EmbeddingProviderNames(), []string{"openai-small", "local"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("embedding names = %v, want %v (declaration order)", got, want)
	}
	if got, want := reg.ChatProviderNames(), []string{"gpt4o"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("chat names = %v, want %v", got, want)
	}

	emb, ok := reg.EmbeddingProvider("openai-small")
	if !ok {
		t.Fatal("embedding provider openai-small not found")
	}
	if emb.Model != "text-embedding-3-small" || emb.Dimensions != 1536 {
		t.Fatalf("unexpected embedding provider: %+v", emb)
	}
	if emb.APIKeySecret == nil || emb.APIKeySecret.Name != "openai-key" || emb.APIKeySecret.Key != "apiKey" {
		t.Fatalf("unexpected embedding secret ref: %+v", emb.APIKeySecret)
	}

	chat, ok := reg.ChatProvider("gpt4o")
	if !ok {
		t.Fatal("chat provider gpt4o not found")
	}
	if len(chat.AllowedModels) != 1 || chat.AllowedModels[0] != "*" {
		t.Fatalf("unexpected allowedModels: %v", chat.AllowedModels)
	}
	if chat.APIKeySecret == nil || chat.APIKeySecret.Namespace != "astron" {
		t.Fatalf("unexpected chat secret ref: %+v", chat.APIKeySecret)
	}

	if _, ok := reg.EmbeddingProvider("missing"); ok {
		t.Fatal("expected missing embedding provider to be absent")
	}
}

func TestLoadProvidersConfigDefaultsProviderToOpenAI(t *testing.T) {
	file := writeProviders(t, `
chatProviders:
  - name: default-openai
    model: gpt-4o-mini
`)
	reg, err := LoadProvidersConfig(file)
	if err != nil {
		t.Fatalf("LoadProvidersConfig: %v", err)
	}
	chat, ok := reg.ChatProvider("default-openai")
	if !ok {
		t.Fatal("provider not found")
	}
	if chat.Provider != ProviderOpenAI {
		t.Fatalf("provider = %q, want %q", chat.Provider, ProviderOpenAI)
	}
}

func TestLoadProvidersConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{
			name: "missing name",
			content: `
embeddingProviders:
  - provider: openai
    model: text-embedding-3-small
`,
		},
		{
			name: "unknown provider",
			content: `
chatProviders:
  - name: bad
    provider: anthropic
    model: claude
`,
		},
		{
			name: "missing model for real provider",
			content: `
embeddingProviders:
  - name: no-model
    provider: openai
`,
		},
		{
			name: "baseURL required for litellm",
			content: `
chatProviders:
  - name: proxy
    provider: litellm
    model: gpt-4o-mini
`,
		},
		{
			name: "duplicate embedding name",
			content: `
embeddingProviders:
  - name: dup
    provider: openai
    model: text-embedding-3-small
  - name: dup
    provider: openai
    model: text-embedding-3-large
`,
		},
		{
			name: "unknown field",
			content: `
embeddingProviders:
  - name: x
    provider: openai
    model: m
    nope: true
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadProvidersConfig(writeProviders(t, tc.content)); err == nil {
				t.Fatal("expected a validation error, got nil")
			}
		})
	}
}

func TestLoadProvidersConfigFakeNeedsNoModel(t *testing.T) {
	file := writeProviders(t, `
embeddingProviders:
  - name: test
    provider: fake
`)
	reg, err := LoadProvidersConfig(file)
	if err != nil {
		t.Fatalf("LoadProvidersConfig: %v", err)
	}
	if _, ok := reg.EmbeddingProvider("test"); !ok {
		t.Fatal("fake embedding provider not registered")
	}
}

func TestLoadProvidersConfigMissingFile(t *testing.T) {
	if _, err := LoadProvidersConfig(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
