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

// Embedding is a single dense vector produced for one input text.
type Embedding []float32

// Embedder turns text into dense vector embeddings. Implementations must be
// safe for concurrent use and should preserve input order: the i-th returned
// embedding corresponds to the i-th input string.
//
// Embedder is intentionally provider-agnostic so the embedding backend
// (OpenAI, Azure OpenAI, Ollama, a local model, or a test fake) can be swapped
// without touching the projection or retrieval code that depends on it.
type Embedder interface {
	// Embed returns one embedding per input text, in the same order. The
	// returned slice has the same length as texts. An empty input yields an
	// empty, non-error result.
	Embed(ctx context.Context, texts []string) ([]Embedding, error)

	// Dimensions reports the length of the vectors this embedder produces. It is
	// used to provision the Neo4J vector index. A value of 0 means the dimension
	// is not known until the first Embed call.
	Dimensions() int

	// Model returns a stable identifier for the embedding model in use, suitable
	// for recording alongside stored vectors so stale-model vectors can be
	// detected and refreshed.
	Model() string
}

// EmbedBatched is a helper that splits texts into batches of at most batchSize
// and concatenates the results, preserving order. It is useful for providers
// with per-request input limits. A batchSize <= 0 embeds everything in a single
// call.
func EmbedBatched(ctx context.Context, e Embedder, texts []string, batchSize int) ([]Embedding, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if batchSize <= 0 || batchSize >= len(texts) {
		return e.Embed(ctx, texts)
	}

	out := make([]Embedding, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := min(start+batchSize, len(texts))
		batch, err := e.Embed(ctx, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("embedding batch [%d:%d]: %w", start, end, err)
		}
		if len(batch) != end-start {
			return nil, fmt.Errorf("embedder returned %d vectors for a batch of %d", len(batch), end-start)
		}
		out = append(out, batch...)
	}
	return out, nil
}
