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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
)

var _ = Describe("GraphProjection Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		graphprojection := &gamerav1alpha1.GraphProjection{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind GraphProjection")
			err := k8sClient.Get(ctx, typeNamespacedName, graphprojection)
			if err != nil && errors.IsNotFound(err) {
				resource := &gamerav1alpha1.GraphProjection{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: gamerav1alpha1.GraphProjectionSpec{
						Neo4j: gamerav1alpha1.Neo4jConnection{
							URI: "neo4j://neo4j.gamera-system.svc:7687",
							AuthSecretRef: gamerav1alpha1.SecretReference{
								Name: "neo4j-credentials",
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &gamerav1alpha1.GraphProjection{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance GraphProjection")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			By("Reconciling so the finalizer is removed and deletion completes")
			controllerReconciler := &GraphProjectionReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				return errors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, &gamerav1alpha1.GraphProjection{}))
			}).Should(BeTrue())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &GraphProjectionReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("First reconcile adds the finalizer")
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Second reconcile updates status")
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the projection status reflects the syncing phase")
			updated := &gamerav1alpha1.GraphProjection{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(phaseSyncing))
			Expect(updated.Status.ObservedGeneration).To(Equal(updated.Generation))
		})
	})
})
