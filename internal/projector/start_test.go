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
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

// TestStartSkipsClusterScopedKindsWhenOwnNamespaceOnly verifies that a
// projection scoped to its own namespace does not create informers for
// cluster-scoped kinds: its informer factory is namespace-filtered, and
// listing a cluster-scoped resource through a namespaced path is a 404 the
// reflector would retry forever. inScope already excludes those objects from
// the graph, so their informers must be skipped entirely.
func TestStartSkipsClusterScopedKindsWhenOwnNamespaceOnly(t *testing.T) {
	podGVK := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	pvGVK := schema.GroupVersionKind{Version: "v1", Kind: "PersistentVolume"}
	crGVK := schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}

	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Version: "v1"},
		{Group: "rbac.authorization.k8s.io", Version: "v1"},
	})
	mapper.Add(podGVK, meta.RESTScopeNamespace)
	mapper.Add(pvGVK, meta.RESTScopeRoot)
	mapper.Add(crGVK, meta.RESTScopeRoot)

	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "pods"}:                                             "PodList",
		{Version: "v1", Resource: "persistentvolumes"}:                                "PersistentVolumeList",
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}: "ClusterRoleList",
	})

	spec := astronv1alpha1.GraphProjectionSpec{
		Scope: astronv1alpha1.ProjectionScope{
			OwnNamespaceOnly: true,
			Resources: []astronv1alpha1.ResourceSelector{
				{Version: "v1", Kind: "Pod"},
				{Version: "v1", Kind: "PersistentVolume"},
				{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"},
			},
		},
	}

	p := New(Options{
		ID:        "proj-start",
		Namespace: "shop",
		Spec:      spec,
		Dynamic:   dyn,
		Mapper:    mapper,
		Store:     &fakeLinkStore{},
	})

	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := p.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	if _, ok := p.informers[podGVK]; !ok {
		t.Errorf("expected an informer for namespaced kind %s", podGVK)
	}
	for _, gvk := range []schema.GroupVersionKind{pvGVK, crGVK} {
		if _, ok := p.informers[gvk]; ok {
			t.Errorf("expected no informer for cluster-scoped kind %s under ownNamespaceOnly", gvk)
		}
	}
}

// TestStartKeepsClusterScopedKindsWithoutOwnNamespaceOnly verifies the
// complementary case: without ownNamespaceOnly the factory watches all
// namespaces, and cluster-scoped kinds get informers as before.
func TestStartKeepsClusterScopedKindsWithoutOwnNamespaceOnly(t *testing.T) {
	pvGVK := schema.GroupVersionKind{Version: "v1", Kind: "PersistentVolume"}

	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	mapper.Add(pvGVK, meta.RESTScopeRoot)

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "persistentvolumes"}: "PersistentVolumeList",
	})

	p := New(Options{
		ID:        "proj-start-all",
		Namespace: "shop",
		Spec: astronv1alpha1.GraphProjectionSpec{
			Scope: astronv1alpha1.ProjectionScope{
				Resources: []astronv1alpha1.ResourceSelector{
					{Version: "v1", Kind: "PersistentVolume"},
				},
			},
		},
		Dynamic: dyn,
		Mapper:  mapper,
		Store:   &fakeLinkStore{},
	})

	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := p.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	if _, ok := p.informers[pvGVK]; !ok {
		t.Errorf("expected an informer for cluster-scoped kind %s when watching all namespaces", pvGVK)
	}
}
