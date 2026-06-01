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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
)

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
	if e := findEdge(edges, "SELECTS", "web", "web-1"); e == nil {
		t.Errorf("expected SELECTS web->web-1, got %+v", edges)
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
	if findEdge(edges, "MOUNTS", "env-config", "web-1") == nil {
		t.Errorf("expected MOUNTS env-config->web-1")
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
