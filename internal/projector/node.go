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

package projector

import (
	"encoding/json"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"

	"github.com/project-gamera/gamera/internal/graph"
)

// newFactory builds a dynamic shared informer factory that watches all
// namespaces. Namespace and label filtering is performed during sync.
func newFactory(client dynamic.Interface, resync time.Duration) dynamicinformer.DynamicSharedInformerFactory {
	return dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, resync, metav1.NamespaceAll, nil)
}

// nodeFor converts a Kubernetes object into a graph.Node, extracting a set of
// generally useful, flat scalar properties (Neo4J properties cannot be nested).
// Maps such as labels and annotations are stored as JSON strings.
func nodeFor(obj *unstructured.Unstructured) graph.Node {
	props := map[string]any{
		"resourceVersion": obj.GetResourceVersion(),
	}
	if ct := obj.GetCreationTimestamp(); !ct.IsZero() {
		props["creationTimestamp"] = ct.UTC().Format(time.RFC3339)
	}
	if labels := obj.GetLabels(); len(labels) > 0 {
		if b, err := json.Marshal(labels); err == nil {
			props["labels"] = string(b)
		}
	}
	if annotations := stripLastApplied(obj.GetAnnotations()); len(annotations) > 0 {
		if b, err := json.Marshal(annotations); err == nil {
			props["annotations"] = string(b)
		}
	}

	return graph.Node{
		Ref: graph.Ref{
			APIVersion: obj.GetAPIVersion(),
			Kind:       obj.GetKind(),
			Namespace:  obj.GetNamespace(),
			Name:       obj.GetName(),
			UID:        string(obj.GetUID()),
		},
		Properties: props,
	}
}

// stripLastApplied removes the noisy kubectl last-applied-configuration
// annotation, which would bloat node properties.
func stripLastApplied(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	const key = "kubectl.kubernetes.io/last-applied-configuration"
	if _, ok := in[key]; !ok {
		return in
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if k == key {
			continue
		}
		out[k] = v
	}
	return out
}
