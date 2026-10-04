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
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

// relationshipPackFile is the YAML document shape read from a --relationship-pack
// file: a user-authored, shippable bundle of relationship rules for a
// CRD-defined API group this CLI doesn't already ship a built-in pack for
// (see crdRelationshipPacks for the built-in equivalent, and
// docs/relationships.md for the full schema and a worked example).
type relationshipPackFile struct {
	Rules []relationshipPackRule `json:"rules"`
}

// relationshipPackRule is one rule in a relationship pack file. It mirrors
// astronv1alpha1.RelationshipRule, with two differences that make it easier
// to author a reusable pack by hand:
//
//   - to is a list of candidate target Kinds, not a single one. The first
//     candidate present among the projection's resources is used as the
//     rule's static target kind -- the same mechanism the built-in
//     cert-manager pack uses to handle issuerRef's Issuer/ClusterIssuer
//     choice, so a dynamic kindPath has a sensible fallback.
//   - strategy may be omitted when fieldRef is set, in which case it defaults
//     to FieldReference (the overwhelmingly common case for a hand-written
//     pack); any other strategy must be named explicitly.
//
// Like every other --include/--include-group-sourced rule, a pack rule is
// only emitted into the generated manifest when both its from Kind and at
// least one to candidate are actually present among the projection's
// resources, so a pack that's only partially relevant to this projection
// doesn't produce a rule with a dangling, uncaptured target.
type relationshipPackRule struct {
	Name     string                              `json:"name"`
	Type     string                              `json:"type"`
	Strategy astronv1alpha1.RelationshipStrategy `json:"strategy,omitempty"`
	From     astronv1alpha1.ResourceSelector     `json:"from"`
	To       []astronv1alpha1.ResourceSelector   `json:"to"`
	FieldRef *astronv1alpha1.FieldReferenceSpec  `json:"fieldRef,omitempty"`
}

// loadRelationshipPackFiles reads and validates each --relationship-pack
// file, in order. A problem with any file (not found, invalid YAML, or a
// rule missing required fields) is a hard error: a relationship pack is an
// explicit request, so a typo in it should be reported immediately rather
// than silently producing an incomplete manifest.
func loadRelationshipPackFiles(paths []string) ([]relationshipPackFile, error) {
	packs := make([]relationshipPackFile, 0, len(paths))
	for _, path := range paths {
		pack, err := loadRelationshipPackFile(path)
		if err != nil {
			return nil, err
		}
		packs = append(packs, pack)
	}
	return packs, nil
}

// loadRelationshipPackFile reads, strictly parses (unknown fields are
// rejected, to catch typos) and validates a single relationship pack file.
func loadRelationshipPackFile(path string) (relationshipPackFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return relationshipPackFile{}, fmt.Errorf("reading relationship pack %s: %w", path, err)
	}
	var pack relationshipPackFile
	if err := yaml.UnmarshalStrict(data, &pack); err != nil {
		return relationshipPackFile{}, fmt.Errorf("parsing relationship pack %s: %w", path, err)
	}
	if err := validateRelationshipPack(pack); err != nil {
		return relationshipPackFile{}, fmt.Errorf("relationship pack %s: %w", path, err)
	}
	return pack, nil
}

// validateRelationshipPack checks the required fields and internal
// consistency of a parsed pack file's rules, so a mistake is reported with a
// precise, actionable message at generate time instead of producing a rule
// that the operator would only reject (or silently never match) later.
func validateRelationshipPack(pack relationshipPackFile) error {
	if len(pack.Rules) == 0 {
		return fmt.Errorf("no rules defined")
	}
	seen := make(map[string]bool, len(pack.Rules))
	for i, r := range pack.Rules {
		if r.Name == "" {
			return fmt.Errorf("rules[%d]: name is required", i)
		}
		if seen[r.Name] {
			return fmt.Errorf("rule %q: duplicate rule name within this file", r.Name)
		}
		seen[r.Name] = true

		if r.Type == "" {
			return fmt.Errorf("rule %q: type is required", r.Name)
		}
		if r.From.Kind == "" {
			return fmt.Errorf("rule %q: from.kind is required", r.Name)
		}
		if len(r.To) == 0 {
			return fmt.Errorf("rule %q: at least one \"to\" candidate is required", r.Name)
		}
		for j, to := range r.To {
			if to.Kind == "" {
				return fmt.Errorf("rule %q: to[%d].kind is required", r.Name, j)
			}
		}

		strategy := r.Strategy
		if strategy == "" && r.FieldRef != nil {
			strategy = astronv1alpha1.FieldReferenceStrategy
		}
		if strategy == "" {
			return fmt.Errorf("rule %q: strategy is required unless fieldRef is set (which implies FieldReference)", r.Name)
		}
		if strategy == astronv1alpha1.FieldReferenceStrategy {
			if r.FieldRef == nil {
				return fmt.Errorf("rule %q: strategy FieldReference requires fieldRef", r.Name)
			}
			if r.FieldRef.NamePath == "" {
				return fmt.Errorf("rule %q: fieldRef.namePath is required", r.Name)
			}
		}
	}
	return nil
}

// buildPackRelationships returns relationship rules from the given loaded
// relationship pack files, gated by endpoint presence the same way
// buildCRDRelationships gates its built-in packs (see gateRule).
func buildPackRelationships(
	selectors []astronv1alpha1.ResourceSelector, packs []relationshipPackFile,
) []astronv1alpha1.RelationshipRule {
	present := presentKinds(selectors)

	var rules []astronv1alpha1.RelationshipRule
	for _, pack := range packs {
		for _, r := range pack.Rules {
			to, ok := gateRule(r.From, r.To, present)
			if !ok {
				continue
			}
			strategy := r.Strategy
			if strategy == "" {
				strategy = astronv1alpha1.FieldReferenceStrategy
			}
			rules = append(rules, astronv1alpha1.RelationshipRule{
				Name:     r.Name,
				Type:     r.Type,
				Strategy: strategy,
				From:     r.From,
				To:       to,
				FieldRef: r.FieldRef,
			})
		}
	}
	return rules
}

// dedupeRelationshipRulesByName drops any rule whose name already appeared
// earlier in the list (first occurrence wins). Rule names only need to be
// unique within one projection's manifest, but relationships can now come
// from three independent sources (the built-in standard-kind candidates, a
// built-in CRD pack, and one or more --relationship-pack files) that have no
// way to coordinate with each other, so a final dedup pass keeps an
// accidental name collision from producing an invalid-looking manifest with
// two same-named rules.
func dedupeRelationshipRulesByName(rules []astronv1alpha1.RelationshipRule) []astronv1alpha1.RelationshipRule {
	seen := make(map[string]bool, len(rules))
	out := make([]astronv1alpha1.RelationshipRule, 0, len(rules))
	for _, r := range rules {
		if seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		out = append(out, r)
	}
	return out
}
