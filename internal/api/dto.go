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
	"encoding/json"
	"time"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
	"github.com/project-astron/astron/internal/agent"
	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/projector"
)

// projectionDTO is the API representation of a GraphProjection summary.
type projectionDTO struct {
	UID               string `json:"uid"`
	Namespace         string `json:"namespace"`
	Name              string `json:"name"`
	Phase             string `json:"phase,omitempty"`
	NodeCount         int64  `json:"nodeCount"`
	RelationshipCount int64  `json:"relationshipCount"`
	// ChatEnabled reports whether the projection has GraphRAG configured with a
	// chat provider, i.e. whether the /rag/answer and /rag/query endpoints can
	// serve natural-language questions for it.
	ChatEnabled bool `json:"chatEnabled,omitempty"`
}

// projectionToDTO converts a GraphProjection to its API summary. hasProviderChats
// reports whether the controller has any controller-wide chat providers, which
// enable chat for any GraphRAG-embedding projection even without a
// per-projection chat model.
func projectionToDTO(p astronv1alpha1.GraphProjection, hasProviderChats bool) projectionDTO {
	rag := p.Spec.GraphRAG
	ragEnabled := rag != nil && rag.Enabled
	perProjectionChat := ragEnabled && rag.Chat != nil && rag.Chat.Enabled
	// Answering needs retrieval (embeddings), so chat requires GraphRAG to be
	// enabled; the chat model may come from the projection or a controller-wide
	// provider.
	chatEnabled := ragEnabled && (perProjectionChat || hasProviderChats)
	return projectionDTO{
		UID:               string(p.UID),
		Namespace:         p.Namespace,
		Name:              p.Name,
		Phase:             p.Status.Phase,
		NodeCount:         p.Status.NodeCount,
		RelationshipCount: p.Status.RelationshipCount,
		ChatEnabled:       chatEnabled,
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
	// Manual is true for user-created links (addable/removable from the UI).
	Manual bool `json:"manual,omitempty"`
}

// graphDTO is the API representation of a projection's full graph.
type graphDTO struct {
	Nodes []nodeDTO `json:"nodes"`
	Edges []edgeDTO `json:"edges"`
}

// seedDTO is a retrieval entry point: a node and its selection score.
type seedDTO struct {
	ID    string  `json:"id"`
	Kind  string  `json:"kind"`
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

// cardDTO is the natural-language description of a node in a retrieval result.
type cardDTO struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Text      string `json:"text"`
}

// retrievalDTO is the API representation of an assembled GraphRAG context: the
// seed nodes, their cards, and the connecting subgraph.
type retrievalDTO struct {
	Query    string    `json:"query"`
	Seeds    []seedDTO `json:"seeds"`
	Cards    []cardDTO `json:"cards"`
	Subgraph graphDTO  `json:"subgraph"`
}

// schemaDTO is the API representation of a projection's graph schema summary.
type schemaDTO struct {
	Schema string `json:"schema"`
}

// answerDTO is the API representation of a RAG answer: the generated answer
// plus the retrieval context that grounded it.
type answerDTO struct {
	Question  string       `json:"question"`
	Answer    string       `json:"answer"`
	Retrieval retrievalDTO `json:"retrieval"`
}

func answerToDTO(a projector.AnswerResult) answerDTO {
	return answerDTO{
		Question:  a.Question,
		Answer:    a.Answer,
		Retrieval: retrievalToDTO(a.Retrieval),
	}
}

// agentStepDTO is one tool call the chat agent made while answering a
// question, for transparency: which tool, with what arguments, and a short
// rendering of its result.
type agentStepDTO struct {
	Tool    string          `json:"tool"`
	Args    json.RawMessage `json:"args,omitempty"`
	Summary string          `json:"summary"`
}

// agentAnswerDTO is the API representation of a tool-using chat agent's
// answer (see agent.Result).
type agentAnswerDTO struct {
	Question string         `json:"question"`
	Answer   string         `json:"answer"`
	Steps    []agentStepDTO `json:"steps"`
	// Agentic reports whether the tool-using loop actually ran; false means the
	// resolved chat backend didn't support tool calling and the fixed Answer
	// pipeline was used instead (see Projector.AnswerWithTools).
	Agentic bool `json:"agentic"`
	// StepBudgetExhausted reports whether the run hit its tool-call budget
	// before the model volunteered a final answer.
	StepBudgetExhausted bool `json:"stepBudgetExhausted,omitempty"`
}

func agentResultToDTO(question string, res agent.Result) agentAnswerDTO {
	steps := make([]agentStepDTO, 0, len(res.Steps))
	for _, step := range res.Steps {
		steps = append(steps, agentStepDTO{Tool: step.Tool, Args: step.Args, Summary: step.Summary})
	}
	return agentAnswerDTO{
		Question:            question,
		Answer:              res.Answer,
		Steps:               steps,
		Agentic:             res.Agentic,
		StepBudgetExhausted: res.StepBudgetExhausted,
	}
}

func retrievalToDTO(r projector.Retrieval) retrievalDTO {
	out := retrievalDTO{
		Query:    r.Query,
		Seeds:    make([]seedDTO, 0, len(r.Seeds)),
		Cards:    make([]cardDTO, 0, len(r.Cards)),
		Subgraph: graphToDTO(r.Subgraph),
	}
	for _, s := range r.Seeds {
		out.Seeds = append(out.Seeds, seedDTO{
			ID:    s.Ref.ID(),
			Kind:  s.Ref.Kind,
			Name:  s.Ref.Name,
			Score: s.Score,
		})
	}
	for _, c := range r.Cards {
		out.Cards = append(out.Cards, cardDTO{
			ID:        c.Ref.ID(),
			Kind:      c.Ref.Kind,
			Namespace: c.Ref.Namespace,
			Name:      c.Ref.Name,
			Text:      c.Text,
		})
	}
	return out
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
			Manual:     r.Manual,
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
	KindMode         string           `json:"kindMode,omitempty"`
	HiddenKinds      []string         `json:"hiddenKinds,omitempty"`
	VisibleKinds     []string         `json:"visibleKinds,omitempty"`
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

func viewToDTO(v *astronv1alpha1.GraphView) viewDTO {
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
			KindMode:         f.KindMode,
			HiddenKinds:      f.HiddenKinds,
			VisibleKinds:     f.VisibleKinds,
			HiddenNamespaces: f.HiddenNamespaces,
			LabelFilters:     labels,
			LabelMode:        f.LabelMode,
			MaxDistance:      f.MaxDistance,
			GroupByNamespace: f.GroupByNamespace,
		},
	}
}

// dtoToViewSpec builds a GraphViewSpec from the API request representation.
func dtoToViewSpec(in viewDTO) astronv1alpha1.GraphViewSpec {
	labels := make([]astronv1alpha1.LabelFilter, 0, len(in.Filters.LabelFilters))
	for _, lf := range in.Filters.LabelFilters {
		labels = append(labels, astronv1alpha1.LabelFilter{Key: lf.Key, Value: lf.Value})
	}
	return astronv1alpha1.GraphViewSpec{
		ProjectionRef: astronv1alpha1.ProjectionReference{
			Name:      in.ProjectionRef.Name,
			Namespace: in.ProjectionRef.Namespace,
		},
		DisplayName: in.DisplayName,
		Description: in.Description,
		Filters: astronv1alpha1.GraphViewFilters{
			KindMode:         in.Filters.KindMode,
			HiddenKinds:      in.Filters.HiddenKinds,
			VisibleKinds:     in.Filters.VisibleKinds,
			HiddenNamespaces: in.Filters.HiddenNamespaces,
			LabelFilters:     labels,
			LabelMode:        in.Filters.LabelMode,
			MaxDistance:      in.Filters.MaxDistance,
			GroupByNamespace: in.Filters.GroupByNamespace,
		},
	}
}

// providerDTO is the JSON shape of one controller-wide model provider (an
// embedding or chat provider configured on the controller and shared by every
// projection). Only non-sensitive descriptive fields are exposed.
type providerDTO struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
}

// providersDTO is the JSON shape of the controller-wide providers listing.
type providersDTO struct {
	EmbeddingProviders []providerDTO `json:"embeddingProviders"`
	ChatProviders      []providerDTO `json:"chatProviders"`
}

// snapshotDTO is the JSON shape of a snapshot's metadata.
type snapshotDTO struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	CreatedAt         string `json:"createdAt"`
	NodeCount         int64  `json:"nodeCount"`
	RelationshipCount int64  `json:"relationshipCount"`
}

// toSnapshotDTO converts a graph.SnapshotInfo to its JSON shape.
func toSnapshotDTO(in graph.SnapshotInfo) snapshotDTO {
	return snapshotDTO{
		ID:                in.ID,
		Name:              in.Name,
		CreatedAt:         in.CreatedAt.UTC().Format(time.RFC3339),
		NodeCount:         in.Nodes,
		RelationshipCount: in.Relationships,
	}
}
