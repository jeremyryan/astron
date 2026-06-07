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

package api

import (
	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
)

// projectionDTO is the API representation of a GraphProjection summary.
type projectionDTO struct {
	UID               string `json:"uid"`
	Namespace         string `json:"namespace"`
	Name              string `json:"name"`
	Phase             string `json:"phase,omitempty"`
	NodeCount         int64  `json:"nodeCount"`
	RelationshipCount int64  `json:"relationshipCount"`
}

func projectionToDTO(p gamerav1alpha1.GraphProjection) projectionDTO {
	return projectionDTO{
		UID:               string(p.UID),
		Namespace:         p.Namespace,
		Name:              p.Name,
		Phase:             p.Status.Phase,
		NodeCount:         p.Status.NodeCount,
		RelationshipCount: p.Status.RelationshipCount,
	}
}

// nodeDTO is the API representation of a graph node.
type nodeDTO struct {
	ID         string         `json:"id"`
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Namespace  string         `json:"namespace,omitempty"`
	Name       string         `json:"name"`
	Properties map[string]any `json:"properties,omitempty"`
}

// edgeDTO is the API representation of a graph relationship.
type edgeDTO struct {
	ID         string         `json:"id"`
	Source     string         `json:"source"`
	Target     string         `json:"target"`
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
}

// graphDTO is the API representation of a projection's full graph.
type graphDTO struct {
	Nodes []nodeDTO `json:"nodes"`
	Edges []edgeDTO `json:"edges"`
}

func graphToDTO(data graph.GraphData) graphDTO {
	out := graphDTO{
		Nodes: make([]nodeDTO, 0, len(data.Nodes)),
		Edges: make([]edgeDTO, 0, len(data.Relationships)),
	}
	for _, n := range data.Nodes {
		out.Nodes = append(out.Nodes, nodeDTO{
			ID:         n.Ref.ID(),
			APIVersion: n.Ref.APIVersion,
			Kind:       n.Ref.Kind,
			Namespace:  n.Ref.Namespace,
			Name:       n.Ref.Name,
			Properties: n.Properties,
		})
	}
	for _, r := range data.Relationships {
		out.Edges = append(out.Edges, edgeDTO{
			ID:         r.From.ID() + "-" + r.Type + "-" + r.To.ID(),
			Source:     r.From.ID(),
			Target:     r.To.ID(),
			Type:       r.Type,
			Properties: r.Properties,
		})
	}
	return out
}

// projectionRefDTO references a GraphProjection a view applies to.
type projectionRefDTO struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// labelFilterDTO is a single label key/value constraint for a view.
type labelFilterDTO struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// viewFiltersDTO mirrors GraphViewFilters in the API representation.
type viewFiltersDTO struct {
	HiddenKinds      []string         `json:"hiddenKinds,omitempty"`
	HiddenNamespaces []string         `json:"hiddenNamespaces,omitempty"`
	LabelFilters     []labelFilterDTO `json:"labelFilters,omitempty"`
	LabelMode        string           `json:"labelMode,omitempty"`
	MaxDistance      *int32           `json:"maxDistance,omitempty"`
	GroupByNamespace *bool            `json:"groupByNamespace,omitempty"`
}

// viewDTO is the API representation of a GraphView (a saved set of filters).
type viewDTO struct {
	Namespace     string           `json:"namespace"`
	Name          string           `json:"name"`
	UID           string           `json:"uid,omitempty"`
	DisplayName   string           `json:"displayName,omitempty"`
	Description   string           `json:"description,omitempty"`
	ProjectionRef projectionRefDTO `json:"projectionRef"`
	Filters       viewFiltersDTO   `json:"filters"`
}

func viewToDTO(v *gamerav1alpha1.GraphView) viewDTO {
	f := v.Spec.Filters
	labels := make([]labelFilterDTO, 0, len(f.LabelFilters))
	for _, lf := range f.LabelFilters {
		labels = append(labels, labelFilterDTO{Key: lf.Key, Value: lf.Value})
	}
	return viewDTO{
		Namespace:   v.Namespace,
		Name:        v.Name,
		UID:         string(v.UID),
		DisplayName: v.Spec.DisplayName,
		Description: v.Spec.Description,
		ProjectionRef: projectionRefDTO{
			Name:      v.Spec.ProjectionRef.Name,
			Namespace: v.Spec.ProjectionRef.Namespace,
		},
		Filters: viewFiltersDTO{
			HiddenKinds:      f.HiddenKinds,
			HiddenNamespaces: f.HiddenNamespaces,
			LabelFilters:     labels,
			LabelMode:        f.LabelMode,
			MaxDistance:      f.MaxDistance,
			GroupByNamespace: f.GroupByNamespace,
		},
	}
}

// dtoToViewSpec builds a GraphViewSpec from the API request representation.
func dtoToViewSpec(in viewDTO) gamerav1alpha1.GraphViewSpec {
	labels := make([]gamerav1alpha1.LabelFilter, 0, len(in.Filters.LabelFilters))
	for _, lf := range in.Filters.LabelFilters {
		labels = append(labels, gamerav1alpha1.LabelFilter{Key: lf.Key, Value: lf.Value})
	}
	return gamerav1alpha1.GraphViewSpec{
		ProjectionRef: gamerav1alpha1.ProjectionReference{
			Name:      in.ProjectionRef.Name,
			Namespace: in.ProjectionRef.Namespace,
		},
		DisplayName: in.DisplayName,
		Description: in.Description,
		Filters: gamerav1alpha1.GraphViewFilters{
			HiddenKinds:      in.Filters.HiddenKinds,
			HiddenNamespaces: in.Filters.HiddenNamespaces,
			LabelFilters:     labels,
			LabelMode:        in.Filters.LabelMode,
			MaxDistance:      in.Filters.MaxDistance,
			GroupByNamespace: in.Filters.GroupByNamespace,
		},
	}
}
