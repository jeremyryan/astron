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
	"encoding/json"
	"strings"
	"testing"

	"github.com/project-astron/astron/internal/agent"
	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

// TestToolSetCoversCatalog verifies every tool in agent.Catalog() is wired to
// a handler (none silently missing).
func TestToolSetCoversCatalog(t *testing.T) {
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, false)
	tools := p.toolSet("")
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Spec.Name] = true
	}
	for _, spec := range agent.Catalog() {
		if !got[spec.Name] {
			t.Errorf("catalog tool %q has no handler in toolSet", spec.Name)
		}
	}
	if len(tools) != len(agent.Catalog()) {
		t.Errorf("toolSet has %d tools, catalog has %d", len(tools), len(agent.Catalog()))
	}
}

func TestToolSearch(t *testing.T) {
	store := &retrievalStore{data: sampleGraph(), hits: []graph.VectorHit{hit(uidPod, 0.9)}}
	p := newRetrievalProjector(store, true)

	out, err := p.toolSearch(context.Background(), json.RawMessage(`{"query":"web pod","hops":1}`))
	if err != nil {
		t.Fatalf("toolSearch: %v", err)
	}
	var obs toolRetrievalObservation
	if err := json.Unmarshal([]byte(out), &obs); err != nil {
		t.Fatalf("observation not valid JSON: %v\n%s", err, out)
	}
	if obs.Query != "web pod" {
		t.Errorf("Query = %q", obs.Query)
	}
	if !containsCard(obs.Cards, "web-1") {
		t.Errorf("expected a card for web-1, got %+v", obs.Cards)
	}
	if !containsRelationship(obs.Relationships, "OWNS") {
		t.Errorf("expected an OWNS relationship, got %+v", obs.Relationships)
	}
}

func TestToolSearchRequiresQuery(t *testing.T) {
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, true)
	if _, err := p.toolSearch(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected an error for a missing query")
	}
}

func TestToolSearchInvalidJSON(t *testing.T) {
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, true)
	if _, err := p.toolSearch(context.Background(), json.RawMessage(`not json`)); err == nil {
		t.Fatal("expected an error for invalid JSON arguments")
	}
}

func TestToolNeighborhood(t *testing.T) {
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, false)

	out, err := p.toolNeighborhood(context.Background(),
		json.RawMessage(`{"kind":"Pod","name":"web-1","namespace":"shop"}`))
	if err != nil {
		t.Fatalf("toolNeighborhood: %v", err)
	}
	var obs toolRetrievalObservation
	if err := json.Unmarshal([]byte(out), &obs); err != nil {
		t.Fatalf("observation not valid JSON: %v\n%s", err, out)
	}
	// Default hop (1) should reach the owning Deployment.
	if !containsCard(obs.Cards, "web") {
		t.Errorf("expected the neighborhood to include Deployment web, got %+v", obs.Cards)
	}
}

func TestToolNeighborhoodRequiresKindAndName(t *testing.T) {
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, false)
	if _, err := p.toolNeighborhood(context.Background(), json.RawMessage(`{"kind":"Pod"}`)); err == nil {
		t.Fatal("expected an error for a missing name")
	}
	if _, err := p.toolNeighborhood(context.Background(), json.RawMessage(`{"name":"web-1"}`)); err == nil {
		t.Fatal("expected an error for a missing kind")
	}
}

func TestToolQuery(t *testing.T) {
	store := &retrievalStore{
		data:      sampleGraph(),
		queryRows: []map[string]any{{"n": int64(3)}},
	}
	chat := rag.NewFakeChat("```cypher\nMATCH (p:Pod {_projection: $projection}) RETURN count(p) AS n\n```")
	p := newQAProjector(store, chat, false)

	out, err := p.toolQuery(context.Background(), json.RawMessage(`{"question":"how many pods?"}`), "")
	if err != nil {
		t.Fatalf("toolQuery: %v", err)
	}
	var result QueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("observation not valid JSON: %v\n%s", err, out)
	}
	if strings.Contains(result.Cypher, "```") {
		t.Errorf("cypher not unwrapped: %q", result.Cypher)
	}
	// JSON round-tripped through the tool observation: numbers decode as
	// float64, not the store's original int64.
	if len(result.Rows) != 1 || result.Rows[0]["n"] != float64(3) {
		t.Errorf("unexpected rows: %+v", result.Rows)
	}
}

func TestToolQueryRequiresQuestion(t *testing.T) {
	store := &retrievalStore{data: sampleGraph()}
	p := newQAProjector(store, rag.NewFakeChat("x"), false)
	if _, err := p.toolQuery(context.Background(), json.RawMessage(`{}`), ""); err == nil {
		t.Fatal("expected an error for a missing question")
	}
}

func TestToolQueryPropagatesChatDisabledError(t *testing.T) {
	// No chat configured: the tool's error becomes an observation for the
	// agent runner to see, rather than panicking.
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, false)
	if _, err := p.toolQuery(context.Background(), json.RawMessage(`{"question":"how many pods?"}`), ""); err == nil {
		t.Fatal("expected an error when chat is not enabled")
	}
}

func TestToolSchema(t *testing.T) {
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, false)
	out, err := p.toolSchema(context.Background(), nil)
	if err != nil {
		t.Fatalf("toolSchema: %v", err)
	}
	if !strings.Contains(out, ":Pod") || !strings.Contains(out, "OWNS") {
		t.Errorf("schema summary missing expected content: %s", out)
	}
}

// testWidgetKind is the CRD Kind used across the search_resource_docs/
// get_resource_schema tool tests below.
const testWidgetKind = "Widget"

// fakeSchemaStore is a minimal graph.SchemaStore for tool tests: it only
// implements the two read paths search_resource_docs/get_resource_schema
// exercise (SearchResourceSchemas, ReadResourceSchema); the write paths are
// exercised by internal/crdschema, not here.
type fakeSchemaStore struct {
	hits                  []graph.ResourceSchemaHit
	doc                   string
	docOK                 bool
	lastKind, lastVersion string
	readErr, searchErr    error
}

func (f *fakeSchemaStore) EnsureResourceSchemaVectorIndex(context.Context, int, string) error {
	return nil
}
func (f *fakeSchemaStore) ExistingResourceSchemaHashes(context.Context, []string) (map[string]string, error) {
	return nil, nil
}
func (f *fakeSchemaStore) UpsertResourceSchemas(context.Context, []graph.ResourceSchema) error {
	return nil
}
func (f *fakeSchemaStore) DeleteResourceSchemas(context.Context, []string) error { return nil }
func (f *fakeSchemaStore) ReadResourceSchema(_ context.Context, kind, version string) (string, bool, error) {
	f.lastKind, f.lastVersion = kind, version
	if f.readErr != nil {
		return "", false, f.readErr
	}
	return f.doc, f.docOK, nil
}
func (f *fakeSchemaStore) SearchResourceSchemas(context.Context, []float32, int) ([]graph.ResourceSchemaHit, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.hits, nil
}

func TestToolSearchResourceDocs(t *testing.T) {
	schema := &fakeSchemaStore{hits: []graph.ResourceSchemaHit{
		{Group: "example.com", Version: "v1", Kind: testWidgetKind, Scope: "Namespaced", Overview: "Kind: Widget", Score: 0.87},
	}}
	p := New(Options{ID: "proj-schema", Store: &retrievalStore{data: sampleGraph()}, SchemaStore: schema, SchemaEmbedder: rag.NewFakeEmbedder(8)})

	out, err := p.toolSearchResourceDocs(context.Background(), json.RawMessage(`{"query":"certificate rotation"}`))
	if err != nil {
		t.Fatalf("toolSearchResourceDocs: %v", err)
	}
	var obs schemaSearchObservation
	if err := json.Unmarshal([]byte(out), &obs); err != nil {
		t.Fatalf("observation not valid JSON: %v\n%s", err, out)
	}
	if obs.Query != "certificate rotation" {
		t.Errorf("Query = %q", obs.Query)
	}
	if len(obs.Hits) != 1 || obs.Hits[0].Kind != testWidgetKind {
		t.Errorf("unexpected hits: %+v", obs.Hits)
	}
}

func TestToolSearchResourceDocsRequiresQuery(t *testing.T) {
	p := New(Options{ID: "proj-schema", Store: &retrievalStore{data: sampleGraph()}, SchemaStore: &fakeSchemaStore{}, SchemaEmbedder: rag.NewFakeEmbedder(8)})
	if _, err := p.toolSearchResourceDocs(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected an error for a missing query")
	}
}

func TestToolSearchResourceDocsNotConfigured(t *testing.T) {
	// Neither SchemaStore nor SchemaEmbedder set: this cluster has no CRD
	// schema capture configured, an error rather than an empty result, so the
	// agent understands the capability is unavailable (see toolQuery's
	// chat-disabled precedent).
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, false)
	if _, err := p.toolSearchResourceDocs(context.Background(), json.RawMessage(`{"query":"x"}`)); err == nil {
		t.Fatal("expected an error when CRD schema capture is not configured")
	}
}

func TestToolGetResourceSchema(t *testing.T) {
	schema := &fakeSchemaStore{doc: "Widget\n...", docOK: true}
	p := New(Options{ID: "proj-schema", Store: &retrievalStore{data: sampleGraph()}, SchemaStore: schema})

	out, err := p.toolGetResourceSchema(context.Background(), json.RawMessage(`{"kind":"`+testWidgetKind+`","version":"v1"}`))
	if err != nil {
		t.Fatalf("toolGetResourceSchema: %v", err)
	}
	if out != "Widget\n..." {
		t.Errorf("out = %q", out)
	}
	if schema.lastKind != testWidgetKind || schema.lastVersion != "v1" {
		t.Errorf("ReadResourceSchema called with (%q, %q)", schema.lastKind, schema.lastVersion)
	}
}

func TestToolGetResourceSchemaNotFoundIsNotAnError(t *testing.T) {
	// A kind with no captured schema is a normal observation the agent can
	// recover from, not an aborting error (see docs/crd-schema-design.md).
	schema := &fakeSchemaStore{docOK: false}
	p := New(Options{ID: "proj-schema", Store: &retrievalStore{data: sampleGraph()}, SchemaStore: schema})

	out, err := p.toolGetResourceSchema(context.Background(), json.RawMessage(`{"kind":"Nope"}`))
	if err != nil {
		t.Fatalf("toolGetResourceSchema: %v", err)
	}
	if !strings.Contains(out, "Nope") {
		t.Errorf("expected the observation to name the requested kind, got %q", out)
	}
}

func TestToolGetResourceSchemaRequiresKind(t *testing.T) {
	p := New(Options{ID: "proj-schema", Store: &retrievalStore{data: sampleGraph()}, SchemaStore: &fakeSchemaStore{}})
	if _, err := p.toolGetResourceSchema(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected an error for a missing kind")
	}
}

func TestToolGetResourceSchemaNotConfigured(t *testing.T) {
	p := newRetrievalProjector(&retrievalStore{data: sampleGraph()}, false)
	if _, err := p.toolGetResourceSchema(context.Background(), json.RawMessage(`{"kind":"`+testWidgetKind+`"}`)); err == nil {
		t.Fatal("expected an error when CRD schema capture is not configured")
	}
}

func containsCard(cards []toolCardEntry, name string) bool {
	for _, c := range cards {
		if c.Name == name {
			return true
		}
	}
	return false
}

func containsRelationship(lines []string, relType string) bool {
	for _, l := range lines {
		if strings.Contains(l, relType) {
			return true
		}
	}
	return false
}
