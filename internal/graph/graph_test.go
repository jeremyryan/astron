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

package graph

import "testing"

func TestSanitizeRelType(t *testing.T) {
	valid := []string{"OWNS", "MOUNTS", "SELECTS", "Owns_Pod", "_private", "A1"}
	for _, v := range valid {
		if _, err := sanitizeRelType(v); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", v, err)
		}
	}

	invalid := []string{"", "1OWNS", "OWNS POD", "OWNS-POD", "OWNS]->(x)", "drop`"}
	for _, v := range invalid {
		if _, err := sanitizeRelType(v); err == nil {
			t.Errorf("expected %q to be rejected, got no error", v)
		}
	}
}

func TestNodeKeyPrefersUID(t *testing.T) {
	withUID := Ref{APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "p", UID: "abc-123"}
	if got, want := nodeKey("proj-a", withUID), "proj-a|abc-123"; got != want {
		t.Errorf("nodeKey with UID = %q, want %q", got, want)
	}

	noUID := Ref{APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "p"}
	if got, want := nodeKey("proj-a", noUID), "proj-a|v1|Pod|default|p"; got != want {
		t.Errorf("nodeKey without UID = %q, want %q", got, want)
	}
}

func TestNodeKeyIsProjectionScoped(t *testing.T) {
	ref := Ref{UID: "same-uid"}
	if nodeKey("proj-a", ref) == nodeKey("proj-b", ref) {
		t.Error("expected node keys to differ across projections for the same UID")
	}
}

func TestNodeParamsIncludesIdentity(t *testing.T) {
	node := Node{
		Ref:        Ref{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "ns", Name: "web", UID: "u1"},
		Properties: map[string]any{"replicas": int64(3)},
	}
	params := nodeParams("proj", node)
	props := params["props"].(map[string]any)

	if props["kind"] != "Deployment" || props["name"] != "web" || props["uid"] != "u1" {
		t.Errorf("identity properties missing or wrong: %#v", props)
	}
	if props[projectionProperty] != "proj" {
		t.Errorf("expected projection property %q, got %v", "proj", props[projectionProperty])
	}
	if props["replicas"] != int64(3) {
		t.Errorf("expected user property to be preserved, got %v", props["replicas"])
	}
}

func TestRefGroupVersionKind(t *testing.T) {
	r := Ref{APIVersion: "apps/v1", Kind: "Deployment"}
	gvk := r.GroupVersionKind()
	if gvk.Group != "apps" || gvk.Version != "v1" || gvk.Kind != "Deployment" {
		t.Errorf("unexpected GVK: %+v", gvk)
	}

	core := Ref{APIVersion: "v1", Kind: "Pod"}
	if g := core.GroupVersionKind(); g.Group != "" || g.Version != "v1" || g.Kind != "Pod" {
		t.Errorf("unexpected core GVK: %+v", g)
	}
}
