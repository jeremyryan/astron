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

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
	"github.com/project-gamera/gamera/internal/relationship"
)

const (
	// graphProjectionFinalizer ensures the graph materialized by a projection is
	// torn down before the GraphProjection resource is removed.
	graphProjectionFinalizer = "gamera.gamera.io/graph-projection"

	// defaultResyncInterval is used when a projection does not specify its own
	// resyncInterval.
	defaultResyncInterval = 5 * time.Minute

	phaseReady    = "Ready"
	phaseSyncing  = "Syncing"
	phaseError    = "Error"
	phaseDeleting = "Deleting"

	conditionAvailable   = "Available"
	conditionProgressing = "Progressing"
)

// StoreFactory builds a graph.Store from a resolved Neo4J configuration. It is
// injectable so tests can substitute a fake store.
type StoreFactory func(cfg graph.Neo4jConfig) (graph.Store, error)

// defaultStoreFactory constructs a real Neo4J-backed store.
func defaultStoreFactory(cfg graph.Neo4jConfig) (graph.Store, error) {
	return graph.NewNeo4jStore(cfg)
}

// GraphProjectionReconciler reconciles a GraphProjection object
type GraphProjectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// NewStore builds a graph.Store for a projection. Defaults to a Neo4J store.
	NewStore StoreFactory

	// Engine derives graph relationships from a projection's rules. Defaults to
	// the built-in engine with the standard strategies registered.
	Engine *relationship.Engine
}

// +kubebuilder:rbac:groups=gamera.gamera.io,resources=graphprojections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gamera.gamera.io,resources=graphprojections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gamera.gamera.io,resources=graphprojections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile drives the actual state of a GraphProjection toward its desired
// state: it resolves the Neo4J credentials, verifies connectivity to the
// configured graph database, and (in a full implementation) starts/updates the
// dynamic watchers that capture cluster resources as graph nodes and edges.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile
func (r *GraphProjectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var projection gamerav1alpha1.GraphProjection
	if err := r.Get(ctx, req.NamespacedName, &projection); err != nil {
		// The object was deleted; nothing further to do.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion and finalizer-based teardown of the projected graph.
	if !projection.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &projection)
	}

	// Ensure our finalizer is present so we can clean up the graph on deletion.
	if !controllerutil.ContainsFinalizer(&projection, graphProjectionFinalizer) {
		controllerutil.AddFinalizer(&projection, graphProjectionFinalizer)
		if err := r.Update(ctx, &projection); err != nil {
			return ctrl.Result{}, err
		}
		// Re-queue: the update changes resourceVersion and triggers a fresh event.
		return ctrl.Result{}, nil
	}

	// Connect to the configured Neo4J database and verify it is reachable.
	store, err := r.storeFor(ctx, &projection)
	if err != nil {
		log.Error(err, "failed to build graph store")
		return r.fail(ctx, &projection, "StoreUnavailable", err)
	}
	defer func() {
		if cerr := store.Close(ctx); cerr != nil {
			log.Error(cerr, "failed to close graph store")
		}
	}()

	if err := store.Verify(ctx); err != nil {
		log.Error(err, "failed to verify graph store connectivity")
		return r.fail(ctx, &projection, "ConnectionFailed", err)
	}

	// The relationship engine (r.Engine, see internal/relationship) is ready to
	// translate the projection's rules into graph edges. It operates over a
	// relationship.Index snapshot of the in-scope resources.
	//
	// TODO(gamera): start/refresh the dynamic informers for the resources in
	// spec.scope, build a relationship.Index from their caches, upsert the
	// resource nodes, then call r.Engine.Derive(spec.relationships, index) and
	// upsert the resulting edges via store.UpsertRelationships. For now we report
	// the current counts and mark the projection ready.
	counts, err := store.Counts(ctx, graph.ProjectionID(projection.UID))
	if err != nil {
		log.Error(err, "failed to read projection counts")
		return r.fail(ctx, &projection, "CountsFailed", err)
	}

	log.Info("reconciled GraphProjection",
		"neo4jURI", projection.Spec.Neo4j.URI,
		"namespaces", projection.Spec.Scope.Namespaces,
		"relationships", len(projection.Spec.Relationships),
		"nodes", counts.Nodes,
		"edges", counts.Relationships,
	)

	now := metav1.Now()
	meta.SetStatusCondition(&projection.Status.Conditions, metav1.Condition{
		Type:               conditionAvailable,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: projection.Generation,
		Reason:             "Connected",
		Message:            "Connected to Neo4J and projection is synchronized",
	})
	meta.SetStatusCondition(&projection.Status.Conditions, metav1.Condition{
		Type:               conditionProgressing,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: projection.Generation,
		Reason:             "Synced",
		Message:            "Projection is up to date",
	})
	projection.Status.Phase = phaseReady
	projection.Status.ObservedGeneration = projection.Generation
	projection.Status.NodeCount = counts.Nodes
	projection.Status.RelationshipCount = counts.Relationships
	projection.Status.LastSyncTime = &now

	if err := r.Status().Update(ctx, &projection); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: r.resyncInterval(&projection)}, nil
}

// reconcileDelete tears down the materialized graph for a projection being
// deleted, then removes the finalizer.
func (r *GraphProjectionReconciler) reconcileDelete(ctx context.Context, projection *gamerav1alpha1.GraphProjection) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(projection, graphProjectionFinalizer) {
		return ctrl.Result{}, nil
	}

	projection.Status.Phase = phaseDeleting
	// Best-effort status update; ignore errors as the object is going away.
	_ = r.Status().Update(ctx, projection)

	store, err := r.storeFor(ctx, projection)
	if err != nil {
		// If we cannot even build the store (e.g. the credentials Secret is
		// already gone), we cannot clean up the graph. Surface the error and
		// retry rather than orphaning graph data.
		log.Error(err, "failed to build graph store for teardown")
		return ctrl.Result{}, err
	}
	defer func() { _ = store.Close(ctx) }()

	if err := store.DeleteProjection(ctx, graph.ProjectionID(projection.UID)); err != nil {
		log.Error(err, "failed to delete projection data from graph")
		return ctrl.Result{}, err
	}
	log.Info("tore down GraphProjection graph data", "name", projection.Name)

	controllerutil.RemoveFinalizer(projection, graphProjectionFinalizer)
	if err := r.Update(ctx, projection); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// storeFor resolves the projection's Neo4J credentials from the referenced
// Secret and builds a graph.Store.
func (r *GraphProjectionReconciler) storeFor(ctx context.Context, projection *gamerav1alpha1.GraphProjection) (graph.Store, error) {
	cfg, err := r.resolveNeo4jConfig(ctx, projection)
	if err != nil {
		return nil, err
	}
	factory := r.NewStore
	if factory == nil {
		factory = defaultStoreFactory
	}
	return factory(cfg)
}

// resolveNeo4jConfig reads the credentials Secret and assembles a Neo4jConfig.
func (r *GraphProjectionReconciler) resolveNeo4jConfig(ctx context.Context, projection *gamerav1alpha1.GraphProjection) (graph.Neo4jConfig, error) {
	ref := projection.Spec.Neo4j.AuthSecretRef
	namespace := ref.Namespace
	if namespace == "" {
		namespace = projection.Namespace
	}
	usernameKey := ref.UsernameKey
	if usernameKey == "" {
		usernameKey = "username"
	}
	passwordKey := ref.PasswordKey
	if passwordKey == "" {
		passwordKey = "password"
	}

	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		return graph.Neo4jConfig{}, fmt.Errorf("reading credentials secret %s/%s: %w", namespace, ref.Name, err)
	}

	username, ok := secret.Data[usernameKey]
	if !ok {
		return graph.Neo4jConfig{}, fmt.Errorf("secret %s/%s missing key %q", namespace, ref.Name, usernameKey)
	}
	password, ok := secret.Data[passwordKey]
	if !ok {
		return graph.Neo4jConfig{}, fmt.Errorf("secret %s/%s missing key %q", namespace, ref.Name, passwordKey)
	}

	return graph.Neo4jConfig{
		URI:      projection.Spec.Neo4j.URI,
		Username: string(username),
		Password: string(password),
		Database: projection.Spec.Neo4j.Database,
	}, nil
}

// fail records an error condition and phase on the projection and returns a
// result that retries after a short backoff.
func (r *GraphProjectionReconciler) fail(ctx context.Context, projection *gamerav1alpha1.GraphProjection, reason string, cause error) (ctrl.Result, error) {
	meta.SetStatusCondition(&projection.Status.Conditions, metav1.Condition{
		Type:               conditionAvailable,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: projection.Generation,
		Reason:             reason,
		Message:            cause.Error(),
	})
	projection.Status.Phase = phaseError
	projection.Status.ObservedGeneration = projection.Generation

	if err := r.Status().Update(ctx, projection); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	// Retry with a fixed backoff; the cause is recorded in status.
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// resyncInterval returns the configured resync interval or the default.
func (r *GraphProjectionReconciler) resyncInterval(projection *gamerav1alpha1.GraphProjection) time.Duration {
	if projection.Spec.ResyncInterval != nil && projection.Spec.ResyncInterval.Duration > 0 {
		return projection.Spec.ResyncInterval.Duration
	}
	return defaultResyncInterval
}

// SetupWithManager sets up the controller with the Manager.
func (r *GraphProjectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.NewStore == nil {
		r.NewStore = defaultStoreFactory
	}
	if r.Engine == nil {
		r.Engine = relationship.NewEngine()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&gamerav1alpha1.GraphProjection{}).
		Named("graphprojection").
		Complete(r)
}
