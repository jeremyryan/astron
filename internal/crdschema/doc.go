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

// Package crdschema implements the standalone, controller-wide CRD schema
// syncer described in docs/crd-schema-design.md: it watches
// CustomResourceDefinition objects cluster-wide, renders their schemas
// (internal/rag.RenderCRDSchemas), and keeps a graph.SchemaStore in sync with
// what's currently selected (an optional name allow-list).
//
// Unlike internal/projector, this has no relation to any GraphProjection: it
// is not per-projection state, has no relationship engine or namespace/label
// scoping, and runs once for the controller's whole lifetime rather than
// being started and stopped alongside a GraphProjection's existence. It is
// the store's only writer for ResourceSchema data, which keeps its sync loop
// a plain diff against "what's selected right now" rather than needing any
// cross-writer coordination.
package crdschema
