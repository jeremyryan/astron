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
	"hash/fnv"
	"math"
)

// FakeEmbedder is a deterministic, dependency-free Embedder for tests and for
// running the operator without an external embedding provider. It maps text to
// a fixed-dimension unit vector by hashing, so identical text always yields an
// identical vector and different text almost always yields a different one.
//
// The vectors are L2-normalized, so a dot product between two of them is their
// cosine similarity. They carry no semantic meaning and must not be used for
// real retrieval quality — only for wiring, tests, and offline development.
type FakeEmbedder struct {
	// Dims is the vector length to produce. Defaults to 8 when zero.
	Dims int
}

// NewFakeEmbedder returns a FakeEmbedder producing vectors of the given
// dimension (or a small default when dims <= 0).
func NewFakeEmbedder(dims int) *FakeEmbedder {
	if dims <= 0 {
		dims = 8
	}
	return &FakeEmbedder{Dims: dims}
}

func (f *FakeEmbedder) dims() int {
	if f.Dims <= 0 {
		return 8
	}
	return f.Dims
}

// Embed returns one deterministic unit vector per input text, in order.
func (f *FakeEmbedder) Embed(_ context.Context, texts []string) ([]Embedding, error) {
	out := make([]Embedding, len(texts))
	for i, t := range texts {
		out[i] = f.vector(t)
	}
	return out, nil
}

// Dimensions reports the configured vector length.
func (f *FakeEmbedder) Dimensions() int { return f.dims() }

// Model identifies the fake embedder, including its dimension.
func (f *FakeEmbedder) Model() string {
	return "fake"
}

// vector derives a deterministic L2-normalized vector from the input text. Each
// component is seeded from an FNV hash of the text and the component index.
func (f *FakeEmbedder) vector(text string) Embedding {
	n := f.dims()
	v := make(Embedding, n)
	var sumSq float64
	for i := range n {
		h := fnv.New32a()
		_, _ = h.Write([]byte(text))
		_, _ = h.Write([]byte{byte(i), byte(i >> 8)})
		// Map the 32-bit hash into [-1, 1).
		x := float64(h.Sum32())/float64(math.MaxUint32)*2 - 1
		v[i] = float32(x)
		sumSq += x * x
	}
	norm := math.Sqrt(sumSq)
	if norm == 0 {
		// Degenerate (e.g. all-zero) case: return a stable unit basis vector.
		v[0] = 1
		return v
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v
}

// compile-time assertion that FakeEmbedder satisfies Embedder.
var _ Embedder = (*FakeEmbedder)(nil)
