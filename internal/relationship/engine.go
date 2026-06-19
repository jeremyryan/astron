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

// Package relationship derives graph edges between Kubernetes resources from
// the relationship rules declared on a GraphProjection.
package relationship

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
)

// Index provides read access to the set of resources currently in scope for a
// projection. Implementations are typically backed by informer caches.
type Index interface {
	// ByKind returns all objects matching the given group/version/kind. An empty
	// version matches any version of the group/kind.
	ByKind(gvk schema.GroupVersionKind) []*unstructured.Unstructured

	// Lookup returns a single object by identity, if present.
	Lookup(apiVersion, kind, namespace, name string) (*unstructured.Unstructured, bool)
}

// Strategy derives relationships for a single rule against the index.
type Strategy interface {
	// Derive returns the edges produced by applying the rule to the index.
	Derive(rule gamerav1alpha1.RelationshipRule, index Index) ([]graph.Relationship, error)
}

// Engine applies a projection's relationship rules to an Index to produce the
// full set of edges that should exist in the graph.
type Engine struct {
	strategies map[gamerav1alpha1.RelationshipStrategy]Strategy
}

// NewEngine constructs an Engine with the built-in strategies registered.
func NewEngine() *Engine {
	return &Engine{
		strategies: map[gamerav1alpha1.RelationshipStrategy]Strategy{
			gamerav1alpha1.OwnerReferenceStrategy: ownerReferenceStrategy{},
			gamerav1alpha1.LabelSelectorStrategy:  labelSelectorStrategy{},
			gamerav1alpha1.VolumeMountStrategy:    volumeMountStrategy{},
			gamerav1alpha1.ClaimRefStrategy:       claimRefStrategy{},
			gamerav1alpha1.ServiceBackendStrategy: serviceBackendStrategy{},
			gamerav1alpha1.GatewayParentStrategy:  parentRefStrategy{},
		},
	}
}

// Register adds or overrides a strategy. Used to plug in Custom strategies.
func (e *Engine) Register(name gamerav1alpha1.RelationshipStrategy, s Strategy) {
	e.strategies[name] = s
}

// Derive applies all rules and returns the aggregated set of relationships.
// Errors from individual rules are collected and returned together with the
// edges that were derived successfully, so a single bad rule does not abort the
// whole projection.
func (e *Engine) Derive(rules []gamerav1alpha1.RelationshipRule, index Index) ([]graph.Relationship, error) {
	var (
		edges []graph.Relationship
		errs  []error
	)
	for _, rule := range rules {
		strategy, ok := e.strategies[rule.Strategy]
		if !ok {
			errs = append(errs, fmt.Errorf("rule %q: unsupported strategy %q", rule.Name, rule.Strategy))
			continue
		}
		ruleEdges, err := strategy.Derive(rule, index)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %q: %w", rule.Name, err))
			continue
		}
		edges = append(edges, ruleEdges...)
	}
	return edges, errors.Join(errs...)
}

// refOf builds a graph.Ref from an unstructured object.
func refOf(obj *unstructured.Unstructured) graph.Ref {
	return graph.Ref{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		UID:        string(obj.GetUID()),
	}
}

// selectorGVK resolves a ResourceSelector into a GroupVersionKind.
func selectorGVK(sel gamerav1alpha1.ResourceSelector) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: sel.Group, Version: sel.Version, Kind: sel.Kind}
}

// matchesSelector reports whether an owner reference / object identity matches
// the kind (and group, when specified) of a ResourceSelector.
func matchesSelectorKind(sel gamerav1alpha1.ResourceSelector, group, kind string) bool {
	if sel.Kind != kind {
		return false
	}
	if sel.Group != "" && sel.Group != group {
		return false
	}
	return true
}
