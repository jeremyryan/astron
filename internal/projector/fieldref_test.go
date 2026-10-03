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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
	"github.com/project-astron/astron/internal/graph"
)

// capturingStore is a fakeLinkStore that records the edges passed to the most
// recent Sync call, so FieldReference's live wiring (scope resolution via
// effectiveResources, namespace scope via the REST mapper) can be asserted
// end-to-end through a real Projector.Sync, not just the strategy in
// isolation.
type capturingStore struct {
	fakeLinkStore
	lastEdges []graph.Relationship
}

func (s *capturingStore) Sync(ctx context.Context, id graph.ProjectionID, nodes []graph.Node, rels []graph.Relationship) (graph.Counts, error) {
	s.lastEdges = rels
	return s.fakeLinkStore.Sync(ctx, id, nodes, rels)
}

// TestSyncDerivesFieldReferenceEdgeWithClusterScopedTarget is an end-to-end
// regression test for the FieldReference strategy's namespace-scope handling:
// a Certificate's spec.issuerRef names a ClusterIssuer (cluster-scoped), and
// the resulting edge must NOT inherit the Certificate's namespace, which
// requires the real wiring from Projector.Sync through MapIndex.Namespaced to
// the REST mapper (gvkNamespaced), not just a hardcoded default in the
// strategy.
func TestSyncDerivesFieldReferenceEdgeWithClusterScopedTarget(t *testing.T) {
	certGVK := schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"}
	issuerGVK := schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Issuer"}
	clusterIssuerGVK := schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "ClusterIssuer"}

	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "cert-manager.io", Version: "v1"}})
	mapper.Add(certGVK, meta.RESTScopeNamespace)
	mapper.Add(issuerGVK, meta.RESTScopeNamespace)
	mapper.Add(clusterIssuerGVK, meta.RESTScopeRoot)

	cert := &unstructured.Unstructured{}
	cert.SetAPIVersion("cert-manager.io/v1")
	cert.SetKind("Certificate")
	cert.SetNamespace("shop")
	cert.SetName("web-tls")
	if err := unstructured.SetNestedMap(cert.Object, map[string]any{
		"name": "letsencrypt-prod", "kind": "ClusterIssuer",
	}, "spec", "issuerRef"); err != nil {
		t.Fatal(err)
	}

	clusterIssuer := &unstructured.Unstructured{}
	clusterIssuer.SetAPIVersion("cert-manager.io/v1")
	clusterIssuer.SetKind("ClusterIssuer")
	clusterIssuer.SetName("letsencrypt-prod")

	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}:   "CertificateList",
		{Group: "cert-manager.io", Version: "v1", Resource: "issuers"}:        "IssuerList",
		{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers"}: "ClusterIssuerList",
	}, cert, clusterIssuer)

	spec := astronv1alpha1.GraphProjectionSpec{
		Scope: astronv1alpha1.ProjectionScope{
			Resources: []astronv1alpha1.ResourceSelector{
				{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"},
				{Group: "cert-manager.io", Version: "v1", Kind: "Issuer"},
				{Group: "cert-manager.io", Version: "v1", Kind: "ClusterIssuer"},
			},
		},
		Relationships: []astronv1alpha1.RelationshipRule{{
			Name: "certificate-issued-by", Type: "ISSUED_BY", Strategy: astronv1alpha1.FieldReferenceStrategy,
			From: astronv1alpha1.ResourceSelector{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"},
			To:   astronv1alpha1.ResourceSelector{Group: "cert-manager.io", Version: "v1", Kind: "Issuer"},
			FieldRef: &astronv1alpha1.FieldReferenceSpec{
				NamePath: "spec.issuerRef.name",
				KindPath: "spec.issuerRef.kind",
			},
		}},
	}

	store := &capturingStore{}
	p := New(Options{
		ID:        "proj-fieldref",
		Namespace: "shop",
		Spec:      spec,
		Dynamic:   dyn,
		Mapper:    mapper,
		Store:     store,
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

	if _, err := p.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	var found *graph.Relationship
	for i := range store.lastEdges {
		if store.lastEdges[i].Type == "ISSUED_BY" {
			found = &store.lastEdges[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected an ISSUED_BY edge, got: %+v", store.lastEdges)
	}
	if found.To.Kind != "ClusterIssuer" || found.To.Name != "letsencrypt-prod" {
		t.Errorf("unexpected target: %+v", found.To)
	}
	if found.To.Namespace != "" {
		t.Errorf("expected the cluster-scoped target to have no namespace, got %q", found.To.Namespace)
	}
}
