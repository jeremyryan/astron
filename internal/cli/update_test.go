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

package cli

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
)

func TestParseResourceSelector(t *testing.T) {
	cases := []struct {
		in   string
		want gamerav1alpha1.ResourceSelector
		err  bool
	}{
		{"Pod", gamerav1alpha1.ResourceSelector{Kind: "Pod"}, false},
		{"v1/Pod", gamerav1alpha1.ResourceSelector{Version: "v1", Kind: "Pod"}, false},
		{"/v1/Pod", gamerav1alpha1.ResourceSelector{Version: "v1", Kind: "Pod"}, false},
		{"apps/v1/Deployment", gamerav1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "Deployment"}, false},
		{" apps/v1/Deployment ", gamerav1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "Deployment"}, false},
		{"", gamerav1alpha1.ResourceSelector{}, true},
		{"a/b/c/d", gamerav1alpha1.ResourceSelector{}, true},
		{"apps/v1/", gamerav1alpha1.ResourceSelector{}, true},
	}
	for _, c := range cases {
		got, err := parseResourceSelector(c.in)
		if c.err {
			if err == nil {
				t.Errorf("parseResourceSelector(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseResourceSelector(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseResourceSelector(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestApplyResourceChanges(t *testing.T) {
	existing := []gamerav1alpha1.ResourceSelector{
		{Version: "v1", Kind: "Pod"},
		{Version: "v1", Kind: "Service"},
	}
	add := []gamerav1alpha1.ResourceSelector{
		{Group: "apps", Version: "v1", Kind: "Deployment"}, // new
		{Version: "v1", Kind: "Pod"},                       // already present, identical -> no-op
	}
	remove := map[string]bool{"Service": true}

	got, added, removed := applyResourceChanges(existing, add, remove)
	if added != 1 || removed != 1 {
		t.Fatalf("expected +1/-1, got +%d/-%d", added, removed)
	}
	want := []gamerav1alpha1.ResourceSelector{
		{Version: "v1", Kind: "Pod"},
		{Group: "apps", Version: "v1", Kind: "Deployment"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestApplyResourceChangesOverridesGroupVersion(t *testing.T) {
	existing := []gamerav1alpha1.ResourceSelector{{Version: "v1beta1", Kind: "Deployment"}}
	add := []gamerav1alpha1.ResourceSelector{{Group: "apps", Version: "v1", Kind: "Deployment"}}

	got, added, removed := applyResourceChanges(existing, add, nil)
	if added != 1 || removed != 0 {
		t.Fatalf("expected +1/-0, got +%d/-%d", added, removed)
	}
	want := []gamerav1alpha1.ResourceSelector{{Group: "apps", Version: "v1", Kind: "Deployment"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected override, got %+v", got)
	}
}

func projectionWithResources(ns, name string, res ...map[string]any) *unstructured.Unstructured {
	u := obj(gamerav1alpha1.GroupVersion.String(), "GraphProjection", ns, name)
	raw := make([]any, 0, len(res))
	for _, r := range res {
		raw = append(raw, r)
	}
	_ = unstructured.SetNestedSlice(u.Object, raw, "spec", "scope", "resources")
	return u
}

func TestUpdateProjectionResources(t *testing.T) {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{graphProjectionGVR: "GraphProjectionList"}
	existing := projectionWithResources(demoNS, "web",
		map[string]any{"version": "v1", "kind": "Pod"},
		map[string]any{"version": "v1", "kind": "Service"},
	)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, existing)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)

	add := []gamerav1alpha1.ResourceSelector{{Group: "apps", Version: "v1", Kind: "Deployment"}}
	remove := map[string]bool{"Service": true}
	if err := updateProjectionResources(cmd, dyn, demoNS, "web", add, remove); err != nil {
		t.Fatalf("updateProjectionResources: %v", err)
	}
	if !strings.Contains(out.String(), "updated in namespace demo (resources +1/-1, relationships +0/-0)") {
		t.Errorf("unexpected confirmation: %q", out.String())
	}

	got, err := dyn.Resource(graphProjectionGVR).Namespace(demoNS).Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	res, _, _ := unstructured.NestedSlice(got.Object, "spec", "scope", "resources")
	kinds := map[string]bool{}
	for _, r := range res {
		m := r.(map[string]any)
		kinds[m["kind"].(string)] = true
	}
	if !kinds["Pod"] || !kinds["Deployment"] || kinds["Service"] {
		t.Fatalf("unexpected resources after update: %v", kinds)
	}
}

func TestApplyRelationshipChangesAddAndRemove(t *testing.T) {
	// Adding Service (with Pod already present) should pull in service-selects-pod.
	existingRels := []gamerav1alpha1.RelationshipRule{}
	updated := []gamerav1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}, {Version: "v1", Kind: "Service"}}
	rels, added, removed := applyRelationshipChanges(existingRels, updated,
		map[string]bool{"Service": true}, nil)
	if added != 1 || removed != 0 {
		t.Fatalf("expected +1/-0 relationships, got +%d/-%d (%+v)", added, removed, rels)
	}
	if len(rels) != 1 || rels[0].Name != "service-selects-pod" {
		t.Fatalf("expected service-selects-pod, got %+v", rels)
	}

	// Removing Service should drop the relationship referencing it.
	updatedAfterRemove := []gamerav1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}}
	rels2, added2, removed2 := applyRelationshipChanges(rels, updatedAfterRemove,
		nil, map[string]bool{"Service": true})
	if added2 != 0 || removed2 != 1 {
		t.Fatalf("expected +0/-1 relationships, got +%d/-%d (%+v)", added2, removed2, rels2)
	}
	if len(rels2) != 0 {
		t.Fatalf("expected no relationships after removing Service, got %+v", rels2)
	}
}

func TestApplyRelationshipChangesPreservesUnrelated(t *testing.T) {
	custom := gamerav1alpha1.RelationshipRule{
		Name: "custom", Type: "LINKS", Strategy: gamerav1alpha1.CustomStrategy,
		From: gamerav1alpha1.ResourceSelector{Version: "v1", Kind: "Pod"},
		To:   gamerav1alpha1.ResourceSelector{Version: "v1", Kind: "Pod"},
	}
	updated := []gamerav1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}, {Group: "apps", Version: "v1", Kind: "Deployment"}}
	// Removing ConfigMap (referenced by nothing here) must not touch the custom rule.
	rels, added, removed := applyRelationshipChanges([]gamerav1alpha1.RelationshipRule{custom}, updated,
		nil, map[string]bool{"ConfigMap": true})
	if added != 0 || removed != 0 || len(rels) != 1 || rels[0].Name != "custom" {
		t.Fatalf("custom rule should be preserved: +%d/-%d %+v", added, removed, rels)
	}
}

func TestUpdateProjectionResourcesReconcilesRelationships(t *testing.T) {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{graphProjectionGVR: "GraphProjectionList"}
	existing := projectionWithResources(demoNS, "web", map[string]any{"version": "v1", "kind": "Pod"})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, existing)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)

	// Add Service: service-selects-pod relationship should be added.
	add := []gamerav1alpha1.ResourceSelector{{Version: "v1", Kind: "Service"}}
	if err := updateProjectionResources(cmd, dyn, demoNS, "web", add, nil); err != nil {
		t.Fatalf("updateProjectionResources: %v", err)
	}
	if !strings.Contains(out.String(), "relationships +1/-0") {
		t.Errorf("expected one relationship added, got: %q", out.String())
	}
	got, _ := dyn.Resource(graphProjectionGVR).Namespace(demoNS).Get(context.Background(), "web", metav1.GetOptions{})
	rels, _, _ := unstructured.NestedSlice(got.Object, "spec", "relationships")
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d: %v", len(rels), rels)
	}

	// Now remove Service: the relationship should be removed again.
	out.Reset()
	if err := updateProjectionResources(cmd, dyn, demoNS, "web", nil, map[string]bool{"Service": true}); err != nil {
		t.Fatalf("updateProjectionResources remove: %v", err)
	}
	if !strings.Contains(out.String(), "relationships +0/-1") {
		t.Errorf("expected one relationship removed, got: %q", out.String())
	}
	got, _ = dyn.Resource(graphProjectionGVR).Namespace(demoNS).Get(context.Background(), "web", metav1.GetOptions{})
	rels, _, _ = unstructured.NestedSlice(got.Object, "spec", "relationships")
	if len(rels) != 0 {
		t.Fatalf("expected relationships removed, got %v", rels)
	}
}

func TestUpdateProjectionResourcesNoChange(t *testing.T) {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{graphProjectionGVR: "GraphProjectionList"}
	existing := projectionWithResources(demoNS, "web", map[string]any{"version": "v1", "kind": "Pod"})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, existing)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)

	// Removing an absent kind and re-adding an identical one is a no-op.
	add := []gamerav1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}}
	if err := updateProjectionResources(cmd, dyn, demoNS, "web", add, map[string]bool{"Ingress": true}); err != nil {
		t.Fatalf("updateProjectionResources: %v", err)
	}
	if !strings.Contains(out.String(), "unchanged in namespace demo") {
		t.Errorf("expected unchanged message, got: %q", out.String())
	}
}

func TestUpdateProjectionResourcesNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{graphProjectionGVR: "GraphProjectionList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&bytes.Buffer{})

	err := updateProjectionResources(cmd, dyn, demoNS, "missing",
		[]gamerav1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
