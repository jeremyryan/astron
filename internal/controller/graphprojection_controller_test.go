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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
	"github.com/project-gamera/gamera/internal/graph"
	"github.com/project-gamera/gamera/internal/projector"
)

// newProjectorManager builds a projector.Manager wired to envtest with a fake
// graph store factory.
func newProjectorManager(restCfg *rest.Config, store graph.Store) *projector.Manager {
	hc, err := rest.HTTPClientFor(restCfg)
	Expect(err).NotTo(HaveOccurred())
	mapper, err := apiutil.NewDynamicRESTMapper(restCfg, hc)
	Expect(err).NotTo(HaveOccurred())
	dyn, err := dynamic.NewForConfig(restCfg)
	Expect(err).NotTo(HaveOccurred())
	return projector.NewManager(dyn, mapper, func(graph.Neo4jConfig) (graph.Store, error) {
		return store, nil
	})
}

var _ = Describe("GraphProjection Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName = "test-resource"
			secretName   = "neo4j-credentials"
			namespace    = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: namespace,
		}

		var (
			store   *fakeStore
			manager *projector.Manager
		)

		newReconciler := func() *GraphProjectionReconciler {
			return &GraphProjectionReconciler{
				Client:     k8sClient,
				Scheme:     k8sClient.Scheme(),
				Projectors: manager,
			}
		}

		BeforeEach(func() {
			store = newFakeStore()
			manager = newProjectorManager(cfg, store)

			By("creating the Neo4J credentials secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
				Data: map[string][]byte{
					"username": []byte("neo4j"),
					"password": []byte("s3cret"),
				},
			}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, &corev1.Secret{})
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			}

			By("creating the custom resource for the Kind GraphProjection")
			err = k8sClient.Get(ctx, typeNamespacedName, &gamerav1alpha1.GraphProjection{})
			if err != nil && errors.IsNotFound(err) {
				resource := &gamerav1alpha1.GraphProjection{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespace,
					},
					Spec: gamerav1alpha1.GraphProjectionSpec{
						Neo4j: gamerav1alpha1.Neo4jConnection{
							URI: "neo4j://neo4j.gamera-system.svc:7687",
							AuthSecretRef: gamerav1alpha1.SecretReference{
								Name: secretName,
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &gamerav1alpha1.GraphProjection{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err == nil {
				By("Cleanup the specific resource instance GraphProjection")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

				By("Reconciling so the finalizer is removed and deletion completes")
				_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() bool {
					return errors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, &gamerav1alpha1.GraphProjection{}))
				}).Should(BeTrue())
			}

			secret := &corev1.Secret{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, secret); err == nil {
				Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
			}
		})

		It("should connect to the graph store and mark the projection ready", func() {
			controllerReconciler := newReconciler()

			By("First reconcile adds the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Second reconcile verifies connectivity and updates status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the store connectivity was checked")
			Expect(store.verifyCalls).To(BeNumerically(">=", 1))

			By("Verifying the projection status reflects the ready phase")
			updated := &gamerav1alpha1.GraphProjection{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(phaseReady))
			Expect(updated.Status.ObservedGeneration).To(Equal(updated.Generation))

			cond := metaCondition(updated, conditionAvailable)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))

			By("Creating a watched resource and verifying the projector syncs it")
			watched := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "watched-config", Namespace: namespace},
				Data:       map[string]string{"key": "value"},
			}
			Expect(k8sClient.Create(ctx, watched)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, watched) })

			Eventually(func() bool {
				return store.hasNodeNamed("watched-config")
			}, "15s", "500ms").Should(BeTrue(), "expected the projector to materialize the ConfigMap as a node")
		})

		It("should mark the projection in error when the store cannot connect", func() {
			store.verifyErr = context.DeadlineExceeded
			controllerReconciler := newReconciler()

			By("First reconcile adds the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("Second reconcile fails connectivity verification")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			updated := &gamerav1alpha1.GraphProjection{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(phaseError))

			cond := metaCondition(updated, conditionAvailable)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should delete projection data from the graph on teardown", func() {
			controllerReconciler := newReconciler()

			By("Reconcile twice to add finalizer and sync")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			projection := &gamerav1alpha1.GraphProjection{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, projection)).To(Succeed())
			projectionID := graph.ProjectionID(projection.UID)

			By("Deleting the resource and reconciling the teardown")
			Expect(k8sClient.Delete(ctx, projection)).To(Succeed())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Expect(store.wasDeleted(projectionID)).To(BeTrue())
		})
	})
})

func metaCondition(p *gamerav1alpha1.GraphProjection, condType string) *metav1.Condition {
	for i := range p.Status.Conditions {
		if p.Status.Conditions[i].Type == condType {
			return &p.Status.Conditions[i]
		}
	}
	return nil
}
