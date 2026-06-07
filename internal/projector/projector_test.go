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
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/relationship"
)

// newCRD builds a CustomResourceDefinition object in the example.com group that
// defines the given kind.
func newCRD(name, kind string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetAPIVersion("apiextensions.k8s.io/v1")
	o.SetKind("CustomResourceDefinition")
	o.SetName(name)
	o.SetUID(types.UID("uid-" + name))
	_ = unstructured.SetNestedField(o.Object, "example.com", "spec", "group")
	_ = unstructured.SetNestedField(o.Object, kind, "spec", "names", "kind")
	return o
}

// newCustom builds an arbitrary custom resource instance.
func newCustom(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetAPIVersion(apiVersion)
	o.SetKind(kind)
	o.SetNamespace(namespace)
	o.SetName(name)
	o.SetUID(types.UID("uid-" + name))
	return o
}

func newObj(namespace, name string, labels map[string]string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetAPIVersion("v1")
	o.SetKind("Pod")
	o.SetNamespace(namespace)
	o.SetName(name)
	o.SetUID(types.UID("uid-" + name))
	o.SetResourceVersion("123")
	if labels != nil {
		o.SetLabels(labels)
	}
	return o
}

func TestNodeFor(t *testing.T) {
	o := newObj("default", "web", map[string]string{"app": "web"})
	o.SetAnnotations(map[string]string{
		"team": "platform",
		"kubectl.kubernetes.io/last-applied-configuration": "{huge}",
	})

	node := nodeFor(o)
	if node.Ref.Kind != "Pod" || node.Ref.Name != "web" || node.Ref.UID != "uid-web" {
		t.Fatalf("unexpected ref: %+v", node.Ref)
	}
	if node.Properties["resourceVersion"] != "123" {
		t.Errorf("expected resourceVersion preserved, got %v", node.Properties["resourceVersion"])
	}

	var gotLabels map[string]string
	if err := json.Unmarshal([]byte(node.Properties["labels"].(string)), &gotLabels); err != nil {
		t.Fatalf("labels not valid JSON: %v", err)
	}
	if gotLabels["app"] != "web" {
		t.Errorf("expected app=web label, got %v", gotLabels)
	}

	var gotAnnotations map[string]string
	if err := json.Unmarshal([]byte(node.Properties["annotations"].(string)), &gotAnnotations); err != nil {
		t.Fatalf("annotations not valid JSON: %v", err)
	}
	if _, ok := gotAnnotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		t.Error("expected last-applied-configuration annotation to be stripped")
	}
	if gotAnnotations["team"] != "platform" {
		t.Errorf("expected team annotation retained, got %v", gotAnnotations)
	}
}

func TestNodeForPodStatus(t *testing.T) {
	o := newObj("default", "web", nil)
	if err := unstructured.SetNestedField(o.Object, "Running", "status", "phase"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(o.Object, "10.1.2.3", "status", "podIP"); err != nil {
		t.Fatal(err)
	}
	containers := []any{
		map[string]any{
			"ready":        true,
			"restartCount": int64(1),
			"state":        map[string]any{"running": map[string]any{}},
		},
		map[string]any{
			"ready":        false,
			"restartCount": int64(4),
			"state": map[string]any{
				"waiting": map[string]any{"reason": "CrashLoopBackOff"},
			},
		},
	}
	if err := unstructured.SetNestedSlice(o.Object, containers, "status", "containerStatuses"); err != nil {
		t.Fatal(err)
	}

	props := nodeFor(o).Properties
	if props["phase"] != "Running" {
		t.Errorf("phase = %v, want Running", props["phase"])
	}
	if props["podIP"] != "10.1.2.3" {
		t.Errorf("podIP = %v, want 10.1.2.3", props["podIP"])
	}
	if props["ready"] != "1/2" {
		t.Errorf("ready = %v, want 1/2", props["ready"])
	}
	if props["restarts"] != int64(5) {
		t.Errorf("restarts = %v, want 5", props["restarts"])
	}
	// A container-level waiting reason refines the coarse phase.
	if props["status"] != "CrashLoopBackOff" {
		t.Errorf("status = %v, want CrashLoopBackOff", props["status"])
	}
}

func TestNodeForNonPodHasNoStatus(t *testing.T) {
	o := newObj("default", "cm", nil)
	o.SetKind("ConfigMap")
	props := nodeFor(o).Properties
	if _, ok := props["phase"]; ok {
		t.Error("non-Pod object should not have a phase property")
	}
	if _, ok := props["restarts"]; ok {
		t.Error("non-Pod object should not have a restarts property")
	}
}

func TestInScope(t *testing.T) {
	spec := gamerav1alpha1.GraphProjectionSpec{
		Scope: gamerav1alpha1.ProjectionScope{
			Namespaces:    []string{"prod"},
			LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "frontend"}},
		},
	}
	p := New(Options{Spec: spec})

	cases := []struct {
		name   string
		obj    *unstructured.Unstructured
		expect bool
	}{
		{"matching", newObj("prod", "a", map[string]string{"tier": "frontend"}), true},
		{"wrong-namespace", newObj("dev", "b", map[string]string{"tier": "frontend"}), false},
		{"wrong-label", newObj("prod", "c", map[string]string{"tier": "backend"}), false},
		{"no-label", newObj("prod", "d", nil), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.inScope(tc.obj); got != tc.expect {
				t.Errorf("inScope = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestInScopeOwnNamespaceOnly(t *testing.T) {
	spec := gamerav1alpha1.GraphProjectionSpec{
		Scope: gamerav1alpha1.ProjectionScope{
			OwnNamespaceOnly: true,
			// namespaces is ignored when ownNamespaceOnly is set.
			Namespaces: []string{"other"},
		},
	}
	p := New(Options{Namespace: "team-a", Spec: spec})

	clusterScoped := newObj("", "some-clusterrole", nil)

	cases := []struct {
		name   string
		obj    *unstructured.Unstructured
		expect bool
	}{
		{"own-namespace", newObj("team-a", "pod-1", nil), true},
		{"other-namespace", newObj("team-b", "pod-2", nil), false},
		{"listed-namespace-ignored", newObj("other", "pod-3", nil), false},
		{"cluster-scoped-excluded", clusterScoped, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.inScope(tc.obj); got != tc.expect {
				t.Errorf("inScope = %v, want %v", got, tc.expect)
			}
		})
	}

	if got := p.watchNamespace(); got != "team-a" {
		t.Errorf("watchNamespace = %q, want %q", got, "team-a")
	}
}

func TestInScopeNoFilters(t *testing.T) {
	p := New(Options{Spec: gamerav1alpha1.GraphProjectionSpec{}})
	if !p.inScope(newObj("anything", "x", nil)) {
		t.Error("expected object to be in scope when no filters are configured")
	}
}

func TestScopedGVKsDefaults(t *testing.T) {
	p := New(Options{Spec: gamerav1alpha1.GraphProjectionSpec{}})
	gvks := p.scopedGVKs()
	if len(gvks) != len(defaultResources()) {
		t.Errorf("expected default resource set, got %d kinds", len(gvks))
	}
}

func TestScopedGVKsIncludesCRDWhenEnabled(t *testing.T) {
	spec := gamerav1alpha1.GraphProjectionSpec{
		Scope: gamerav1alpha1.ProjectionScope{
			Resources: []gamerav1alpha1.ResourceSelector{{Group: "example.com", Version: "v1", Kind: "Widget"}},
			CRDs:      &gamerav1alpha1.CRDSelection{Include: true},
		},
	}
	p := New(Options{Spec: spec})
	gvks := p.scopedGVKs()
	var hasCRD, hasWidget bool
	for _, g := range gvks {
		if g == crdGVK {
			hasCRD = true
		}
		if g.Kind == "Widget" {
			hasWidget = true
		}
	}
	if !hasCRD || !hasWidget {
		t.Fatalf("expected Widget and CRD GVKs, got %+v", gvks)
	}
}

func TestCRDSelected(t *testing.T) {
	crdA := newCRD("widgets.example.com", "Widget")
	crdB := newCRD("gadgets.example.com", "Gadget")

	// Not enabled: no CRD captured.
	p := New(Options{Spec: gamerav1alpha1.GraphProjectionSpec{}})
	if p.inScope(crdA) {
		t.Error("expected CRD not captured when crds is unset")
	}

	// Include all.
	pAll := New(Options{Spec: gamerav1alpha1.GraphProjectionSpec{
		Scope: gamerav1alpha1.ProjectionScope{CRDs: &gamerav1alpha1.CRDSelection{Include: true}},
	}})
	if !pAll.inScope(crdA) || !pAll.inScope(crdB) {
		t.Error("expected all CRDs captured when include is true")
	}

	// Named list only.
	pNamed := New(Options{Spec: gamerav1alpha1.GraphProjectionSpec{
		Scope: gamerav1alpha1.ProjectionScope{CRDs: &gamerav1alpha1.CRDSelection{Names: []string{"widgets.example.com"}}},
	}})
	if !pNamed.inScope(crdA) {
		t.Error("expected named CRD to be captured")
	}
	if pNamed.inScope(crdB) {
		t.Error("expected unnamed CRD to be excluded")
	}
}

func TestCRDSelectedBypassesOwnNamespaceFilter(t *testing.T) {
	// CRDs are cluster-scoped; capturing them must work even under
	// ownNamespaceOnly, which otherwise drops cluster-scoped objects.
	p := New(Options{
		Namespace: "team-a",
		Spec: gamerav1alpha1.GraphProjectionSpec{
			Scope: gamerav1alpha1.ProjectionScope{
				OwnNamespaceOnly: true,
				CRDs:             &gamerav1alpha1.CRDSelection{Include: true},
			},
		},
	})
	if !p.inScope(newCRD("widgets.example.com", "Widget")) {
		t.Error("expected CRD captured despite ownNamespaceOnly")
	}
}

func TestCRDEdges(t *testing.T) {
	crd := newCRD("widgets.example.com", "Widget")
	w1 := newCustom("example.com/v1", "Widget", "ns1", "w1")
	w2 := newCustom("example.com/v1", "Widget", "ns2", "w2")
	pod := newObj("ns1", "p1", nil) // unrelated kind: must not be linked
	objs := []*unstructured.Unstructured{crd, w1, w2, pod}

	p := New(Options{Spec: gamerav1alpha1.GraphProjectionSpec{
		Scope: gamerav1alpha1.ProjectionScope{CRDs: &gamerav1alpha1.CRDSelection{Include: true}},
	}})
	index := relationship.NewMapIndex(objs...)
	edges := p.crdEdges(objs, index)

	if len(edges) != 2 {
		t.Fatalf("expected 2 DEFINES edges, got %d: %+v", len(edges), edges)
	}
	gotTargets := map[string]bool{}
	for _, e := range edges {
		if e.Type != crdDefinesType {
			t.Errorf("unexpected edge type %q", e.Type)
		}
		if e.From.Name != "widgets.example.com" || e.From.Kind != crdKind {
			t.Errorf("unexpected edge source %+v", e.From)
		}
		gotTargets[e.To.Name] = true
	}
	if !gotTargets["w1"] || !gotTargets["w2"] {
		t.Errorf("expected edges to w1 and w2, got %v", gotTargets)
	}
}

func TestCRDEdgesDisabled(t *testing.T) {
	p := New(Options{Spec: gamerav1alpha1.GraphProjectionSpec{}})
	crd := newCRD("widgets.example.com", "Widget")
	w1 := newCustom("example.com/v1", "Widget", "ns1", "w1")
	objs := []*unstructured.Unstructured{crd, w1}
	if edges := p.crdEdges(objs, relationship.NewMapIndex(objs...)); edges != nil {
		t.Errorf("expected no edges when crds disabled, got %+v", edges)
	}
}

func TestScopedGVKsExplicit(t *testing.T) {
	spec := gamerav1alpha1.GraphProjectionSpec{
		Scope: gamerav1alpha1.ProjectionScope{
			Resources: []gamerav1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}},
		},
	}
	p := New(Options{Spec: spec})
	gvks := p.scopedGVKs()
	if len(gvks) != 1 || gvks[0].Kind != "Pod" {
		t.Errorf("expected only Pod, got %+v", gvks)
	}
}
