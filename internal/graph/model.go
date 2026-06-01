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

// Package graph defines the domain model and storage abstraction used to
// project Kubernetes resources and their relationships into a graph database.
package graph

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Ref uniquely identifies a Kubernetes object within a cluster. It is used as
// the stable identity (the merge key) of a node in the graph.
type Ref struct {
	// APIVersion is the group/version of the object, e.g. "apps/v1" or "v1".
	APIVersion string
	// Kind is the object kind, e.g. "Deployment".
	Kind string
	// Namespace is the object namespace; empty for cluster-scoped objects.
	Namespace string
	// Name is the object name.
	Name string
	// UID is the Kubernetes UID of the object. It is the most stable identity
	// and is preferred as the merge key when available.
	UID string
}

// GroupVersionKind returns the schema.GroupVersionKind for the ref.
func (r Ref) GroupVersionKind() schema.GroupVersionKind {
	gv, err := schema.ParseGroupVersion(r.APIVersion)
	if err != nil {
		return schema.GroupVersionKind{Kind: r.Kind}
	}
	return gv.WithKind(r.Kind)
}

// String returns a human-readable identifier for the ref.
func (r Ref) String() string {
	if r.Namespace == "" {
		return fmt.Sprintf("%s/%s %s", r.APIVersion, r.Kind, r.Name)
	}
	return fmt.Sprintf("%s/%s %s/%s", r.APIVersion, r.Kind, r.Namespace, r.Name)
}

// ID returns a stable identifier for the ref, suitable for use as a graph node
// id in API responses. The UID is preferred; otherwise a composite of the
// identifying fields is used.
func (r Ref) ID() string {
	if r.UID != "" {
		return r.UID
	}
	return fmt.Sprintf("%s/%s/%s/%s", r.APIVersion, r.Kind, r.Namespace, r.Name)
}

// Node is a Kubernetes resource materialized as a graph node.
type Node struct {
	// Ref identifies the underlying Kubernetes object.
	Ref Ref
	// Properties are additional scalar attributes stored on the node, such as
	// creationTimestamp, resourceVersion or selected labels/annotations.
	Properties map[string]any
}

// Relationship is a directed edge between two nodes in the graph.
type Relationship struct {
	// Type is the Neo4J relationship type, e.g. "OWNS", "MOUNTS", "SELECTS".
	Type string
	// From is the source node ref.
	From Ref
	// To is the target node ref.
	To Ref
	// Properties are additional scalar attributes stored on the edge.
	Properties map[string]any
}

// ProjectionID identifies the GraphProjection that owns a set of nodes and
// relationships, so that data from different projections can be tracked and
// cleaned up independently.
type ProjectionID string

// GraphData is a read-only snapshot of a projection's materialized graph.
type GraphData struct {
	Nodes         []Node
	Relationships []Relationship
}

// NewRefFromObjectMeta builds a Ref from an object's GVK and metadata.
func NewRefFromObjectMeta(gvk schema.GroupVersionKind, meta metav1.Object) Ref {
	return Ref{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Namespace:  meta.GetNamespace(),
		Name:       meta.GetName(),
		UID:        string(meta.GetUID()),
	}
}
