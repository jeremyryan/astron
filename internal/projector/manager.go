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
	"github.com/project-gamera/gamera/internal/relationship"
)

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
func (m *Manager) Ensure(ctx context.Context, id graph.ProjectionID, namespace string, spec gamerav1alpha1.GraphProjectionSpec, cfg graph.Neo4jConfig) (*Projector, error) {
	hash, err := specHash(spec, cfg)
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

	p := New(Options{
		ID:             id,
		Namespace:      namespace,
		Spec:           spec,
		Dynamic:        m.dynamicClient,
		Mapper:         m.mapper,
		Store:          store,
		Engine:         m.engine,
		ResyncInterval: resyncInterval(spec),
	})
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

// specHash produces a stable fingerprint of the inputs that affect a
// projector's behaviour, so the manager can detect meaningful changes.
func specHash(spec gamerav1alpha1.GraphProjectionSpec, cfg graph.Neo4jConfig) (string, error) {
	payload := struct {
		Spec gamerav1alpha1.GraphProjectionSpec
		Cfg  graph.Neo4jConfig
	}{spec, cfg}
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
