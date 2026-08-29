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
	"errors"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

// fakeSchemaStore is an in-memory graph.SchemaStore for tests, recording
// calls for assertions.
type fakeSchemaStore struct {
	nodes map[string]graph.ResourceSchema

	ensureIndexCalls int
	ensureIndexDims  int
	upsertCalls      int
	deleteCalls      int
	lastDeletedKeys  []string

	hashesErr error
	upsertErr error
	deleteErr error
}

func newFakeSchemaStore() *fakeSchemaStore {
	return &fakeSchemaStore{nodes: map[string]graph.ResourceSchema{}}
}

func (f *fakeSchemaStore) EnsureResourceSchemaVectorIndex(_ context.Context, dims int, _ string) error {
	f.ensureIndexCalls++
	f.ensureIndexDims = dims
	return nil
}

func (f *fakeSchemaStore) ExistingResourceSchemaHashes(_ context.Context, keys []string) (map[string]string, error) {
	if f.hashesErr != nil {
		return nil, f.hashesErr
	}
	out := map[string]string{}
	for _, k := range keys {
		if n, ok := f.nodes[k]; ok {
			out[k] = n.OverviewHash
		}
	}
	return out, nil
}

func (f *fakeSchemaStore) UpsertResourceSchemas(_ context.Context, schemas []graph.ResourceSchema) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upsertCalls++
	for _, sc := range schemas {
		key := graph.ResourceSchemaKey(sc.Group, sc.Version, sc.Kind)
		existing, had := f.nodes[key]
		if had && len(sc.Embedding) == 0 {
			// Mirror the real store's "embedding-only-when-present" upsert:
			// a doc-only write preserves any previously stored embedding.
			sc.Embedding = existing.Embedding
			sc.EmbeddingModel = existing.EmbeddingModel
		}
		f.nodes[key] = sc
	}
	return nil
}

func (f *fakeSchemaStore) DeleteResourceSchemas(_ context.Context, keys []string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCalls++
	f.lastDeletedKeys = keys
	for _, k := range keys {
		delete(f.nodes, k)
	}
	return nil
}

func (f *fakeSchemaStore) ReadResourceSchema(_ context.Context, kind, version string) (string, bool, error) {
	for _, n := range f.nodes {
		if n.Kind != kind {
			continue
		}
		if version != "" && n.Version != version {
			continue
		}
		if version == "" && !n.Storage {
			continue
		}
		return n.FullDoc, true, nil
	}
	return "", false, nil
}

func (f *fakeSchemaStore) SearchResourceSchemas(context.Context, []float32, int) ([]graph.ResourceSchemaHit, error) {
	return nil, nil
}

// widgetCRD builds a small, single-version CRD for tests.
func widgetCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "widgets.example.com"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "Widget"},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"size": {Type: "integer"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// gadgetCRD builds a two-version CRD (a served-but-not-storage v1beta1 plus a
// storage v1) for tests exercising multi-version behavior.
func gadgetCRD(name string) *apiextensionsv1.CustomResourceDefinition {
	schema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"speed": {Type: "integer"},
			}},
		},
	}
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "Gadget"},
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1beta1", Served: true, Storage: false, Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: schema}},
				{Name: "v1", Served: true, Storage: true, Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: schema}},
			},
		},
	}
}

func newTestSyncer(store graph.SchemaStore) *Syncer {
	return NewSyncer(Options{
		Store:    store,
		Embedder: rag.NewFakeEmbedder(8),
	})
}

func TestSyncCRDsWritesAndEmbedsStorageVersionOnly(t *testing.T) {
	store := newFakeSchemaStore()
	s := newTestSyncer(store)

	result, err := s.syncCRDs(context.Background(), []*apiextensionsv1.CustomResourceDefinition{gadgetCRD("gadgets.example.com")})
	if err != nil {
		t.Fatalf("syncCRDs: %v", err)
	}
	if result.CRDs != 1 || result.Versions != 2 || result.Changed != 2 || result.Embedded != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(store.nodes) != 2 {
		t.Fatalf("store has %d nodes, want 2", len(store.nodes))
	}

	storageKey := graph.ResourceSchemaKey("example.com", "v1", "Gadget")
	servedKey := graph.ResourceSchemaKey("example.com", "v1beta1", "Gadget")
	if len(store.nodes[storageKey].Embedding) == 0 {
		t.Error("storage version was not embedded")
	}
	if len(store.nodes[servedKey].Embedding) != 0 {
		t.Error("non-storage version should not be embedded")
	}
	if store.ensureIndexCalls != 1 {
		t.Errorf("ensureIndexCalls = %d, want 1", store.ensureIndexCalls)
	}
}

func TestSyncCRDsSkipsUnchangedOnSecondSync(t *testing.T) {
	store := newFakeSchemaStore()
	s := newTestSyncer(store)
	crds := []*apiextensionsv1.CustomResourceDefinition{widgetCRD()}

	if _, err := s.syncCRDs(context.Background(), crds); err != nil {
		t.Fatalf("first syncCRDs: %v", err)
	}
	result, err := s.syncCRDs(context.Background(), crds)
	if err != nil {
		t.Fatalf("second syncCRDs: %v", err)
	}
	if result.Changed != 0 || result.Embedded != 0 {
		t.Fatalf("second sync of unchanged CRD should be a no-op, got %+v", result)
	}
	// The vector index is only created once, on the first embed.
	if store.ensureIndexCalls != 1 {
		t.Errorf("ensureIndexCalls = %d, want 1", store.ensureIndexCalls)
	}
}

func TestSyncCRDsReembedsOnOverviewChange(t *testing.T) {
	store := newFakeSchemaStore()
	s := newTestSyncer(store)

	widget := widgetCRD()
	if _, err := s.syncCRDs(context.Background(), []*apiextensionsv1.CustomResourceDefinition{widget}); err != nil {
		t.Fatalf("first syncCRDs: %v", err)
	}

	// Change the schema (a new top-level spec field), which changes Overview.
	widget.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"size":  {Type: "integer"},
			"color": {Type: "string"},
		},
	}
	result, err := s.syncCRDs(context.Background(), []*apiextensionsv1.CustomResourceDefinition{widget})
	if err != nil {
		t.Fatalf("second syncCRDs: %v", err)
	}
	if result.Changed != 1 || result.Embedded != 1 {
		t.Fatalf("expected the changed overview to be re-embedded, got %+v", result)
	}
}

func TestSyncCRDsDeletesStaleVersions(t *testing.T) {
	store := newFakeSchemaStore()
	s := newTestSyncer(store)

	gadget := gadgetCRD("gadgets.example.com")
	if _, err := s.syncCRDs(context.Background(), []*apiextensionsv1.CustomResourceDefinition{gadget}); err != nil {
		t.Fatalf("first syncCRDs: %v", err)
	}
	if len(store.nodes) != 2 {
		t.Fatalf("expected 2 nodes after first sync, got %d", len(store.nodes))
	}

	// The CRD no longer serves v1beta1.
	gadget.Spec.Versions = gadget.Spec.Versions[1:]
	result, err := s.syncCRDs(context.Background(), []*apiextensionsv1.CustomResourceDefinition{gadget})
	if err != nil {
		t.Fatalf("second syncCRDs: %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("result.Deleted = %d, want 1", result.Deleted)
	}
	if len(store.nodes) != 1 {
		t.Fatalf("expected 1 node remaining, got %d", len(store.nodes))
	}
}

func TestSyncCRDsDeletesEverythingWhenCRDRemoved(t *testing.T) {
	store := newFakeSchemaStore()
	s := newTestSyncer(store)
	widget := widgetCRD()

	if _, err := s.syncCRDs(context.Background(), []*apiextensionsv1.CustomResourceDefinition{widget}); err != nil {
		t.Fatalf("first syncCRDs: %v", err)
	}
	result, err := s.syncCRDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("second syncCRDs: %v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("result.Deleted = %d, want 1", result.Deleted)
	}
	if len(store.nodes) != 0 {
		t.Fatalf("expected an empty store, got %d nodes", len(store.nodes))
	}
}

func TestSyncCRDsFirstSyncNeverDeletes(t *testing.T) {
	store := newFakeSchemaStore()
	s := newTestSyncer(store)
	// A fresh Syncer's lastKeys is empty, so even an empty CRD list on the
	// very first sync must not attempt any deletion.
	result, err := s.syncCRDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("syncCRDs: %v", err)
	}
	if result.Deleted != 0 || store.deleteCalls != 0 {
		t.Fatalf("expected no deletion on the first sync, got %+v (deleteCalls=%d)", result, store.deleteCalls)
	}
}

// TestSyncerSelectedAppliesNameAllowList exercises Syncer.selected, the
// allow-list filter Sync applies before candidates ever reach syncCRDs
// (syncCRDs itself has no allow-list concept: it operates on whatever CRDs
// it's given, already filtered).
func TestSyncerSelectedAppliesNameAllowList(t *testing.T) {
	s := NewSyncer(Options{
		Store:    newFakeSchemaStore(),
		Embedder: rag.NewFakeEmbedder(8),
		Names:    []string{"widgets.example.com"},
	})
	if !s.selected("widgets.example.com") {
		t.Error("expected widgets.example.com to be selected")
	}
	if s.selected("gadgets.example.com") {
		t.Error("expected gadgets.example.com to be excluded by the allow-list")
	}
}

// TestSyncerSelectedEmptyAllowListSelectsEverything verifies an empty (the
// default) allow-list captures every CRD, not none.
func TestSyncerSelectedEmptyAllowListSelectsEverything(t *testing.T) {
	s := newTestSyncer(newFakeSchemaStore())
	if !s.selected("anything.example.com") {
		t.Error("expected an empty allow-list to select every CRD")
	}
}

func TestSyncCRDsPropagatesHashLookupError(t *testing.T) {
	store := newFakeSchemaStore()
	store.hashesErr = errors.New("boom")
	s := newTestSyncer(store)

	if _, err := s.syncCRDs(context.Background(), []*apiextensionsv1.CustomResourceDefinition{widgetCRD()}); err == nil {
		t.Fatal("expected an error from ExistingResourceSchemaHashes to propagate")
	}
}

func TestSyncCRDsPropagatesUpsertError(t *testing.T) {
	store := newFakeSchemaStore()
	store.upsertErr = errors.New("boom")
	s := newTestSyncer(store)

	if _, err := s.syncCRDs(context.Background(), []*apiextensionsv1.CustomResourceDefinition{widgetCRD()}); err == nil {
		t.Fatal("expected an error from UpsertResourceSchemas to propagate")
	}
}

func TestSyncCRDsUpsertFailureDoesNotAdvanceLastKeys(t *testing.T) {
	store := newFakeSchemaStore()
	s := newTestSyncer(store)
	widget := widgetCRD()

	if _, err := s.syncCRDs(context.Background(), []*apiextensionsv1.CustomResourceDefinition{widget}); err != nil {
		t.Fatalf("first syncCRDs: %v", err)
	}

	store.upsertErr = errors.New("boom")
	// A second CRD is added, but the upsert now fails.
	crds := []*apiextensionsv1.CustomResourceDefinition{widget, gadgetCRD("gadgets.example.com")}
	if _, err := s.syncCRDs(context.Background(), crds); err == nil {
		t.Fatal("expected the failed upsert to propagate")
	}

	// lastKeys must still reflect only the widget, so a later, successful
	// sync back to just the widget does not spuriously delete anything it
	// never actually wrote.
	store.upsertErr = nil
	result, err := s.syncCRDs(context.Background(), []*apiextensionsv1.CustomResourceDefinition{widget})
	if err != nil {
		t.Fatalf("third syncCRDs: %v", err)
	}
	if result.Deleted != 0 {
		t.Fatalf("result.Deleted = %d, want 0", result.Deleted)
	}
}

func TestNewSyncerAppliesDefaults(t *testing.T) {
	s := NewSyncer(Options{Store: newFakeSchemaStore(), Embedder: rag.NewFakeEmbedder(8)})
	if s.opts.ResyncInterval != defaultResyncInterval {
		t.Errorf("ResyncInterval = %v, want %v", s.opts.ResyncInterval, defaultResyncInterval)
	}
	if s.opts.VectorSimilarity != defaultVectorSimilarity {
		t.Errorf("VectorSimilarity = %q, want %q", s.opts.VectorSimilarity, defaultVectorSimilarity)
	}
	if s.opts.EmbeddingBatchSize != defaultEmbeddingBatchSize {
		t.Errorf("EmbeddingBatchSize = %d, want %d", s.opts.EmbeddingBatchSize, defaultEmbeddingBatchSize)
	}
}

func TestSyncerStartRequiresDependencies(t *testing.T) {
	s := NewSyncer(Options{})
	if err := s.Start(context.Background()); err == nil {
		t.Fatal("expected Start to fail without Dynamic/Store/Embedder configured")
	}
}
