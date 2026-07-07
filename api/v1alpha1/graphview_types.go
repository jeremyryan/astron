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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GraphViewSpec defines the desired state of GraphView.
//
// A GraphView is a named, saved set of filters applied to a GraphProjection.
// Projections capture a set of resources and relationships from the cluster;
// a view narrows that graph down to a meaningful subset for display, and can be
// recalled later by name. A GraphView is data-only: it has no reconcile loop and
// behaves like a schema-validated piece of configuration.
type GraphViewSpec struct {
	// projectionRef selects the GraphProjection this view filters.
	// +required
	ProjectionRef ProjectionReference `json:"projectionRef"`

	// displayName is a human-friendly name shown in the UI. When empty, the
	// resource name is used.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description is an optional human-readable description of the view.
	// +optional
	Description string `json:"description,omitempty"`

	// filters defines the set of filters this view applies to the projected
	// graph. All fields are optional; an empty filters block shows everything.
	// +optional
	Filters GraphViewFilters `json:"filters,omitempty"`
}

// ProjectionReference references a GraphProjection by name and optional
// namespace.
type ProjectionReference struct {
	// name is the name of the GraphProjection.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// namespace is the namespace of the GraphProjection. When omitted, the
	// namespace of the GraphView resource is used.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// GraphViewFilters captures the filtering options applied to a projection's
// graph when rendering a view. These mirror the interactive filters in the UI.
type GraphViewFilters struct {
	// kindMode selects how the resource-kind filter is interpreted: "hide"
	// (a hide-list: everything is shown except hiddenKinds) or "show" (an
	// allow-list: only visibleKinds are shown). An allow-list view is immune to
	// newly-captured kinds appearing unexpectedly. Defaults to "hide".
	// +optional
	// +kubebuilder:validation:Enum=hide;show
	// +kubebuilder:default=hide
	KindMode string `json:"kindMode,omitempty"`

	// hiddenKinds lists resource kinds to hide (e.g. "ConfigMap", "Secret").
	// Used when kindMode is "hide".
	// +optional
	HiddenKinds []string `json:"hiddenKinds,omitempty"`

	// visibleKinds lists the only resource kinds to show. Used when kindMode is
	// "show" (allow-list); any kind not listed is hidden.
	// +optional
	VisibleKinds []string `json:"visibleKinds,omitempty"`

	// hiddenNamespaces lists namespaces to hide.
	// +optional
	HiddenNamespaces []string `json:"hiddenNamespaces,omitempty"`

	// labelFilters restricts displayed nodes to those matching the given label
	// key/value pairs, combined according to labelMode.
	// +optional
	LabelFilters []LabelFilter `json:"labelFilters,omitempty"`

	// labelMode controls how labelFilters are combined: "any" (OR) or "all"
	// (AND).
	// +optional
	// +kubebuilder:validation:Enum=any;all
	// +kubebuilder:default=any
	LabelMode string `json:"labelMode,omitempty"`

	// maxDistance limits visible nodes to those within this many hops of the
	// selected node. When omitted, all connections are shown.
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxDistance *int32 `json:"maxDistance,omitempty"`

	// groupByNamespace groups resources into compound nodes by namespace when
	// true.
	// +optional
	GroupByNamespace *bool `json:"groupByNamespace,omitempty"`
}

// LabelFilter is a single label key/value constraint. An empty value matches any
// value for the given key (a presence check).
type LabelFilter struct {
	// key is the label key to match.
	// +required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// value is the label value to match. When empty, any value matches.
	// +optional
	Value string `json:"value,omitempty"`
}

// GraphViewStatus defines the observed state of GraphView.
//
// GraphView has no controller in Phase 1; this status is reserved for future
// validation (e.g. reporting a dangling projectionRef).
type GraphViewStatus struct {
	// observedGeneration is the most recent generation observed for this view by
	// the controller, if any.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the GraphView resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Projection",type="string",JSONPath=".spec.projectionRef.name"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// GraphView is the Schema for the graphviews API.
type GraphView struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GraphView.
	// +required
	Spec GraphViewSpec `json:"spec"`

	// status defines the observed state of GraphView.
	// +optional
	Status GraphViewStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GraphViewList contains a list of GraphView.
type GraphViewList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GraphView `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GraphView{}, &GraphViewList{})
}
