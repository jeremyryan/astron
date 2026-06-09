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

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/rag"
)

func embeddingScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := gamerav1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newReconciler(t *testing.T, objs ...client.Object) *GraphProjectionReconciler {
	t.Helper()
	scheme := embeddingScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &GraphProjectionReconciler{Client: c, Scheme: scheme}
}

func boolPtr(b bool) *bool { return &b }

func TestResolveEmbeddingConfigDisabledWhenAbsent(t *testing.T) {
	r := newReconciler(t)
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
	}
	emb, err := r.resolveEmbeddingConfig(context.Background(), proj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emb.Enabled {
		t.Errorf("expected disabled embedding config when graphRAG absent")
	}
}

func TestResolveEmbeddingConfigReadsSecretAndDefaults(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "emb-creds", Namespace: "default"},
		Data:       map[string][]byte{"apiKey": []byte("sk-test")},
	}
	r := newReconciler(t, secret)

	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Spec: gamerav1alpha1.GraphProjectionSpec{
			GraphRAG: &gamerav1alpha1.GraphRAGSpec{
				Enabled: true,
				Embedding: gamerav1alpha1.EmbeddingConfig{
					Provider:   "openai",
					Model:      "text-embedding-3-small",
					Dimensions: 1536,
					AuthSecretRef: &gamerav1alpha1.EmbeddingSecretReference{
						Name: "emb-creds",
					},
				},
			},
		},
	}

	emb, err := r.resolveEmbeddingConfig(context.Background(), proj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !emb.Enabled {
		t.Fatal("expected enabled embedding config")
	}
	if emb.Embedder.APIKey != "sk-test" {
		t.Errorf("API key = %q, want sk-test", emb.Embedder.APIKey)
	}
	if emb.Embedder.Provider != rag.ProviderOpenAI || emb.Embedder.Model != "text-embedding-3-small" || emb.Embedder.Dimensions != 1536 {
		t.Errorf("unexpected embedder config: %+v", emb.Embedder)
	}
	// Defaults: cosine similarity, labels-in / annotations-out cards.
	if emb.Similarity != "cosine" {
		t.Errorf("similarity = %q, want cosine", emb.Similarity)
	}
	if !emb.CardOptions.IncludeLabels || emb.CardOptions.IncludeAnnotations {
		t.Errorf("unexpected card options: %+v", emb.CardOptions)
	}
}

func TestResolveEmbeddingConfigHonorsOverrides(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("xyz")},
	}
	r := newReconciler(t, secret)

	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Spec: gamerav1alpha1.GraphProjectionSpec{
			GraphRAG: &gamerav1alpha1.GraphRAGSpec{
				Enabled: true,
				Embedding: gamerav1alpha1.EmbeddingConfig{
					Provider: "azure",
					Model:    "embed",
					BaseURL:  "https://example.openai.azure.com",
					AuthSecretRef: &gamerav1alpha1.EmbeddingSecretReference{
						Name:      "creds",
						APIKeyKey: "token",
					},
				},
				Include:     &gamerav1alpha1.CardInclude{Labels: boolPtr(false), Annotations: true},
				VectorIndex: &gamerav1alpha1.VectorIndexConfig{Similarity: "euclidean"},
			},
		},
	}

	emb, err := r.resolveEmbeddingConfig(context.Background(), proj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emb.Embedder.APIKey != "xyz" || emb.Embedder.BaseURL != "https://example.openai.azure.com" {
		t.Errorf("override secret/baseURL not applied: %+v", emb.Embedder)
	}
	if emb.Similarity != "euclidean" {
		t.Errorf("similarity = %q, want euclidean", emb.Similarity)
	}
	if emb.CardOptions.IncludeLabels || !emb.CardOptions.IncludeAnnotations {
		t.Errorf("card include overrides not applied: %+v", emb.CardOptions)
	}
}

func TestResolveEmbeddingConfigResolvesChat(t *testing.T) {
	embSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "emb", Namespace: "default"},
		Data:       map[string][]byte{"apiKey": []byte("emb-key")},
	}
	chatSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "chat", Namespace: "default"},
		Data:       map[string][]byte{"apiKey": []byte("chat-key")},
	}
	r := newReconciler(t, embSecret, chatSecret)

	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Spec: gamerav1alpha1.GraphProjectionSpec{
			GraphRAG: &gamerav1alpha1.GraphRAGSpec{
				Enabled: true,
				Embedding: gamerav1alpha1.EmbeddingConfig{
					Provider:      "openai",
					Model:         "text-embedding-3-small",
					AuthSecretRef: &gamerav1alpha1.EmbeddingSecretReference{Name: "emb"},
				},
				Chat: &gamerav1alpha1.ChatModelConfig{
					Enabled:       true,
					Provider:      "openai",
					Model:         "gpt-4o-mini",
					AuthSecretRef: &gamerav1alpha1.EmbeddingSecretReference{Name: "chat"},
				},
			},
		},
	}

	emb, err := r.resolveEmbeddingConfig(context.Background(), proj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !emb.ChatEnabled {
		t.Fatal("expected chat to be enabled")
	}
	if emb.Chat.APIKey != "chat-key" || emb.Chat.Model != "gpt-4o-mini" {
		t.Errorf("unexpected chat config: %+v", emb.Chat)
	}
	// Embedding and chat keys are resolved independently.
	if emb.Embedder.APIKey != "emb-key" {
		t.Errorf("embedding key cross-contaminated: %q", emb.Embedder.APIKey)
	}
}

func TestResolveEmbeddingConfigChatDisabledByDefault(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "emb", Namespace: "default"},
		Data:       map[string][]byte{"apiKey": []byte("k")},
	}
	r := newReconciler(t, secret)
	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Spec: gamerav1alpha1.GraphProjectionSpec{
			GraphRAG: &gamerav1alpha1.GraphRAGSpec{
				Enabled:   true,
				Embedding: gamerav1alpha1.EmbeddingConfig{Provider: "openai", Model: "m", AuthSecretRef: &gamerav1alpha1.EmbeddingSecretReference{Name: "emb"}},
			},
		},
	}
	emb, err := r.resolveEmbeddingConfig(context.Background(), proj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emb.ChatEnabled {
		t.Error("chat should be disabled when no chat block is present")
	}
}

func TestResolveEmbeddingConfigMissingSecretKeyErrors(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "default"},
		Data:       map[string][]byte{"wrong": []byte("x")},
	}
	r := newReconciler(t, secret)

	proj := &gamerav1alpha1.GraphProjection{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
		Spec: gamerav1alpha1.GraphProjectionSpec{
			GraphRAG: &gamerav1alpha1.GraphRAGSpec{
				Enabled:   true,
				Embedding: gamerav1alpha1.EmbeddingConfig{Provider: "openai", Model: "m", AuthSecretRef: &gamerav1alpha1.EmbeddingSecretReference{Name: "creds"}},
			},
		},
	}
	if _, err := r.resolveEmbeddingConfig(context.Background(), proj); err == nil {
		t.Fatal("expected error when secret is missing the api key")
	}
}
