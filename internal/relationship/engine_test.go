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

package relationship

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
)

//nolint:unparam // version is always v1 in tests but kept for clarity
func sel(group, version, kind string) gamerav1alpha1.ResourceSelector {
	return gamerav1alpha1.ResourceSelector{Group: group, Version: version, Kind: kind}
}

func obj(apiVersion, kind, namespace, name, uid string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetAPIVersion(apiVersion)
	o.SetKind(kind)
	o.SetNamespace(namespace)
	o.SetName(name)
	o.SetUID(types.UID(uid))
	return o
}

// ownerRefs builds owner references from simple maps for test fixtures.
func ownerRefs(refs ...map[string]any) []metav1.OwnerReference {
	out := make([]metav1.OwnerReference, 0, len(refs))
	for _, r := range refs {
		or := metav1.OwnerReference{
			APIVersion: r["apiVersion"].(string),
			Kind:       r["kind"].(string),
			Name:       r["name"].(string),
			UID:        types.UID(r["uid"].(string)),
		}
		if ctrl, ok := r["controller"].(bool); ok {
			or.Controller = &ctrl
		}
		out = append(out, or)
	}
	return out
}

//nolint:unparam // toName is always web-1 in current tests but kept for generality
func findEdge(edges []graph.Relationship, typ, fromName, toName string) *graph.Relationship {
	for i := range edges {
		if edges[i].Type == typ && edges[i].From.Name == fromName && edges[i].To.Name == toName {
			return &edges[i]
		}
	}
	return nil
}

func TestOwnerReferenceStrategy(t *testing.T) {
	pod := obj("v1", "Pod", "default", "web-abc", "pod-uid")
	pod.SetOwnerReferences(ownerRefs(map[string]any{
		"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "web-rs", "uid": "rs-uid", "controller": true,
	}))
	other := obj("v1", "Pod", "default", "lone", "lone-uid")

	index := NewMapIndex(pod, other)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "rs-owns-pod", Type: "OWNS", Strategy: gamerav1alpha1.OwnerReferenceStrategy,
		From: sel("apps", "v1", "ReplicaSet"), To: sel("", "v1", "Pod"),
	}

	edges, err := (ownerReferenceStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d: %+v", len(edges), edges)
	}
	e := edges[0]
	if e.Type != "OWNS" || e.From.Name != "web-rs" || e.From.UID != "rs-uid" || e.To.Name != "web-abc" {
		t.Errorf("unexpected edge: %+v", e)
	}
	if e.Properties["controller"] != true {
		t.Errorf("expected controller=true, got %v", e.Properties["controller"])
	}
}

func TestLabelSelectorStrategy_ServiceStyle(t *testing.T) {
	svc := obj("v1", "Service", "default", "web", "svc-uid")
	_ = unstructured.SetNestedStringMap(svc.Object, map[string]string{"app": "web"}, "spec", "selector")

	matching := obj("v1", "Pod", "default", "web-1", "p1")
	matching.SetLabels(map[string]string{"app": "web", "tier": "frontend"})
	nonMatching := obj("v1", "Pod", "default", "db-1", "p2")
	nonMatching.SetLabels(map[string]string{"app": "db"})
	otherNS := obj("v1", "Pod", "other", "web-2", "p3")
	otherNS.SetLabels(map[string]string{"app": "web"})

	index := NewMapIndex(svc, matching, nonMatching, otherNS)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "svc-selects-pod", Type: "SELECTS", Strategy: gamerav1alpha1.LabelSelectorStrategy,
		From: sel("", "v1", "Service"), To: sel("", "v1", "Pod"),
	}

	edges, err := (labelSelectorStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d: %+v", len(edges), edges)
	}
	e := findEdge(edges, "SELECTS", "web", "web-1")
	if e == nil {
		t.Fatalf("expected SELECTS web->web-1, got %+v", edges)
	}
	// The SELECTS edge records the selector that forms the relationship.
	if e.Properties["selector"] != "app=web" {
		t.Errorf("expected selector 'app=web', got %v", e.Properties["selector"])
	}
	if e.Properties["selectorLabels"] != `{"app":"web"}` {
		t.Errorf("expected selectorLabels JSON, got %v", e.Properties["selectorLabels"])
	}
}

func TestLabelSelectorStrategy_WorkloadStyle(t *testing.T) {
	deploy := obj("apps/v1", "Deployment", "default", "web", "d-uid")
	_ = unstructured.SetNestedStringMap(deploy.Object, map[string]string{"app": "web"}, "spec", "selector", "matchLabels")

	pod := obj("v1", "Pod", "default", "web-1", "p1")
	pod.SetLabels(map[string]string{"app": "web"})

	index := NewMapIndex(deploy, pod)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "deploy-targets-pod", Type: "TARGETS", Strategy: gamerav1alpha1.LabelSelectorStrategy,
		From: sel("apps", "v1", "Deployment"), To: sel("", "v1", "Pod"),
	}

	edges, err := (labelSelectorStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if findEdge(edges, "TARGETS", "web", "web-1") == nil {
		t.Errorf("expected TARGETS web->web-1, got %+v", edges)
	}
}

func TestLabelSelectorStrategy_EmptySelectorMatchesNothing(t *testing.T) {
	svc := obj("v1", "Service", "default", "headless", "svc-uid")
	pod := obj("v1", "Pod", "default", "web-1", "p1")
	pod.SetLabels(map[string]string{"app": "web"})

	index := NewMapIndex(svc, pod)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "svc-selects-pod", Type: "SELECTS", Strategy: gamerav1alpha1.LabelSelectorStrategy,
		From: sel("", "v1", "Service"), To: sel("", "v1", "Pod"),
	}

	edges, err := (labelSelectorStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected no edges for empty selector, got %+v", edges)
	}
}

func TestVolumeMountStrategy_ConfigMap(t *testing.T) {
	cm := obj("v1", "ConfigMap", "default", "app-config", "cm-uid")
	pod := obj("v1", "Pod", "default", "web-1", "p1")
	_ = unstructured.SetNestedSlice(pod.Object, []any{
		map[string]any{"name": "config", "configMap": map[string]any{"name": "app-config"}},
	}, "spec", "volumes")
	_ = unstructured.SetNestedSlice(pod.Object, []any{
		map[string]any{
			"name": "app",
			"envFrom": []any{
				map[string]any{"configMapRef": map[string]any{"name": "env-config"}},
			},
		},
	}, "spec", "containers")

	index := NewMapIndex(cm, pod)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "cm-mounts-pod", Type: "MOUNTS", Strategy: gamerav1alpha1.VolumeMountStrategy,
		From: sel("", "v1", "ConfigMap"), To: sel("", "v1", "Pod"),
	}

	edges, err := (volumeMountStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// app-config (volume, in index -> has UID) and env-config (envFrom, not in index)
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d: %+v", len(edges), edges)
	}
	mounted := findEdge(edges, "MOUNTS", "app-config", "web-1")
	if mounted == nil {
		t.Fatalf("expected MOUNTS app-config->web-1")
	}
	if mounted.From.UID != "cm-uid" {
		t.Errorf("expected indexed configmap UID to be resolved, got %q", mounted.From.UID)
	}
	// app-config is referenced through a volume; the edge records the mechanism.
	if got := mounted.Properties["via"]; !reflect.DeepEqual(got, []string{"volume"}) {
		t.Errorf("expected via [volume] for app-config, got %v", got)
	}
	envEdge := findEdge(edges, "MOUNTS", "env-config", "web-1")
	if envEdge == nil {
		t.Fatalf("expected MOUNTS env-config->web-1")
	}
	if got := envEdge.Properties["via"]; !reflect.DeepEqual(got, []string{"envFrom"}) {
		t.Errorf("expected via [envFrom] for env-config, got %v", got)
	}
}

func TestClaimRefStrategy(t *testing.T) {
	pvc := obj("v1", kindPersistentVolumeClaim, "default", "data-web", "pvc-uid")
	pv := obj("v1", "PersistentVolume", "", "pv-abc", "pv-uid")
	_ = unstructured.SetNestedField(pv.Object, "default", "spec", "claimRef", "namespace")
	_ = unstructured.SetNestedField(pv.Object, "data-web", "spec", "claimRef", "name")

	index := NewMapIndex(pvc, pv)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "pv-binds-pvc", Type: "BINDS", Strategy: gamerav1alpha1.ClaimRefStrategy,
		From: sel("", "v1", "PersistentVolume"), To: sel("", "v1", kindPersistentVolumeClaim),
	}
	edges, err := (claimRefStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d: %+v", len(edges), edges)
	}
	e := edges[0]
	if e.From.Kind != "PersistentVolume" || e.From.Name != "pv-abc" {
		t.Errorf("unexpected from: %+v", e.From)
	}
	if e.To.Kind != kindPersistentVolumeClaim || e.To.Name != "data-web" || e.To.UID != "pvc-uid" {
		t.Errorf("unexpected to (PVC UID should resolve): %+v", e.To)
	}

	// Reversed orientation: PVC -> PV.
	rule.From, rule.To = sel("", "v1", kindPersistentVolumeClaim), sel("", "v1", "PersistentVolume")
	edges, _ = (claimRefStrategy{}).Derive(rule, index)
	if len(edges) != 1 || edges[0].From.Kind != kindPersistentVolumeClaim || edges[0].To.Kind != "PersistentVolume" {
		t.Fatalf("expected reversed PVC->PV edge, got %+v", edges)
	}
}

func TestServiceAccountStrategy(t *testing.T) {
	sa := obj("v1", kindServiceAccount, "default", "builder", "sa-uid")
	defSA := obj("v1", kindServiceAccount, "default", "default", "def-uid")

	// Pod with an explicit serviceAccountName.
	explicit := obj("v1", "Pod", "default", "web", "web-uid")
	_ = unstructured.SetNestedField(explicit.Object, "builder", "spec", "serviceAccountName")
	// Pod with only the deprecated serviceAccount field.
	deprecated := obj("v1", "Pod", "default", "legacy", "legacy-uid")
	_ = unstructured.SetNestedField(deprecated.Object, "builder", "spec", "serviceAccount")
	// Pod with neither set: falls back to the namespace "default" SA.
	implicit := obj("v1", "Pod", "default", "bare", "bare-uid")

	index := NewMapIndex(sa, defSA, explicit, deprecated, implicit)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "sa-runs-pod", Type: "RUNS", Strategy: gamerav1alpha1.ServiceAccountStrategy,
		From: sel("", "v1", kindServiceAccount), To: sel("", "v1", "Pod"),
	}
	edges, err := (serviceAccountStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 3 {
		t.Fatalf("expected 3 edges, got %d: %+v", len(edges), edges)
	}

	// Explicit serviceAccountName resolves to the named SA (UID enriched).
	if e := findEdge(edges, "RUNS", "builder", "web"); e == nil {
		t.Errorf("missing builder->web edge: %+v", edges)
	} else if e.From.UID != "sa-uid" || e.From.Kind != kindServiceAccount {
		t.Errorf("explicit SA edge should resolve UID: %+v", e.From)
	}
	// Deprecated spec.serviceAccount is honored.
	if findEdge(edges, "RUNS", "builder", "legacy") == nil {
		t.Errorf("missing builder->legacy edge (deprecated field): %+v", edges)
	}
	// No SA set falls back to the "default" SA.
	if e := findEdge(edges, "RUNS", "default", "bare"); e == nil {
		t.Errorf("missing default->bare edge (implicit default SA): %+v", edges)
	} else if e.From.UID != "def-uid" {
		t.Errorf("implicit default SA edge should resolve UID: %+v", e.From)
	}
}

func TestServiceBackendStrategy_Ingress(t *testing.T) {
	svc := obj("v1", "Service", "default", "web", "svc-uid")
	ing := obj("networking.k8s.io/v1", "Ingress", "default", "web-ing", "ing-uid")
	_ = unstructured.SetNestedField(ing.Object, "web", "spec", "defaultBackend", "service", "name")
	_ = unstructured.SetNestedSlice(ing.Object, []any{
		map[string]any{"host": "example.com", "http": map[string]any{"paths": []any{
			map[string]any{"path": "/", "backend": map[string]any{"service": map[string]any{
				"name": "web", "port": map[string]any{"number": int64(80)},
			}}},
			map[string]any{"path": "/api", "backend": map[string]any{"service": map[string]any{
				"name": "api", "port": map[string]any{"name": "http"},
			}}},
		}}},
	}, "spec", "rules")

	index := NewMapIndex(svc, ing)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "ingress-routes-service", Type: "ROUTES", Strategy: gamerav1alpha1.ServiceBackendStrategy,
		From: sel("networking.k8s.io", "v1", "Ingress"), To: sel("", "v1", "Service"),
	}
	edges, err := (serviceBackendStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "web" appears in defaultBackend and a path but must be de-duplicated; "api" is the second.
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges (web, api), got %d: %+v", len(edges), edges)
	}
	web := findEdge(edges, "ROUTES", "web-ing", "web")
	if web == nil || web.To.UID != "svc-uid" {
		t.Fatalf("expected ROUTES web-ing->web with resolved svc UID, got %+v", edges)
	}
	// The web edge aggregates the default backend and the "/" path backend.
	if got := web.Properties["hosts"]; !reflect.DeepEqual(got, []string{"example.com"}) {
		t.Errorf("expected hosts [example.com], got %v", got)
	}
	if got := web.Properties["paths"]; !reflect.DeepEqual(got, []string{"/"}) {
		t.Errorf("expected paths [/], got %v", got)
	}
	if got := web.Properties["ports"]; !reflect.DeepEqual(got, []string{"80"}) {
		t.Errorf("expected ports [80], got %v", got)
	}
	api := findEdge(edges, "ROUTES", "web-ing", "api")
	if api == nil {
		t.Fatalf("expected ROUTES web-ing->api")
	}
	if got := api.Properties["ports"]; !reflect.DeepEqual(got, []string{"http"}) {
		t.Errorf("expected api ports [http], got %v", got)
	}
}

func TestServiceBackendStrategy_HTTPRoute(t *testing.T) {
	route := obj("gateway.networking.k8s.io/v1", "HTTPRoute", "default", "rt", "rt-uid")
	_ = unstructured.SetNestedSlice(route.Object, []any{
		map[string]any{"backendRefs": []any{
			map[string]any{"name": "web", "port": int64(8080)},             // implicit Service
			map[string]any{"name": "api", "kind": "Service"},               // explicit Service
			map[string]any{"name": "bucket", "kind": "S3", "group": "aws"}, // non-Service, ignored
		}},
	}, "spec", "rules")

	index := NewMapIndex(route)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "httproute-routes-service", Type: "ROUTES", Strategy: gamerav1alpha1.ServiceBackendStrategy,
		From: sel("gateway.networking.k8s.io", "v1", "HTTPRoute"), To: sel("", "v1", "Service"),
	}
	edges, err := (serviceBackendStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 Service edges (web, api), got %d: %+v", len(edges), edges)
	}
	webEdge := findEdge(edges, "ROUTES", "rt", "web")
	if webEdge == nil || findEdge(edges, "ROUTES", "rt", "api") == nil {
		t.Errorf("expected ROUTES rt->web and rt->api, got %+v", edges)
	}
	if webEdge != nil {
		if got := webEdge.Properties["ports"]; !reflect.DeepEqual(got, []string{"8080"}) {
			t.Errorf("expected web ports [8080], got %v", got)
		}
	}
	if findEdge(edges, "ROUTES", "rt", "bucket") != nil {
		t.Errorf("non-Service backendRef should be ignored")
	}
}

func TestParentRefStrategy_HTTPRoute(t *testing.T) {
	gw := obj("gateway.networking.k8s.io/v1", "Gateway", "infra", "shared-gw", "gw-uid")
	route := obj("gateway.networking.k8s.io/v1", "HTTPRoute", "default", "rt", "rt-uid")
	_ = unstructured.SetNestedSlice(route.Object, []any{
		// Gateway in another namespace, with attachment metadata.
		map[string]any{"name": "shared-gw", "namespace": "infra", "sectionName": "https", "port": int64(443)},
		// Implicit kind/group (defaults to Gateway in the Gateway API group),
		// in the route's own namespace.
		map[string]any{"name": "local-gw"},
		// Non-Gateway parent, ignored.
		map[string]any{"name": "mesh", "kind": "Mesh", "group": "example.com"},
	}, "spec", "parentRefs")

	index := NewMapIndex(gw, route)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "gateway-routes-httproute", Type: "ROUTES", Strategy: gamerav1alpha1.GatewayParentStrategy,
		From: sel("gateway.networking.k8s.io", "v1", "Gateway"), To: sel("gateway.networking.k8s.io", "v1", "HTTPRoute"),
	}
	edges, err := (parentRefStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 Gateway edges (shared-gw, local-gw), got %d: %+v", len(edges), edges)
	}
	// Edge is oriented Gateway -> HTTPRoute and resolves the in-index Gateway UID.
	shared := findEdge(edges, "ROUTES", "shared-gw", "rt")
	if shared == nil || shared.From.UID != "gw-uid" {
		t.Fatalf("expected ROUTES shared-gw->rt with resolved gateway UID, got %+v", edges)
	}
	if got := shared.Properties["sectionName"]; got != "https" {
		t.Errorf("expected sectionName https, got %v", got)
	}
	if got := shared.Properties["port"]; got != "443" {
		t.Errorf("expected port 443, got %v", got)
	}
	if local := findEdge(edges, "ROUTES", "local-gw", "rt"); local == nil {
		t.Errorf("expected ROUTES local-gw->rt (default namespace/kind/group), got %+v", edges)
	}
	if findEdge(edges, "ROUTES", "mesh", "rt") != nil {
		t.Errorf("non-Gateway parentRef should be ignored")
	}
}

func TestVolumeMountStrategy_PersistentVolumeClaim(t *testing.T) {
	pvc := obj("v1", kindPersistentVolumeClaim, "default", "data", "pvc-uid")
	pod := obj("v1", "Pod", "default", "web-1", "p1")
	_ = unstructured.SetNestedSlice(pod.Object, []any{
		map[string]any{"name": "vol", "persistentVolumeClaim": map[string]any{"claimName": "data"}},
		// A configMap volume must be ignored when deriving PVC edges.
		map[string]any{"name": "cfg", "configMap": map[string]any{"name": "app-config"}},
	}, "spec", "volumes")

	index := NewMapIndex(pvc, pod)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "pvc-mounts-pod", Type: "MOUNTS", Strategy: gamerav1alpha1.VolumeMountStrategy,
		From: sel("", "v1", kindPersistentVolumeClaim), To: sel("", "v1", "Pod"),
	}

	edges, err := (volumeMountStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d: %+v", len(edges), edges)
	}
	e := findEdge(edges, "MOUNTS", "data", "web-1")
	if e == nil {
		t.Fatalf("expected MOUNTS data->web-1")
	}
	if e.From.Kind != kindPersistentVolumeClaim || e.From.UID != "pvc-uid" {
		t.Errorf("unexpected from ref: %+v", e.From)
	}
}

func TestVolumeMountStrategy_SecretAndDedup(t *testing.T) {
	pod := obj("v1", "Pod", "default", "web-1", "p1")
	_ = unstructured.SetNestedSlice(pod.Object, []any{
		map[string]any{"name": "s", "secret": map[string]any{"secretName": "tls"}},
	}, "spec", "volumes")
	_ = unstructured.SetNestedSlice(pod.Object, []any{
		map[string]any{
			"name": "app",
			"env": []any{
				map[string]any{"name": "T", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "tls"}}},
			},
		},
	}, "spec", "containers")

	index := NewMapIndex(pod)
	rule := gamerav1alpha1.RelationshipRule{
		Name: "secret-mounts-pod", Type: "MOUNTS", Strategy: gamerav1alpha1.VolumeMountStrategy,
		From: sel("", "v1", "Secret"), To: sel("", "v1", "Pod"),
	}

	edges, err := (volumeMountStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "tls" referenced twice (volume + env) should be de-duplicated.
	if len(edges) != 1 {
		t.Fatalf("expected 1 deduplicated edge, got %d: %+v", len(edges), edges)
	}
}

func TestEngine_DeriveAggregatesAndReportsUnknownStrategy(t *testing.T) {
	pod := obj("v1", "Pod", "default", "web", "p1")
	pod.SetOwnerReferences(ownerRefs(map[string]any{
		"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "rs", "uid": "rs-uid",
	}))
	index := NewMapIndex(pod)

	rules := []gamerav1alpha1.RelationshipRule{
		{Name: "ok", Type: "OWNS", Strategy: gamerav1alpha1.OwnerReferenceStrategy,
			From: sel("apps", "v1", "ReplicaSet"), To: sel("", "v1", "Pod")},
		{Name: "bad", Type: "X", Strategy: gamerav1alpha1.RelationshipStrategy("Bogus"),
			From: sel("", "v1", "Service"), To: sel("", "v1", "Pod")},
	}

	edges, err := NewEngine().Derive(rules, index)
	if err == nil {
		t.Error("expected an error for the unknown strategy")
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge from the valid rule, got %d", len(edges))
	}
}
