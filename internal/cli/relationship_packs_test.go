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
	"os"
	"path/filepath"
	"strings"
	"testing"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

const validPackYAML = `
rules:
  - name: certificate-uses-secret
    type: USES_SECRET
    from: { group: cert-manager.io, version: v1, kind: Certificate }
    to:
      - { version: v1, kind: Secret }
    fieldRef:
      namePath: spec.secretName
  - name: certificate-issued-by
    type: ISSUED_BY
    from: { group: cert-manager.io, version: v1, kind: Certificate }
    to:
      - { group: cert-manager.io, version: v1, kind: Issuer }
      - { group: cert-manager.io, version: v1, kind: ClusterIssuer }
    fieldRef:
      namePath: spec.issuerRef.name
      kindPath: spec.issuerRef.kind
`

func writeTempPack(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pack.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing temp pack file: %v", err)
	}
	return path
}

func TestLoadRelationshipPackFileParsesValidFile(t *testing.T) {
	pack, err := loadRelationshipPackFile(writeTempPack(t, validPackYAML))
	if err != nil {
		t.Fatalf("loadRelationshipPackFile: %v", err)
	}
	if len(pack.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %+v", pack.Rules)
	}
	r := pack.Rules[1]
	if r.Name != "certificate-issued-by" || r.Type != "ISSUED_BY" {
		t.Errorf("unexpected rule: %+v", r)
	}
	if r.Strategy != "" { // strategy omitted in the YAML; inference happens in buildPackRelationships
		t.Errorf("expected empty strategy as parsed, got %q", r.Strategy)
	}
	if len(r.To) != 2 || r.To[0].Kind != "Issuer" || r.To[1].Kind != "ClusterIssuer" {
		t.Errorf("unexpected to candidates: %+v", r.To)
	}
	if r.FieldRef == nil || r.FieldRef.NamePath != "spec.issuerRef.name" || r.FieldRef.KindPath != "spec.issuerRef.kind" {
		t.Errorf("unexpected fieldRef: %+v", r.FieldRef)
	}
}

func TestLoadRelationshipPackFileMissingFileErrors(t *testing.T) {
	_, err := loadRelationshipPackFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestLoadRelationshipPackFileInvalidYAMLErrors(t *testing.T) {
	_, err := loadRelationshipPackFile(writeTempPack(t, "rules: [not, valid, :::"))
	if err == nil {
		t.Fatal("expected an error for invalid YAML")
	}
}

func TestLoadRelationshipPackFileUnknownFieldErrors(t *testing.T) {
	_, err := loadRelationshipPackFile(writeTempPack(t, `
rules:
  - name: x
    type: X
    from: { version: v1, kind: A }
    to: [{ version: v1, kind: B }]
    strategy: OwnerReference
    bogusField: true
`))
	if err == nil {
		t.Fatal("expected an error for an unknown field (strict parsing)")
	}
}

func TestValidateRelationshipPackRequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "no rules",
			yaml:    "rules: []",
			wantErr: "no rules defined",
		},
		{
			name:    "missing name",
			yaml:    "rules:\n  - type: X\n    from: {version: v1, kind: A}\n    to: [{version: v1, kind: B}]\n    strategy: OwnerReference",
			wantErr: "name is required",
		},
		{
			name:    "duplicate name",
			yaml:    "rules:\n  - {name: r, type: X, from: {version: v1, kind: A}, to: [{version: v1, kind: B}], strategy: OwnerReference}\n  - {name: r, type: Y, from: {version: v1, kind: A}, to: [{version: v1, kind: C}], strategy: OwnerReference}",
			wantErr: "duplicate rule name",
		},
		{
			name:    "missing type",
			yaml:    "rules:\n  - {name: r, from: {version: v1, kind: A}, to: [{version: v1, kind: B}], strategy: OwnerReference}",
			wantErr: "type is required",
		},
		{
			name:    "missing from.kind",
			yaml:    "rules:\n  - {name: r, type: X, from: {version: v1}, to: [{version: v1, kind: B}], strategy: OwnerReference}",
			wantErr: "from.kind is required",
		},
		{
			name:    "empty to",
			yaml:    "rules:\n  - {name: r, type: X, from: {version: v1, kind: A}, to: [], strategy: OwnerReference}",
			wantErr: "at least one",
		},
		{
			name:    "to candidate missing kind",
			yaml:    "rules:\n  - {name: r, type: X, from: {version: v1, kind: A}, to: [{version: v1}], strategy: OwnerReference}",
			wantErr: "to[0].kind is required",
		},
		{
			name:    "no strategy and no fieldRef",
			yaml:    "rules:\n  - {name: r, type: X, from: {version: v1, kind: A}, to: [{version: v1, kind: B}]}",
			wantErr: "strategy is required",
		},
		{
			name:    "FieldReference strategy without fieldRef",
			yaml:    "rules:\n  - {name: r, type: X, strategy: FieldReference, from: {version: v1, kind: A}, to: [{version: v1, kind: B}]}",
			wantErr: "requires fieldRef",
		},
		{
			name:    "fieldRef without namePath",
			yaml:    "rules:\n  - {name: r, type: X, from: {version: v1, kind: A}, to: [{version: v1, kind: B}], fieldRef: {kindPath: spec.kind}}",
			wantErr: "namePath is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadRelationshipPackFile(writeTempPack(t, tc.yaml))
			if err == nil {
				t.Fatalf("expected an error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateRelationshipPackStrategyInferredFromFieldRef(t *testing.T) {
	// No explicit strategy, but fieldRef is set: should be valid (strategy
	// inference happens in buildPackRelationships, not validation).
	_, err := loadRelationshipPackFile(writeTempPack(t, `
rules:
  - name: r
    type: X
    from: { version: v1, kind: A }
    to: [{ version: v1, kind: B }]
    fieldRef:
      namePath: spec.ref.name
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildPackRelationshipsGatingAndFallback(t *testing.T) {
	pack, err := loadRelationshipPackFile(writeTempPack(t, validPackYAML))
	if err != nil {
		t.Fatalf("loadRelationshipPackFile: %v", err)
	}

	// Full set present: both rules fire, issued-by prefers Issuer (first
	// candidate) over ClusterIssuer.
	full := []astronv1alpha1.ResourceSelector{certificate, issuer, clusterIssuer, secret}
	rules := buildPackRelationships(full, []relationshipPackFile{pack})
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %+v", rules)
	}
	issuedBy := findRule(rules, "certificate-issued-by")
	if issuedBy == nil || issuedBy.To != issuer {
		t.Errorf("expected certificate-issued-by -> Issuer, got %+v", issuedBy)
	}
	if issuedBy.Strategy != astronv1alpha1.FieldReferenceStrategy {
		t.Errorf("expected inferred FieldReference strategy, got %q", issuedBy.Strategy)
	}

	// Only ClusterIssuer present (not Issuer): falls back to the second
	// candidate.
	fallback := []astronv1alpha1.ResourceSelector{certificate, clusterIssuer}
	rules = buildPackRelationships(fallback, []relationshipPackFile{pack})
	issuedBy = findRule(rules, "certificate-issued-by")
	if issuedBy == nil || issuedBy.To != clusterIssuer {
		t.Errorf("expected fallback to ClusterIssuer, got %+v", issuedBy)
	}

	// Neither Issuer nor ClusterIssuer nor Secret present: no rules at all.
	none := []astronv1alpha1.ResourceSelector{certificate}
	if rules := buildPackRelationships(none, []relationshipPackFile{pack}); len(rules) != 0 {
		t.Fatalf("expected no rules, got %+v", rules)
	}
}

func TestBuildPackRelationshipsExplicitStrategy(t *testing.T) {
	pack, err := loadRelationshipPackFile(writeTempPack(t, `
rules:
  - name: a-owns-b
    type: OWNS
    strategy: OwnerReference
    from: { version: v1, kind: A }
    to: [{ version: v1, kind: B }]
`))
	if err != nil {
		t.Fatalf("loadRelationshipPackFile: %v", err)
	}
	a := astronv1alpha1.ResourceSelector{Version: "v1", Kind: "A"}
	b := astronv1alpha1.ResourceSelector{Version: "v1", Kind: "B"}
	rules := buildPackRelationships([]astronv1alpha1.ResourceSelector{a, b}, []relationshipPackFile{pack})
	if len(rules) != 1 || rules[0].Strategy != astronv1alpha1.OwnerReferenceStrategy || rules[0].FieldRef != nil {
		t.Fatalf("unexpected rules: %+v", rules)
	}
}

func TestDedupeRelationshipRulesByNameKeepsFirst(t *testing.T) {
	rules := []astronv1alpha1.RelationshipRule{
		{Name: "r", Type: "A"},
		{Name: "other", Type: "B"},
		{Name: "r", Type: "C"}, // later duplicate dropped
	}
	got := dedupeRelationshipRulesByName(rules)
	if len(got) != 2 {
		t.Fatalf("expected 2 rules, got %+v", got)
	}
	if got[0].Type != "A" {
		t.Errorf("expected the first occurrence to win, got %+v", got[0])
	}
}

// TestBuildManifestMergesPackRelationshipsIndependentOfWithRelationships
// verifies --relationship-pack-sourced rules are included even when
// --with-relationships=false (they're an explicit, individually-named
// request, unlike the broad auto-detected set that flag controls).
func TestBuildManifestMergesPackRelationshipsIndependentOfWithRelationships(t *testing.T) {
	pack, err := loadRelationshipPackFile(writeTempPack(t, `
rules:
  - name: a-owns-b
    type: OWNS
    strategy: OwnerReference
    from: { version: v1, kind: A }
    to: [{ version: v1, kind: B }]
`))
	if err != nil {
		t.Fatalf("loadRelationshipPackFile: %v", err)
	}
	a := astronv1alpha1.ResourceSelector{Version: "v1", Kind: "A"}
	b := astronv1alpha1.ResourceSelector{Version: "v1", Kind: "B"}

	gopts := &generateOptions{
		options:           &options{},
		withRelationships: false,
		relationshipPacks: []relationshipPackFile{pack},
	}
	m := buildManifest(gopts, demoNS, []astronv1alpha1.ResourceSelector{a, b})
	if findRule(m.Spec.Relationships, "a-owns-b") == nil {
		t.Fatalf("expected a-owns-b despite withRelationships=false, got %+v", m.Spec.Relationships)
	}
}
