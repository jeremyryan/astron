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

package projector

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
	"github.com/project-gamera/gamera/internal/relationship"
)

// crdGroup/crdKind identify CustomResourceDefinition objects, and crdDefinesType
// is the edge type from a CRD to its instances.
const (
	crdGroup       = "apiextensions.k8s.io"
	crdKind        = "CustomResourceDefinition"
	crdDefinesType = "DEFINES"
)

// crdGVK is the GroupVersionKind of CustomResourceDefinition resources.
var crdGVK = schema.GroupVersionKind{Group: crdGroup, Version: "v1", Kind: crdKind}

// Start builds and starts the informers for the projection's scope and runs the
// sync loop until the given context is cancelled or Stop is called. It blocks
// until the informer caches are synced, then returns; the sync loop continues
// in the background.
func (p *Projector) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.started = true
	p.mu.Unlock()

	log := logf.FromContext(ctx).WithValues("projection", p.opts.ID)

	p.gvks = p.scopedGVKs()

	// Watch across all namespaces; namespace/label filtering is applied during
	// sync so multiple namespaces and label selectors are handled uniformly.
	p.factory = newFactory(p.opts.Dynamic, p.opts.ResyncInterval, p.watchNamespace())

	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { p.enqueue() },
		UpdateFunc: func(any, any) { p.enqueue() },
		DeleteFunc: func(any) { p.enqueue() },
	}

	for _, gvk := range p.gvks {
		gvr, err := p.gvrFor(gvk)
		if err != nil {
			log.Error(err, "skipping kind with no REST mapping", "gvk", gvk.String())
			continue
		}
		inf := p.factory.ForResource(gvr)
		if _, err := inf.Informer().AddEventHandler(handler); err != nil {
			return fmt.Errorf("adding event handler for %s: %w", gvk, err)
		}
		p.informers[gvk] = inf
	}

	p.factory.Start(runCtx.Done())
	synced := p.factory.WaitForCacheSync(runCtx.Done())
	for gvr, ok := range synced {
		if !ok {
			cancel()
			return fmt.Errorf("failed to sync cache for %s", gvr)
		}
	}

	// Carry the logger on the context so the background loop can retrieve it.
	go p.run(logf.IntoContext(runCtx, log))

	// Trigger an initial sync now that caches are warm.
	p.enqueue()
	return nil
}

// Stop stops the informers, removes the projection's data from the graph, and
// closes the store.
func (p *Projector) Stop(ctx context.Context) error {
	p.mu.Lock()
	cancel := p.cancel
	started := p.started
	p.started = false
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if !started {
		return nil
	}

	var firstErr error
	if err := p.opts.Store.DeleteProjection(ctx, p.opts.ID); err != nil {
		firstErr = err
	}
	if err := p.opts.Store.Close(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// LastCounts returns the counts and error from the most recent sync.
func (p *Projector) LastCounts() (graph.Counts, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastCounts, p.lastSyncErr
}

// ReadGraph returns the materialized graph for this projection from the store.
func (p *Projector) ReadGraph(ctx context.Context) (graph.GraphData, error) {
	return p.opts.Store.ReadGraph(ctx, p.opts.ID)
}

// AddLink creates a user-defined link between two nodes of this projection, if
// the backing store supports manual links. It returns ErrLinksNotSupported
// otherwise.
func (p *Projector) AddLink(ctx context.Context, fromID, toID, relType string) error {
	ls, ok := p.opts.Store.(graph.LinkStore)
	if !ok {
		return ErrLinksNotSupported
	}
	return ls.AddManualLink(ctx, p.opts.ID, fromID, toID, relType)
}

// DeleteLink removes a user-defined link between two nodes of this projection,
// if the backing store supports manual links. It returns ErrLinksNotSupported
// otherwise.
func (p *Projector) DeleteLink(ctx context.Context, fromID, toID, relType string) error {
	ls, ok := p.opts.Store.(graph.LinkStore)
	if !ok {
		return ErrLinksNotSupported
	}
	return ls.DeleteManualLink(ctx, p.opts.ID, fromID, toID, relType)
}

// enqueue requests a (debounced) re-sync without blocking the caller.
func (p *Projector) enqueue() {
	select {
	case p.trigger <- struct{}{}:
	default:
	}
}

// run is the debounced sync loop. The logger is carried on the context.
func (p *Projector) run(ctx context.Context) {
	ticker := time.NewTicker(p.opts.ResyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.doSync(ctx)
		case <-p.trigger:
			// Debounce: coalesce a burst of events.
			select {
			case <-ctx.Done():
				return
			case <-time.After(debounceWindow):
			}
			drain(p.trigger)
			p.doSync(ctx)
		}
	}
}

func (p *Projector) doSync(ctx context.Context) {
	log := logf.FromContext(ctx)
	counts, err := p.Sync(ctx)
	p.mu.Lock()
	p.lastCounts = counts
	p.lastSyncErr = err
	p.mu.Unlock()
	if err != nil {
		log.Error(err, "projection sync failed")
		return
	}
	log.Info("projection synced", "nodes", counts.Nodes, "edges", counts.Relationships)
}

// Sync builds the desired graph from the current informer caches and writes it
// to the store. It is safe to call directly (e.g. from tests).
func (p *Projector) Sync(ctx context.Context) (graph.Counts, error) {
	objs := p.snapshot()
	index := relationship.NewMapIndex(objs...)

	nodes := make([]graph.Node, 0, len(objs))
	for _, o := range objs {
		nodes = append(nodes, nodeFor(o))
	}

	edges, deriveErr := p.engine.Derive(p.opts.Spec.Relationships, index)
	// When CRDs are captured, link each CRD to the in-scope resources it defines.
	edges = append(edges, p.crdEdges(objs, index)...)

	counts, err := p.opts.Store.Sync(ctx, p.opts.ID, nodes, edges)
	if err != nil {
		return graph.Counts{}, err
	}

	// Refresh GraphRAG embeddings for any changed nodes. This is best-effort: a
	// failure is logged but does not fail the sync, so the projected graph stays
	// correct even if embeddings momentarily lag.
	if err := p.refreshEmbeddings(ctx, nodes, edges); err != nil {
		logf.FromContext(ctx).Error(err, "refreshing embeddings failed")
	}

	// Surface a derive error after the (partial) write succeeds, so good edges
	// are still persisted.
	return counts, deriveErr
}

// snapshot collects all in-scope objects from the informer caches, applying
// namespace and label filtering.
func (p *Projector) snapshot() []*unstructured.Unstructured {
	var out []*unstructured.Unstructured
	for _, inf := range p.informers {
		for _, item := range inf.Informer().GetStore().List() {
			obj, ok := item.(*unstructured.Unstructured)
			if !ok || !p.inScope(obj) {
				continue
			}
			out = append(out, obj)
		}
	}
	return out
}

// crdEdges derives DEFINES edges from each captured CustomResourceDefinition to
// every captured resource that is an instance of it (i.e. whose group and kind
// match the CRD's spec.group and spec.names.kind). Because only resources in
// the projection's configured set are present in the index, edges are naturally
// limited to instances that are themselves captured.
func (p *Projector) crdEdges(objs []*unstructured.Unstructured, index relationship.Index) []graph.Relationship {
	if !p.crdInclude {
		return nil
	}
	var edges []graph.Relationship
	for _, o := range objs {
		if !isCRD(o) {
			continue
		}
		group, _, _ := unstructured.NestedString(o.Object, "spec", "group")
		kind, _, _ := unstructured.NestedString(o.Object, "spec", "names", "kind")
		if kind == "" {
			continue
		}
		crdRef := refForObj(o)
		for _, instance := range index.ByKind(schema.GroupVersionKind{Group: group, Kind: kind}) {
			edges = append(edges, graph.Relationship{
				Type: crdDefinesType,
				From: crdRef,
				To:   refForObj(instance),
			})
		}
	}
	return edges
}

// isCRD reports whether an object is a CustomResourceDefinition.
func isCRD(obj *unstructured.Unstructured) bool {
	gvk := obj.GroupVersionKind()
	return gvk.Group == crdGroup && gvk.Kind == crdKind
}

// refForObj builds a graph.Ref identifying an unstructured object.
func refForObj(o *unstructured.Unstructured) graph.Ref {
	return graph.Ref{
		APIVersion: o.GetAPIVersion(),
		Kind:       o.GetKind(),
		Namespace:  o.GetNamespace(),
		Name:       o.GetName(),
		UID:        string(o.GetUID()),
	}
}

// inScope reports whether an object passes the namespace and label filters.
func (p *Projector) inScope(obj *unstructured.Unstructured) bool {
	// CustomResourceDefinitions are cluster-scoped and explicitly requested via
	// the crds selection, so they bypass the namespace/label filters and are
	// included only when selected.
	if isCRD(obj) {
		return p.crdSelected(obj)
	}

	ns := obj.GetNamespace()
	if p.ownNamespaceOnly {
		// Only namespaced resources in the projection's own namespace. This
		// excludes cluster-scoped resources (empty namespace) as well.
		if ns != p.ownNamespace {
			return false
		}
	} else if len(p.namespace) > 0 && ns != "" && !p.namespace[ns] {
		return false
	}
	return p.selector.Matches(labels.Set(obj.GetLabels()))
}

// crdSelected reports whether a CustomResourceDefinition object should be
// captured, based on the crds selection. When a name list is configured, only
// those CRDs are captured; otherwise all CRDs are captured when enabled.
func (p *Projector) crdSelected(obj *unstructured.Unstructured) bool {
	if !p.crdInclude {
		return false
	}
	if len(p.crdNames) > 0 {
		return p.crdNames[obj.GetName()]
	}
	return true
}

// watchNamespace returns the namespace the informers should watch. When the
// projection is scoped to its own namespace, only that namespace is watched
// (reducing watch load); otherwise all namespaces are watched and filtering is
// applied during sync.
func (p *Projector) watchNamespace() string {
	if p.ownNamespaceOnly {
		return p.ownNamespace
	}
	return metav1.NamespaceAll
}

// scopedGVKs resolves the resource selectors in the projection scope to GVKs.
// When no resources are configured, a built-in default set is used.
func (p *Projector) scopedGVKs() []schema.GroupVersionKind {
	resources := p.opts.Spec.Scope.Resources
	if len(resources) == 0 {
		resources = defaultResources()
	}
	gvks := make([]schema.GroupVersionKind, 0, len(resources)+1)
	seen := map[schema.GroupVersionKind]bool{}
	for _, r := range resources {
		gvk := schema.GroupVersionKind{Group: r.Group, Version: r.Version, Kind: r.Kind}
		if seen[gvk] {
			continue
		}
		seen[gvk] = true
		gvks = append(gvks, gvk)
	}
	// When CRDs are captured, also watch CustomResourceDefinition objects.
	if p.crdInclude && !seen[crdGVK] {
		gvks = append(gvks, crdGVK)
	}
	return gvks
}

// gvrFor maps a GVK to a GVR using the REST mapper.
func (p *Projector) gvrFor(gvk schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	var mapping *meta.RESTMapping
	var err error
	if gvk.Version != "" {
		mapping, err = p.opts.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	} else {
		mapping, err = p.opts.Mapper.RESTMapping(gvk.GroupKind())
	}
	if err != nil {
		return schema.GroupVersionResource{}, err
	}
	return mapping.Resource, nil
}

// defaultResources is the built-in set of resource kinds captured when a
// projection does not enumerate its own.
func defaultResources() []gamerav1alpha1.ResourceSelector {
	return []gamerav1alpha1.ResourceSelector{
		{Group: "apps", Version: "v1", Kind: "Deployment"},
		{Group: "apps", Version: "v1", Kind: "StatefulSet"},
		{Group: "apps", Version: "v1", Kind: "DaemonSet"},
		{Group: "apps", Version: "v1", Kind: "ReplicaSet"},
		{Version: "v1", Kind: "Pod"},
		{Version: "v1", Kind: "Service"},
		{Version: "v1", Kind: "ConfigMap"},
		{Version: "v1", Kind: "Secret"},
		{Version: "v1", Kind: "PersistentVolumeClaim"},
		{Version: "v1", Kind: "PersistentVolume"},
		{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
		// Gateway API; captured when its CRDs are installed, otherwise skipped.
		{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"},
	}
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
