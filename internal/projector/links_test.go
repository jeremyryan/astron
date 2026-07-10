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
	"errors"
	"testing"

	"github.com/project-astron/astron/internal/graph"
)

// fakeLinkStore is a minimal graph.Store + graph.LinkStore whose link methods
// can be made to fail, for exercising the projector's manual-link plumbing.
type fakeLinkStore struct {
	linkErr error // returned by all LinkStore mutations when set
}

func (f *fakeLinkStore) Verify(context.Context) error { return nil }
func (f *fakeLinkStore) Sync(context.Context, graph.ProjectionID, []graph.Node, []graph.Relationship) (graph.Counts, error) {
	return graph.Counts{}, nil
}
func (f *fakeLinkStore) DeleteProjection(context.Context, graph.ProjectionID) error { return nil }
func (f *fakeLinkStore) Counts(context.Context, graph.ProjectionID) (graph.Counts, error) {
	return graph.Counts{}, nil
}
func (f *fakeLinkStore) ReadGraph(context.Context, graph.ProjectionID) (graph.GraphData, error) {
	return graph.GraphData{}, nil
}
func (f *fakeLinkStore) Close(context.Context) error { return nil }

func (f *fakeLinkStore) AddManualLink(context.Context, graph.ProjectionID, string, string, string) error {
	return f.linkErr
}
func (f *fakeLinkStore) DeleteManualLink(context.Context, graph.ProjectionID, string, string, string) error {
	return f.linkErr
}
func (f *fakeLinkStore) SetManualLinkNote(context.Context, graph.ProjectionID, string, string, string, string) error {
	return f.linkErr
}
func (f *fakeLinkStore) ManualLinks(context.Context, graph.ProjectionID) ([]graph.Relationship, error) {
	return nil, nil
}

// drainTrigger empties the projector's re-sync trigger channel and reports
// whether a re-sync had been requested.
func drainTrigger(p *Projector) bool {
	select {
	case <-p.trigger:
		return true
	default:
		return false
	}
}

// TestLinkMutationsEnqueueResync verifies that creating, deleting, and
// annotating a manual link each request a debounced re-sync, so the link (and
// its note) flows into GraphRAG cards and embeddings promptly rather than
// waiting for the periodic resync.
func TestLinkMutationsEnqueueResync(t *testing.T) {
	ctx := context.Background()
	p := New(Options{ID: "p", Store: &fakeLinkStore{}})

	if err := p.AddLink(ctx, "a", "b", "CUSTOM"); err != nil {
		t.Fatalf("AddLink: %v", err)
	}
	if !drainTrigger(p) {
		t.Error("AddLink did not enqueue a re-sync")
	}

	if err := p.UpdateLinkNote(ctx, "a", "b", "CUSTOM", "a note"); err != nil {
		t.Fatalf("UpdateLinkNote: %v", err)
	}
	if !drainTrigger(p) {
		t.Error("UpdateLinkNote did not enqueue a re-sync")
	}

	if err := p.DeleteLink(ctx, "a", "b", "CUSTOM"); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	if !drainTrigger(p) {
		t.Error("DeleteLink did not enqueue a re-sync")
	}
}

// TestLinkMutationFailuresDoNotEnqueue verifies that a failed link mutation
// does not request a re-sync.
func TestLinkMutationFailuresDoNotEnqueue(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	p := New(Options{ID: "p", Store: &fakeLinkStore{linkErr: boom}})

	if err := p.AddLink(ctx, "a", "b", "CUSTOM"); !errors.Is(err, boom) {
		t.Fatalf("AddLink error = %v, want %v", err, boom)
	}
	if err := p.DeleteLink(ctx, "a", "b", "CUSTOM"); !errors.Is(err, boom) {
		t.Fatalf("DeleteLink error = %v, want %v", err, boom)
	}
	if err := p.UpdateLinkNote(ctx, "a", "b", "CUSTOM", "n"); !errors.Is(err, boom) {
		t.Fatalf("UpdateLinkNote error = %v, want %v", err, boom)
	}
	if drainTrigger(p) {
		t.Error("failed link mutations should not enqueue a re-sync")
	}
}
