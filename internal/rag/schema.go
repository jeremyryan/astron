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

package rag

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/project-astron/astron/internal/graph"
)

// SchemaSummary renders a compact, deterministic description of a projection's
// graph schema from a snapshot: the node labels (Kubernetes kinds) with their
// observed property keys, and the relationship patterns between kinds. It is
// used to ground a text-to-Cypher prompt so generated queries reference real
// labels, properties and relationship types.
//
// Every K8sResource node also carries the identity properties apiVersion, kind,
// namespace, name and uid, which are noted once up front.
func SchemaSummary(data graph.GraphData) string {
	var b strings.Builder
	b.WriteString("Every node has exactly ONE label, `K8sResource` — there is NO ")
	b.WriteString("per-kind label such as `:Pod` or `:Deployment`. The Kubernetes kind ")
	b.WriteString("is instead the `kind` property, e.g. `{kind: 'Pod'}`. Every node also ")
	b.WriteString("has properties: apiVersion, kind, namespace, name, uid")
	b.WriteString(" (plus the kind-specific properties below).\n\n")

	b.WriteString("Kubernetes kinds present (`kind` property values) and their extra properties:\n")
	for _, line := range nodeKindLines(data.Nodes) {
		b.WriteString("  " + line + "\n")
	}

	b.WriteString("\nRelationship patterns (`kind` is a property, not a label):\n")
	patterns := relationshipPatterns(data)
	if len(patterns) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, p := range patterns {
		b.WriteString("  " + p + "\n")
	}
	return b.String()
}

// identityKeys are the always-present node properties omitted from the per-kind
// property listing to keep it focused on distinguishing attributes.
var identityKeys = map[string]bool{
	"apiVersion": true, "kind": true, "namespace": true, "name": true, "uid": true,
}

// maxExampleValueLen caps a rendered example property value in the schema
// summary, so a large value (e.g. the JSON-encoded labels/annotations map)
// doesn't bloat it.
const maxExampleValueLen = 40

// renderExampleValue renders one property value the way it would appear as a
// Cypher literal: quoted for strings, bare for numbers/bools/etc. Showing a
// real example (not just the property's name) is what lets a text-to-Cypher
// model see a property's actual stored format instead of guessing it -- e.g.
// Pod's `ready` is the string "1/1", not a boolean, which a name-only schema
// summary gave a model no way to know: MATCH (p {ready: true}) matches
// nothing, ever, regardless of how many pods are actually healthy.
func renderExampleValue(v any) string {
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	if len(s) > maxExampleValueLen {
		s = s[:maxExampleValueLen] + "…"
	}
	return strconv.Quote(s)
}

// nodeKindLines returns one "kind=Kind — prop=example, prop=example" line per
// kind, sorted, with one representative example value per property (the
// first one observed, for determinism given a fixed input).
func nodeKindLines(nodes []graph.Node) []string {
	examplesByKind := map[string]map[string]string{}
	for _, n := range nodes {
		kind := n.Ref.Kind
		if kind == "" {
			continue
		}
		if examplesByKind[kind] == nil {
			examplesByKind[kind] = map[string]string{}
		}
		for k, v := range n.Properties {
			if identityKeys[k] {
				continue
			}
			if _, seen := examplesByKind[kind][k]; seen {
				continue
			}
			examplesByKind[kind][k] = renderExampleValue(v)
		}
	}

	kinds := make([]string, 0, len(examplesByKind))
	for k := range examplesByKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	lines := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		examples := examplesByKind[kind]
		keys := make([]string, 0, len(examples))
		for k := range examples {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			lines = append(lines, fmt.Sprintf("kind=%s", kind))
		} else {
			parts := make([]string, len(keys))
			for i, k := range keys {
				parts[i] = fmt.Sprintf("%s=%s", k, examples[k])
			}
			lines = append(lines, fmt.Sprintf("kind=%s — %s", kind, strings.Join(parts, ", ")))
		}
	}
	return lines
}

// relationshipPatterns returns sorted "(:From)-[:TYPE]->(:To)" patterns observed
// in the graph.
func relationshipPatterns(data graph.GraphData) []string {
	kindByID := make(map[string]string, len(data.Nodes))
	for _, n := range data.Nodes {
		kindByID[n.Ref.ID()] = n.Ref.Kind
	}

	seen := map[string]bool{}
	var patterns []string
	for _, r := range data.Relationships {
		from := kindByID[r.From.ID()]
		to := kindByID[r.To.ID()]
		if from == "" {
			from = "?"
		}
		if to == "" {
			to = "?"
		}
		p := fmt.Sprintf("(kind=%s)-[:%s]->(kind=%s)", from, r.Type, to)
		if !seen[p] {
			seen[p] = true
			patterns = append(patterns, p)
		}
	}
	sort.Strings(patterns)
	return patterns
}
