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

// temperatureSent reads a chat-completions request body, reporting whether the
// temperature field was present (nil pointer => omitted).
func temperatureSent(t *testing.T, r *http.Request) bool {
	t.Helper()
	var body struct {
		Temperature *float64 `json:"temperature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}
	return body.Temperature != nil
}

// TestOpenAIChatRetriesWithoutTemperature verifies that when a model rejects an
// explicit temperature, Complete transparently retries without it and returns
// the successful reply.
func TestOpenAIChatRetriesWithoutTemperature(t *testing.T) {
	var sentTemperature []bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hasTemp := temperatureSent(t, r)
		sentTemperature = append(sentTemperature, hasTemp)
		if hasTemp {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "Unsupported value: 'temperature' does not support 0 with this model. Only the default (1) value is supported.",
					"type":    "invalid_request_error",
					"param":   "temperature",
					"code":    "unsupported_value",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ok"}}},
		})
	}))
	defer ts.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "o3", BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}

	out, err := chat.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "ok" {
		t.Fatalf("Complete = %q, want ok", out)
	}
	if len(sentTemperature) != 2 || !sentTemperature[0] || sentTemperature[1] {
		t.Fatalf("expected [with-temp, without-temp] requests, got %v", sentTemperature)
	}

	// A subsequent call must skip temperature outright (no wasted retry),
	// having remembered the model's preference.
	sentTemperature = nil
	if _, err := chat.Complete(context.Background(), []Message{{Role: RoleUser, Content: "again"}}); err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	if len(sentTemperature) != 1 || sentTemperature[0] {
		t.Fatalf("expected a single without-temp request, got %v", sentTemperature)
	}
}

// TestOpenAIChatSendsTemperatureByDefault verifies the temperature is included
// on the happy path (models that accept it), and that non-temperature errors
// are not retried.
func TestOpenAIChatSendsTemperatureByDefault(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !temperatureSent(t, r) {
			t.Errorf("expected temperature to be sent")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "hi"}}},
		})
	}))
	defer ts.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "gpt-4o-mini", BaseURL: ts.URL, Temperature: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one request, got %d", calls)
	}
}

// TestOpenAIChatDoesNotRetryUnrelatedError verifies a non-temperature API error
// surfaces immediately without a second request.
func TestOpenAIChatDoesNotRetryUnrelatedError(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "invalid api key", "type": "invalid_request_error"},
		})
	}))
	defer ts.Close()

	chat, err := NewOpenAIChat(OpenAIChatConfig{APIKey: "k", Model: "gpt-4o-mini", BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Fatalf("expected exactly one request (no retry), got %d", calls)
	}
}
