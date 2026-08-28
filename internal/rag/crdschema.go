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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// JSON Schema type-string constants, used when building or comparing
// apiextensionsv1.JSONSchemaProps.Type values.
const (
	schemaTypeObject = "object"
	schemaTypeArray  = "array"
)

// maxSchemaRenderDepth bounds recursion into nested object/array schemas,
// protecting against pathologically deep CRD schemas (e.g. long chains of
// x-kubernetes-embedded-resource) blowing up the rendered document size.
const maxSchemaRenderDepth = 12

// maxRenderedDocBytes caps the size of a single rendered document (Overview
// or Full), truncating anything larger with a trailing marker. This is a
// coarse safety valve on top of maxSchemaRenderDepth, not the primary
// defense — very wide (not deep) schemas can still produce large output.
const maxRenderedDocBytes = 64 * 1024

// CRDSchema is the rendered documentation for one version served by a
// CustomResourceDefinition: a compact Overview suitable for embedding, and a
// complete Full field-by-field document for on-demand lookup. See
// RenderCRDSchemas.
type CRDSchema struct {
	// Group, Version and Kind identify this CRD version, e.g.
	// ("example.com", "v1", "Widget").
	Group, Version, Kind string
	// Scope is "Namespaced" or "Cluster", mirroring
	// apiextensionsv1.ResourceScope.
	Scope string
	// Storage reports whether this is the CRD's storage version (there is
	// exactly one per CRD). Only the storage version's Overview is intended
	// to be embedded for discovery; every version's Full document is
	// available for on-demand lookup regardless.
	Storage bool

	// Overview is a compact summary: kind/group/version/scope, the schema's
	// top-level description, and the top-level spec/status field names (not
	// nested detail). Intended for embedding.
	Overview string
	// Full is the complete rendered schema: every field, at every depth,
	// with its type, required marker, description, enum and default when
	// present. Intended for on-demand lookup, not embedding.
	Full string
}

// RenderCRDSchemas renders one CRDSchema per version served by crd. It
// performs no I/O. A CRD with no versions (not expected in practice) yields
// an empty slice.
func RenderCRDSchemas(crd *apiextensionsv1.CustomResourceDefinition) []CRDSchema {
	group := crd.Spec.Group
	kind := crd.Spec.Names.Kind
	scope := string(crd.Spec.Scope)

	out := make([]CRDSchema, 0, len(crd.Spec.Versions))
	for _, v := range crd.Spec.Versions {
		var schema *apiextensionsv1.JSONSchemaProps
		if v.Schema != nil {
			schema = v.Schema.OpenAPIV3Schema
		}
		out = append(out, CRDSchema{
			Group:    group,
			Version:  v.Name,
			Kind:     kind,
			Scope:    scope,
			Storage:  v.Storage,
			Overview: truncateDoc(renderOverview(group, kind, scope, v.Name, schema)),
			Full:     truncateDoc(renderFull(group, kind, scope, v.Name, schema)),
		})
	}
	return out
}

// apiVersion joins a group and version the way Kubernetes does, omitting the
// group for core (group-less) types.
func apiVersion(group, version string) string {
	if group == "" {
		return version
	}
	return group + "/" + version
}

// renderOverview builds the compact, embeddable summary of one CRD version.
func renderOverview(group, kind, scope, version string, schema *apiextensionsv1.JSONSchemaProps) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kind: %s\n", kind)
	fmt.Fprintf(&b, "API Version: %s\n", apiVersion(group, version))
	fmt.Fprintf(&b, "Scope: %s\n", scope)
	if schema == nil {
		b.WriteString("(no schema published for this version)\n")
		return b.String()
	}
	if d := strings.TrimSpace(schema.Description); d != "" {
		fmt.Fprintf(&b, "Description: %s\n", d)
	}
	if fields := topLevelFieldSummary(schema, "spec"); fields != "" {
		fmt.Fprintf(&b, "spec fields: %s\n", fields)
	}
	if fields := topLevelFieldSummary(schema, "status"); fields != "" {
		fmt.Fprintf(&b, "status fields: %s\n", fields)
	}
	return b.String()
}

// topLevelFieldSummary renders "name (type), name (type), ..." for the direct
// properties of schema's named top-level field (e.g. "spec"), sorted by name.
// Returns "" when that top-level field doesn't exist or has no properties.
func topLevelFieldSummary(schema *apiextensionsv1.JSONSchemaProps, name string) string {
	top, ok := schema.Properties[name]
	if !ok || len(top.Properties) == 0 {
		return ""
	}
	names := sortedKeys(top.Properties)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s (%s)", k, typeLabel(top.Properties[k])))
	}
	return strings.Join(parts, ", ")
}

// renderFull builds the complete field-by-field document for one CRD
// version.
func renderFull(group, kind, scope, version string, schema *apiextensionsv1.JSONSchemaProps) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", kind)
	fmt.Fprintf(&b, "API Version: %s\n", apiVersion(group, version))
	fmt.Fprintf(&b, "Scope: %s\n\n", scope)
	if schema == nil {
		b.WriteString("(no schema published for this version)\n")
		return b.String()
	}
	if d := strings.TrimSpace(schema.Description); d != "" {
		fmt.Fprintf(&b, "%s\n\n", d)
	}
	renderFields(&b, schema, "", 0)
	return b.String()
}

// renderFields writes one line per property of schema (depth-first, indented
// two spaces per level), each annotated with its type, a [required] marker,
// description, enum and default when present. Recursion is capped at
// maxSchemaRenderDepth to bound output size for pathologically deep schemas.
func renderFields(b *strings.Builder, schema *apiextensionsv1.JSONSchemaProps, indent string, depth int) {
	if schema == nil || depth > maxSchemaRenderDepth || len(schema.Properties) == 0 {
		return
	}
	required := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		required[r] = true
	}
	for _, name := range sortedKeys(schema.Properties) {
		p := schema.Properties[name]
		line := indent + "- " + name + " (" + typeLabel(p) + ")"
		if required[name] {
			line += " [required]"
		}
		b.WriteString(line)
		b.WriteString("\n")
		if d := strings.TrimSpace(p.Description); d != "" {
			b.WriteString(indent + "    " + d + "\n")
		}
		if len(p.Enum) > 0 {
			b.WriteString(indent + "    enum: " + jsonValues(p.Enum) + "\n")
		}
		if p.Default != nil {
			b.WriteString(indent + "    default: " + string(p.Default.Raw) + "\n")
		}
		renderNested(b, p, indent+"  ", depth+1)
	}
}

// renderNested recurses into a property's nested object properties (direct,
// or through an array's item schema); every other shape (scalars, maps,
// oneOf/anyOf alternatives, embedded resources) is fully described by
// typeLabel on the property's own line and has nothing further to recurse
// into here.
func renderNested(b *strings.Builder, p apiextensionsv1.JSONSchemaProps, indent string, depth int) {
	switch {
	case p.Type == schemaTypeObject && len(p.Properties) > 0:
		renderFields(b, &p, indent, depth)
	case p.Type == schemaTypeArray && p.Items != nil && p.Items.Schema != nil && p.Items.Schema.Type == schemaTypeObject:
		renderFields(b, p.Items.Schema, indent, depth)
	}
}

// typeLabel renders a short, human-readable type description for one schema
// property, e.g. "string", "array of object", "map of string",
// "one of 2 alternatives", "int-or-string", "object (embedded resource)".
func typeLabel(p apiextensionsv1.JSONSchemaProps) string {
	switch {
	case p.XIntOrString:
		return "int-or-string"
	case p.XEmbeddedResource:
		return "object (embedded resource)"
	case len(p.OneOf) > 0:
		return fmt.Sprintf("one of %d alternatives", len(p.OneOf))
	case len(p.AnyOf) > 0:
		return fmt.Sprintf("any of %d alternatives", len(p.AnyOf))
	case p.Type == schemaTypeArray:
		if p.Items != nil && p.Items.Schema != nil {
			return "array of " + typeLabel(*p.Items.Schema)
		}
		return schemaTypeArray
	case p.Type == schemaTypeObject:
		return objectTypeLabel(p)
	case p.Type != "":
		return p.Type
	default:
		return "any"
	}
}

// objectTypeLabel renders the type label for an object-typed property,
// distinguishing a map (additionalProperties, no fixed properties), an
// arbitrary-fields object (x-kubernetes-preserve-unknown-fields), and an
// ordinary struct-shaped object.
func objectTypeLabel(p apiextensionsv1.JSONSchemaProps) string {
	if p.AdditionalProperties != nil {
		if p.AdditionalProperties.Schema != nil {
			return "map of " + typeLabel(*p.AdditionalProperties.Schema)
		}
		if p.AdditionalProperties.Allows && len(p.Properties) == 0 {
			return "map"
		}
	}
	if p.XPreserveUnknownFields != nil && *p.XPreserveUnknownFields && len(p.Properties) == 0 {
		return "object (arbitrary fields)"
	}
	return "object"
}

// jsonValues renders a list of raw JSON values (e.g. an enum) as a
// comma-separated string, using each value's own JSON encoding (so a string
// enum value like "Running" keeps its quotes, a number like 3 doesn't).
func jsonValues(vals []apiextensionsv1.JSON) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = string(v.Raw)
	}
	return strings.Join(parts, ", ")
}

// sortedKeys returns the keys of a JSONSchemaProps property map, sorted, for
// deterministic rendering (map iteration order is not stable, and
// deterministic output matters here the same way it does for rag.BuildCards:
// it drives content-hash-based incremental re-embedding).
func sortedKeys(m map[string]apiextensionsv1.JSONSchemaProps) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// truncateDoc caps a rendered document at maxRenderedDocBytes, appending a
// marker when it does.
func truncateDoc(s string) string {
	if len(s) <= maxRenderedDocBytes {
		return s
	}
	return s[:maxRenderedDocBytes] + "\n…(truncated)"
}
