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
	"testing"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

func findRule(rules []astronv1alpha1.RelationshipRule, name string) *astronv1alpha1.RelationshipRule {
	for i := range rules {
		if rules[i].Name == name {
			return &rules[i]
		}
	}
	return nil
}

// TestBuildCRDRelationshipsCertManagerFullGroup verifies that when every
// cert-manager.io/acme.cert-manager.io kind is present, all three
// FieldReference rules from the pack are emitted, with ISSUED_BY's static `to`
// defaulting to Issuer (the first toCandidate) even though kindPath lets the
// live edge resolve to ClusterIssuer too.
func TestBuildCRDRelationshipsCertManagerFullGroup(t *testing.T) {
	selectors := []astronv1alpha1.ResourceSelector{certificate, certificateRequest, issuer, clusterIssuer, secret}
	rules := buildCRDRelationships(selectors)

	uses := findRule(rules, "certificate-uses-secret")
	if uses == nil {
		t.Fatalf("expected certificate-uses-secret, got %+v", rules)
	}
	if uses.Strategy != astronv1alpha1.FieldReferenceStrategy || uses.To != secret {
		t.Errorf("unexpected certificate-uses-secret rule: %+v", *uses)
	}
	if uses.FieldRef == nil || uses.FieldRef.NamePath != "spec.secretName" {
		t.Errorf("unexpected fieldRef: %+v", uses.FieldRef)
	}

	issuedBy := findRule(rules, "certificate-issued-by")
	if issuedBy == nil {
		t.Fatalf("expected certificate-issued-by, got %+v", rules)
	}
	if issuedBy.To != issuer { // first present toCandidate
		t.Errorf("expected static to=Issuer, got %+v", issuedBy.To)
	}
	if issuedBy.FieldRef == nil || issuedBy.FieldRef.KindPath != "spec.issuerRef.kind" {
		t.Errorf("unexpected fieldRef: %+v", issuedBy.FieldRef)
	}

	if findRule(rules, "certificaterequest-issued-by") == nil {
		t.Errorf("expected certificaterequest-issued-by, got %+v", rules)
	}

	if len(rules) != 3 {
		t.Errorf("expected exactly 3 rules, got %d: %+v", len(rules), rules)
	}
}

// TestBuildCRDRelationshipsFallsBackToClusterIssuer verifies the static `to`
// falls back to the next present toCandidate (ClusterIssuer) when Issuer
// itself wasn't included.
func TestBuildCRDRelationshipsFallsBackToClusterIssuer(t *testing.T) {
	selectors := []astronv1alpha1.ResourceSelector{certificate, clusterIssuer, secret}
	rules := buildCRDRelationships(selectors)

	issuedBy := findRule(rules, "certificate-issued-by")
	if issuedBy == nil {
		t.Fatalf("expected certificate-issued-by, got %+v", rules)
	}
	if issuedBy.To != clusterIssuer {
		t.Errorf("expected static to=ClusterIssuer, got %+v", issuedBy.To)
	}
}

// TestBuildCRDRelationshipsGatedOnBothEndpoints verifies a rule is skipped
// when neither of its target candidates is present (Certificate alone,
// without Issuer, ClusterIssuer or Secret, yields no rules at all), even
// though the cert-manager.io group is otherwise "present".
func TestBuildCRDRelationshipsGatedOnBothEndpoints(t *testing.T) {
	rules := buildCRDRelationships([]astronv1alpha1.ResourceSelector{certificate})
	if len(rules) != 0 {
		t.Fatalf("expected no rules without any target kind present, got %+v", rules)
	}
}

// TestBuildCRDRelationshipsNoKnownGroupYieldsNoRules verifies an unrelated set
// of selectors (no cert-manager.io kinds at all) produces no CRD relationship
// rules.
func TestBuildCRDRelationshipsNoKnownGroupYieldsNoRules(t *testing.T) {
	rules := buildCRDRelationships([]astronv1alpha1.ResourceSelector{pod, service})
	if len(rules) != 0 {
		t.Fatalf("expected no rules, got %+v", rules)
	}
}

// TestBuildCRDRelationshipsRulesHaveIndependentFieldRefPointers verifies the
// FieldRef pointer on each emitted rule is independently owned (not aliasing
// the shared candidate table), so mutating one rule's FieldRef can't affect
// another's.
func TestBuildCRDRelationshipsRulesHaveIndependentFieldRefPointers(t *testing.T) {
	selectors := []astronv1alpha1.ResourceSelector{certificate, certificateRequest, issuer, clusterIssuer, secret}
	rules := buildCRDRelationships(selectors)
	a := findRule(rules, "certificate-issued-by")
	b := findRule(rules, "certificaterequest-issued-by")
	if a == nil || b == nil {
		t.Fatalf("expected both rules, got %+v", rules)
	}
	if a.FieldRef == b.FieldRef {
		t.Error("expected independent FieldRef pointers, got the same pointer")
	}
}

// TestBuildManifestIncludesCRDRelationships verifies buildManifest wires
// buildCRDRelationships in alongside the built-in relationship candidates
// when --with-relationships is set (the default).
func TestBuildManifestIncludesCRDRelationships(t *testing.T) {
	gopts := &generateOptions{options: &options{}, withRelationships: true}
	selectors := []astronv1alpha1.ResourceSelector{certificate, issuer, secret}
	m := buildManifest(gopts, demoNS, selectors)
	if findRule(m.Spec.Relationships, "certificate-uses-secret") == nil {
		t.Errorf("expected certificate-uses-secret in generated manifest, got %+v", m.Spec.Relationships)
	}
	if findRule(m.Spec.Relationships, "certificate-issued-by") == nil {
		t.Errorf("expected certificate-issued-by in generated manifest, got %+v", m.Spec.Relationships)
	}
}
