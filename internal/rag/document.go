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

// Package rag turns the projected resource graph into representations suitable
// for retrieval-augmented generation (GraphRAG). Its first responsibility is
// textualization: rendering a graph node, together with its immediate typed
// relationships, into a compact natural-language "resource card" that can be
// embedded and retrieved.
//
// Card rendering is deterministic (relationships, labels and annotations are
// sorted) so that a content hash of the text changes only when the meaningful
// content changes. That hash drives incremental re-embedding: unchanged nodes
// need not be re-embedded on every projection re-sync.
package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/project-astron/astron/internal/graph"
)

// Options controls which optional node properties are folded into a card.
type Options struct {
	// IncludeLabels includes the node's labels in the rendered card.
	IncludeLabels bool
	// IncludeAnnotations includes the node's annotations in the rendered card.
	// Annotations are noisy and excluded by default.
	IncludeAnnotations bool
}

// DefaultOptions is the recommended card configuration: labels in, annotations
// out.
var DefaultOptions = Options{IncludeLabels: true, IncludeAnnotations: false}

// Card is the textual representation of a single resource node plus its
// immediate relationships, ready to be embedded.
type Card struct {
	// Ref identifies the resource the card describes.
	Ref graph.Ref
	// Text is the natural-language description of the resource.
	Text string
	// Hash is the hex-encoded SHA-256 of Text. It changes only when the rendered
	// content changes, enabling incremental re-embedding.
	Hash string
}

// Edge is a single relationship incident to the node a card describes, with the
// peer (other endpoint) already resolved to a Ref.
type Edge struct {
	// Type is the relationship type, e.g. "OWNS", "MOUNTS", "SELECTS".
	Type string
	// Peer is the resource at the other end of the relationship.
	Peer graph.Ref
	// Outgoing is true when the described node is the source of the edge
	// (node -> Peer), false when it is the target (Peer -> node).
	Outgoing bool
	// Note is the free-text note attached to the edge (user-created links only).
	// When present it is rendered into the card so it influences the embedding.
	Note string
}

// relationshipPhrasing maps a known relationship type to the verb phrases used
// when the described node is the source (out) or the target (in) of the edge.
type relationshipPhrasing struct {
	out, in string
}

var knownPhrasings = map[string]relationshipPhrasing{
	"OWNS":    {out: "Owns", in: "Owned by"},
	"MOUNTS":  {out: "Mounts", in: "Mounted by"},
	"SELECTS": {out: "Selects", in: "Selected by"},
	"DEFINES": {out: "Defines", in: "Defined by"},
}

// phrasingFor returns the verb phrases for a relationship type, synthesizing a
// generic phrasing for unknown types.
func phrasingFor(relType string) relationshipPhrasing {
	if p, ok := knownPhrasings[strings.ToUpper(relType)]; ok {
		return p
	}
	t := strings.ToLower(relType)
	return relationshipPhrasing{
		out: fmt.Sprintf("Has %s relationship to", t),
		in:  fmt.Sprintf("Has %s relationship from", t),
	}
}

// BuildCards renders a card for every node in a graph snapshot, resolving each
// node's incident relationships into natural-language clauses. Relationship
// endpoints are resolved against the snapshot's nodes by their stable ID; an
// endpoint absent from the snapshot is rendered from whatever identity the edge
// itself carries.
func BuildCards(data graph.GraphData, opts Options) []Card {
	byID := make(map[string]graph.Ref, len(data.Nodes))
	for _, n := range data.Nodes {
		byID[n.Ref.ID()] = n.Ref
	}
	resolve := func(ref graph.Ref) graph.Ref {
		if full, ok := byID[ref.ID()]; ok {
			return full
		}
		return ref
	}

	edgesByID := make(map[string][]Edge)
	for _, r := range data.Relationships {
		fromID, toID := r.From.ID(), r.To.ID()
		note := asString(r.Properties["note"])
		edgesByID[fromID] = append(edgesByID[fromID], Edge{
			Type: r.Type, Peer: resolve(r.To), Outgoing: true, Note: note,
		})
		edgesByID[toID] = append(edgesByID[toID], Edge{
			Type: r.Type, Peer: resolve(r.From), Outgoing: false, Note: note,
		})
	}

	cards := make([]Card, 0, len(data.Nodes))
	for _, n := range data.Nodes {
		cards = append(cards, RenderCard(n, edgesByID[n.Ref.ID()], opts))
	}
	return cards
}

// RenderCard renders a single node and its incident edges into a Card. It is
// deterministic: edges, labels and annotations are sorted before rendering so
// the resulting text (and its hash) is stable across calls.
func RenderCard(node graph.Node, edges []Edge, opts Options) Card {
	var b strings.Builder
	b.WriteString(identityClause(node.Ref))
	if s := statusClause(node.Properties); s != "" {
		b.WriteString(s)
	}
	b.WriteByte('.')

	for _, clause := range relationshipClauses(edges) {
		b.WriteByte(' ')
		b.WriteString(clause)
		b.WriteByte('.')
	}

	for _, clause := range noteClauses(edges) {
		b.WriteByte(' ')
		b.WriteString(clause)
		b.WriteByte('.')
	}

	if opts.IncludeLabels {
		if s := mapClause("Labels", node.Properties["labels"]); s != "" {
			b.WriteByte(' ')
			b.WriteString(s)
		}
	}
	if opts.IncludeAnnotations {
		if s := mapClause("Annotations", node.Properties["annotations"]); s != "" {
			b.WriteByte(' ')
			b.WriteString(s)
		}
	}

	text := b.String()
	sum := sha256.Sum256([]byte(text))
	return Card{Ref: node.Ref, Text: text, Hash: hex.EncodeToString(sum[:])}
}

// identityClause renders the leading "what is this" clause, e.g.
// "Pod `web-7d9` in namespace `shop`" or, for cluster-scoped resources,
// "Namespace `shop`".
func identityClause(ref graph.Ref) string {
	kind := ref.Kind
	if kind == "" {
		kind = "Resource"
	}
	if ref.Namespace == "" {
		return fmt.Sprintf("%s `%s`", kind, ref.Name)
	}
	return fmt.Sprintf("%s `%s` in namespace `%s`", kind, ref.Name, ref.Namespace)
}

// statusClause renders a health/status clause from flattened status properties
// (as surfaced for Pods by the projector). It returns "" when no status-like
// properties are present.
func statusClause(props map[string]any) string {
	if props == nil {
		return ""
	}
	phase := asString(props["status"])
	if phase == "" {
		phase = asString(props["phase"])
	}
	if phase == "" {
		return ""
	}

	var details []string
	if ready := asString(props["ready"]); ready != "" {
		details = append(details, ready+" ready")
	}
	if restarts, ok := asInt(props["restarts"]); ok {
		details = append(details, fmt.Sprintf("%d restarts", restarts))
	}

	if len(details) == 0 {
		return fmt.Sprintf(" is %s", phase)
	}
	return fmt.Sprintf(" is %s (%s)", phase, strings.Join(details, ", "))
}

// relationshipClauses groups incident edges by (direction, type) and renders one
// clause per group, e.g. "Owns Pods `a`, `b`" or "Mounted by Pod `web-7d9`".
// Clauses and the targets within them are sorted for determinism.
func relationshipClauses(edges []Edge) []string {
	type groupKey struct {
		outgoing bool
		relType  string
	}
	groups := map[groupKey][]graph.Ref{}
	for _, e := range edges {
		k := groupKey{outgoing: e.Outgoing, relType: e.Type}
		groups[k] = append(groups[k], e.Peer)
	}

	keys := make([]groupKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].relType != keys[j].relType {
			return keys[i].relType < keys[j].relType
		}
		// Incoming before outgoing for a stable, readable order.
		return !keys[i].outgoing && keys[j].outgoing
	})

	clauses := make([]string, 0, len(keys))
	for _, k := range keys {
		peers := groups[k]
		sort.Slice(peers, func(i, j int) bool {
			if peers[i].Kind != peers[j].Kind {
				return peers[i].Kind < peers[j].Kind
			}
			return peers[i].Name < peers[j].Name
		})
		clauses = append(clauses, renderGroup(k.relType, k.outgoing, peers))
	}
	return clauses
}

// noteClauses renders one clause per edge that carries a note, tying the note to
// its peer and direction, e.g. `Note on link to Service "payments": critical
// dependency`. Clauses are sorted for deterministic output.
func noteClauses(edges []Edge) []string {
	var clauses []string
	for _, e := range edges {
		note := strings.TrimSpace(e.Note)
		if note == "" {
			continue
		}
		kind := e.Peer.Kind
		if kind == "" {
			kind = "Resource"
		}
		dir := "from"
		if e.Outgoing {
			dir = "to"
		}
		clauses = append(clauses, fmt.Sprintf("Note on link %s %s `%s`: %s", dir, kind, e.Peer.Name, note))
	}
	sort.Strings(clauses)
	return clauses
}

// renderGroup renders a single (direction, type) group of peers into a clause.
// Peers of the same kind are collapsed under one (pluralized) kind label.
func renderGroup(relType string, outgoing bool, peers []graph.Ref) string {
	phrasing := phrasingFor(relType)
	verb := phrasing.in
	if outgoing {
		verb = phrasing.out
	}

	// Preserve first-seen kind order (peers are already sorted by kind, name).
	var kindOrder []string
	byKind := map[string][]string{}
	for _, p := range peers {
		kind := p.Kind
		if kind == "" {
			kind = "Resource"
		}
		if _, seen := byKind[kind]; !seen {
			kindOrder = append(kindOrder, kind)
		}
		byKind[kind] = append(byKind[kind], "`"+p.Name+"`")
	}

	parts := make([]string, 0, len(kindOrder))
	for _, kind := range kindOrder {
		names := byKind[kind]
		label := kind
		if len(names) > 1 {
			label = pluralizeKind(kind)
		}
		parts = append(parts, fmt.Sprintf("%s %s", label, strings.Join(names, ", ")))
	}
	return fmt.Sprintf("%s %s", verb, strings.Join(parts, ", "))
}

// mapClause renders a JSON-encoded string map property (as stored by the
// projector for labels/annotations) into a sorted "Label: k=v, k=v" clause.
func mapClause(label string, raw any) string {
	s, ok := raw.(string)
	if !ok || s == "" {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil || len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, m[k]))
	}
	return fmt.Sprintf("%s: %s.", label, strings.Join(pairs, ", "))
}

// pluralizeKind applies a minimal English pluralization sufficient for
// Kubernetes kinds (e.g. Pod -> Pods, Ingress -> Ingresses).
func pluralizeKind(kind string) string {
	switch {
	case strings.HasSuffix(kind, "s"), strings.HasSuffix(kind, "x"),
		strings.HasSuffix(kind, "ch"), strings.HasSuffix(kind, "sh"):
		return kind + "es"
	case strings.HasSuffix(kind, "y") && len(kind) > 1 && !isVowel(kind[len(kind)-2]):
		return kind[:len(kind)-1] + "ies"
	default:
		return kind + "s"
	}
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u', 'A', 'E', 'I', 'O', 'U':
		return true
	default:
		return false
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// asInt coerces the numeric types Neo4J / JSON decoding may yield into an int64.
func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}
