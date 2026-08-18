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

package agent

import "testing"

// TestCatalogHasExpectedTools verifies the phase-1 catalog advertises exactly
// the five documented tools, each with a non-empty description and an object
// schema.
func TestCatalogHasExpectedTools(t *testing.T) {
	want := []string{
		ToolSearchClusterGraph,
		ToolGetResourceNeighborhood,
		ToolQueryGraph,
		ToolGetGraphSchema,
		ToolGetResourceYAML,
	}
	specs := Catalog()
	if len(specs) != len(want) {
		t.Fatalf("Catalog() has %d tools, want %d: %+v", len(specs), len(want), specs)
	}
	for i, spec := range specs {
		if spec.Name != want[i] {
			t.Errorf("specs[%d].Name = %q, want %q", i, spec.Name, want[i])
		}
		if spec.Description == "" {
			t.Errorf("%s: empty description", spec.Name)
		}
		if spec.Parameters["type"] != "object" {
			t.Errorf("%s: parameters type = %v, want object", spec.Name, spec.Parameters["type"])
		}
		if _, ok := spec.Parameters["properties"]; !ok {
			t.Errorf("%s: parameters missing properties", spec.Name)
		}
	}
}

// TestCatalogNoProjectionRoutingParameters verifies the phase-1 (in-process,
// single-projection) catalog does not require the caller to name a
// projection, unlike the equivalent MCP tools exposed to external clients.
func TestCatalogNoProjectionRoutingParameters(t *testing.T) {
	for _, spec := range Catalog() {
		props, _ := spec.Parameters["properties"].(map[string]any)
		for _, forbidden := range []string{"projectionNamespace", "projectionName"} {
			if _, ok := props[forbidden]; ok {
				t.Errorf("%s: parameters unexpectedly include %q", spec.Name, forbidden)
			}
		}
	}
}

// TestCatalogRequiredParametersAreDeclared verifies every parameter listed as
// required is also declared in properties, for each tool that has required
// parameters.
func TestCatalogRequiredParametersAreDeclared(t *testing.T) {
	for _, spec := range Catalog() {
		props, _ := spec.Parameters["properties"].(map[string]any)
		required, _ := spec.Parameters["required"].([]string)
		for _, name := range required {
			if _, ok := props[name]; !ok {
				t.Errorf("%s: required parameter %q not declared in properties", spec.Name, name)
			}
		}
	}
}

func TestCatalogByName(t *testing.T) {
	spec, ok := CatalogByName(ToolQueryGraph)
	if !ok || spec.Name != ToolQueryGraph {
		t.Fatalf("CatalogByName(%q) = %+v, %v", ToolQueryGraph, spec, ok)
	}

	if _, ok := CatalogByName("no_such_tool"); ok {
		t.Fatal("did not expect to find an unregistered tool")
	}
}

// TestForMultiProjectionAddsRoutingParameters verifies the wrapped spec keeps
// the tool's name, description and original parameters, while adding
// projectionNamespace/projectionName as required.
func TestForMultiProjectionAddsRoutingParameters(t *testing.T) {
	spec, ok := CatalogByName(ToolGetResourceNeighborhood)
	if !ok {
		t.Fatal("catalog missing get_resource_neighborhood")
	}
	wrapped := ForMultiProjection(spec)

	if wrapped.Name != spec.Name || wrapped.Description != spec.Description {
		t.Fatalf("name/description changed: %+v", wrapped)
	}

	props, _ := wrapped.Parameters["properties"].(map[string]any)
	for _, want := range []string{"projectionNamespace", "projectionName", "kind", "name"} {
		if _, ok := props[want]; !ok {
			t.Errorf("wrapped parameters missing %q: %+v", want, props)
		}
	}

	required, _ := wrapped.Parameters["required"].([]string)
	for _, want := range []string{"projectionNamespace", "projectionName", "kind", "name"} {
		found := false
		for _, r := range required {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Errorf("wrapped required missing %q: %v", want, required)
		}
	}

	// The original spec (and catalog) must be unaffected.
	if orig, ok := CatalogByName(ToolGetResourceNeighborhood); !ok {
		t.Fatal("catalog entry disappeared")
	} else if _, ok := orig.Parameters["properties"].(map[string]any)["projectionNamespace"]; ok {
		t.Fatal("ForMultiProjection must not mutate the original catalog spec")
	}
}
