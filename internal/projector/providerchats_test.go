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

package projector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/project-astron/astron/internal/rag"
)

func loadTestRegistry(t *testing.T, content string) *rag.ProviderRegistry {
	t.Helper()
	file := filepath.Join(t.TempDir(), "providers.yaml")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := rag.LoadProvidersConfig(file)
	if err != nil {
		t.Fatalf("LoadProvidersConfig: %v", err)
	}
	return reg
}

func TestBuildProviderChats(t *testing.T) {
	reg := loadTestRegistry(t, `
chatProviders:
  - name: fake
    provider: fake
  - name: openai
    provider: openai
    model: gpt-4o-mini
    apiKeySecret:
      name: openai-key
  - name: broken
    provider: openai
    model: gpt-4o
    apiKeySecret:
      name: missing-key
`)
	read := func(_ context.Context, ns, name, key string) (string, error) {
		if name == "openai-key" && key == "apiKey" {
			return "sk-test", nil
		}
		return "", fmt.Errorf("secret %s/%s missing", ns, name)
	}

	chats, warnings := BuildProviderChats(context.Background(), reg, "astron", read)

	// fake (no Secret) and openai (Secret resolved) load; broken is skipped.
	if _, ok := chats["fake"]; !ok {
		t.Errorf("fake provider chat missing")
	}
	if _, ok := chats["openai"]; !ok {
		t.Errorf("openai provider chat missing")
	}
	if _, ok := chats["broken"]; ok {
		t.Errorf("broken provider should have been skipped")
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one warning for the broken provider, got %v", warnings)
	}
}

func TestBuildProviderChatsFakeNeedsNoReader(t *testing.T) {
	reg := loadTestRegistry(t, `
chatProviders:
  - name: fake
    provider: fake
`)
	// A nil reader is fine when only key-less providers are configured.
	chats, warnings := BuildProviderChats(context.Background(), reg, "astron", nil)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	chat, ok := chats["fake"]
	if !ok {
		t.Fatal("fake provider chat missing")
	}
	// The fake chat echoes the last user message.
	reply, err := chat.Complete(context.Background(), []rag.Message{{Role: rag.RoleUser, Content: "ping"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if reply != "ping" {
		t.Fatalf("fake chat reply = %q, want echo of %q", reply, "ping")
	}
}

func TestBuildProviderChatsSecretWithoutReader(t *testing.T) {
	reg := loadTestRegistry(t, `
chatProviders:
  - name: openai
    provider: openai
    model: gpt-4o-mini
    apiKeySecret:
      name: openai-key
`)
	chats, warnings := BuildProviderChats(context.Background(), reg, "astron", nil)
	if len(chats) != 0 {
		t.Fatalf("expected no chats without a reader, got %v", chats)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected a warning, got %v", warnings)
	}
}

func TestBuildProviderChatsNilRegistry(t *testing.T) {
	chats, warnings := BuildProviderChats(context.Background(), nil, "astron", nil)
	if len(chats) != 0 || len(warnings) != 0 {
		t.Fatalf("nil registry should yield nothing, got chats=%v warnings=%v", chats, warnings)
	}
}
