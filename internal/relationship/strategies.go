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

package relationship

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
)

// kindConfigMap, kindSecret and kindPersistentVolumeClaim are the resource kinds
// understood by the volume-mount strategy.
const (
	kindConfigMap             = "ConfigMap"
	kindSecret                = "Secret"
	kindPersistentVolumeClaim = "PersistentVolumeClaim"
	kindPersistentVolume      = "PersistentVolume"
	kindIngress               = "Ingress"
	kindHTTPRoute             = "HTTPRoute"
	kindService               = "Service"
)

// ownerReferenceStrategy derives edges from Kubernetes ownerReferences. For
// every target object (rule.To) whose ownerReferences include an owner matching
// rule.From, an edge owner -> target is produced. The owner's UID from the
// ownerReference gives a stable identity even if the owner is not in the index.
type ownerReferenceStrategy struct{}

func (ownerReferenceStrategy) Derive(rule gamerav1alpha1.RelationshipRule, index Index) ([]graph.Relationship, error) {
	var edges []graph.Relationship
	targets := index.ByKind(selectorGVK(rule.To))
	for _, target := range targets {
		for _, owner := range target.GetOwnerReferences() {
			ownerGroup := schema.FromAPIVersionAndKind(owner.APIVersion, owner.Kind).Group
			if !matchesSelectorKind(rule.From, ownerGroup, owner.Kind) {
				continue
			}
			edges = append(edges, graph.Relationship{
				Type: rule.Type,
				From: graph.Ref{
					APIVersion: owner.APIVersion,
					Kind:       owner.Kind,
					Namespace:  target.GetNamespace(), // owners are namespace-local
					Name:       owner.Name,
					UID:        string(owner.UID),
				},
				To: refOf(target),
				Properties: map[string]any{
					"controller": owner.Controller != nil && *owner.Controller,
				},
			})
		}
	}
	return edges, nil
}

// labelSelectorStrategy derives edges by evaluating a selector defined on the
// source object (rule.From) against the labels of target objects (rule.To) in
// the same namespace. It supports both the Service-style plain selector map
// (.spec.selector as map[string]string) and the workload-style LabelSelector
// (.spec.selector with matchLabels/matchExpressions).
type labelSelectorStrategy struct{}

func (labelSelectorStrategy) Derive(rule gamerav1alpha1.RelationshipRule, index Index) ([]graph.Relationship, error) {
	var edges []graph.Relationship
	sources := index.ByKind(selectorGVK(rule.From))
	targets := index.ByKind(selectorGVK(rule.To))

	for _, source := range sources {
		selector, ok, err := extractSelector(source)
		if err != nil {
			return nil, err
		}
		if !ok || selector.Empty() {
			// An empty or absent selector matches nothing (avoids fanning out to
			// every pod in the namespace).
			continue
		}
		for _, target := range targets {
			if target.GetNamespace() != source.GetNamespace() {
				continue
			}
			if selector.Matches(labels.Set(target.GetLabels())) {
				edges = append(edges, graph.Relationship{
					Type: rule.Type,
					From: refOf(source),
					To:   refOf(target),
				})
			}
		}
	}
	return edges, nil
}

// extractSelector reads .spec.selector from an object and converts it to a
// labels.Selector. It returns ok=false when no selector is present.
func extractSelector(obj *unstructured.Unstructured) (labels.Selector, bool, error) {
	raw, found, err := unstructured.NestedMap(obj.Object, "spec", "selector")
	if err != nil || !found || len(raw) == 0 {
		return nil, false, err
	}

	// Workload-style: spec.selector is a metav1.LabelSelector.
	if _, hasMatchLabels := raw["matchLabels"]; hasMatchLabels {
		return labelSelectorFromMap(raw)
	}
	if _, hasMatchExpr := raw["matchExpressions"]; hasMatchExpr {
		return labelSelectorFromMap(raw)
	}

	// Service-style: spec.selector is a plain map[string]string.
	set := labels.Set{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			set[k] = s
		}
	}
	return set.AsSelector(), len(set) > 0, nil
}

func labelSelectorFromMap(raw map[string]any) (labels.Selector, bool, error) {
	ls := &metav1.LabelSelector{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, ls); err != nil {
		return nil, false, err
	}
	sel, err := metav1.LabelSelectorAsSelector(ls)
	if err != nil {
		return nil, false, err
	}
	return sel, true, nil
}

// volumeMountStrategy derives edges from a Pod (rule.To) back to the
// ConfigMaps or Secrets (rule.From) it consumes via volumes, envFrom or env
// valueFrom references. The edge direction is source(config) -> target(pod).
type volumeMountStrategy struct{}

func (volumeMountStrategy) Derive(rule gamerav1alpha1.RelationshipRule, index Index) ([]graph.Relationship, error) {
	var edges []graph.Relationship
	pods := index.ByKind(selectorGVK(rule.To))
	// rule.From.Kind is "ConfigMap", "Secret" or "PersistentVolumeClaim".
	wantKind := rule.From.Kind

	for _, pod := range pods {
		seen := map[string]bool{}
		for _, name := range referencedConfigNames(pod, wantKind) {
			if seen[name] {
				continue
			}
			seen[name] = true

			ref := graph.Ref{APIVersion: "v1", Kind: wantKind, Namespace: pod.GetNamespace(), Name: name}
			// Enrich with the real UID when the source object is in the index.
			if src, ok := index.Lookup("v1", wantKind, pod.GetNamespace(), name); ok {
				ref = refOf(src)
			}
			edges = append(edges, graph.Relationship{
				Type: rule.Type,
				From: ref,
				To:   refOf(pod),
			})
		}
	}
	return edges, nil
}

// referencedConfigNames returns the names of ConfigMaps, Secrets or
// PersistentVolumeClaims referenced by a pod spec, depending on wantKind.
func referencedConfigNames(pod *unstructured.Unstructured, wantKind string) []string {
	var names []string

	volumes, _, _ := unstructured.NestedSlice(pod.Object, "spec", "volumes")
	for _, v := range volumes {
		vol, ok := v.(map[string]any)
		if !ok {
			continue
		}
		switch wantKind {
		case kindConfigMap:
			if n, ok, _ := unstructured.NestedString(vol, "configMap", "name"); ok && n != "" {
				names = append(names, n)
			}
			// projected volumes
			names = append(names, projectedNames(vol, "configMap")...)
		case kindSecret:
			if n, ok, _ := unstructured.NestedString(vol, "secret", "secretName"); ok && n != "" {
				names = append(names, n)
			}
			names = append(names, projectedNames(vol, "secret")...)
		case kindPersistentVolumeClaim:
			if n, ok, _ := unstructured.NestedString(vol, "persistentVolumeClaim", "claimName"); ok && n != "" {
				names = append(names, n)
			}
		}
	}

	// PVCs are only referenced via pod volumes, not container env, so skip the
	// container scan for that kind.
	if wantKind == kindPersistentVolumeClaim {
		return names
	}

	for _, path := range [][]string{{"spec", "containers"}, {"spec", "initContainers"}, {"spec", "ephemeralContainers"}} {
		containers, _, _ := unstructured.NestedSlice(pod.Object, path...)
		for _, c := range containers {
			container, ok := c.(map[string]any)
			if !ok {
				continue
			}
			names = append(names, containerConfigNames(container, wantKind)...)
		}
	}
	return names
}

// projectedNames extracts names from a projected volume's sources.
func projectedNames(vol map[string]any, sourceKey string) []string {
	var names []string
	sources, _, _ := unstructured.NestedSlice(vol, "projected", "sources")
	for _, s := range sources {
		src, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if n, ok, _ := unstructured.NestedString(src, sourceKey, "name"); ok && n != "" {
			names = append(names, n)
		}
	}
	return names
}

// containerConfigNames extracts config references from a single container.
func containerConfigNames(container map[string]any, wantKind string) []string {
	var names []string

	// envFrom[].configMapRef / .secretRef
	envFrom, _, _ := unstructured.NestedSlice(container, "envFrom")
	for _, e := range envFrom {
		ef, ok := e.(map[string]any)
		if !ok {
			continue
		}
		switch wantKind {
		case kindConfigMap:
			if n, ok, _ := unstructured.NestedString(ef, "configMapRef", "name"); ok && n != "" {
				names = append(names, n)
			}
		case kindSecret:
			if n, ok, _ := unstructured.NestedString(ef, "secretRef", "name"); ok && n != "" {
				names = append(names, n)
			}
		}
	}

	// env[].valueFrom.configMapKeyRef / .secretKeyRef
	env, _, _ := unstructured.NestedSlice(container, "env")
	for _, e := range env {
		ev, ok := e.(map[string]any)
		if !ok {
			continue
		}
		switch wantKind {
		case kindConfigMap:
			if n, ok, _ := unstructured.NestedString(ev, "valueFrom", "configMapKeyRef", "name"); ok && n != "" {
				names = append(names, n)
			}
		case kindSecret:
			if n, ok, _ := unstructured.NestedString(ev, "valueFrom", "secretKeyRef", "name"); ok && n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

// claimRefStrategy derives edges between a PersistentVolume and the
// PersistentVolumeClaim it is bound to. The binding is discovered from the
// PersistentVolume's spec.claimRef (a namespaced reference to the PVC). The edge
// is oriented according to the rule's From/To kinds, so a projection can model
// either PV -> PVC or PVC -> PV.
type claimRefStrategy struct{}

func (claimRefStrategy) Derive(rule gamerav1alpha1.RelationshipRule, index Index) ([]graph.Relationship, error) {
	pvs := index.ByKind(schema.GroupVersionKind{Version: "v1", Kind: kindPersistentVolume})
	fromIsPV := rule.From.Kind == kindPersistentVolume

	var edges []graph.Relationship
	for _, pv := range pvs {
		ns, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "namespace")
		name, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "name")
		if name == "" {
			continue
		}

		pvcRef := graph.Ref{APIVersion: "v1", Kind: kindPersistentVolumeClaim, Namespace: ns, Name: name}
		if pvc, ok := index.Lookup("v1", kindPersistentVolumeClaim, ns, name); ok {
			pvcRef = refOf(pvc)
		}
		pvRef := refOf(pv)

		from, to := pvRef, pvcRef
		if !fromIsPV {
			from, to = pvcRef, pvRef
		}
		edges = append(edges, graph.Relationship{Type: rule.Type, From: from, To: to})
	}
	return edges, nil
}

// serviceBackendStrategy derives edges from a traffic-routing resource
// (rule.From: Ingress or HTTPRoute) to the Services (rule.To) it forwards
// traffic to. References are read from the routing object's spec and resolved to
// Services in the same namespace (unless the backendRef names another).
type serviceBackendStrategy struct{}

func (serviceBackendStrategy) Derive(rule gamerav1alpha1.RelationshipRule, index Index) ([]graph.Relationship, error) {
	sources := index.ByKind(selectorGVK(rule.From))

	var edges []graph.Relationship
	for _, src := range sources {
		seen := map[string]bool{}
		for _, b := range backendServices(src, rule.From.Kind) {
			key := b.namespace + "/" + b.name
			if b.name == "" || seen[key] {
				continue
			}
			seen[key] = true

			ref := graph.Ref{APIVersion: "v1", Kind: kindService, Namespace: b.namespace, Name: b.name}
			if svc, ok := index.Lookup("v1", kindService, b.namespace, b.name); ok {
				ref = refOf(svc)
			}
			edges = append(edges, graph.Relationship{Type: rule.Type, From: refOf(src), To: ref})
		}
	}
	return edges, nil
}

// serviceBackend is a resolved Service reference (namespace + name).
type serviceBackend struct {
	namespace string
	name      string
}

// backendServices extracts the Service backends referenced by an Ingress or
// HTTPRoute object.
func backendServices(obj *unstructured.Unstructured, kind string) []serviceBackend {
	switch kind {
	case kindIngress:
		return ingressBackendServices(obj)
	case kindHTTPRoute:
		return httpRouteBackendServices(obj)
	default:
		return nil
	}
}

// ingressBackendServices reads spec.defaultBackend and
// spec.rules[].http.paths[].backend from a networking.k8s.io Ingress.
func ingressBackendServices(obj *unstructured.Unstructured) []serviceBackend {
	ns := obj.GetNamespace()
	var out []serviceBackend

	if n, ok, _ := unstructured.NestedString(obj.Object, "spec", "defaultBackend", "service", "name"); ok && n != "" {
		out = append(out, serviceBackend{namespace: ns, name: n})
	}

	rules, _, _ := unstructured.NestedSlice(obj.Object, "spec", "rules")
	for _, r := range rules {
		rule, ok := r.(map[string]any)
		if !ok {
			continue
		}
		paths, _, _ := unstructured.NestedSlice(rule, "http", "paths")
		for _, p := range paths {
			path, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if n, ok, _ := unstructured.NestedString(path, "backend", "service", "name"); ok && n != "" {
				out = append(out, serviceBackend{namespace: ns, name: n})
			}
		}
	}
	return out
}

// httpRouteBackendServices reads spec.rules[].backendRefs[] from a Gateway API
// HTTPRoute, keeping only Service backends (the default kind).
func httpRouteBackendServices(obj *unstructured.Unstructured) []serviceBackend {
	routeNS := obj.GetNamespace()
	var out []serviceBackend

	rules, _, _ := unstructured.NestedSlice(obj.Object, "spec", "rules")
	for _, r := range rules {
		rule, ok := r.(map[string]any)
		if !ok {
			continue
		}
		refs, _, _ := unstructured.NestedSlice(rule, "backendRefs")
		for _, br := range refs {
			ref, ok := br.(map[string]any)
			if !ok {
				continue
			}
			// backendRef kind defaults to Service; group defaults to core ("").
			if k, ok, _ := unstructured.NestedString(ref, "kind"); ok && k != "" && k != kindService {
				continue
			}
			if g, ok, _ := unstructured.NestedString(ref, "group"); ok && g != "" {
				continue
			}
			name, _, _ := unstructured.NestedString(ref, "name")
			if name == "" {
				continue
			}
			ns := routeNS
			if n, ok, _ := unstructured.NestedString(ref, "namespace"); ok && n != "" {
				ns = n
			}
			out = append(out, serviceBackend{namespace: ns, name: name})
		}
	}
	return out
}
