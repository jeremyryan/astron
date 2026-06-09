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
	"strings"
	"testing"

	"github.com/project-gamera/gamera/internal/graph"
)

func ref(kind, ns, name, uid string) graph.Ref {
	return graph.Ref{APIVersion: "v1", Kind: kind, Namespace: ns, Name: name, UID: uid}
}

func TestRenderCardIdentityAndStatus(t *testing.T) {
	node := graph.Node{
		Ref: ref("Pod", "shop", "web-7d9", "u-pod"),
		Properties: map[string]any{
			"status":   "Running",
			"ready":    "2/2",
			"restarts": int64(0),
		},
	}
	card := RenderCard(node, nil, DefaultOptions)

	want := "Pod `web-7d9` in namespace `shop` is Running (2/2 ready, 0 restarts)."
	if card.Text != want {
		t.Errorf("card text =\n  %q\nwant\n  %q", card.Text, want)
	}
	if card.Ref.UID != "u-pod" {
		t.Errorf("card ref UID = %q, want u-pod", card.Ref.UID)
	}
	if len(card.Hash) != 64 {
		t.Errorf("expected 64-char sha256 hex hash, got %d chars", len(card.Hash))
	}
}

func TestRenderCardClusterScoped(t *testing.T) {
	node := graph.Node{Ref: ref("Namespace", "", "shop", "u-ns")}
	card := RenderCard(node, nil, DefaultOptions)
	if card.Text != "Namespace `shop`." {
		t.Errorf("unexpected cluster-scoped card: %q", card.Text)
	}
}

func TestRenderCardRelationshipPhrasingAndPluralization(t *testing.T) {
	node := graph.Node{Ref: ref("Deployment", "shop", "web", "u-deploy")}
	edges := []Edge{
		{Type: "OWNS", Peer: ref("Pod", "shop", "web-b", "u-b"), Outgoing: true},
		{Type: "OWNS", Peer: ref("Pod", "shop", "web-a", "u-a"), Outgoing: true},
		{Type: "SELECTS", Peer: ref("Service", "shop", "web-svc", "u-svc"), Outgoing: false},
	}
	card := RenderCard(node, edges, DefaultOptions)

	// OWNS sorts before SELECTS; the two owned Pods are pluralized and
	// name-sorted; the incoming SELECTS uses the passive phrasing.
	want := "Deployment `web` in namespace `shop`. " +
		"Owns Pods `web-a`, `web-b`. " +
		"Selected by Service `web-svc`."
	if card.Text != want {
		t.Errorf("card text =\n  %q\nwant\n  %q", card.Text, want)
	}
}

func TestRenderCardIncludesLabelsSorted(t *testing.T) {
	node := graph.Node{
		Ref:        ref("Pod", "shop", "web-7d9", "u-pod"),
		Properties: map[string]any{"labels": `{"tier":"frontend","app":"web"}`},
	}
	card := RenderCard(node, nil, DefaultOptions)
	if !strings.HasSuffix(card.Text, "Labels: app=web, tier=frontend.") {
		t.Errorf("expected sorted labels clause, got: %q", card.Text)
	}
}

func TestRenderCardAnnotationsOptIn(t *testing.T) {
	node := graph.Node{
		Ref:        ref("Pod", "shop", "web", "u"),
		Properties: map[string]any{"annotations": `{"team":"payments"}`},
	}

	off := RenderCard(node, nil, DefaultOptions)
	if strings.Contains(off.Text, "payments") {
		t.Errorf("annotations should be excluded by default, got: %q", off.Text)
	}

	on := RenderCard(node, nil, Options{IncludeAnnotations: true})
	if !strings.Contains(on.Text, "Annotations: team=payments.") {
		t.Errorf("expected annotations when opted in, got: %q", on.Text)
	}
}

func TestRenderCardUnknownRelationshipTypeFallback(t *testing.T) {
	node := graph.Node{Ref: ref("Pod", "shop", "web", "u")}
	edges := []Edge{
		{Type: "ROUTES", Peer: ref("Service", "shop", "api", "u-api"), Outgoing: true},
	}
	card := RenderCard(node, edges, DefaultOptions)
	if !strings.Contains(card.Text, "Has routes relationship to Service `api`") {
		t.Errorf("unexpected fallback phrasing: %q", card.Text)
	}
}

func TestRenderCardIsDeterministic(t *testing.T) {
	node := graph.Node{
		Ref:        ref("Deployment", "shop", "web", "u-deploy"),
		Properties: map[string]any{"labels": `{"b":"2","a":"1"}`},
	}
	// Same edges supplied in different orders must yield identical text/hash.
	edgesA := []Edge{
		{Type: "OWNS", Peer: ref("Pod", "shop", "p2", "u2"), Outgoing: true},
		{Type: "OWNS", Peer: ref("Pod", "shop", "p1", "u1"), Outgoing: true},
	}
	edgesB := []Edge{
		{Type: "OWNS", Peer: ref("Pod", "shop", "p1", "u1"), Outgoing: true},
		{Type: "OWNS", Peer: ref("Pod", "shop", "p2", "u2"), Outgoing: true},
	}
	a := RenderCard(node, edgesA, DefaultOptions)
	b := RenderCard(node, edgesB, DefaultOptions)
	if a.Hash != b.Hash || a.Text != b.Text {
		t.Errorf("card rendering is not order-independent:\n  %q\n  %q", a.Text, b.Text)
	}
}

func TestHashChangesWithContent(t *testing.T) {
	node := graph.Node{Ref: ref("Pod", "shop", "web", "u"), Properties: map[string]any{"status": "Running"}}
	base := RenderCard(node, nil, DefaultOptions)

	changed := node
	changed.Properties = map[string]any{"status": "CrashLoopBackOff"}
	other := RenderCard(changed, nil, DefaultOptions)

	if base.Hash == other.Hash {
		t.Error("expected hash to change when status changes")
	}
}

func TestBuildCardsResolvesEndpointsFromSnapshot(t *testing.T) {
	deploy := graph.Node{Ref: ref("Deployment", "shop", "web", "u-deploy")}
	pod := graph.Node{Ref: ref("Pod", "shop", "web-7d9", "u-pod")}
	data := graph.GraphData{
		Nodes: []graph.Node{deploy, pod},
		// The relationship carries only UIDs on its endpoints, as returned by
		// the store's ReadGraph; BuildCards must resolve them to full refs.
		Relationships: []graph.Relationship{
			{Type: "OWNS", From: graph.Ref{UID: "u-deploy"}, To: graph.Ref{UID: "u-pod"}},
		},
	}

	cards := BuildCards(data, DefaultOptions)
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(cards))
	}
	byUID := map[string]Card{}
	for _, c := range cards {
		byUID[c.Ref.UID] = c
	}

	if got := byUID["u-deploy"].Text; !strings.Contains(got, "Owns Pod `web-7d9`") {
		t.Errorf("deployment card missing resolved owns clause: %q", got)
	}
	if got := byUID["u-pod"].Text; !strings.Contains(got, "Owned by Deployment `web`") {
		t.Errorf("pod card missing resolved owned-by clause: %q", got)
	}
}

func TestBuildCardsUnresolvedEndpointFallsBack(t *testing.T) {
	pod := graph.Node{Ref: ref("Pod", "shop", "web-7d9", "u-pod")}
	data := graph.GraphData{
		Nodes: []graph.Node{pod},
		Relationships: []graph.Relationship{
			// Owner endpoint is not in the snapshot but carries its own identity.
			{Type: "OWNS", From: ref("Deployment", "shop", "web", "u-deploy"), To: graph.Ref{UID: "u-pod"}},
		},
	}
	cards := BuildCards(data, DefaultOptions)
	if len(cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(cards))
	}
	if !strings.Contains(cards[0].Text, "Owned by Deployment `web`") {
		t.Errorf("expected fallback to edge-carried identity, got: %q", cards[0].Text)
	}
}
