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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

const demoNS = "demo"

func obj(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	u.SetNamespace(namespace)
	u.SetName(name)
	return u
}

func TestSelectNamespacedKinds(t *testing.T) {
	const ns = "demo"
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Group: "", Version: "v1", Resource: "pods"}:            "PodList",
		{Group: "", Version: "v1", Resource: "configmaps"}:      "ConfigMapList",
		{Group: "", Version: "v1", Resource: "secrets"}:         "SecretList",
		{Group: "", Version: "v1", Resource: "events"}:          "EventList",
		{Group: "apps", Version: "v1", Resource: "deployments"}: "DeploymentList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind,
		obj("v1", "Pod", ns, "p1"),
		obj("v1", "ConfigMap", ns, "c1"),
		// A Secret exists, but in a different namespace, so it must be excluded.
		obj("v1", "Secret", "other", "s1"),
		// An Event exists in the namespace, but is not a standard kind.
		obj("v1", "Event", ns, "e1"),
		obj("apps/v1", "Deployment", ns, "d1"),
	)

	lists := []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"list"}},
				{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: metav1.Verbs{"list"}},
				{Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: metav1.Verbs{"list"}},
				{Name: "events", Kind: "Event", Namespaced: true, Verbs: metav1.Verbs{"list"}},
				{Name: "pods/log", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"get"}}, // subresource
			},
		},
		{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{
				{Name: "deployments", Kind: "Deployment", Namespaced: true, Verbs: metav1.Verbs{"list"}},
			},
		},
	}

	// Standard-only mode: Event has an instance but is not a standard kind.
	got, err := selectNamespacedKinds(context.Background(), lists, dyn, ns, []string{"ConfigMap"}, true)
	if err != nil {
		t.Fatalf("selectNamespacedKinds failed: %v", err)
	}

	kinds := map[string]bool{}
	for _, s := range got {
		kinds[s.Kind] = true
	}
	if !kinds["Pod"] {
		t.Errorf("expected Pod to be selected: %+v", got)
	}
	if !kinds["Deployment"] {
		t.Errorf("expected Deployment to be selected: %+v", got)
	}
	if kinds["ConfigMap"] {
		t.Errorf("ConfigMap was --excluded but still selected")
	}
	if kinds["Secret"] {
		t.Errorf("Secret has no instance in %q but was selected", ns)
	}
	if kinds["Event"] {
		t.Errorf("Event is not a standard kind but was selected in standard-only mode")
	}

	// With --all-resources, the non-standard Event kind is included.
	gotAll, err := selectNamespacedKinds(context.Background(), lists, dyn, ns, []string{"ConfigMap"}, false)
	if err != nil {
		t.Fatalf("selectNamespacedKinds (all) failed: %v", err)
	}
	allKinds := map[string]bool{}
	for _, s := range gotAll {
		allKinds[s.Kind] = true
	}
	if !allKinds["Event"] {
		t.Errorf("expected Event to be selected with --all-resources: %+v", gotAll)
	}
}

// TestSelectNamespacedKindsAllListsForbidden verifies that when every
// candidate kind fails to list (e.g. a ServiceAccount without read RBAC), the
// command reports a permissions problem instead of the misleading "no
// instances found".
func TestSelectNamespacedKindsAllListsForbidden(t *testing.T) {
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "pods"}: "PodList",
	})
	dyn.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "pods"}, "", errors.New("no RBAC"))
	})

	lists := []*metav1.APIResourceList{{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{
			{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"list"}},
		},
	}}

	_, err := selectNamespacedKinds(context.Background(), lists, dyn, demoNS, nil, true)
	if err == nil {
		t.Fatal("expected an error when every kind fails to list")
	}
	for _, want := range []string{"authorized", "forbidden"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestBuildRelationshipsGatedOnKinds(t *testing.T) {
	// Only Service and Pod present: only the service-selects-pod rule applies.
	rules := buildRelationships([]astronv1alpha1.ResourceSelector{service, pod})
	if len(rules) != 1 || rules[0].Name != "service-selects-pod" {
		t.Fatalf("expected only service-selects-pod, got %+v", rules)
	}

	// No Pod present: nothing that targets Pod should be emitted.
	rules = buildRelationships([]astronv1alpha1.ResourceSelector{service, configMap})
	if len(rules) != 0 {
		t.Fatalf("expected no rules without Pod, got %+v", rules)
	}

	// PVC and Pod present: the pvc-mounts-pod MOUNTS rule should be emitted so
	// PVCs get linked to the Pods that mount them.
	rules = buildRelationships([]astronv1alpha1.ResourceSelector{pvc, pod})
	var pvcRule *astronv1alpha1.RelationshipRule
	for i := range rules {
		if rules[i].Name == "pvc-mounts-pod" {
			pvcRule = &rules[i]
		}
	}
	if pvcRule == nil {
		t.Fatalf("expected pvc-mounts-pod rule, got %+v", rules)
	}
	if pvcRule.Type != "MOUNTS" || pvcRule.Strategy != astronv1alpha1.VolumeMountStrategy {
		t.Errorf("unexpected pvc-mounts-pod rule: %+v", *pvcRule)
	}
	if pvcRule.From.Kind != "PersistentVolumeClaim" || pvcRule.To.Kind != "Pod" {
		t.Errorf("unexpected pvc-mounts-pod endpoints: %+v", *pvcRule)
	}
}

func TestBuildManifest(t *testing.T) {
	gopts := &generateOptions{
		options:           &options{},
		name:              "",
		neo4jURI:          "neo4j://example:7687",
		neo4jDatabase:     "neo4j",
		neo4jSecret:       "creds",
		resyncInterval:    "10m",
		withRelationships: true,
	}
	selectors := []astronv1alpha1.ResourceSelector{pod, service, configMap}

	m := buildManifest(gopts, demoNS, selectors)

	if m.APIVersion != astronv1alpha1.GroupVersion.String() {
		t.Errorf("unexpected apiVersion: %s", m.APIVersion)
	}
	if m.Kind != "GraphProjection" {
		t.Errorf("unexpected kind: %s", m.Kind)
	}
	if m.Metadata.Name != demoNS || m.Metadata.Namespace != demoNS {
		t.Errorf("unexpected metadata: %+v", m.Metadata)
	}
	if len(m.Spec.Scope.Namespaces) != 1 || m.Spec.Scope.Namespaces[0] != demoNS {
		t.Errorf("expected scope namespaces [demo], got %+v", m.Spec.Scope.Namespaces)
	}
	if m.Spec.ResyncInterval == nil || m.Spec.ResyncInterval.Minutes() != 10 {
		t.Errorf("expected 10m resync interval, got %+v", m.Spec.ResyncInterval)
	}
	if len(m.Spec.Relationships) == 0 {
		t.Errorf("expected relationships to be populated")
	}
}

func TestBuildManifestWithoutRelationships(t *testing.T) {
	gopts := &generateOptions{options: &options{}, withRelationships: false}
	m := buildManifest(gopts, demoNS, []astronv1alpha1.ResourceSelector{pod, service})
	if len(m.Spec.Relationships) != 0 {
		t.Errorf("expected no relationships, got %+v", m.Spec.Relationships)
	}
}

func TestWriteManifestToStdout(t *testing.T) {
	m := buildManifest(&generateOptions{options: &options{}}, demoNS, []astronv1alpha1.ResourceSelector{pod})
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := writeManifest(cmd, "", m); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}
	if !strings.Contains(out.String(), "kind: GraphProjection") {
		t.Fatalf("unexpected stdout: %s", out.String())
	}
}

func TestWriteManifestToFile(t *testing.T) {
	m := buildManifest(&generateOptions{options: &options{}}, demoNS, []astronv1alpha1.ResourceSelector{pod})
	path := filepath.Join(t.TempDir(), "proj.yaml")
	cmd := &cobra.Command{}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if err := writeManifest(cmd, path, m); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected nothing on stdout, got %q", out.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(data), "kind: GraphProjection") || !strings.Contains(string(data), "name: demo") {
		t.Fatalf("unexpected file contents: %s", data)
	}
}

func TestParseViewSelection(t *testing.T) {
	if got, err := parseViewSelection(""); err != nil || got != nil {
		t.Fatalf("empty selection = %v, %v; want nil, nil", got, err)
	}
	all, err := parseViewSelection("defaults")
	if err != nil || len(all) != 4 {
		t.Fatalf("defaults = %v (%d), %v; want 4 views", all, len(all), err)
	}
	sel, err := parseViewSelection("compute, persistence, Compute")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sel) != 2 || sel[0].displayName != "Compute" || sel[1].displayName != "Persistence" {
		t.Fatalf("unexpected selection: %+v", sel)
	}
	if _, err := parseViewSelection("compute,bogus"); err == nil {
		t.Fatal("expected error for unknown view name")
	}
}

func TestWriteManifestsWithViews(t *testing.T) {
	m := buildManifest(&generateOptions{options: &options{}}, demoNS, []astronv1alpha1.ResourceSelector{pod})
	views, _ := parseViewSelection("compute,networking")
	vms := make([]viewManifest, 0, len(views))
	for _, v := range views {
		vms = append(vms, buildViewManifest(demoNS, m.Metadata.Name, v))
	}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := writeManifests(cmd, "", m, vms); err != nil {
		t.Fatalf("writeManifests: %v", err)
	}
	s := out.String()
	if strings.Count(s, "---") != 2 {
		t.Errorf("expected 2 document separators, got:\n%s", s)
	}
	for _, want := range []string{"kind: GraphProjection", "kind: GraphView", "name: demo-compute", "name: demo-networking", "displayName: Compute"} {
		if !strings.Contains(s, want) {
			t.Errorf("multi-doc output missing %q:\n%s", want, s)
		}
	}
}

func TestApplyView(t *testing.T) {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{graphViewGVR: "GraphViewList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)

	views, _ := parseViewSelection("compute")
	vm := buildViewManifest(demoNS, "web", views[0])

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := applyView(cmd, dyn, vm); err != nil {
		t.Fatalf("applyView: %v", err)
	}
	if !strings.Contains(out.String(), "graphview.astron.astron.io/web-compute created in namespace demo") {
		t.Errorf("unexpected confirmation: %q", out.String())
	}

	got, err := dyn.Resource(graphViewGVR).Namespace(demoNS).Get(context.Background(), "web-compute", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after apply: %v", err)
	}
	name, _, _ := unstructured.NestedString(got.Object, "spec", "projectionRef", "name")
	if name != "web" {
		t.Fatalf("expected projectionRef.name web, got %q", name)
	}
	mode, _, _ := unstructured.NestedString(got.Object, "spec", "filters", "kindMode")
	if mode != "show" {
		t.Errorf("compute view should use allow-list mode, got %q", mode)
	}
	visible, _, _ := unstructured.NestedStringSlice(got.Object, "spec", "filters", "visibleKinds")
	found := false
	for _, k := range visible {
		if k == "Pod" {
			found = true
		}
	}
	if !found {
		t.Errorf("compute view should show Pod: %v", visible)
	}
}

func TestApplyProjection(t *testing.T) {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		graphProjectionGVR: "GraphProjectionList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)

	gopts := &generateOptions{
		options:       &options{},
		neo4jURI:      "neo4j://x:7687",
		neo4jDatabase: "neo4j",
		neo4jSecret:   "creds",
	}
	m := buildManifest(gopts, demoNS, []astronv1alpha1.ResourceSelector{pod, service})

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := applyProjection(cmd, dyn, m); err != nil {
		t.Fatalf("applyProjection: %v", err)
	}
	if !strings.Contains(out.String(), "graphprojection.astron.astron.io/demo created") {
		t.Errorf("unexpected confirmation: %q", out.String())
	}

	// Re-applying updates in place and reports "configured".
	out.Reset()
	if err := applyProjection(cmd, dyn, m); err != nil {
		t.Fatalf("re-applyProjection: %v", err)
	}
	if !strings.Contains(out.String(), "graphprojection.astron.astron.io/demo configured") {
		t.Errorf("expected 'configured' on re-apply, got: %q", out.String())
	}

	got, err := dyn.Resource(graphProjectionGVR).Namespace(demoNS).Get(context.Background(), demoNS, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after apply: %v", err)
	}
	if got.GetKind() != "GraphProjection" || got.GetName() != demoNS {
		t.Fatalf("unexpected applied object: %s/%s", got.GetKind(), got.GetName())
	}
	ns, found, _ := unstructured.NestedStringSlice(got.Object, "spec", "scope", "namespaces")
	if !found || len(ns) != 1 || ns[0] != demoNS {
		t.Fatalf("expected spec.scope.namespaces [demo], got %v (found=%v)", ns, found)
	}
}

func TestDeleteProjection(t *testing.T) {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		graphProjectionGVR: "GraphProjectionList",
	}
	existing := obj(astronv1alpha1.GroupVersion.String(), "GraphProjection", demoNS, "web")
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, existing)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := deleteProjection(cmd, dyn, demoNS, "web"); err != nil {
		t.Fatalf("deleteProjection: %v", err)
	}
	if !strings.Contains(out.String(), "graphprojection.astron.astron.io/web deleted from namespace demo") {
		t.Errorf("unexpected confirmation: %q", out.String())
	}

	// The object is gone after deletion.
	if _, err := dyn.Resource(graphProjectionGVR).Namespace(demoNS).Get(context.Background(), "web", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound after delete, got %v", err)
	}

	// Deleting a missing projection reports a clear not-found error.
	err := deleteProjection(cmd, dyn, demoNS, "web")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func specConfigMap(namespace, name, spec string) *unstructured.Unstructured {
	u := obj("v1", "ConfigMap", namespace, name)
	if spec != "" {
		_ = unstructured.SetNestedField(u.Object, spec, "data", "spec")
	}
	return u
}

func TestLoadSpecOverlay(t *testing.T) {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{configMapGVR: "ConfigMapList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind,
		specConfigMap(demoNS, "shared-spec", "graphRAG:\n  enabled: true\n"),
		specConfigMap("astron", "global-spec", "resyncInterval: 15m\n"),
		specConfigMap(demoNS, "no-spec", ""),
		specConfigMap(demoNS, "bad-spec", "{not yaml: ["),
	)
	ctx := context.Background()

	// Bare name resolves in the target namespace.
	overlay, err := loadSpecOverlay(ctx, dyn, demoNS, "shared-spec")
	if err != nil {
		t.Fatalf("loadSpecOverlay: %v", err)
	}
	rag, ok := overlay["graphRAG"].(map[string]any)
	if !ok || rag["enabled"] != true {
		t.Fatalf("unexpected overlay: %+v", overlay)
	}

	// namespace/name resolves in the named namespace.
	overlay, err = loadSpecOverlay(ctx, dyn, demoNS, "astron/global-spec")
	if err != nil {
		t.Fatalf("loadSpecOverlay (namespace/name): %v", err)
	}
	if overlay["resyncInterval"] != "15m" {
		t.Fatalf("unexpected overlay: %+v", overlay)
	}

	// Missing ConfigMap, missing "spec" key, and invalid YAML all error clearly.
	if _, err := loadSpecOverlay(ctx, dyn, demoNS, "absent"); err == nil || !strings.Contains(err.Error(), "fetching ConfigMap") {
		t.Errorf("expected fetch error, got %v", err)
	}
	if _, err := loadSpecOverlay(ctx, dyn, demoNS, "no-spec"); err == nil || !strings.Contains(err.Error(), `no "spec" key`) {
		t.Errorf("expected missing-key error, got %v", err)
	}
	if _, err := loadSpecOverlay(ctx, dyn, demoNS, "bad-spec"); err == nil || !strings.Contains(err.Error(), "parsing") {
		t.Errorf("expected parse error, got %v", err)
	}
	if _, err := loadSpecOverlay(ctx, dyn, demoNS, "/name"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected invalid-reference error, got %v", err)
	}
}

func TestMergeSpecOverlay(t *testing.T) {
	gopts := &generateOptions{
		options:           &options{},
		neo4jURI:          "neo4j://example:7687",
		neo4jDatabase:     "neo4j",
		neo4jSecret:       "creds",
		resyncInterval:    "10m",
		withRelationships: true,
	}
	m := buildManifest(gopts, demoNS, []astronv1alpha1.ResourceSelector{pod, service})

	overlay := map[string]any{
		"graphRAG": map[string]any{
			"enabled": true,
			"embedding": map[string]any{
				"provider": "openai",
				"model":    "text-embedding-3-small",
			},
		},
		"neo4j": map[string]any{"database": "override"},
	}
	if err := mergeSpecOverlay(&m, overlay); err != nil {
		t.Fatalf("mergeSpecOverlay: %v", err)
	}

	// Overlay values are applied.
	if m.Spec.GraphRAG == nil || !m.Spec.GraphRAG.Enabled || m.Spec.GraphRAG.Embedding.Model != "text-embedding-3-small" {
		t.Errorf("graphRAG overlay not merged: %+v", m.Spec.GraphRAG)
	}
	// Nested maps merge: the overridden key wins, siblings survive.
	if m.Spec.Neo4j.Database != "override" {
		t.Errorf("expected neo4j.database override, got %q", m.Spec.Neo4j.Database)
	}
	if m.Spec.Neo4j.URI != "neo4j://example:7687" {
		t.Errorf("generated neo4j.uri lost in merge: %q", m.Spec.Neo4j.URI)
	}
	// Generated fields not present in the overlay are untouched.
	if len(m.Spec.Scope.Namespaces) != 1 || m.Spec.Scope.Namespaces[0] != demoNS {
		t.Errorf("scope lost in merge: %+v", m.Spec.Scope)
	}
	if len(m.Spec.Relationships) == 0 {
		t.Errorf("relationships lost in merge")
	}

	// Unknown fields in the overlay are rejected.
	if err := mergeSpecOverlay(&m, map[string]any{"bogusField": 1}); err == nil {
		t.Fatal("expected error for unknown overlay field")
	}

	// An empty overlay is a no-op.
	before := m.Spec
	if err := mergeSpecOverlay(&m, nil); err != nil {
		t.Fatalf("empty overlay: %v", err)
	}
	if m.Spec.Neo4j != before.Neo4j {
		t.Errorf("empty overlay changed spec")
	}
}

func TestParseDuration(t *testing.T) {
	d, err := parseDuration("5m")
	if err != nil || d == nil || d.Minutes() != 5 {
		t.Fatalf("parseDuration(5m) = %v, %v", d, err)
	}
	d, err = parseDuration("")
	if err != nil || d != nil {
		t.Fatalf("parseDuration(\"\") = %v, %v; want nil, nil", d, err)
	}
}

func TestProjectionsAddCommandWiring(t *testing.T) {
	cmd := newProjectionsAddCmd(&options{})

	// add always applies, so the generate-only output flags must not exist.
	if cmd.Flags().Lookup("apply") != nil {
		t.Error("add should not expose an --apply flag")
	}
	if cmd.Flags().Lookup("output-file") != nil {
		t.Error("add should not expose an --output-file flag")
	}

	// The shared generation flags are available.
	for _, name := range []string{
		"kubeconfig", "context", "name", "neo4j-uri", "neo4j-database",
		"neo4j-secret", "neo4j-secret-namespace", "resync-interval",
		"with-relationships", "views", "exclude", "all-resources",
		"spec-from-configmap",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("add is missing shared flag --%s", name)
		}
	}
}

func TestProjectionsAddRegistered(t *testing.T) {
	root := newRootCmd()
	cmd, _, err := root.Find([]string{"projections", "add"})
	if err != nil || cmd.Name() != "add" {
		t.Fatalf("projections add not registered: %v", err)
	}
	// The create alias resolves to the same command.
	alias, _, err := root.Find([]string{"projections", "create"})
	if err != nil || alias != cmd {
		t.Fatalf("projections create alias not resolved: %v", err)
	}
}

func TestBuildManifestLabelSelectorAndCRDs(t *testing.T) {
	gopts := &generateOptions{
		options:       &options{},
		labelSelector: "app=web,env in (prod,staging)",
		includeCRDs:   true,
	}
	m := buildManifest(gopts, demoNS, []astronv1alpha1.ResourceSelector{pod})

	sel := m.Spec.Scope.LabelSelector
	if sel == nil || sel.MatchLabels["app"] != "web" || len(sel.MatchExpressions) != 1 {
		t.Fatalf("unexpected label selector: %+v", sel)
	}
	if m.Spec.Scope.CRDs == nil || !m.Spec.Scope.CRDs.Include {
		t.Errorf("expected scope.crds.include=true, got %+v", m.Spec.Scope.CRDs)
	}

	// Both features default to off.
	m = buildManifest(&generateOptions{options: &options{}}, demoNS, []astronv1alpha1.ResourceSelector{pod})
	if m.Spec.Scope.LabelSelector != nil || m.Spec.Scope.CRDs != nil {
		t.Errorf("selector/CRDs should be unset by default: %+v", m.Spec.Scope)
	}
}

func TestParseLabelSelector(t *testing.T) {
	if sel, err := parseLabelSelector(""); err != nil || sel != nil {
		t.Fatalf("empty selector = %v, %v; want nil, nil", sel, err)
	}
	if _, err := parseLabelSelector("=bad="); err == nil {
		t.Fatal("expected error for invalid selector")
	}
}
