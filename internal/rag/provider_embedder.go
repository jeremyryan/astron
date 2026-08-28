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
)

// SecretReader reads a single key from a Kubernetes Secret and returns its
// value. It abstracts the Kubernetes client dependency out of this package so
// provider resolution stays testable and rag has no client-go/
// controller-runtime dependency of its own.
//
// This mirrors the equivalent type used to resolve controller-wide chat
// providers (internal/projector's SecretReader, used by BuildProviderChats);
// it is defined again here, rather than shared, so this package doesn't need
// to import internal/projector for a one-line function type.
type SecretReader func(ctx context.Context, namespace, name, key string) (string, error)

// BuildProviderEmbedder resolves one named controller-wide embedding provider
// (from reg) into a live Embedder, reading its API key Secret (if any) via
// read. defaultNamespace is used for a SecretKeyRef that doesn't specify its
// own namespace.
//
// Unlike BuildProviderChats (which resolves every configured chat provider
// and reports per-provider failures as warnings, since any subset of them may
// be usable), this resolves exactly the one named provider a caller has
// already chosen to depend on, so a failure here is a hard error: there is no
// fallback embedding provider to fall back to.
func BuildProviderEmbedder(
	ctx context.Context, reg *ProviderRegistry, defaultNamespace, name string, read SecretReader,
) (Embedder, error) {
	if reg == nil {
		return nil, fmt.Errorf("embedding provider %q is not configured", name)
	}
	cfg, ok := reg.EmbeddingProvider(name)
	if !ok {
		return nil, fmt.Errorf("embedding provider %q is not configured", name)
	}

	apiKey := ""
	if cfg.APIKeySecret != nil {
		if read == nil {
			return nil, fmt.Errorf("embedding provider %q references a Secret but no reader is available", name)
		}
		ns := cfg.APIKeySecret.Namespace
		if ns == "" {
			ns = defaultNamespace
		}
		key, err := read(ctx, ns, cfg.APIKeySecret.Name, cfg.APIKeySecret.DataKey())
		if err != nil {
			return nil, fmt.Errorf("embedding provider %q: %w", name, err)
		}
		apiKey = key
	}

	emb, err := NewEmbedder(cfg.EmbedderConfig(apiKey))
	if err != nil {
		return nil, fmt.Errorf("embedding provider %q: %w", name, err)
	}
	return emb, nil
}
