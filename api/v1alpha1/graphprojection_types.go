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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GraphProjectionSpec defines the desired state of GraphProjection.
//
// A GraphProjection declares how the live state of a Kubernetes cluster is
// projected into a Neo4J graph database: which Neo4J instance to write to,
// which portion of the cluster to watch, and which relationships to materialize
// as edges between resource nodes.
type GraphProjectionSpec struct {
	// neo4j configures the connection to the target Neo4J database that this
	// projection writes nodes and relationships into.
	// +required
	Neo4j Neo4jConnection `json:"neo4j"`

	// scope selects the portion of the cluster that this projection watches and
	// captures. When omitted, the projection watches the entire cluster.
	// +optional
	Scope ProjectionScope `json:"scope,omitempty"`

	// relationships enumerates the relationship rules used to derive edges
	// between resource nodes in the graph. When empty, a built-in default set of
	// well-known relationships (e.g. OWNS, MOUNTS, SELECTS) is applied.
	// +listType=map
	// +listMapKey=name
	// +optional
	Relationships []RelationshipRule `json:"relationships,omitempty"`

	// resyncInterval is how often the projection performs a full reconciliation
	// of cluster state against the graph, independent of watch events. This acts
	// as a safety net against missed events and drift.
	// +optional
	ResyncInterval *metav1.Duration `json:"resyncInterval,omitempty"`
}

// Neo4jConnection describes how to reach and authenticate against a Neo4J
// database.
type Neo4jConnection struct {
	// uri is the bolt/neo4j connection URI of the target database,
	// e.g. "neo4j://neo4j.gamera-system.svc:7687".
	// +required
	// +kubebuilder:validation:MinLength=1
	URI string `json:"uri"`

	// database is the name of the Neo4J database to write into.
	// +optional
	// +kubebuilder:default=neo4j
	Database string `json:"database,omitempty"`

	// authSecretRef references a Secret containing the credentials used to
	// authenticate to Neo4J. The Secret is expected to contain "username" and
	// "password" keys unless overridden by usernameKey/passwordKey.
	// +required
	AuthSecretRef SecretReference `json:"authSecretRef"`
}

// SecretReference references a key-bearing Secret used for Neo4J credentials.
type SecretReference struct {
	// name is the name of the Secret.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// namespace is the namespace of the Secret. When omitted, the namespace of
	// the GraphProjection resource is used.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// usernameKey is the Secret data key holding the Neo4J username.
	// +optional
	// +kubebuilder:default=username
	UsernameKey string `json:"usernameKey,omitempty"`

	// passwordKey is the Secret data key holding the Neo4J password.
	// +optional
	// +kubebuilder:default=password
	PasswordKey string `json:"passwordKey,omitempty"`
}

// ProjectionScope constrains which resources a projection captures.
type ProjectionScope struct {
	// ownNamespaceOnly restricts the projection to the namespace in which the
	// GraphProjection resource itself is defined. When true, only namespaced
	// resources in that namespace are captured (cluster-scoped resources and the
	// namespaces field are ignored). This takes precedence over namespaces.
	// +optional
	OwnNamespaceOnly bool `json:"ownNamespaceOnly,omitempty"`

	// namespaces restricts the projection to the named namespaces. When empty,
	// resources from all namespaces (and cluster-scoped resources) are captured.
	// Ignored when ownNamespaceOnly is true.
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`

	// resources is an allow-list of resource kinds to capture as nodes. When
	// empty, a built-in default set of common workload, configuration and
	// networking resources is captured.
	// +listType=map
	// +listMapKey=kind
	// +optional
	Resources []ResourceSelector `json:"resources,omitempty"`

	// crds controls whether CustomResourceDefinitions are captured as graph
	// nodes. When CRDs are captured, the projection additionally creates a
	// DEFINES edge from each captured CRD to every captured resource that is an
	// instance of that CRD. When omitted, CRDs are not captured.
	// +optional
	CRDs *CRDSelection `json:"crds,omitempty"`

	// labelSelector further restricts captured resources to those matching the
	// given label selector.
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
}

// CRDSelection controls capturing CustomResourceDefinitions (CRDs) as graph
// nodes.
type CRDSelection struct {
	// include, when true, captures CustomResourceDefinitions as graph nodes. When
	// true and names is empty, all CRDs in the cluster are captured. Capturing is
	// also implied when names is non-empty.
	// +optional
	Include bool `json:"include,omitempty"`

	// names optionally restricts the captured CRDs to the named
	// CustomResourceDefinitions, identified by their metadata.name
	// (e.g. "widgets.example.com"). When non-empty, only these CRDs are captured
	// regardless of the include flag; when empty, the include flag governs
	// whether all CRDs are captured.
	// +optional
	Names []string `json:"names,omitempty"`
}

// ResourceSelector identifies a Kubernetes resource type to capture as graph
// nodes.
type ResourceSelector struct {
	// group is the API group of the resource, e.g. "apps". The empty string
	// denotes the core API group.
	// +optional
	Group string `json:"group,omitempty"`

	// version is the API version of the resource, e.g. "v1".
	// +optional
	Version string `json:"version,omitempty"`

	// kind is the kind of the resource, e.g. "Pod" or "Deployment".
	// +required
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`
}

// RelationshipRule defines how an edge between two resource kinds is derived.
type RelationshipRule struct {
	// name is a unique identifier for this relationship rule within the
	// projection.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// type is the Neo4J relationship (edge) type to create,
	// e.g. "OWNS", "MOUNTS", or "SELECTS".
	// +required
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// from is the source resource kind of the edge.
	// +required
	From ResourceSelector `json:"from"`

	// to is the target resource kind of the edge.
	// +required
	To ResourceSelector `json:"to"`

	// strategy selects how the relationship between source and target resources
	// is determined.
	// +required
	// +kubebuilder:validation:Enum=OwnerReference;LabelSelector;VolumeMount;ClaimRef;ServiceBackend;Custom
	Strategy RelationshipStrategy `json:"strategy"`
}

// RelationshipStrategy enumerates the supported ways of deriving an edge.
type RelationshipStrategy string

const (
	// OwnerReferenceStrategy derives edges from Kubernetes ownerReferences.
	OwnerReferenceStrategy RelationshipStrategy = "OwnerReference"
	// LabelSelectorStrategy derives edges by matching a selector on the source
	// against labels on the target (e.g. Service -> Pod).
	LabelSelectorStrategy RelationshipStrategy = "LabelSelector"
	// VolumeMountStrategy derives edges from volume/mount references
	// (e.g. ConfigMap/Secret/PersistentVolumeClaim -> Pod).
	VolumeMountStrategy RelationshipStrategy = "VolumeMount"
	// ClaimRefStrategy derives edges between a PersistentVolume and the
	// PersistentVolumeClaim it is bound to (via spec.claimRef / spec.volumeName).
	ClaimRefStrategy RelationshipStrategy = "ClaimRef"
	// ServiceBackendStrategy derives edges from a traffic-routing resource
	// (Ingress or HTTPRoute) to the Services it forwards traffic to.
	ServiceBackendStrategy RelationshipStrategy = "ServiceBackend"
	// CustomStrategy is reserved for projection-specific relationship logic.
	CustomStrategy RelationshipStrategy = "Custom"
)

// GraphProjectionStatus defines the observed state of GraphProjection.
type GraphProjectionStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the GraphProjection resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// observedGeneration is the most recent generation observed by the
	// controller for this GraphProjection.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// phase is a high-level summary of where the projection is in its lifecycle.
	// +optional
	Phase string `json:"phase,omitempty"`

	// nodeCount is the number of resource nodes currently materialized in the
	// graph by this projection.
	// +optional
	NodeCount int64 `json:"nodeCount,omitempty"`

	// relationshipCount is the number of relationships (edges) currently
	// materialized in the graph by this projection.
	// +optional
	RelationshipCount int64 `json:"relationshipCount,omitempty"`

	// lastSyncTime is the timestamp of the last successful full reconciliation
	// against the graph database.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Nodes",type="integer",JSONPath=".status.nodeCount"
// +kubebuilder:printcolumn:name="Edges",type="integer",JSONPath=".status.relationshipCount"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// GraphProjection is the Schema for the graphprojections API
type GraphProjection struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GraphProjection
	// +required
	Spec GraphProjectionSpec `json:"spec"`

	// status defines the observed state of GraphProjection
	// +optional
	Status GraphProjectionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GraphProjectionList contains a list of GraphProjection
type GraphProjectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GraphProjection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GraphProjection{}, &GraphProjectionList{})
}
