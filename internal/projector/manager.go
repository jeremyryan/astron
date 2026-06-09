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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/dynamic"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
	"github.com/project-gamera/gamera/internal/rag"
	"github.com/project-gamera/gamera/internal/relationship"
)

// EmbeddingConfig is the resolved GraphRAG embedding configuration for a
// projection (the CRD's graphRAG block with any referenced Secret already
// read). When Enabled is false it is a no-op and the projector runs without
// embeddings.
type EmbeddingConfig struct {
	Enabled     bool
	Embedder    rag.EmbedderConfig
	CardOptions rag.Options
	Similarity  string
	BatchSize   int

	// ChatEnabled turns on natural-language answering and text-to-Cypher.
	ChatEnabled bool
	// Chat is the resolved chat-model configuration, used when ChatEnabled.
	Chat rag.ChatConfig
}

// ErrNotRunning indicates no projector is currently serving a projection.
var ErrNotRunning = errors.New("no projector running for projection")

// StoreFactory builds a graph.Store for a projection from a resolved config.
type StoreFactory func(cfg graph.Neo4jConfig) (graph.Store, error)

// Manager owns the set of running Projectors, one per GraphProjection. It
// (re)starts projectors when their configuration changes and stops them when
// the projection is deleted. Manager is safe for concurrent use.
type Manager struct {
	dynamicClient dynamic.Interface
	mapper        meta.RESTMapper
	newStore      StoreFactory
	engine        *relationship.Engine

	mu      sync.Mutex
	running map[graph.ProjectionID]*entry
}

type entry struct {
	projector *Projector
	specHash  string
}

// NewManager constructs a Manager.
func NewManager(dynamicClient dynamic.Interface, mapper meta.RESTMapper, newStore StoreFactory) *Manager {
	return &Manager{
		dynamicClient: dynamicClient,
		mapper:        mapper,
		newStore:      newStore,
		engine:        relationship.NewEngine(),
		running:       map[graph.ProjectionID]*entry{},
	}
}

// Ensure makes the running state match the desired projection: it starts a new
// projector, restarts one whose spec changed, or leaves an unchanged one alone.
// It returns the projector currently serving the projection.
func (m *Manager) Ensure(ctx context.Context, id graph.ProjectionID, namespace string, spec gamerav1alpha1.GraphProjectionSpec, cfg graph.Neo4jConfig, emb EmbeddingConfig) (*Projector, error) {
	hash, err := specHash(spec, cfg, emb)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	existing, ok := m.running[id]
	m.mu.Unlock()

	if ok && existing.specHash == hash {
		return existing.projector, nil
	}

	// Configuration changed (or new): stop any existing projector first.
	if ok {
		_ = existing.projector.Stop(ctx)
		m.mu.Lock()
		delete(m.running, id)
		m.mu.Unlock()
	}

	store, err := m.newStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating store: %w", err)
	}
	if err := store.Verify(ctx); err != nil {
		_ = store.Close(ctx)
		return nil, fmt.Errorf("verifying store: %w", err)
	}

	opts := Options{
		ID:             id,
		Namespace:      namespace,
		Spec:           spec,
		Dynamic:        m.dynamicClient,
		Mapper:         m.mapper,
		Store:          store,
		Engine:         m.engine,
		ResyncInterval: resyncInterval(spec),
	}

	// Enable GraphRAG embedding when configured and the store supports vectors.
	if emb.Enabled {
		vs, ok := store.(graph.VectorStore)
		if !ok {
			_ = store.Close(ctx)
			return nil, fmt.Errorf("graphRAG is enabled but the store does not support vector search")
		}
		embedder, err := rag.NewEmbedder(emb.Embedder)
		if err != nil {
			_ = store.Close(ctx)
			return nil, fmt.Errorf("building embedder: %w", err)
		}
		opts.Embedder = embedder
		opts.VectorStore = vs
		opts.CardOptions = emb.CardOptions
		opts.VectorSimilarity = emb.Similarity
		opts.EmbeddingBatchSize = emb.BatchSize
	}

	// Enable natural-language answering / text-to-Cypher when configured.
	if emb.ChatEnabled {
		chat, err := rag.NewChat(emb.Chat)
		if err != nil {
			_ = store.Close(ctx)
			return nil, fmt.Errorf("building chat model: %w", err)
		}
		opts.Chat = chat
		if qs, ok := store.(graph.QueryStore); ok {
			opts.QueryStore = qs
		}
	}

	p := New(opts)
	if err := p.Start(ctx); err != nil {
		_ = store.Close(ctx)
		return nil, fmt.Errorf("starting projector: %w", err)
	}

	m.mu.Lock()
	m.running[id] = &entry{projector: p, specHash: hash}
	m.mu.Unlock()
	return p, nil
}

// Remove stops and removes the projector for a projection, if present.
func (m *Manager) Remove(ctx context.Context, id graph.ProjectionID) error {
	m.mu.Lock()
	existing, ok := m.running[id]
	if ok {
		delete(m.running, id)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return existing.projector.Stop(ctx)
}

// Get returns the running projector for a projection, if any.
func (m *Manager) Get(id graph.ProjectionID) (*Projector, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.running[id]
	if !ok {
		return nil, false
	}
	return e.projector, true
}

// ReadGraph returns the materialized graph for a running projection. It returns
// ErrNotRunning when no projector is serving the projection.
func (m *Manager) ReadGraph(ctx context.Context, id graph.ProjectionID) (graph.GraphData, error) {
	p, ok := m.Get(id)
	if !ok {
		return graph.GraphData{}, ErrNotRunning
	}
	return p.ReadGraph(ctx)
}

// Search runs hybrid (vector + graph) retrieval against a running projection.
// It returns ErrNotRunning when no projector is serving the projection, or
// ErrRAGNotEnabled when the projection has no embedding configured.
func (m *Manager) Search(ctx context.Context, id graph.ProjectionID, query string, opts SearchOptions) (Retrieval, error) {
	p, ok := m.Get(id)
	if !ok {
		return Retrieval{}, ErrNotRunning
	}
	return p.Search(ctx, query, opts)
}

// Neighborhood runs structural retrieval (no embeddings) around a single
// resource in a running projection. It returns ErrNotRunning when no projector
// is serving the projection.
func (m *Manager) Neighborhood(ctx context.Context, id graph.ProjectionID, ref graph.Ref, hops int, edgeTypes []string) (Retrieval, error) {
	p, ok := m.Get(id)
	if !ok {
		return Retrieval{}, ErrNotRunning
	}
	return p.Neighborhood(ctx, ref, hops, edgeTypes)
}

// Query runs guarded text-to-Cypher against a running projection. It returns
// ErrNotRunning when no projector is serving the projection, or
// ErrChatNotEnabled when no chat model is configured.
func (m *Manager) Query(ctx context.Context, id graph.ProjectionID, question string) (QueryResult, error) {
	p, ok := m.Get(id)
	if !ok {
		return QueryResult{}, ErrNotRunning
	}
	return p.Query(ctx, question)
}

// Answer runs retrieval-augmented question answering against a running
// projection. It returns ErrNotRunning when no projector is serving the
// projection, ErrChatNotEnabled when no chat model is configured, or
// ErrRAGNotEnabled when embeddings are not configured.
func (m *Manager) Answer(ctx context.Context, id graph.ProjectionID, question string, opts SearchOptions) (AnswerResult, error) {
	p, ok := m.Get(id)
	if !ok {
		return AnswerResult{}, ErrNotRunning
	}
	return p.Answer(ctx, question, opts)
}

// specHash produces a stable fingerprint of the inputs that affect a
// projector's behaviour, so the manager can detect meaningful changes.
func specHash(spec gamerav1alpha1.GraphProjectionSpec, cfg graph.Neo4jConfig, emb EmbeddingConfig) (string, error) {
	payload := struct {
		Spec gamerav1alpha1.GraphProjectionSpec
		Cfg  graph.Neo4jConfig
		Emb  EmbeddingConfig
	}{spec, cfg, emb}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum), nil
}

func resyncInterval(spec gamerav1alpha1.GraphProjectionSpec) (d time.Duration) {
	if spec.ResyncInterval != nil && spec.ResyncInterval.Duration > 0 {
		return spec.ResyncInterval.Duration
	}
	return 0
}
