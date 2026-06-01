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
	"k8s.io/client-go/dynamic"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
	"github.com/project-gamera/gamera/internal/projector"
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

// GraphProjectionReconciler reconciles a GraphProjection object. It translates
// each GraphProjection into a running projector (see internal/projector) that
// watches the in-scope resources and keeps the graph store in sync.
type GraphProjectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Projectors manages the lifecycle of the per-projection resource graph
	// watchers. It must be set before Reconcile is called; SetupWithManager
	// installs a default backed by a dynamic client and a Neo4J store.
	Projectors *projector.Manager
}

// +kubebuilder:rbac:groups=gamera.gamera.io,resources=graphprojections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gamera.gamera.io,resources=graphprojections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gamera.gamera.io,resources=graphprojections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="*",resources="*",verbs=get;list;watch

// Reconcile drives the actual state of a GraphProjection toward its desired
// state: it resolves the Neo4J credentials and ensures a projector is running
// that watches the in-scope resources and synchronizes the graph store.
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
	id := graph.ProjectionID(projection.UID)

	// Handle deletion and finalizer-based teardown of the projected graph.
	if !projection.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &projection, id)
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

	// Resolve the Neo4J credentials from the referenced Secret.
	cfg, err := r.resolveNeo4jConfig(ctx, &projection)
	if err != nil {
		log.Error(err, "failed to resolve Neo4J credentials")
		return r.fail(ctx, &projection, "CredentialsUnavailable", err)
	}

	// Ensure a projector is running for this projection with the current config.
	p, err := r.Projectors.Ensure(ctx, id, projection.Spec, cfg)
	if err != nil {
		log.Error(err, "failed to start projector")
		return r.fail(ctx, &projection, "ProjectorStartFailed", err)
	}

	counts, syncErr := p.LastCounts()
	log.Info("reconciled GraphProjection",
		"neo4jURI", projection.Spec.Neo4j.URI,
		"namespaces", projection.Spec.Scope.Namespaces,
		"relationships", len(projection.Spec.Relationships),
		"nodes", counts.Nodes,
		"edges", counts.Relationships,
	)

	now := metav1.Now()
	availability := metav1.Condition{
		Type:               conditionAvailable,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: projection.Generation,
		Reason:             "Synced",
		Message:            "Projector running and graph is synchronized",
	}
	if syncErr != nil {
		availability.Reason = "SyncError"
		availability.Message = syncErr.Error()
		projection.Status.Phase = phaseSyncing
	} else {
		projection.Status.Phase = phaseReady
	}
	meta.SetStatusCondition(&projection.Status.Conditions, availability)
	meta.SetStatusCondition(&projection.Status.Conditions, metav1.Condition{
		Type:               conditionProgressing,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: projection.Generation,
		Reason:             "Running",
		Message:            "Projector is running",
	})
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

// reconcileDelete stops the projector (which removes the projection's graph
// data) and then removes the finalizer.
func (r *GraphProjectionReconciler) reconcileDelete(ctx context.Context, projection *gamerav1alpha1.GraphProjection, id graph.ProjectionID) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(projection, graphProjectionFinalizer) {
		return ctrl.Result{}, nil
	}

	projection.Status.Phase = phaseDeleting
	_ = r.Status().Update(ctx, projection)

	if err := r.Projectors.Remove(ctx, id); err != nil {
		log.Error(err, "failed to tear down projector")
		return ctrl.Result{}, err
	}
	log.Info("tore down GraphProjection", "name", projection.Name)

	controllerutil.RemoveFinalizer(projection, graphProjectionFinalizer)
	if err := r.Update(ctx, projection); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
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
	if r.Projectors == nil {
		dyn, err := dynamic.NewForConfig(mgr.GetConfig())
		if err != nil {
			return fmt.Errorf("creating dynamic client: %w", err)
		}
		r.Projectors = projector.NewManager(dyn, mgr.GetRESTMapper(), func(cfg graph.Neo4jConfig) (graph.Store, error) {
			return graph.NewNeo4jStore(cfg)
		})
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&gamerav1alpha1.GraphProjection{}).
		Named("graphprojection").
		Complete(r)
}
