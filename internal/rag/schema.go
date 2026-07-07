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
	b.WriteString("Nodes are labeled `K8sResource` and by their Kubernetes kind ")
	b.WriteString("(e.g. `:Pod`, `:Deployment`). Every node has properties: ")
	b.WriteString("apiVersion, kind, namespace, name, uid")
	b.WriteString(" (plus the kind-specific properties below).\n\n")

	b.WriteString("Node kinds and their properties:\n")
	for _, line := range nodeKindLines(data.Nodes) {
		b.WriteString("  " + line + "\n")
	}

	b.WriteString("\nRelationship patterns:\n")
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

// nodeKindLines returns one "Kind: prop, prop" line per kind, sorted.
func nodeKindLines(nodes []graph.Node) []string {
	propsByKind := map[string]map[string]bool{}
	for _, n := range nodes {
		kind := n.Ref.Kind
		if kind == "" {
			continue
		}
		if propsByKind[kind] == nil {
			propsByKind[kind] = map[string]bool{}
		}
		for k := range n.Properties {
			if !identityKeys[k] {
				propsByKind[kind][k] = true
			}
		}
	}

	kinds := make([]string, 0, len(propsByKind))
	for k := range propsByKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	lines := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		keys := make([]string, 0, len(propsByKind[kind]))
		for k := range propsByKind[kind] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			lines = append(lines, fmt.Sprintf(":%s", kind))
		} else {
			lines = append(lines, fmt.Sprintf(":%s — %s", kind, strings.Join(keys, ", ")))
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
		p := fmt.Sprintf("(:%s)-[:%s]->(:%s)", from, r.Type, to)
		if !seen[p] {
			seen[p] = true
			patterns = append(patterns, p)
		}
	}
	sort.Strings(patterns)
	return patterns
}
