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

package crdschema

import (
	"context"
	"fmt"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/project-astron/astron/internal/graph"
	"github.com/project-astron/astron/internal/rag"
)

// crdGVR identifies CustomResourceDefinition objects. They are themselves
// cluster-scoped, so no namespace filtering is needed to watch them.
var crdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

// debounceWindow coalesces a burst of CRD change events into a single sync,
// mirroring internal/projector's debounce window.
const debounceWindow = 1 * time.Second

// defaultResyncInterval is both the informer relist interval and the sync
// loop's periodic full-resync period, used when Options.ResyncInterval is
// unset. A period this long is only a safety net against a missed/dropped
// watch event; ordinary changes are picked up promptly via the informer's
// event handlers.
const defaultResyncInterval = 5 * time.Minute

// defaultEmbeddingBatchSize bounds how many overviews are embedded per
// provider call, used when Options.EmbeddingBatchSize is unset.
const defaultEmbeddingBatchSize = 64

// defaultVectorSimilarity is the similarity function used for the schema
// vector index, used when Options.VectorSimilarity is unset.
const defaultVectorSimilarity = "cosine"

// Options configures a Syncer.
type Options struct {
	// Dynamic is the dynamic client used to watch CustomResourceDefinition
	// objects. Reuse the same client cmd/main.go already builds for
	// projector.NewManager and api.NewServer.
	Dynamic dynamic.Interface
	// Store is where rendered CRD schemas are written. Required.
	Store graph.SchemaStore
	// Embedder embeds each selected CRD's storage-version overview for
	// search. Required.
	Embedder rag.Embedder
	// Names optionally restricts capture to these CRD names (e.g.
	// "widgets.example.com"). Empty captures every CRD in the cluster.
	Names []string
	// ResyncInterval overrides defaultResyncInterval.
	ResyncInterval time.Duration
	// VectorSimilarity overrides defaultVectorSimilarity.
	VectorSimilarity string
	// EmbeddingBatchSize overrides defaultEmbeddingBatchSize.
	EmbeddingBatchSize int
}

// Result summarizes one Sync call, for logging.
type Result struct {
	// CRDs is the number of CustomResourceDefinitions considered (already
	// filtered by the configured name allow-list).
	CRDs int
	// Versions is the number of rendered CRD-version documents considered
	// (one or more per CRD, one per served version).
	Versions int
	// Changed is the number of documents that were new or had a changed
	// overview, and were written this sync.
	Changed int
	// Embedded is the number of Changed documents that were also embedded
	// (i.e. were a CRD's storage version).
	Embedded int
	// Deleted is the number of previously-captured documents removed because
	// their CRD (or version) is no longer present or no longer selected.
	Deleted int
}

// Syncer watches CustomResourceDefinition objects cluster-wide and keeps
// their rendered schema documentation in sync with a graph.SchemaStore. See
// the package doc for how this relates to internal/projector.
type Syncer struct {
	opts  Options
	names map[string]bool // nil/empty means "all"

	mu               sync.Mutex
	started          bool
	cancel           context.CancelFunc
	informer         cache.SharedIndexInformer
	trigger          chan struct{}
	vectorIndexReady bool
	lastKeys         map[string]bool
	lastSyncErr      error
}

// NewSyncer builds a Syncer. Call Start to begin watching and syncing.
func NewSyncer(opts Options) *Syncer {
	if opts.ResyncInterval <= 0 {
		opts.ResyncInterval = defaultResyncInterval
	}
	if opts.VectorSimilarity == "" {
		opts.VectorSimilarity = defaultVectorSimilarity
	}
	if opts.EmbeddingBatchSize == 0 {
		opts.EmbeddingBatchSize = defaultEmbeddingBatchSize
	}
	names := make(map[string]bool, len(opts.Names))
	for _, n := range opts.Names {
		names[n] = true
	}
	return &Syncer{
		opts:     opts,
		names:    names,
		trigger:  make(chan struct{}, 1),
		lastKeys: map[string]bool{},
	}
}

// Start begins watching CustomResourceDefinition objects and runs the sync
// loop in the background until ctx is cancelled or Stop is called. It blocks
// until the informer cache is synced, then returns.
func (s *Syncer) Start(ctx context.Context) error {
	if s.opts.Dynamic == nil || s.opts.Store == nil || s.opts.Embedder == nil {
		return fmt.Errorf("crdschema.Syncer requires Dynamic, Store and Embedder")
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.started = true
	s.mu.Unlock()

	log := logf.FromContext(ctx).WithValues("component", "crdschema")

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		s.opts.Dynamic, s.opts.ResyncInterval, metav1.NamespaceAll, nil)
	inf := factory.ForResource(crdGVR).Informer()
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { s.enqueue() },
		UpdateFunc: func(any, any) { s.enqueue() },
		DeleteFunc: func(any) { s.enqueue() },
	}
	if _, err := inf.AddEventHandler(handler); err != nil {
		cancel()
		return fmt.Errorf("adding CustomResourceDefinition event handler: %w", err)
	}
	s.informer = inf

	factory.Start(runCtx.Done())
	if !cache.WaitForCacheSync(runCtx.Done(), inf.HasSynced) {
		cancel()
		return fmt.Errorf("failed to sync CustomResourceDefinition informer cache")
	}

	go s.run(logf.IntoContext(runCtx, log))
	s.enqueue()
	return nil
}

// Stop stops watching and syncing. It does not delete any previously
// captured schema data.
func (s *Syncer) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.started = false
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// LastSyncErr returns the error from the most recent sync, if any.
func (s *Syncer) LastSyncErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSyncErr
}

func (s *Syncer) enqueue() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// run is the debounced sync loop. The logger is carried on the context.
func (s *Syncer) run(ctx context.Context) {
	ticker := time.NewTicker(s.opts.ResyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.doSync(ctx)
		case <-s.trigger:
			select {
			case <-ctx.Done():
				return
			case <-time.After(debounceWindow):
			}
			drain(s.trigger)
			s.doSync(ctx)
		}
	}
}

func (s *Syncer) doSync(ctx context.Context) {
	log := logf.FromContext(ctx)
	result, err := s.Sync(ctx)
	s.mu.Lock()
	s.lastSyncErr = err
	s.mu.Unlock()
	if err != nil {
		log.Error(err, "CRD schema sync failed")
		return
	}
	log.Info("CRD schemas synced",
		"crds", result.CRDs, "versions", result.Versions,
		"changed", result.Changed, "embedded", result.Embedded, "deleted", result.Deleted)
}

// Sync lists the currently cached CustomResourceDefinitions, converts and
// filters them by the configured name allow-list, and reconciles the store.
// It is safe to call directly (e.g. right after Start, or from tests once the
// informer cache is populated).
func (s *Syncer) Sync(ctx context.Context) (Result, error) {
	var crds []*apiextensionsv1.CustomResourceDefinition
	for _, item := range s.informer.GetStore().List() {
		u, ok := item.(*unstructured.Unstructured)
		if !ok || !s.selected(u.GetName()) {
			continue
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &crd); err != nil {
			logf.FromContext(ctx).Error(err, "converting CustomResourceDefinition", "name", u.GetName())
			continue
		}
		crds = append(crds, &crd)
	}
	return s.syncCRDs(ctx, crds)
}

// selected reports whether a CRD name passes the configured allow-list.
func (s *Syncer) selected(name string) bool {
	if len(s.names) == 0 {
		return true
	}
	return s.names[name]
}

func drain(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
