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

// Package projector implements the resource graph watchers. A Projector starts
// dynamic informers for the resource kinds in a GraphProjection's scope,
// materializes them as nodes in the graph store, and applies the relationship
// engine to materialize edges. Changes trigger a debounced full re-sync.
package projector

import (
	"context"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
	"github.com/project-gamera/gamera/internal/rag"
	"github.com/project-gamera/gamera/internal/relationship"
)

// defaultEmbeddingBatchSize bounds how many cards are sent to the embedding
// provider in a single request when refreshing embeddings.
const defaultEmbeddingBatchSize = 64

// defaultVectorSimilarity is the similarity function used for the vector index
// when none is configured.
const defaultVectorSimilarity = "cosine"

// debounceWindow is how long the projector waits to coalesce a burst of change
// events before performing a full re-sync.
const debounceWindow = 1 * time.Second

// Options configures a Projector.
type Options struct {
	// ID is the projection identity (used to scope graph data).
	ID graph.ProjectionID
	// Namespace is the namespace in which the GraphProjection resource is
	// defined. It is used when Spec.Scope.OwnNamespaceOnly is set.
	Namespace string
	// Spec is the GraphProjection spec driving this projector.
	Spec gamerav1alpha1.GraphProjectionSpec
	// Dynamic is the dynamic client used to build informers.
	Dynamic dynamic.Interface
	// Mapper resolves GroupVersionKind to GroupVersionResource.
	Mapper meta.RESTMapper
	// Store is the graph store this projector writes to. The projector takes
	// ownership and closes it on Stop.
	Store graph.Store
	// Engine derives relationships. Defaults to relationship.NewEngine().
	Engine *relationship.Engine
	// ResyncInterval is the periodic full re-sync interval. Defaults to 5m.
	ResyncInterval time.Duration

	// Embedder, when set together with VectorStore, enables GraphRAG embedding
	// refresh after each successful sync. When either is nil, embedding is
	// disabled and the projector behaves exactly as before.
	Embedder rag.Embedder
	// VectorStore receives node embeddings; typically the same backend as Store.
	VectorStore graph.VectorStore
	// Chat, when set, enables natural-language question answering and
	// text-to-Cypher (see Answer and Query). When nil, those features are
	// disabled.
	Chat rag.Chat
	// QueryStore, when set, enables guarded read-only Cypher execution for
	// text-to-Cypher. Typically the same backend as Store.
	QueryStore graph.QueryStore
	// CardOptions controls which node properties are folded into the textual
	// cards that are embedded. Defaults to rag.DefaultOptions.
	CardOptions rag.Options
	// VectorSimilarity is the similarity function for the vector index
	// ("cosine" or "euclidean"). Defaults to "cosine".
	VectorSimilarity string
	// EmbeddingBatchSize bounds how many cards are embedded per provider call.
	// Defaults to 64; a non-positive value embeds all changed cards in one call.
	EmbeddingBatchSize int
}

// Projector watches the resources in a projection's scope and keeps the graph
// store in sync.
type Projector struct {
	opts             Options
	engine           *relationship.Engine
	namespace        map[string]bool
	ownNamespaceOnly bool
	ownNamespace     string
	selector         labels.Selector
	gvks             []schema.GroupVersionKind
	// crdInclude is true when CustomResourceDefinitions should be captured as
	// nodes. crdNames, when non-empty, restricts capture to those CRD names.
	crdInclude bool
	crdNames   map[string]bool

	factory   dynamicinformer.DynamicSharedInformerFactory
	informers map[schema.GroupVersionKind]informers.GenericInformer

	trigger chan struct{}

	mu          sync.Mutex
	started     bool
	cancel      context.CancelFunc
	lastCounts  graph.Counts
	lastSyncErr error

	// embedding state, guarded by embedMu so it never contends with the sync
	// bookkeeping above.
	embedMu          sync.Mutex
	cardHashes       map[string]string // node ID -> hash of the last embedded card
	vectorIndexReady bool
	lastEmbedTime    time.Time
}

// New constructs a Projector from the given options.
func New(opts Options) *Projector {
	engine := opts.Engine
	if engine == nil {
		engine = relationship.NewEngine()
	}
	resync := opts.ResyncInterval
	if resync <= 0 {
		resync = 5 * time.Minute
	}
	opts.ResyncInterval = resync

	if opts.VectorSimilarity == "" {
		opts.VectorSimilarity = defaultVectorSimilarity
	}
	if opts.EmbeddingBatchSize == 0 {
		opts.EmbeddingBatchSize = defaultEmbeddingBatchSize
	}
	// A zero-value CardOptions means "unset"; fall back to the recommended
	// defaults (labels in, annotations out).
	if opts.CardOptions == (rag.Options{}) {
		opts.CardOptions = rag.DefaultOptions
	}

	nsSet := map[string]bool{}
	for _, ns := range opts.Spec.Scope.Namespaces {
		nsSet[ns] = true
	}

	sel := labels.Everything()
	if opts.Spec.Scope.LabelSelector != nil {
		if s, err := metav1.LabelSelectorAsSelector(opts.Spec.Scope.LabelSelector); err == nil {
			sel = s
		}
	}

	crdInclude := false
	crdNames := map[string]bool{}
	if c := opts.Spec.Scope.CRDs; c != nil {
		crdInclude = c.Include || len(c.Names) > 0
		for _, n := range c.Names {
			crdNames[n] = true
		}
	}

	return &Projector{
		opts:             opts,
		engine:           engine,
		namespace:        nsSet,
		ownNamespaceOnly: opts.Spec.Scope.OwnNamespaceOnly,
		ownNamespace:     opts.Namespace,
		selector:         sel,
		crdInclude:       crdInclude,
		crdNames:         crdNames,
		trigger:          make(chan struct{}, 1),
		informers:        map[schema.GroupVersionKind]informers.GenericInformer{},
		cardHashes:       map[string]string{},
	}
}
