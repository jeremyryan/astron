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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

// certManagerIndex builds a MapIndex with scope and namespace resolution
// configured the way the real Projector would, for a cert-manager-shaped
// fixture: a namespaced Issuer, a cluster-scoped ClusterIssuer, and a
// Certificate that references one or the other via spec.issuerRef.
func certManagerIndex(objs ...*unstructured.Unstructured) *MapIndex {
	return NewMapIndex(objs...).
		WithScope([]astronv1alpha1.ResourceSelector{
			{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"},
			{Group: "cert-manager.io", Version: "v1", Kind: "Issuer"},
			{Group: "cert-manager.io", Version: "v1", Kind: "ClusterIssuer"},
			{Version: "v1", Kind: "Secret"},
		}).
		WithNamespaceResolver(func(gvk schema.GroupVersionKind) (bool, bool) {
			if gvk.Kind == "ClusterIssuer" {
				return false, true
			}
			return true, true
		})
}

func withSpecField(o *unstructured.Unstructured, value any, path ...string) *unstructured.Unstructured {
	if err := unstructured.SetNestedField(o.Object, value, path...); err != nil {
		panic(err)
	}
	return o
}

func TestFieldReferenceStrategy_PlainStringField(t *testing.T) {
	cert := withSpecField(obj("cert-manager.io/v1", "Certificate", "shop", "web-tls", "cert-uid"),
		"web-tls-secret", "spec", "secretName")
	secret := obj("v1", "Secret", "shop", "web-tls-secret", "secret-uid")

	index := certManagerIndex(cert, secret)
	rule := astronv1alpha1.RelationshipRule{
		Name: "certificate-uses-secret", Type: "USES_SECRET", Strategy: astronv1alpha1.FieldReferenceStrategy,
		From: sel("cert-manager.io", "v1", "Certificate"), To: sel("", "v1", "Secret"),
		FieldRef: &astronv1alpha1.FieldReferenceSpec{NamePath: "spec.secretName"},
	}

	edges, err := (fieldReferenceStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := findEdge(edges, "USES_SECRET", "web-tls", "web-tls-secret")
	if e == nil {
		t.Fatalf("expected an edge, got: %+v", edges)
	}
	if e.To.UID != "secret-uid" {
		t.Errorf("expected the real secret UID to be resolved, got %q", e.To.UID)
	}
}

func TestFieldReferenceStrategy_KindPathDisambiguatesNamespacedTarget(t *testing.T) {
	cert := withSpecField(obj("cert-manager.io/v1", "Certificate", "shop", "web-tls", "cert-uid"),
		map[string]any{"name": "letsencrypt", "kind": "Issuer"}, "spec", "issuerRef")
	issuer := obj("cert-manager.io/v1", "Issuer", "shop", "letsencrypt", "issuer-uid")

	index := certManagerIndex(cert, issuer)
	rule := astronv1alpha1.RelationshipRule{
		Name: "certificate-issued-by", Type: "ISSUED_BY", Strategy: astronv1alpha1.FieldReferenceStrategy,
		From: sel("cert-manager.io", "v1", "Certificate"), To: sel("cert-manager.io", "v1", "Issuer"),
		FieldRef: &astronv1alpha1.FieldReferenceSpec{
			NamePath: "spec.issuerRef.name",
			KindPath: "spec.issuerRef.kind",
		},
	}

	edges, err := (fieldReferenceStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := findEdge(edges, "ISSUED_BY", "web-tls", "letsencrypt")
	if e == nil {
		t.Fatalf("expected an edge, got: %+v", edges)
	}
	if e.To.Kind != "Issuer" || e.To.Namespace != "shop" || e.To.UID != "issuer-uid" {
		t.Errorf("unexpected target ref: %+v", e.To)
	}
}

func TestFieldReferenceStrategy_KindPathDisambiguatesClusterScopedTarget(t *testing.T) {
	cert := withSpecField(obj("cert-manager.io/v1", "Certificate", "shop", "web-tls", "cert-uid"),
		map[string]any{"name": "letsencrypt-prod", "kind": "ClusterIssuer"}, "spec", "issuerRef")
	clusterIssuer := obj("cert-manager.io/v1", "ClusterIssuer", "", "letsencrypt-prod", "ci-uid")

	index := certManagerIndex(cert, clusterIssuer)
	rule := astronv1alpha1.RelationshipRule{
		Name: "certificate-issued-by", Type: "ISSUED_BY", Strategy: astronv1alpha1.FieldReferenceStrategy,
		From: sel("cert-manager.io", "v1", "Certificate"), To: sel("cert-manager.io", "v1", "Issuer"),
		FieldRef: &astronv1alpha1.FieldReferenceSpec{
			NamePath: "spec.issuerRef.name",
			KindPath: "spec.issuerRef.kind",
		},
	}

	edges, err := (fieldReferenceStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := findEdge(edges, "ISSUED_BY", "web-tls", "letsencrypt-prod")
	if e == nil {
		t.Fatalf("expected an edge, got: %+v", edges)
	}
	// The cluster-scoped target must not inherit the Certificate's namespace.
	if e.To.Kind != "ClusterIssuer" || e.To.Namespace != "" || e.To.UID != "ci-uid" {
		t.Errorf("unexpected target ref: %+v", e.To)
	}
}

func TestFieldReferenceStrategy_KindPathOutOfScopeYieldsNoEdgeNoError(t *testing.T) {
	cert := withSpecField(obj("cert-manager.io/v1", "Certificate", "shop", "web-tls", "cert-uid"),
		map[string]any{"name": "vault-issuer", "kind": "VaultIssuer"}, "spec", "issuerRef")

	index := certManagerIndex(cert)
	rule := astronv1alpha1.RelationshipRule{
		Name: "certificate-issued-by", Type: "ISSUED_BY", Strategy: astronv1alpha1.FieldReferenceStrategy,
		From: sel("cert-manager.io", "v1", "Certificate"), To: sel("cert-manager.io", "v1", "Issuer"),
		FieldRef: &astronv1alpha1.FieldReferenceSpec{
			NamePath: "spec.issuerRef.name",
			KindPath: "spec.issuerRef.kind",
		},
	}

	edges, err := (fieldReferenceStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("expected no edges for an out-of-scope kindPath value, got: %+v", edges)
	}
}

func TestFieldReferenceStrategy_ListPathIteratesSubjects(t *testing.T) {
	binding := obj("rbac.authorization.k8s.io/v1", "RoleBinding", "shop", "deployers", "binding-uid")
	if err := unstructured.SetNestedSlice(binding.Object, []any{
		map[string]any{"kind": "ServiceAccount", "name": "ci", "namespace": "shop"},
		map[string]any{"kind": "ServiceAccount", "name": "cd", "namespace": "shop"},
		map[string]any{"kind": "User", "name": "alice"}, // no name field matches namePath shape; still has a name
	}, "subjects"); err != nil {
		t.Fatal(err)
	}
	ci := obj("v1", "ServiceAccount", "shop", "ci", "ci-uid")
	cd := obj("v1", "ServiceAccount", "shop", "cd", "cd-uid")

	index := NewMapIndex(binding, ci, cd).WithScope([]astronv1alpha1.ResourceSelector{
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "RoleBinding"},
		{Version: "v1", Kind: "ServiceAccount"},
	})
	rule := astronv1alpha1.RelationshipRule{
		Name: "binding-subjects", Type: "SUBJECT", Strategy: astronv1alpha1.FieldReferenceStrategy,
		From: sel("rbac.authorization.k8s.io", "v1", "RoleBinding"), To: sel("", "v1", "ServiceAccount"),
		FieldRef: &astronv1alpha1.FieldReferenceSpec{
			ListPath: "subjects",
			NamePath: "name",
		},
	}

	edges, err := (fieldReferenceStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 3 { // ci, cd, and alice (the User "alice" still has a name field; kindPath isn't set so no filtering)
		t.Fatalf("expected 3 edges, got %d: %+v", len(edges), edges)
	}
	if e := findEdge(edges, "SUBJECT", "deployers", "ci"); e == nil || e.To.UID != "ci-uid" {
		t.Errorf("expected a resolved edge to ci, got: %+v", edges)
	}
	if e := findEdge(edges, "SUBJECT", "deployers", "cd"); e == nil || e.To.UID != "cd-uid" {
		t.Errorf("expected a resolved edge to cd, got: %+v", edges)
	}
}

func TestFieldReferenceStrategy_OnToScansTheOtherSide(t *testing.T) {
	// Mirrors VolumeMount's convention: From is the config resource, but the
	// reference field lives on the Pod (the To side).
	pod := withSpecField(obj("v1", "Pod", "shop", "web-1", "pod-uid"),
		"app-config", "spec", "configRef", "name")
	cm := obj("v1", "ConfigMap", "shop", "app-config", "cm-uid")

	index := NewMapIndex(pod, cm).WithScope([]astronv1alpha1.ResourceSelector{
		{Version: "v1", Kind: "Pod"},
		{Version: "v1", Kind: "ConfigMap"},
	})
	rule := astronv1alpha1.RelationshipRule{
		Name: "pod-uses-configmap", Type: "USES", Strategy: astronv1alpha1.FieldReferenceStrategy,
		From: sel("", "v1", "ConfigMap"), To: sel("", "v1", "Pod"),
		FieldRef: &astronv1alpha1.FieldReferenceSpec{
			On:       "To",
			NamePath: "spec.configRef.name",
		},
	}

	edges, err := (fieldReferenceStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := findEdge(edges, "USES", "app-config", "web-1")
	if e == nil {
		t.Fatalf("expected an edge from the ConfigMap to the Pod, got: %+v", edges)
	}
	if e.From.UID != "cm-uid" {
		t.Errorf("expected the real configmap UID to be resolved, got %q", e.From.UID)
	}
}

func TestFieldReferenceStrategy_MissingNamePathValueIsSkippedNotError(t *testing.T) {
	cert := obj("cert-manager.io/v1", "Certificate", "shop", "no-secret-yet", "cert-uid") // spec.secretName unset
	index := certManagerIndex(cert)
	rule := astronv1alpha1.RelationshipRule{
		Name: "certificate-uses-secret", Type: "USES_SECRET", Strategy: astronv1alpha1.FieldReferenceStrategy,
		From: sel("cert-manager.io", "v1", "Certificate"), To: sel("", "v1", "Secret"),
		FieldRef: &astronv1alpha1.FieldReferenceSpec{NamePath: "spec.secretName"},
	}

	edges, err := (fieldReferenceStrategy{}).Derive(rule, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("expected no edges, got: %+v", edges)
	}
}

func TestFieldReferenceStrategy_RequiresFieldRef(t *testing.T) {
	index := NewMapIndex()
	rule := astronv1alpha1.RelationshipRule{
		Name: "bad-rule", Type: "X", Strategy: astronv1alpha1.FieldReferenceStrategy,
		From: sel("", "v1", "A"), To: sel("", "v1", "B"),
	}
	if _, err := (fieldReferenceStrategy{}).Derive(rule, index); err == nil {
		t.Fatal("expected an error for a FieldReference rule with no fieldRef")
	}
}

func TestFieldReferenceStrategy_RegisteredInEngine(t *testing.T) {
	cert := withSpecField(obj("cert-manager.io/v1", "Certificate", "shop", "web-tls", "cert-uid"),
		"web-tls-secret", "spec", "secretName")
	secret := obj("v1", "Secret", "shop", "web-tls-secret", "secret-uid")
	index := certManagerIndex(cert, secret)

	engine := NewEngine()
	edges, err := engine.Derive([]astronv1alpha1.RelationshipRule{{
		Name: "certificate-uses-secret", Type: "USES_SECRET", Strategy: astronv1alpha1.FieldReferenceStrategy,
		From: sel("cert-manager.io", "v1", "Certificate"), To: sel("", "v1", "Secret"),
		FieldRef: &astronv1alpha1.FieldReferenceSpec{NamePath: "spec.secretName"},
	}}, index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if findEdge(edges, "USES_SECRET", "web-tls", "web-tls-secret") == nil {
		t.Fatalf("expected the engine to route the rule to fieldReferenceStrategy, got: %+v", edges)
	}
}
