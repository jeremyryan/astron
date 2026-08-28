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
	"testing"
)

func TestBuildProviderEmbedderFakeNeedsNoReader(t *testing.T) {
	reg, err := LoadProvidersConfig(writeProviders(t, `
embeddingProviders:
  - name: fake
    provider: fake
`))
	if err != nil {
		t.Fatal(err)
	}

	emb, err := BuildProviderEmbedder(context.Background(), reg, "astron", "fake", nil)
	if err != nil {
		t.Fatalf("BuildProviderEmbedder: %v", err)
	}
	vecs, err := emb.Embed(context.Background(), []string{"hello"})
	if err != nil || len(vecs) != 1 {
		t.Fatalf("Embed: vecs=%v err=%v", vecs, err)
	}
}

func TestBuildProviderEmbedderResolvesSecret(t *testing.T) {
	reg, err := LoadProvidersConfig(writeProviders(t, `
embeddingProviders:
  - name: openai
    provider: openai
    model: test-embed-model
    apiKeySecret:
      name: astron-embeddings
`))
	if err != nil {
		t.Fatal(err)
	}

	var gotNS, gotName, gotKey string
	read := func(_ context.Context, ns, name, key string) (string, error) {
		gotNS, gotName, gotKey = ns, name, key
		return "sk-test", nil
	}

	emb, err := BuildProviderEmbedder(context.Background(), reg, "astron", "openai", read)
	if err != nil {
		t.Fatalf("BuildProviderEmbedder: %v", err)
	}
	if emb.Model() != "test-embed-model" {
		t.Errorf("Model() = %q", emb.Model())
	}
	if gotNS != "astron" || gotName != "astron-embeddings" || gotKey != DefaultProviderAPIKeyKey {
		t.Errorf("read called with (%q, %q, %q)", gotNS, gotName, gotKey)
	}
}

func TestBuildProviderEmbedderUnknownProvider(t *testing.T) {
	reg, err := LoadProvidersConfig(writeProviders(t, `
embeddingProviders:
  - name: fake
    provider: fake
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildProviderEmbedder(context.Background(), reg, "astron", "missing", nil); err == nil {
		t.Fatal("expected an error for an unconfigured provider")
	}
}

func TestBuildProviderEmbedderNilRegistry(t *testing.T) {
	if _, err := BuildProviderEmbedder(context.Background(), nil, "astron", "openai", nil); err == nil {
		t.Fatal("expected an error for a nil registry")
	}
}

func TestBuildProviderEmbedderMissingReader(t *testing.T) {
	reg, err := LoadProvidersConfig(writeProviders(t, `
embeddingProviders:
  - name: openai
    provider: openai
    model: test-embed-model
    apiKeySecret:
      name: astron-embeddings
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildProviderEmbedder(context.Background(), reg, "astron", "openai", nil); err == nil {
		t.Fatal("expected an error when a Secret is referenced but no reader is available")
	}
}

func TestBuildProviderEmbedderSecretReadFailure(t *testing.T) {
	reg, err := LoadProvidersConfig(writeProviders(t, `
embeddingProviders:
  - name: openai
    provider: openai
    model: test-embed-model
    apiKeySecret:
      name: astron-embeddings
      namespace: other-ns
`))
	if err != nil {
		t.Fatal(err)
	}
	read := func(_ context.Context, ns, name, _ string) (string, error) {
		return "", fmt.Errorf("secret %s/%s not found", ns, name)
	}
	if _, err := BuildProviderEmbedder(context.Background(), reg, "astron", "openai", read); err == nil {
		t.Fatal("expected the Secret read failure to propagate")
	}
}
