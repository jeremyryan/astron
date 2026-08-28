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
	"os"
	"strconv"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// widgetCRD builds a small, hand-crafted CRD exercising the common,
// well-behaved case: a handful of scalar and nested fields, one served and
// stored version.
func widgetCRD() *apiextensionsv1.CustomResourceDefinition {
	trueVal := true
	return &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "Widget"},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Description: "A Widget is a thing that widgets.",
							Type:        schemaTypeObject,
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type:     schemaTypeObject,
									Required: []string{"size"},
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"size": {
											Type:        "integer",
											Description: "Size of the widget.",
										},
										"color": {
											Type:    "string",
											Default: &apiextensionsv1.JSON{Raw: []byte(`"red"`)},
											Enum: []apiextensionsv1.JSON{
												{Raw: []byte(`"red"`)},
												{Raw: []byte(`"blue"`)},
											},
										},
										"tags": {
											Type:  schemaTypeArray,
											Items: &apiextensionsv1.JSONSchemaPropsOrArray{Schema: &apiextensionsv1.JSONSchemaProps{Type: "string"}},
										},
										"labels": {
											Type: schemaTypeObject,
											AdditionalProperties: &apiextensionsv1.JSONSchemaPropsOrBool{
												Schema: &apiextensionsv1.JSONSchemaProps{Type: "string"},
											},
										},
										"metadata": {
											Type:                   schemaTypeObject,
											XPreserveUnknownFields: &trueVal,
										},
										"template": {
											Type:              schemaTypeObject,
											XEmbeddedResource: true,
										},
										"port": {
											XIntOrString: true,
										},
										"selector": {
											OneOf: []apiextensionsv1.JSONSchemaProps{
												{Type: "string"},
												{Type: schemaTypeObject},
											},
										},
										"nested": {
											Type: schemaTypeObject,
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"inner": {Type: "string", Description: "An inner field."},
											},
										},
									},
								},
								"status": {
									Type: schemaTypeObject,
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"phase": {Type: "string"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestRenderCRDSchemasBasicOverview(t *testing.T) {
	schemas := RenderCRDSchemas(widgetCRD())
	if len(schemas) != 1 {
		t.Fatalf("expected 1 rendered version, got %d", len(schemas))
	}
	s := schemas[0]
	if s.Group != "example.com" || s.Version != "v1" || s.Kind != "Widget" || s.Scope != "Namespaced" {
		t.Fatalf("unexpected identity: %+v", s)
	}
	if !s.Storage {
		t.Error("expected Storage = true")
	}

	for _, want := range []string{
		"Kind: Widget",
		"API Version: example.com/v1",
		"Scope: Namespaced",
		"Description: A Widget is a thing that widgets.",
		"spec fields:",
		"size (integer)",
		"status fields:",
		"phase (string)",
	} {
		if !strings.Contains(s.Overview, want) {
			t.Errorf("Overview missing %q:\n%s", want, s.Overview)
		}
	}
	// The overview only lists top-level field names/types, not nested detail.
	if strings.Contains(s.Overview, "inner") {
		t.Errorf("Overview should not include nested field detail:\n%s", s.Overview)
	}
}

func TestRenderCRDSchemasFullDocument(t *testing.T) {
	schemas := RenderCRDSchemas(widgetCRD())
	full := schemas[0].Full

	cases := []struct {
		name string
		want string
	}{
		{"required marker", "size (integer) [required]"},
		{"description", "Size of the widget."},
		{"enum", "enum: \"red\", \"blue\""},
		{"default", "default: \"red\""},
		{"array of scalar", "tags (array of string)"},
		{"map of scalar", "labels (map of string)"},
		{"arbitrary-fields object", "metadata (object (arbitrary fields))"},
		{"embedded resource", "template (object (embedded resource))"},
		{"int-or-string", "port (int-or-string)"},
		{"oneOf alternatives", "selector (one of 2 alternatives)"},
		{"nested object recursion", "- inner (string)"},
		{"nested description", "An inner field."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(full, c.want) {
				t.Errorf("Full missing %q:\n%s", c.want, full)
			}
		})
	}
}

func TestRenderCRDSchemasMultipleVersionsAndStorage(t *testing.T) {
	crd := widgetCRD()
	crd.Spec.Versions = append(crd.Spec.Versions, apiextensionsv1.CustomResourceDefinitionVersion{
		Name:    "v1beta1",
		Served:  true,
		Storage: false,
		Schema: &apiextensionsv1.CustomResourceValidation{
			OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: schemaTypeObject},
		},
	})

	schemas := RenderCRDSchemas(crd)
	if len(schemas) != 2 {
		t.Fatalf("expected 2 rendered versions, got %d", len(schemas))
	}
	byVersion := map[string]CRDSchema{}
	for _, s := range schemas {
		byVersion[s.Version] = s
	}
	if !byVersion["v1"].Storage {
		t.Error("v1 should be the storage version")
	}
	if byVersion["v1beta1"].Storage {
		t.Error("v1beta1 should not be the storage version")
	}
}

func TestRenderCRDSchemasNoSchemaPublished(t *testing.T) {
	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "Empty"},
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1", Served: true, Storage: true}, // no Schema at all
			},
		},
	}
	schemas := RenderCRDSchemas(crd)
	if len(schemas) != 1 {
		t.Fatalf("expected 1 rendered version, got %d", len(schemas))
	}
	if !strings.Contains(schemas[0].Overview, "no schema published") {
		t.Errorf("Overview = %q", schemas[0].Overview)
	}
	if !strings.Contains(schemas[0].Full, "no schema published") {
		t.Errorf("Full = %q", schemas[0].Full)
	}
}

// TestRenderCRDSchemasDepthCapTerminates verifies rendering a schema nested
// far beyond maxSchemaRenderDepth completes (doesn't panic or hang) and stops
// descending at the cap, rather than attempting a real-world pathological
// case (deeply chained x-kubernetes-embedded-resource is the closest thing to
// a "recursive" schema CRDs actually produce; JSONSchemaProps itself is a
// finite Go value, so there's no literal cycle to guard against, only depth).
func TestRenderCRDSchemasDepthCapTerminates(t *testing.T) {
	// Build a chain of nested objects deeper than the cap.
	leaf := apiextensionsv1.JSONSchemaProps{Type: "string"}
	current := leaf
	depth := maxSchemaRenderDepth + 10
	for range depth {
		current = apiextensionsv1.JSONSchemaProps{
			Type:       schemaTypeObject,
			Properties: map[string]apiextensionsv1.JSONSchemaProps{"child": current},
		}
	}
	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "Deep"},
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1", Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type:       schemaTypeObject,
					Properties: map[string]apiextensionsv1.JSONSchemaProps{"spec": current},
				}},
			}},
		},
	}

	schemas := RenderCRDSchemas(crd)
	if len(schemas) != 1 {
		t.Fatalf("expected 1 rendered version, got %d", len(schemas))
	}
	// Count how many nested "child" lines were actually rendered; it must be
	// far fewer than the constructed depth.
	got := strings.Count(schemas[0].Full, "- child (object)")
	if got == 0 {
		t.Fatal("expected at least the top few levels of child to render")
	}
	if got > maxSchemaRenderDepth+1 {
		t.Errorf("rendered %d levels of child, want at most ~%d (depth cap)", got, maxSchemaRenderDepth+1)
	}
}

// TestRenderCRDSchemasTruncatesLargeDocuments verifies a document larger than
// maxRenderedDocBytes is truncated rather than growing unbounded.
func TestRenderCRDSchemasTruncatesLargeDocuments(t *testing.T) {
	props := make(map[string]apiextensionsv1.JSONSchemaProps, 5000)
	longDescription := strings.Repeat("x", 200)
	for i := range 5000 {
		props["f"+strconv.Itoa(i)] = apiextensionsv1.JSONSchemaProps{
			Type: "string", Description: longDescription,
		}
	}
	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "Huge"},
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1", Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type:       schemaTypeObject,
					Properties: map[string]apiextensionsv1.JSONSchemaProps{"spec": {Type: schemaTypeObject, Properties: props}},
				}},
			}},
		},
	}

	schemas := RenderCRDSchemas(crd)
	full := schemas[0].Full
	if len(full) > maxRenderedDocBytes+100 {
		t.Fatalf("Full is %d bytes, want capped near %d", len(full), maxRenderedDocBytes)
	}
	if !strings.HasSuffix(full, "…(truncated)") {
		t.Errorf("expected a truncation marker, got suffix: %q", full[max(0, len(full)-40):])
	}
}

// TestRenderCRDSchemasRealWorldFixture renders a trimmed, cert-manager-shaped
// Certificate CRD (see testdata/certificate-crd.yaml) to validate against
// realistic schema complexity, not just synthetic constructs: multiple
// versions (one deprecated, non-storage), nested required objects, arrays of
// enum-constrained strings, arrays of objects, a map (additionalProperties),
// and a mix of documented/undocumented fields.
func TestRenderCRDSchemasRealWorldFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/certificate-crd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("unmarshaling fixture: %v", err)
	}

	schemas := RenderCRDSchemas(&crd)
	if len(schemas) != 2 {
		t.Fatalf("expected 2 rendered versions, got %d", len(schemas))
	}
	byVersion := map[string]CRDSchema{}
	for _, s := range schemas {
		byVersion[s.Version] = s
		if s.Kind != "Certificate" || s.Group != "cert-manager.io" || s.Scope != "Namespaced" {
			t.Errorf("unexpected identity for %s: %+v", s.Version, s)
		}
	}
	if !byVersion["v1"].Storage {
		t.Error("v1 should be the storage version")
	}
	if byVersion["v1alpha2"].Storage {
		t.Error("v1alpha2 should not be the storage version")
	}

	v1 := byVersion["v1"]
	for _, want := range []string{"secretName (string) [required]", "issuerRef (object) [required]"} {
		if !strings.Contains(v1.Full, want) {
			t.Errorf("Full missing %q", want)
		}
	}
	for _, want := range []string{
		"dnsNames (array of string)",
		"usages (array of string)", // array of enum-constrained strings still summarized as "array of string"
		"issuerRef (object)",
		"- name (string) [required]",
		"algorithm (string)",
		`default: "RSA"`,
		"annotations (map of string)",
		"port (int-or-string)",
		"conditions (array of object)",
		"- type (string) [required]",
		`enum: "True", "False", "Unknown"`,
	} {
		if !strings.Contains(v1.Full, want) {
			t.Errorf("Full missing %q:\n%s", want, v1.Full)
		}
	}
	if !strings.Contains(v1.Overview, "secretName") {
		t.Errorf("Overview missing top-level spec field secretName:\n%s", v1.Overview)
	}
}
