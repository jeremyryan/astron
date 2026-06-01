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

package controller

import (
	"context"
	"sync"

	"github.com/project-gamera/gamera/internal/graph"
)

// fakeStore is an in-memory graph.Store used for tests.
type fakeStore struct {
	mu            sync.Mutex
	verifyErr     error
	nodes         map[string]graph.Node
	rels          []graph.Relationship
	deleted       map[graph.ProjectionID]bool
	closed        bool
	verifyCalls   int
	deleteProjErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		nodes:   map[string]graph.Node{},
		deleted: map[graph.ProjectionID]bool{},
	}
}

func (f *fakeStore) Verify(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifyCalls++
	return f.verifyErr
}

func (f *fakeStore) UpsertNode(_ context.Context, _ graph.ProjectionID, n graph.Node) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes[n.Ref.String()] = n
	return nil
}

func (f *fakeStore) UpsertNodes(ctx context.Context, p graph.ProjectionID, ns []graph.Node) error {
	for _, n := range ns {
		if err := f.UpsertNode(ctx, p, n); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeStore) DeleteNode(_ context.Context, _ graph.ProjectionID, ref graph.Ref) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.nodes, ref.String())
	return nil
}

func (f *fakeStore) UpsertRelationship(_ context.Context, _ graph.ProjectionID, r graph.Relationship) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rels = append(f.rels, r)
	return nil
}

func (f *fakeStore) UpsertRelationships(ctx context.Context, p graph.ProjectionID, rs []graph.Relationship) error {
	for _, r := range rs {
		if err := f.UpsertRelationship(ctx, p, r); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeStore) DeleteProjection(_ context.Context, p graph.ProjectionID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteProjErr != nil {
		return f.deleteProjErr
	}
	f.deleted[p] = true
	return nil
}

func (f *fakeStore) Counts(context.Context, graph.ProjectionID) (graph.Counts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return graph.Counts{Nodes: int64(len(f.nodes)), Relationships: int64(len(f.rels))}, nil
}

func (f *fakeStore) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeStore) wasDeleted(p graph.ProjectionID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deleted[p]
}
