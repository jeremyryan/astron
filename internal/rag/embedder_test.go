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
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFakeEmbedderIsDeterministicAndNormalized(t *testing.T) {
	e := NewFakeEmbedder(16)
	if e.Dimensions() != 16 {
		t.Fatalf("Dimensions() = %d, want 16", e.Dimensions())
	}

	got, err := e.Embed(context.Background(), []string{"pod web", "pod web", "service api"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d embeddings, want 3", len(got))
	}
	for i, v := range got {
		if len(v) != 16 {
			t.Errorf("embedding %d has length %d, want 16", i, len(v))
		}
		if norm := l2(v); math.Abs(norm-1) > 1e-5 {
			t.Errorf("embedding %d is not unit-normalized: norm=%v", i, norm)
		}
	}

	// Identical text -> identical vector; different text -> different vector.
	if !equalVec(got[0], got[1]) {
		t.Error("identical text produced different vectors")
	}
	if equalVec(got[0], got[2]) {
		t.Error("different text produced identical vectors")
	}
}

func TestEmbedBatchedPreservesOrderAndLength(t *testing.T) {
	e := NewFakeEmbedder(8)
	texts := []string{"a", "b", "c", "d", "e"}

	whole, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	batched, err := EmbedBatched(context.Background(), e, texts, 2)
	if err != nil {
		t.Fatalf("EmbedBatched: %v", err)
	}
	if len(batched) != len(texts) {
		t.Fatalf("batched length = %d, want %d", len(batched), len(texts))
	}
	for i := range whole {
		if !equalVec(whole[i], batched[i]) {
			t.Errorf("batched vector %d differs from single-call vector", i)
		}
	}
}

func TestEmbedBatchedEmptyInput(t *testing.T) {
	out, err := EmbedBatched(context.Background(), NewFakeEmbedder(4), nil, 3)
	if err != nil {
		t.Fatalf("EmbedBatched(nil): %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty result, got %d", len(out))
	}
}

func TestNewEmbedderFactory(t *testing.T) {
	if _, err := NewEmbedder(EmbedderConfig{}); err != nil {
		t.Errorf("empty config should default to fake embedder: %v", err)
	}
	if _, err := NewEmbedder(EmbedderConfig{Provider: ProviderFake, Dimensions: 32}); err != nil {
		t.Errorf("fake provider: %v", err)
	}
	if _, err := NewEmbedder(EmbedderConfig{Provider: ProviderOpenAI, Model: "m", APIKey: "k"}); err != nil {
		t.Errorf("openai provider: %v", err)
	}
	if _, err := NewEmbedder(EmbedderConfig{Provider: ProviderOpenAI, Model: "m"}); err == nil {
		t.Error("openai provider without API key should error")
	}
	if _, err := NewEmbedder(EmbedderConfig{Provider: "bogus"}); err == nil {
		t.Error("unknown provider should error")
	}
}

func TestOpenAIEmbedderHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Errorf("missing/incorrect auth header: %q", got)
		}
		if r.URL.Path != "/embeddings" {
			t.Errorf("unexpected path: %q", r.URL.Path)
		}
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		// Respond out of order to verify the client re-sorts by index.
		resp := map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": []float32{0.3, 0.4}},
				{"index": 0, "embedding": []float32{0.1, 0.2}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e, err := NewOpenAIEmbedder(OpenAIConfig{
		APIKey:  "secret-key",
		Model:   "text-embedding-3-small",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder: %v", err)
	}
	if e.Model() != "text-embedding-3-small" {
		t.Errorf("Model() = %q", e.Model())
	}

	out, err := e.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d embeddings, want 2", len(out))
	}
	if !equalVec(out[0], Embedding{0.1, 0.2}) || !equalVec(out[1], Embedding{0.3, 0.4}) {
		t.Errorf("embeddings out of order: %v", out)
	}
}

func TestOpenAIEmbedderAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "invalid api key", "type": "auth"},
		})
	}))
	defer srv.Close()

	e, _ := NewOpenAIEmbedder(OpenAIConfig{APIKey: "bad", Model: "m", BaseURL: srv.URL})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("expected an error for a 401 response")
	}
}

func TestOpenAIEmbedderCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"index": 0, "embedding": []float32{1}}},
		})
	}))
	defer srv.Close()

	e, _ := NewOpenAIEmbedder(OpenAIConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("expected an error when response count != input count")
	}
}

func l2(v Embedding) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func equalVec(a, b Embedding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
