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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
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

// GraphProjectionReconciler reconciles a GraphProjection object
type GraphProjectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=gamera.gamera.io,resources=graphprojections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gamera.gamera.io,resources=graphprojections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gamera.gamera.io,resources=graphprojections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile drives the actual state of a GraphProjection toward its desired
// state: it validates the projection, ensures the connection to the configured
// Neo4J database, and (in a full implementation) starts/updates the dynamic
// watchers that capture cluster resources as graph nodes and relationships.
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

	// TODO(gamera): establish the Neo4J connection from spec.neo4j and the
	// referenced credentials Secret, then start/refresh the dynamic informers
	// for the resources in scope and apply the relationship rules. For now we
	// record that the projection has been observed and mark it progressing.
	log.Info("reconciling GraphProjection",
		"neo4jURI", projection.Spec.Neo4j.URI,
		"namespaces", projection.Spec.Scope.Namespaces,
		"relationships", len(projection.Spec.Relationships),
	)

	meta.SetStatusCondition(&projection.Status.Conditions, metav1.Condition{
		Type:               conditionProgressing,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: projection.Generation,
		Reason:             "Reconciling",
		Message:            "Projection configuration accepted; establishing graph sync",
	})
	projection.Status.Phase = phaseSyncing
	projection.Status.ObservedGeneration = projection.Generation

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

	// TODO(gamera): remove the nodes and relationships owned by this projection
	// from the Neo4J database and stop the associated watchers.
	log.Info("tearing down GraphProjection", "name", projection.Name)

	controllerutil.RemoveFinalizer(projection, graphProjectionFinalizer)
	if err := r.Update(ctx, projection); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&gamerav1alpha1.GraphProjection{}).
		Named("graphprojection").
		Complete(r)
}
