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
