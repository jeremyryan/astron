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

package crdschema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

// schemaCandidate is one rendered CRD-version document under consideration
// for a write, alongside the identity/content-hash bookkeeping syncCRDs needs.
type schemaCandidate struct {
	schema  rag.CRDSchema
	crdName string
	key     string
	hash    string
}

// syncCRDs renders, diffs, embeds and writes schema documents for the given
// CRDs (already filtered by the name allow-list), then deletes any
// previously captured document no longer present. It is the reconciliation
// core, separated from Sync's informer listing so it can be exercised
// directly against hand-built CRDs in tests.
//
// Because a Syncer is the store's only writer, deletion is a plain diff
// against lastKeys (the full key set this Syncer wrote last time): any key
// missing from the current selection is stale and removed. lastKeys lives in
// memory, so a controller restart resets it; a CRD deleted from the cluster
// entirely while the controller was down leaves its schema orphaned until
// the controller restarts with a chance to observe the deletion itself. This
// is judged an acceptable, narrow edge case (see docs/crd-schema-design.md).
func (s *Syncer) syncCRDs(ctx context.Context, crds []*apiextensionsv1.CustomResourceDefinition) (Result, error) {
	candidates, currentKeys := renderCandidates(crds)
	result := Result{CRDs: len(crds), Versions: len(candidates)}

	if len(candidates) > 0 {
		changed, err := s.changedCandidates(ctx, candidates)
		if err != nil {
			return Result{}, err
		}
		result.Changed = len(changed)
		if len(changed) > 0 {
			if err := s.writeChanged(ctx, changed); err != nil {
				return Result{}, err
			}
			for _, c := range changed {
				if c.schema.Storage {
					result.Embedded++
				}
			}
		}
	}

	deleted, err := s.deleteStale(ctx, currentKeys)
	if err != nil {
		return Result{}, err
	}
	result.Deleted = deleted

	s.mu.Lock()
	s.lastKeys = currentKeys
	s.mu.Unlock()

	return result, nil
}

// renderCandidates renders every served version of every given CRD into a
// schemaCandidate, and the full set of keys they occupy.
func renderCandidates(crds []*apiextensionsv1.CustomResourceDefinition) ([]schemaCandidate, map[string]bool) {
	var candidates []schemaCandidate
	currentKeys := make(map[string]bool)
	for _, crd := range crds {
		for _, sc := range rag.RenderCRDSchemas(crd) {
			key := graph.ResourceSchemaKey(sc.Group, sc.Version, sc.Kind)
			currentKeys[key] = true
			candidates = append(candidates, schemaCandidate{
				schema:  sc,
				crdName: crd.Name,
				key:     key,
				hash:    overviewHash(sc.Overview),
			})
		}
	}
	return candidates, currentKeys
}

// changedCandidates returns the subset of candidates that are new or whose
// overview hash differs from what's currently stored.
func (s *Syncer) changedCandidates(ctx context.Context, candidates []schemaCandidate) ([]schemaCandidate, error) {
	keys := make([]string, len(candidates))
	for i, c := range candidates {
		keys[i] = c.key
	}
	existing, err := s.opts.Store.ExistingResourceSchemaHashes(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("reading existing resource schema hashes: %w", err)
	}
	var changed []schemaCandidate
	for _, c := range candidates {
		if existing[c.key] != c.hash {
			changed = append(changed, c)
		}
	}
	return changed, nil
}

// deleteStale removes any previously written key absent from currentKeys.
func (s *Syncer) deleteStale(ctx context.Context, currentKeys map[string]bool) (int, error) {
	s.mu.Lock()
	lastKeys := s.lastKeys
	s.mu.Unlock()

	var stale []string
	for key := range lastKeys {
		if !currentKeys[key] {
			stale = append(stale, key)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}
	if err := s.opts.Store.DeleteResourceSchemas(ctx, stale); err != nil {
		return 0, fmt.Errorf("deleting %d stale resource schemas: %w", len(stale), err)
	}
	return len(stale), nil
}

// writeChanged embeds each storage-version entry in changed (batched, one
// provider call) and upserts every entry — storage and non-storage — as a
// graph.ResourceSchema. A non-storage entry never carries an embedding: only
// a CRD's storage version is ever embedded (see docs/crd-schema-design.md's
// two-tier retrieval), so leaving Embedding unset for it is correct, not a
// missed update.
func (s *Syncer) writeChanged(ctx context.Context, changed []schemaCandidate) error {
	vectors, err := s.embedStorageOverviews(ctx, changed)
	if err != nil {
		return err
	}

	model := s.opts.Embedder.Model()
	rows := make([]graph.ResourceSchema, len(changed))
	for i, c := range changed {
		row := graph.ResourceSchema{
			Group:        c.schema.Group,
			Version:      c.schema.Version,
			Kind:         c.schema.Kind,
			Scope:        c.schema.Scope,
			CRDName:      c.crdName,
			Storage:      c.schema.Storage,
			Overview:     c.schema.Overview,
			OverviewHash: c.hash,
			FullDoc:      c.schema.Full,
		}
		if vec, ok := vectors[i]; ok {
			row.Embedding = vec
			row.EmbeddingModel = model
		}
		rows[i] = row
	}

	if err := s.opts.Store.UpsertResourceSchemas(ctx, rows); err != nil {
		return fmt.Errorf("upserting %d resource schemas: %w", len(rows), err)
	}
	return nil
}

// embedStorageOverviews embeds the Overview of every storage-version entry in
// changed, in one batched call, and ensures the schema vector index exists
// before returning any vectors. The result maps each vector back to its
// index in changed (not a separate compacted slice), so writeChanged can
// attach it to the right row.
func (s *Syncer) embedStorageOverviews(ctx context.Context, changed []schemaCandidate) (map[int][]float32, error) {
	var texts []string
	var indexes []int
	for i, c := range changed {
		if c.schema.Storage {
			indexes = append(indexes, i)
			texts = append(texts, c.schema.Overview)
		}
	}
	if len(texts) == 0 {
		return nil, nil
	}

	embedded, err := rag.EmbedBatched(ctx, s.opts.Embedder, texts, s.opts.EmbeddingBatchSize)
	if err != nil {
		return nil, fmt.Errorf("embedding %d CRD schema overviews: %w", len(texts), err)
	}
	if len(embedded) != len(texts) {
		return nil, fmt.Errorf("embedder returned %d vectors for %d overviews", len(embedded), len(texts))
	}
	if err := s.ensureVectorIndex(ctx, len(embedded[0])); err != nil {
		return nil, err
	}

	vectors := make(map[int][]float32, len(indexes))
	for i, idx := range indexes {
		vectors[idx] = []float32(embedded[i])
	}
	return vectors, nil
}

// ensureVectorIndex lazily creates the schema overview vector index on first
// use, mirroring internal/projector's own ensureVectorIndex. dims comes from
// the embedder when it reports one, otherwise from fallbackDims (the length
// of an actual embedding).
func (s *Syncer) ensureVectorIndex(ctx context.Context, fallbackDims int) error {
	s.mu.Lock()
	ready := s.vectorIndexReady
	s.mu.Unlock()
	if ready {
		return nil
	}

	dims := s.opts.Embedder.Dimensions()
	if dims <= 0 {
		dims = fallbackDims
	}
	if err := s.opts.Store.EnsureResourceSchemaVectorIndex(ctx, dims, s.opts.VectorSimilarity); err != nil {
		return fmt.Errorf("ensuring resource schema vector index: %w", err)
	}

	s.mu.Lock()
	s.vectorIndexReady = true
	s.mu.Unlock()
	return nil
}

// overviewHash returns the hex-encoded SHA-256 of a rendered overview, used
// to skip unchanged CRD versions without re-embedding or re-writing them.
func overviewHash(overview string) string {
	sum := sha256.Sum256([]byte(overview))
	return hex.EncodeToString(sum[:])
}
