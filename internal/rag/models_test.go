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
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListModelsFakeProvider(t *testing.T) {
	got, err := ListModels(context.Background(), ChatConfig{Provider: ProviderFake})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 1 || got[0] != "fake" {
		t.Fatalf("unexpected models: %v", got)
	}
}

func TestListModelsAzureReturnsConfiguredModel(t *testing.T) {
	got, err := ListModels(context.Background(), ChatConfig{Provider: ProviderAzureOpenAI, Model: "my-deployment"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 1 || got[0] != "my-deployment" {
		t.Fatalf("unexpected models: %v", got)
	}
}

func TestListModelsOpenAICompatible(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "gpt-b"}, {"id": "gpt-a"}},
		})
	}))
	defer ts.Close()

	got, err := ListModels(context.Background(), ChatConfig{
		Provider: ProviderOpenAI, APIKey: "k", BaseURL: ts.URL,
	})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	// The list is sorted for stable output.
	if len(got) != 2 || got[0] != "gpt-a" || got[1] != "gpt-b" {
		t.Fatalf("unexpected models: %v", got)
	}
	if gotAuth != "Bearer k" {
		t.Errorf("Authorization = %q, want bearer key", gotAuth)
	}
}

func TestListModelsProviderError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "bad key"}})
	}))
	defer ts.Close()

	_, err := ListModels(context.Background(), ChatConfig{Provider: ProviderOpenAI, BaseURL: ts.URL})
	if err == nil {
		t.Fatal("expected an error from a 401 response")
	}
}

func TestOpenAIChatWithModel(t *testing.T) {
	var gotModel string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "hi"}}},
		})
	}))
	defer ts.Close()

	base, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "gpt-default", BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	override := base.WithModel("gpt-other")
	if override.Model() != "gpt-other" {
		t.Fatalf("Model() = %q, want gpt-other", override.Model())
	}
	if base.Model() != "gpt-default" {
		t.Fatalf("original chat mutated: %q", base.Model())
	}
	if _, err := override.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hello"}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotModel != "gpt-other" {
		t.Fatalf("request used model %q, want gpt-other", gotModel)
	}
}
