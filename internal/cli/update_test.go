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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

func TestParseResourceSelector(t *testing.T) {
	cases := []struct {
		in   string
		want astronv1alpha1.ResourceSelector
		err  bool
	}{
		{"Pod", astronv1alpha1.ResourceSelector{Kind: "Pod"}, false},
		{"v1/Pod", astronv1alpha1.ResourceSelector{Version: "v1", Kind: "Pod"}, false},
		{"/v1/Pod", astronv1alpha1.ResourceSelector{Version: "v1", Kind: "Pod"}, false},
		{"apps/v1/Deployment", astronv1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "Deployment"}, false},
		{" apps/v1/Deployment ", astronv1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "Deployment"}, false},
		{"", astronv1alpha1.ResourceSelector{}, true},
		{"a/b/c/d", astronv1alpha1.ResourceSelector{}, true},
		{"apps/v1/", astronv1alpha1.ResourceSelector{}, true},
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
	existing := []astronv1alpha1.ResourceSelector{
		{Version: "v1", Kind: "Pod"},
		{Version: "v1", Kind: "Service"},
	}
	add := []astronv1alpha1.ResourceSelector{
		{Group: "apps", Version: "v1", Kind: "Deployment"}, // new
		{Version: "v1", Kind: "Pod"},                       // already present, identical -> no-op
	}
	remove := map[string]bool{"Service": true}

	got, added, removed := applyResourceChanges(existing, add, remove)
	if added != 1 || removed != 1 {
		t.Fatalf("expected +1/-1, got +%d/-%d", added, removed)
	}
	want := []astronv1alpha1.ResourceSelector{
		{Version: "v1", Kind: "Pod"},
		{Group: "apps", Version: "v1", Kind: "Deployment"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestApplyResourceChangesOverridesGroupVersion(t *testing.T) {
	existing := []astronv1alpha1.ResourceSelector{{Version: "v1beta1", Kind: "Deployment"}}
	add := []astronv1alpha1.ResourceSelector{{Group: "apps", Version: "v1", Kind: "Deployment"}}

	got, added, removed := applyResourceChanges(existing, add, nil)
	if added != 1 || removed != 0 {
		t.Fatalf("expected +1/-0, got +%d/-%d", added, removed)
	}
	want := []astronv1alpha1.ResourceSelector{{Group: "apps", Version: "v1", Kind: "Deployment"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected override, got %+v", got)
	}
}

func projectionWithResources(ns, name string, res ...map[string]any) *unstructured.Unstructured {
	u := obj(astronv1alpha1.GroupVersion.String(), "GraphProjection", ns, name)
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

	add := []astronv1alpha1.ResourceSelector{{Group: "apps", Version: "v1", Kind: "Deployment"}}
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
	existingRels := []astronv1alpha1.RelationshipRule{}
	updated := []astronv1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}, {Version: "v1", Kind: "Service"}}
	rels, added, removed := applyRelationshipChanges(existingRels, updated,
		map[string]bool{"Service": true}, nil)
	if added != 1 || removed != 0 {
		t.Fatalf("expected +1/-0 relationships, got +%d/-%d (%+v)", added, removed, rels)
	}
	if len(rels) != 1 || rels[0].Name != "service-selects-pod" {
		t.Fatalf("expected service-selects-pod, got %+v", rels)
	}

	// Removing Service should drop the relationship referencing it.
	updatedAfterRemove := []astronv1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}}
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
	custom := astronv1alpha1.RelationshipRule{
		Name: "custom", Type: "LINKS", Strategy: astronv1alpha1.CustomStrategy,
		From: astronv1alpha1.ResourceSelector{Version: "v1", Kind: "Pod"},
		To:   astronv1alpha1.ResourceSelector{Version: "v1", Kind: "Pod"},
	}
	updated := []astronv1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}, {Group: "apps", Version: "v1", Kind: "Deployment"}}
	// Removing ConfigMap (referenced by nothing here) must not touch the custom rule.
	rels, added, removed := applyRelationshipChanges([]astronv1alpha1.RelationshipRule{custom}, updated,
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
	add := []astronv1alpha1.ResourceSelector{{Version: "v1", Kind: "Service"}}
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
	add := []astronv1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}}
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
		[]astronv1alpha1.ResourceSelector{{Version: "v1", Kind: "Pod"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

const fileProjection = `apiVersion: astron.astron.io/v1alpha1
kind: GraphProjection
metadata:
  name: web
  namespace: demo
spec:
  scope:
    resources:
    - kind: Pod
      version: v1
  relationships: []
`

func writeTempManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proj.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing temp manifest: %v", err)
	}
	return path
}

func TestUpdateFileAddsResourceAndRelationship(t *testing.T) {
	path := writeTempManifest(t, fileProjection)

	out, err := runCmd(t, "projections", "update", "demo", "web", "-f", path, "--add", "v1/Service")
	if err != nil {
		t.Fatalf("update -f failed: %v (%s)", err, out)
	}
	if !strings.Contains(out, "updated in "+path+" (resources +1/-0, relationships +1/-0)") {
		t.Errorf("unexpected confirmation: %q", out)
	}

	s := string(mustReadFile(t, path))
	if !strings.Contains(s, "kind: Service") || !strings.Contains(s, "kind: Pod") {
		t.Errorf("expected Pod and Service in resources:\n%s", s)
	}
	if !strings.Contains(s, "name: service-selects-pod") || !strings.Contains(s, "strategy: LabelSelector") {
		t.Errorf("expected service-selects-pod relationship:\n%s", s)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}

func TestUpdateFileMultiDocumentPreservesOtherDocs(t *testing.T) {
	content := fileProjection + `---
# keep me
apiVersion: astron.astron.io/v1alpha1
kind: GraphView
metadata:
  name: web-compute
  namespace: demo
spec:
  projectionRef:
    name: web
    namespace: demo
`
	path := writeTempManifest(t, content)

	if _, err := runCmd(t, "projections", "update", "demo", "web", "-f", path, "--add", "v1/Service"); err != nil {
		t.Fatalf("update -f failed: %v", err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	if !strings.Contains(s, "# keep me") || !strings.Contains(s, "kind: GraphView") {
		t.Errorf("expected the GraphView document to be preserved:\n%s", s)
	}
	if !strings.Contains(s, "name: service-selects-pod") {
		t.Errorf("expected the projection to gain the relationship:\n%s", s)
	}
}

func TestUpdateFileNotMatchingIsError(t *testing.T) {
	path := writeTempManifest(t, fileProjection)
	_, err := runCmd(t, "projections", "update", "demo", "other", "-f", path, "--add", "v1/Service")
	if err == nil || !strings.Contains(err.Error(), "no GraphProjection") {
		t.Fatalf("expected not-found error for mismatched name, got %v", err)
	}
}

func TestUpdateFileUnchanged(t *testing.T) {
	path := writeTempManifest(t, fileProjection)
	before, _ := os.ReadFile(path)
	// Pod is already present; adding it again is a no-op.
	out, err := runCmd(t, "projections", "update", "demo", "web", "-f", path, "--add", "v1/Pod")
	if err != nil {
		t.Fatalf("update -f failed: %v", err)
	}
	if !strings.Contains(out, "unchanged in "+path) {
		t.Errorf("expected unchanged message, got: %q", out)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("file should be untouched when nothing changed")
	}
}

func TestSplitAndJoinYAMLDocuments(t *testing.T) {
	in := "---\napiVersion: v1\nkind: A\n---\nkind: B\n"
	docs := splitYAMLDocuments(in)
	// Leading separator yields an empty first document.
	nonEmpty := 0
	for _, d := range docs {
		if strings.TrimSpace(d) != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 2 {
		t.Fatalf("expected 2 non-empty documents, got %d from %q", nonEmpty, docs)
	}
	joined := joinYAMLDocuments(docs)
	if strings.Count(joined, "---\n") != 1 {
		t.Errorf("expected a single separator after dropping the empty leading doc: %q", joined)
	}
	if !strings.Contains(joined, "kind: A") || !strings.Contains(joined, "kind: B") {
		t.Errorf("join lost content: %q", joined)
	}
}
