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
	"fmt"
	"maps"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"

	"github.com/project-astron/astron/internal/graph"
)

// podKind is the Kind of core/v1 Pod objects, whose status is surfaced as flat
// node properties so that lifecycle changes are visible in the UI.
const podKind = "Pod"

// httpRouteKind is the Kind of Gateway API HTTPRoute objects, whose spec
// hostnames are surfaced as a node property so the UI can link to them.
const httpRouteKind = "HTTPRoute"

// newFactory builds a dynamic shared informer factory scoped to the given
// namespace (use metav1.NamespaceAll to watch the whole cluster). Additional
// namespace and label filtering is performed during sync.
func newFactory(client dynamic.Interface, resync time.Duration, namespace string) dynamicinformer.DynamicSharedInformerFactory {
	return dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, resync, namespace, nil)
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

	// Surface Pod status as flat scalar properties so status changes (phase,
	// readiness, restarts) flow through to the graph and the UI on each re-sync.
	if obj.GetAPIVersion() == "v1" && obj.GetKind() == podKind {
		maps.Copy(props, podStatusProps(obj))
	}

	// Surface HTTPRoute hostnames so the UI can list them as clickable links.
	if obj.GetKind() == httpRouteKind {
		if hostnames, found, err := unstructured.NestedStringSlice(obj.Object, "spec", "hostnames"); err == nil && found && len(hostnames) > 0 {
			props["hostnames"] = hostnames
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

// podStatusProps extracts a set of flat, generally useful Pod status fields.
// Neo4J properties cannot be nested, so the status is flattened into scalars:
//
//   - phase:     the high-level pod phase (Pending/Running/Succeeded/Failed)
//   - status:    a human-friendly status, mirroring kubectl's STATUS column
//     (e.g. "CrashLoopBackOff", "Completed") when a container condition refines
//     the phase; otherwise equal to phase
//   - ready:     ready container count over total, e.g. "2/3"
//   - restarts:  total container restart count
//   - podIP:     the assigned pod IP, when present
//   - startTime: when the pod was acknowledged by the kubelet (RFC3339)
func podStatusProps(obj *unstructured.Unstructured) map[string]any {
	out := map[string]any{}

	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	if phase != "" {
		out["phase"] = phase
	}
	if ip, ok, _ := unstructured.NestedString(obj.Object, "status", "podIP"); ok && ip != "" {
		out["podIP"] = ip
	}
	if st, ok, _ := unstructured.NestedString(obj.Object, "status", "startTime"); ok && st != "" {
		out["startTime"] = st
	}

	statuses, _, _ := unstructured.NestedSlice(obj.Object, "status", "containerStatuses")
	ready, restarts := 0, int64(0)
	var reason string
	for _, c := range statuses {
		cs, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if r, ok, _ := unstructured.NestedBool(cs, "ready"); ok && r {
			ready++
		}
		if rc, ok, _ := unstructured.NestedInt64(cs, "restartCount"); ok {
			restarts += rc
		}
		if reason == "" {
			reason = containerStateReason(cs)
		}
	}
	if len(statuses) > 0 {
		out["ready"] = fmt.Sprintf("%d/%d", ready, len(statuses))
	}
	out["restarts"] = restarts

	// Mirror kubectl: a container-level reason (e.g. CrashLoopBackOff,
	// ImagePullBackOff, Completed) refines the coarse phase when present.
	switch {
	case reason != "":
		out["status"] = reason
	case phase != "":
		out["status"] = phase
	}

	return out
}

// containerStateReason returns a refining status reason from a single container
// status: a waiting reason (e.g. CrashLoopBackOff), or a terminated reason
// (e.g. Completed, Error, OOMKilled). It returns "" when the container is
// running normally.
func containerStateReason(cs map[string]any) string {
	if r, ok, _ := unstructured.NestedString(cs, "state", "waiting", "reason"); ok && r != "" {
		return r
	}
	if r, ok, _ := unstructured.NestedString(cs, "state", "terminated", "reason"); ok && r != "" {
		return r
	}
	return ""
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
