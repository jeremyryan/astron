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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

// MapIndex is a simple in-memory Index. It is suitable for building a snapshot
// of the resources in scope (e.g. from informer caches) before deriving edges.
type MapIndex struct {
	// byKind maps "<group>/<kind>" to the objects of that kind. Version is not
	// part of the key so lookups tolerate a relationship rule that omits the
	// version.
	byKind map[string][]*unstructured.Unstructured
	// byID maps a "<kind>/<namespace>/<name>" identity to an object.
	byID map[string]*unstructured.Unstructured
	// scopeByKind maps a bare Kind name to its GroupVersionKind, from the
	// projection's configured scope.resources (set via WithScope). Backs
	// ResolveKind independent of whether any instance of the kind has been
	// observed yet.
	scopeByKind map[string]schema.GroupVersionKind
	// namespaced answers Namespaced queries, when set via WithNamespaceResolver.
	namespaced func(schema.GroupVersionKind) (bool, bool)
}

// NewMapIndex builds a MapIndex from the given objects.
func NewMapIndex(objs ...*unstructured.Unstructured) *MapIndex {
	idx := &MapIndex{
		byKind: map[string][]*unstructured.Unstructured{},
		byID:   map[string]*unstructured.Unstructured{},
	}
	for _, o := range objs {
		idx.Add(o)
	}
	return idx
}

// Add inserts an object into the index.
func (m *MapIndex) Add(obj *unstructured.Unstructured) {
	gvk := obj.GroupVersionKind()
	kindKey := groupKindKey(gvk.Group, gvk.Kind)
	m.byKind[kindKey] = append(m.byKind[kindKey], obj)
	m.byID[idKey(gvk.Kind, obj.GetNamespace(), obj.GetName())] = obj
}

// ByKind implements Index. An empty version in the requested GVK matches any
// version of the group/kind.
func (m *MapIndex) ByKind(gvk schema.GroupVersionKind) []*unstructured.Unstructured {
	candidates := m.byKind[groupKindKey(gvk.Group, gvk.Kind)]
	if gvk.Version == "" {
		return candidates
	}
	var out []*unstructured.Unstructured
	for _, c := range candidates {
		if c.GroupVersionKind().Version == gvk.Version {
			out = append(out, c)
		}
	}
	return out
}

// Lookup implements Index. The apiVersion is currently ignored; identity is
// keyed by kind, namespace and name.
func (m *MapIndex) Lookup(apiVersion, kind, namespace, name string) (*unstructured.Unstructured, bool) {
	_ = apiVersion
	obj, ok := m.byID[idKey(kind, namespace, name)]
	return obj, ok
}

// WithScope configures the Kind name resolution used by ResolveKind, from the
// projection's own scope.resources. It returns the receiver so it can be
// chained with NewMapIndex.
func (m *MapIndex) WithScope(resources []astronv1alpha1.ResourceSelector) *MapIndex {
	m.scopeByKind = make(map[string]schema.GroupVersionKind, len(resources))
	for _, r := range resources {
		m.scopeByKind[r.Kind] = schema.GroupVersionKind{Group: r.Group, Version: r.Version, Kind: r.Kind}
	}
	return m
}

// ResolveKind implements Index.
func (m *MapIndex) ResolveKind(kind string) (schema.GroupVersionKind, bool) {
	gvk, ok := m.scopeByKind[kind]
	return gvk, ok
}

// WithNamespaceResolver configures the function used to answer Namespaced
// queries (typically backed by a REST mapper). It returns the receiver so it
// can be chained with NewMapIndex.
func (m *MapIndex) WithNamespaceResolver(f func(schema.GroupVersionKind) (bool, bool)) *MapIndex {
	m.namespaced = f
	return m
}

// Namespaced implements Index. When no resolver has been configured, it
// reports unknown (false, false) rather than guessing.
func (m *MapIndex) Namespaced(gvk schema.GroupVersionKind) (bool, bool) {
	if m.namespaced == nil {
		return false, false
	}
	return m.namespaced(gvk)
}

func groupKindKey(group, kind string) string {
	return group + "/" + kind
}

func idKey(kind, namespace, name string) string {
	return kind + "/" + namespace + "/" + name
}
