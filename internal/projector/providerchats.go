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

	"github.com/project-astron/astron/internal/rag"
)

// SecretReader reads a single key from a Kubernetes Secret and returns its
// value. It abstracts the Kubernetes client so provider resolution stays
// testable.
type SecretReader func(ctx context.Context, namespace, name, key string) (string, error)

// BuildProviderChats resolves the controller-wide chat providers into ready
// Chats, reading each provider's API key from its Secret via read (the fake
// provider and any key-less provider need no read, so a nil read is fine when
// only those are configured). defaultNamespace is used for Secret references
// that omit a namespace.
//
// Providers whose Secret cannot be read, or whose configuration is invalid,
// are skipped and reported in the returned warnings rather than failing the
// whole set — so one misconfigured provider (e.g. a missing OpenAI key) never
// prevents the others (e.g. a token-free "fake" provider) from loading.
func BuildProviderChats(ctx context.Context, reg *rag.ProviderRegistry, defaultNamespace string, read SecretReader) (map[string]rag.Chat, []string) {
	chats := map[string]rag.Chat{}
	if reg == nil {
		return chats, nil
	}
	var warnings []string
	for _, name := range reg.ChatProviderNames() {
		p, ok := reg.ChatProvider(name)
		if !ok {
			continue
		}
		apiKey := ""
		if p.APIKeySecret != nil {
			if read == nil {
				warnings = append(warnings, fmt.Sprintf("chat provider %q references a Secret but no reader is available", name))
				continue
			}
			ns := p.APIKeySecret.Namespace
			if ns == "" {
				ns = defaultNamespace
			}
			key, err := read(ctx, ns, p.APIKeySecret.Name, p.APIKeySecret.DataKey())
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("chat provider %q: %v", name, err))
				continue
			}
			apiKey = key
		}
		chat, err := rag.NewChat(p.ChatConfig(apiKey))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("chat provider %q: %v", name, err))
			continue
		}
		chats[name] = chat
	}
	return chats, warnings
}
