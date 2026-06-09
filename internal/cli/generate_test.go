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
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
)

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
		{Group: "apps", Version: "v1", Resource: "deployments"}: "DeploymentList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind,
		obj("v1", "Pod", ns, "p1"),
		obj("v1", "ConfigMap", ns, "c1"),
		// A Secret exists, but in a different namespace, so it must be excluded.
		obj("v1", "Secret", "other", "s1"),
		obj("apps/v1", "Deployment", ns, "d1"),
	)

	lists := []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"list"}},
				{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: metav1.Verbs{"list"}},
				{Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: metav1.Verbs{"list"}},
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

	got, err := selectNamespacedKinds(context.Background(), lists, dyn, ns, []string{"ConfigMap"})
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
}

func TestBuildRelationshipsGatedOnKinds(t *testing.T) {
	// Only Service and Pod present: only the service-selects-pod rule applies.
	rules := buildRelationships([]gamerav1alpha1.ResourceSelector{service, pod})
	if len(rules) != 1 || rules[0].Name != "service-selects-pod" {
		t.Fatalf("expected only service-selects-pod, got %+v", rules)
	}

	// No Pod present: nothing that targets Pod should be emitted.
	rules = buildRelationships([]gamerav1alpha1.ResourceSelector{service, configMap})
	if len(rules) != 0 {
		t.Fatalf("expected no rules without Pod, got %+v", rules)
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
	selectors := []gamerav1alpha1.ResourceSelector{pod, service, configMap}

	m := buildManifest(gopts, "demo", selectors)

	if m.APIVersion != gamerav1alpha1.GroupVersion.String() {
		t.Errorf("unexpected apiVersion: %s", m.APIVersion)
	}
	if m.Kind != "GraphProjection" {
		t.Errorf("unexpected kind: %s", m.Kind)
	}
	if m.Metadata.Name != "demo" || m.Metadata.Namespace != "demo" {
		t.Errorf("unexpected metadata: %+v", m.Metadata)
	}
	if len(m.Spec.Scope.Namespaces) != 1 || m.Spec.Scope.Namespaces[0] != "demo" {
		t.Errorf("expected scope namespaces [demo], got %+v", m.Spec.Scope.Namespaces)
	}
	if m.Spec.ResyncInterval == nil || m.Spec.ResyncInterval.Duration.Minutes() != 10 {
		t.Errorf("expected 10m resync interval, got %+v", m.Spec.ResyncInterval)
	}
	if len(m.Spec.Relationships) == 0 {
		t.Errorf("expected relationships to be populated")
	}
}

func TestBuildManifestWithoutRelationships(t *testing.T) {
	gopts := &generateOptions{options: &options{}, withRelationships: false}
	m := buildManifest(gopts, "demo", []gamerav1alpha1.ResourceSelector{pod, service})
	if len(m.Spec.Relationships) != 0 {
		t.Errorf("expected no relationships, got %+v", m.Spec.Relationships)
	}
}

func TestParseDuration(t *testing.T) {
	d, err := parseDuration("5m")
	if err != nil || d == nil || d.Duration.Minutes() != 5 {
		t.Fatalf("parseDuration(5m) = %v, %v", d, err)
	}
	d, err = parseDuration("")
	if err != nil || d != nil {
		t.Fatalf("parseDuration(\"\") = %v, %v; want nil, nil", d, err)
	}
}
