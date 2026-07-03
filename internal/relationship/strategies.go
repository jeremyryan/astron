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
	"encoding/json"
	"maps"
	"sort"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
)

// jsonString marshals a value to a compact JSON string, returning "" on error.
// Used to store map-shaped relationship data (e.g. a selector) as an edge
// property, since graph stores only accept primitive/array property values.
func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// sortedUniqueStrings returns the distinct non-empty values of in, sorted.
func sortedUniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

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
	kindServiceAccount        = "ServiceAccount"
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
		selector, raw, ok, err := extractSelector(source)
		if err != nil {
			return nil, err
		}
		if !ok || selector.Empty() {
			// An empty or absent selector matches nothing (avoids fanning out to
			// every pod in the namespace).
			continue
		}
		// The selector that forms the relationship is recorded on each edge. The
		// raw .spec.selector is stored as JSON (e.g. {"app":"web"} for a Service,
		// or {"matchLabels":{...}} for a workload), plus its canonical string form.
		props := map[string]any{"selector": selector.String()}
		if js := jsonString(raw); js != "" {
			props["selectorLabels"] = js
		}
		for _, target := range targets {
			if target.GetNamespace() != source.GetNamespace() {
				continue
			}
			if selector.Matches(labels.Set(target.GetLabels())) {
				edges = append(edges, graph.Relationship{
					Type:       rule.Type,
					From:       refOf(source),
					To:         refOf(target),
					Properties: cloneProps(props),
				})
			}
		}
	}
	return edges, nil
}

// cloneProps returns a shallow copy of a property map so each edge owns its own
// map (the graph store may mutate per-edge property maps).
func cloneProps(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}

// extractSelector reads .spec.selector from an object and converts it to a
// labels.Selector. It also returns the raw selector map so callers can record
// the selecting data on the edge. It returns ok=false when no selector is
// present.
func extractSelector(obj *unstructured.Unstructured) (labels.Selector, map[string]any, bool, error) {
	raw, found, err := unstructured.NestedMap(obj.Object, "spec", "selector")
	if err != nil || !found || len(raw) == 0 {
		return nil, nil, false, err
	}

	// Workload-style: spec.selector is a metav1.LabelSelector.
	if _, hasMatchLabels := raw["matchLabels"]; hasMatchLabels {
		sel, ok, err := labelSelectorFromMap(raw)
		return sel, raw, ok, err
	}
	if _, hasMatchExpr := raw["matchExpressions"]; hasMatchExpr {
		sel, ok, err := labelSelectorFromMap(raw)
		return sel, raw, ok, err
	}

	// Service-style: spec.selector is a plain map[string]string.
	set := labels.Set{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			set[k] = s
		}
	}
	return set.AsSelector(), raw, len(set) > 0, nil
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
	pods := index.ByKind(selectorGVK(rule.To))
	edges := make([]graph.Relationship, 0, len(pods))
	// rule.From.Kind is "ConfigMap", "Secret" or "PersistentVolumeClaim".
	wantKind := rule.From.Kind

	for _, pod := range pods {
		// Group references by source name, collecting the mechanisms ("via") each
		// name is referenced through so a single edge records all of them.
		via := map[string][]string{}
		var order []string
		for _, r := range referencedConfigRefs(pod, wantKind) {
			if _, seen := via[r.name]; !seen {
				order = append(order, r.name)
			}
			via[r.name] = append(via[r.name], r.via)
		}

		for _, name := range order {
			ref := graph.Ref{APIVersion: "v1", Kind: wantKind, Namespace: pod.GetNamespace(), Name: name}
			// Enrich with the real UID when the source object is in the index.
			if src, ok := index.Lookup("v1", wantKind, pod.GetNamespace(), name); ok {
				ref = refOf(src)
			}
			edges = append(edges, graph.Relationship{
				Type:       rule.Type,
				From:       ref,
				To:         refOf(pod),
				Properties: map[string]any{"via": sortedUniqueStrings(via[name])},
			})
		}
	}
	return edges, nil
}

// configRef is a single reference from a pod to a ConfigMap/Secret/PVC, together
// with the mechanism ("via") it is referenced through.
type configRef struct {
	name string
	via  string
}

// referencedConfigRefs returns the ConfigMaps, Secrets or PersistentVolumeClaims
// referenced by a pod spec (depending on wantKind), each tagged with how it is
// referenced: "volume", "projected", "envFrom" or "env".
func referencedConfigRefs(pod *unstructured.Unstructured, wantKind string) []configRef {
	var refs []configRef

	volumes, _, _ := unstructured.NestedSlice(pod.Object, "spec", "volumes")
	for _, v := range volumes {
		vol, ok := v.(map[string]any)
		if !ok {
			continue
		}
		switch wantKind {
		case kindConfigMap:
			if n, ok, _ := unstructured.NestedString(vol, "configMap", "name"); ok && n != "" {
				refs = append(refs, configRef{n, "volume"})
			}
			for _, n := range projectedNames(vol, "configMap") {
				refs = append(refs, configRef{n, "projected"})
			}
		case kindSecret:
			if n, ok, _ := unstructured.NestedString(vol, "secret", "secretName"); ok && n != "" {
				refs = append(refs, configRef{n, "volume"})
			}
			for _, n := range projectedNames(vol, "secret") {
				refs = append(refs, configRef{n, "projected"})
			}
		case kindPersistentVolumeClaim:
			if n, ok, _ := unstructured.NestedString(vol, "persistentVolumeClaim", "claimName"); ok && n != "" {
				refs = append(refs, configRef{n, "volume"})
			}
		}
	}

	// PVCs are only referenced via pod volumes, not container env, so skip the
	// container scan for that kind.
	if wantKind == kindPersistentVolumeClaim {
		return refs
	}

	for _, path := range [][]string{{"spec", "containers"}, {"spec", "initContainers"}, {"spec", "ephemeralContainers"}} {
		containers, _, _ := unstructured.NestedSlice(pod.Object, path...)
		for _, c := range containers {
			container, ok := c.(map[string]any)
			if !ok {
				continue
			}
			refs = append(refs, containerConfigRefs(container, wantKind)...)
		}
	}
	return refs
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

// containerConfigRefs extracts config references from a single container,
// tagged with the mechanism ("envFrom" or "env").
func containerConfigRefs(container map[string]any, wantKind string) []configRef {
	var refs []configRef

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
				refs = append(refs, configRef{n, "envFrom"})
			}
		case kindSecret:
			if n, ok, _ := unstructured.NestedString(ef, "secretRef", "name"); ok && n != "" {
				refs = append(refs, configRef{n, "envFrom"})
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
				refs = append(refs, configRef{n, "env"})
			}
		case kindSecret:
			if n, ok, _ := unstructured.NestedString(ev, "valueFrom", "secretKeyRef", "name"); ok && n != "" {
				refs = append(refs, configRef{n, "env"})
			}
		}
	}
	return refs
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

// serviceAccountStrategy derives edges from a ServiceAccount (rule.From) to the
// Pods (rule.To) that run under it. A Pod names its ServiceAccount in the scalar
// field spec.serviceAccountName (or the deprecated spec.serviceAccount); when
// neither is set Kubernetes assigns the namespace's "default" ServiceAccount, so
// that is used as the fallback and every Pod links to some ServiceAccount. The
// edge is only persisted when the ServiceAccount is itself captured (its UID is
// needed to match the endpoint), which the index Lookup resolves.
type serviceAccountStrategy struct{}

func (serviceAccountStrategy) Derive(rule gamerav1alpha1.RelationshipRule, index Index) ([]graph.Relationship, error) {
	pods := index.ByKind(selectorGVK(rule.To))
	edges := make([]graph.Relationship, 0, len(pods))
	for _, pod := range pods {
		name := podServiceAccountName(pod)
		if name == "" {
			continue
		}
		ref := graph.Ref{APIVersion: "v1", Kind: kindServiceAccount, Namespace: pod.GetNamespace(), Name: name}
		if sa, ok := index.Lookup("v1", kindServiceAccount, pod.GetNamespace(), name); ok {
			ref = refOf(sa)
		}
		edges = append(edges, graph.Relationship{
			Type: rule.Type,
			From: ref,
			To:   refOf(pod),
		})
	}
	return edges, nil
}

// podServiceAccountName returns the ServiceAccount a Pod runs as, preferring the
// current spec.serviceAccountName over the deprecated spec.serviceAccount, and
// falling back to the implicit "default" ServiceAccount when neither is set.
func podServiceAccountName(pod *unstructured.Unstructured) string {
	if n, ok, _ := unstructured.NestedString(pod.Object, "spec", "serviceAccountName"); ok && n != "" {
		return n
	}
	if n, ok, _ := unstructured.NestedString(pod.Object, "spec", "serviceAccount"); ok && n != "" {
		return n
	}
	return "default"
}

// serviceBackendStrategy derives edges from a traffic-routing resource
// (rule.From: Ingress or HTTPRoute) to the Services (rule.To) it forwards
// traffic to. References are read from the routing object's spec and resolved to
// Services in the same namespace (unless the backendRef names another).
type serviceBackendStrategy struct{}

func (serviceBackendStrategy) Derive(rule gamerav1alpha1.RelationshipRule, index Index) ([]graph.Relationship, error) {
	sources := index.ByKind(selectorGVK(rule.From))
	edges := make([]graph.Relationship, 0, len(sources))
	for _, src := range sources {
		// Aggregate all backends targeting the same Service into a single edge,
		// recording the routing data (hosts/paths/ports) that forms it.
		aggs := map[string]*backendAgg{}
		var order []string
		for _, b := range backendServices(src, rule.From.Kind) {
			if b.name == "" {
				continue
			}
			key := b.namespace + "/" + b.name
			a := aggs[key]
			if a == nil {
				ref := graph.Ref{APIVersion: "v1", Kind: kindService, Namespace: b.namespace, Name: b.name}
				if svc, ok := index.Lookup("v1", kindService, b.namespace, b.name); ok {
					ref = refOf(svc)
				}
				a = &backendAgg{ref: ref}
				aggs[key] = a
				order = append(order, key)
			}
			a.hosts = append(a.hosts, b.host)
			a.paths = append(a.paths, b.path)
			a.ports = append(a.ports, b.port)
		}

		for _, key := range order {
			a := aggs[key]
			props := map[string]any{}
			if hosts := sortedUniqueStrings(a.hosts); len(hosts) > 0 {
				props["hosts"] = hosts
			}
			if paths := sortedUniqueStrings(a.paths); len(paths) > 0 {
				props["paths"] = paths
			}
			if ports := sortedUniqueStrings(a.ports); len(ports) > 0 {
				props["ports"] = ports
			}
			edges = append(edges, graph.Relationship{Type: rule.Type, From: refOf(src), To: a.ref, Properties: props})
		}
	}
	return edges, nil
}

// backendAgg accumulates the routing data for all backends pointing at one
// Service from a single routing object.
type backendAgg struct {
	ref          graph.Ref
	hosts, paths []string
	ports        []string
}

// serviceBackend is a resolved Service reference plus the routing data that
// targets it (host/path/port; host and path are empty for HTTPRoute backends).
type serviceBackend struct {
	namespace string
	name      string
	host      string
	path      string
	port      string
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

	if svc, ok, _ := unstructured.NestedMap(obj.Object, "spec", "defaultBackend", "service"); ok {
		if n, _, _ := unstructured.NestedString(svc, "name"); n != "" {
			out = append(out, serviceBackend{namespace: ns, name: n, port: servicePortString(svc)})
		}
	}

	rules, _, _ := unstructured.NestedSlice(obj.Object, "spec", "rules")
	for _, r := range rules {
		rule, ok := r.(map[string]any)
		if !ok {
			continue
		}
		host, _, _ := unstructured.NestedString(rule, "host")
		paths, _, _ := unstructured.NestedSlice(rule, "http", "paths")
		for _, p := range paths {
			path, ok := p.(map[string]any)
			if !ok {
				continue
			}
			svc, ok, _ := unstructured.NestedMap(path, "backend", "service")
			if !ok {
				continue
			}
			n, _, _ := unstructured.NestedString(svc, "name")
			if n == "" {
				continue
			}
			httpPath, _, _ := unstructured.NestedString(path, "path")
			out = append(out, serviceBackend{
				namespace: ns, name: n, host: host, path: httpPath, port: servicePortString(svc),
			})
		}
	}
	return out
}

// servicePortString renders an Ingress backend service port (a name or a
// number) as a string, or "" when absent.
func servicePortString(svc map[string]any) string {
	if name, _, _ := unstructured.NestedString(svc, "port", "name"); name != "" {
		return name
	}
	return numberString(svc, "port", "number")
}

// numberString reads a numeric field (stored as int64 or float64 in
// unstructured data) and renders it as a string, or "" when absent/zero.
func numberString(m map[string]any, fields ...string) string {
	if i, ok, _ := unstructured.NestedInt64(m, fields...); ok && i != 0 {
		return strconv.FormatInt(i, 10)
	}
	if f, ok, _ := unstructured.NestedFloat64(m, fields...); ok && f != 0 {
		return strconv.FormatInt(int64(f), 10)
	}
	return ""
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
			out = append(out, serviceBackend{namespace: ns, name: name, port: numberString(ref, "port")})
		}
	}
	return out
}
