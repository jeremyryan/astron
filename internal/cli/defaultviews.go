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

package cli

import (
	"fmt"
	"sort"
	"strings"
)

// Resource kinds grouped by the concern each default view focuses on. A default
// view shows its own category (and any uncategorized kinds) by hiding the kinds
// belonging to the other categories.
var (
	computeKinds = []string{
		"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet",
		"Pod", "Job", "CronJob", "HorizontalPodAutoscaler",
	}
	networkingKinds = []string{
		"Service", "Endpoints", "EndpointSlice", "Ingress", "IngressClass",
		"NetworkPolicy", "HTTPRoute", "Gateway", "GatewayClass",
	}
	persistenceKinds = []string{
		"PersistentVolume", "PersistentVolumeClaim", "StorageClass",
		"ConfigMap", "Secret",
	}
)

// defaultViewCategory describes a built-in view: its canonical display name, a
// short description, and the resource kinds it focuses on.
type defaultViewCategory struct {
	displayName string
	description string
	kinds       []string
}

// defaultViewCategories enumerates the built-in views, in presentation order.
var defaultViewCategories = []defaultViewCategory{
	{
		displayName: "Compute",
		description: "Workload and scheduling resources (Deployments, Pods, Jobs, ...).",
		kinds:       computeKinds,
	},
	{
		displayName: "Networking",
		description: "Service and ingress/routing resources (Services, Ingresses, Gateways, ...).",
		kinds:       networkingKinds,
	},
	{
		displayName: "Persistence",
		description: "Storage and configuration resources (PVCs, PVs, ConfigMaps, Secrets, ...).",
		kinds:       persistenceKinds,
	},
}

// defaultViewNames returns the canonical display names of the built-in views.
func defaultViewNames() []string {
	names := make([]string, 0, len(defaultViewCategories))
	for _, c := range defaultViewCategories {
		names = append(names, c.displayName)
	}
	return names
}

// lookupDefaultView resolves a user-supplied view name (case-insensitively) to
// its built-in category definition.
func lookupDefaultView(name string) (defaultViewCategory, bool) {
	for _, c := range defaultViewCategories {
		if strings.EqualFold(c.displayName, name) {
			return c, true
		}
	}
	return defaultViewCategory{}, false
}

// alwaysVisibleKinds are kinds kept visible in every default view because they
// are central to each concern (e.g. Pods are the targets of networking and
// persistence relationships as well as compute workloads).
var alwaysVisibleKinds = map[string]bool{
	"Pod": true,
}

// hiddenKindsFor returns the kinds a view should hide: the union of every other
// category's kinds, excluding always-visible kinds, sorted for deterministic
// output.
func hiddenKindsFor(view defaultViewCategory) []string {
	seen := map[string]bool{}
	var hidden []string
	for _, c := range defaultViewCategories {
		if c.displayName == view.displayName {
			continue
		}
		for _, k := range c.kinds {
			if seen[k] || alwaysVisibleKinds[k] {
				continue
			}
			seen[k] = true
			hidden = append(hidden, k)
		}
	}
	sort.Strings(hidden)
	return hidden
}

// defaultViewResourceName derives a GraphView's resource name from the
// projection and the view (e.g. "web-compute").
func defaultViewResourceName(projection string, view defaultViewCategory) string {
	return fmt.Sprintf("%s-%s", projection, strings.ToLower(view.displayName))
}

// parseViewSelection parses a --views selection into the chosen built-in view
// categories. The special value "defaults" selects all views; otherwise a
// comma-separated list of view names (case-insensitive) is expected. An empty
// string selects no views. Duplicates are removed and unknown names error.
func parseViewSelection(value string) ([]defaultViewCategory, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if strings.EqualFold(value, "defaults") {
		return append([]defaultViewCategory(nil), defaultViewCategories...), nil
	}
	seen := map[string]bool{}
	var out []defaultViewCategory
	for part := range strings.SplitSeq(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cat, ok := lookupDefaultView(part)
		if !ok {
			return nil, fmt.Errorf("unknown view %q: must be 'defaults' or a comma-separated list of %s",
				part, strings.Join(defaultViewNames(), ", "))
		}
		if seen[cat.displayName] {
			continue
		}
		seen[cat.displayName] = true
		out = append(out, cat)
	}
	return out, nil
}

// buildDefaultView constructs the View to create for a built-in view name,
// filtering the given projection. The GraphView's resource name is derived from
// the projection and the view (e.g. "web-compute").
func buildDefaultView(namespace, projection string, view defaultViewCategory) View {
	return View{
		Namespace:   namespace,
		Name:        defaultViewResourceName(projection, view),
		DisplayName: view.displayName,
		Description: view.description,
		ProjectionRef: ViewProjectionRef{
			Name:      projection,
			Namespace: namespace,
		},
		Filters: ViewFilters{
			HiddenKinds: hiddenKindsFor(view),
		},
	}
}
