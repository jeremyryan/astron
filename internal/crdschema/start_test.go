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
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

// toUnstructuredCRD converts a typed CustomResourceDefinition into the
// unstructured form a real dynamic client/informer would hand back, the same
// conversion direction Start's real watch produces (Sync converts back with
// FromUnstructured) — round-tripping this way in a test exercises that
// conversion against a realistic, nested schema rather than assuming it works.
func toUnstructuredCRD(t *testing.T, crd *apiextensionsv1.CustomResourceDefinition) *unstructured.Unstructured {
	t.Helper()
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(crd)
	if err != nil {
		t.Fatalf("converting CRD to unstructured: %v", err)
	}
	return &unstructured.Unstructured{Object: obj}
}

// TestSyncerStartWatchesAndSyncsRealDynamicClient exercises Start end-to-end
// against a fake dynamic client/informer (not just syncCRDs against
// already-typed CRDs directly), to verify the informer wiring, the
// unstructured<->typed CRD conversion, and an actual store write all work
// together for a realistic, nested CRD schema.
func TestSyncerStartWatchesAndSyncsRealDynamicClient(t *testing.T) {
	crd := widgetCRD()
	crd.Kind = "CustomResourceDefinition"
	crd.APIVersion = "apiextensions.k8s.io/v1"

	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"},
		toUnstructuredCRD(t, crd),
	)

	store := newFakeSchemaStore()
	s := NewSyncer(Options{
		Dynamic:  dyn,
		Store:    store,
		Embedder: rag.NewFakeEmbedder(8),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	// Start's own initial enqueue races with this direct call, so retry
	// briefly instead of asserting after a single Sync.
	deadline := time.Now().Add(2 * time.Second)
	for {
		result, err := s.Sync(ctx)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if result.CRDs == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Sync never observed the CRD from the informer cache: %+v", result)
		}
		time.Sleep(10 * time.Millisecond)
	}

	key := graph.ResourceSchemaKey("example.com", "v1", "Widget")
	node, ok := store.nodes[key]
	if !ok {
		t.Fatal("expected a stored schema node for the Widget CRD")
	}
	if node.CRDName != "widgets.example.com" {
		t.Errorf("CRDName = %q, want widgets.example.com", node.CRDName)
	}
	if len(node.Embedding) == 0 {
		t.Error("expected the storage version to be embedded")
	}
}
