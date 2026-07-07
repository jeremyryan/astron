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

package cli

import (
	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

// known group/version/kind shorthands used when emitting relationship rules.
var (
	pod            = astronv1alpha1.ResourceSelector{Version: "v1", Kind: "Pod"}
	service        = astronv1alpha1.ResourceSelector{Version: "v1", Kind: "Service"}
	configMap      = astronv1alpha1.ResourceSelector{Version: "v1", Kind: "ConfigMap"}
	secret         = astronv1alpha1.ResourceSelector{Version: "v1", Kind: "Secret"}
	serviceAccount = astronv1alpha1.ResourceSelector{Version: "v1", Kind: "ServiceAccount"}
	pvc            = astronv1alpha1.ResourceSelector{Version: "v1", Kind: "PersistentVolumeClaim"}

	deployment  = astronv1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "Deployment"}
	replicaSet  = astronv1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
	statefulSet = astronv1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "StatefulSet"}
	daemonSet   = astronv1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "DaemonSet"}
	job         = astronv1alpha1.ResourceSelector{Group: "batch", Version: "v1", Kind: "Job"}
	cronJob     = astronv1alpha1.ResourceSelector{Group: "batch", Version: "v1", Kind: "CronJob"}

	ingress   = astronv1alpha1.ResourceSelector{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"}
	httpRoute = astronv1alpha1.ResourceSelector{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"}
	gateway   = astronv1alpha1.ResourceSelector{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}

	role               = astronv1alpha1.ResourceSelector{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "Role"}
	clusterRole        = astronv1alpha1.ResourceSelector{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}
	roleBinding        = astronv1alpha1.ResourceSelector{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "RoleBinding"}
	clusterRoleBinding = astronv1alpha1.ResourceSelector{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding"}
)

// buildRelationships returns the subset of well-known relationship rules whose
// endpoint kinds are both present among the discovered selectors. This keeps the
// generated manifest's relationships consistent with its captured resources.
func buildRelationships(selectors []astronv1alpha1.ResourceSelector) []astronv1alpha1.RelationshipRule {
	present := map[string]bool{}
	for _, s := range selectors {
		present[s.Kind] = true
	}

	type candidate struct {
		name     string
		relType  string
		strategy astronv1alpha1.RelationshipStrategy
		from, to astronv1alpha1.ResourceSelector
	}

	candidates := []candidate{
		// Ownership (derived from ownerReferences).
		{"deployment-owns-replicaset", "OWNS", astronv1alpha1.OwnerReferenceStrategy, deployment, replicaSet},
		{"replicaset-owns-pod", "OWNS", astronv1alpha1.OwnerReferenceStrategy, replicaSet, pod},
		{"statefulset-owns-pod", "OWNS", astronv1alpha1.OwnerReferenceStrategy, statefulSet, pod},
		{"daemonset-owns-pod", "OWNS", astronv1alpha1.OwnerReferenceStrategy, daemonSet, pod},
		{"job-owns-pod", "OWNS", astronv1alpha1.OwnerReferenceStrategy, job, pod},
		{"cronjob-owns-job", "OWNS", astronv1alpha1.OwnerReferenceStrategy, cronJob, job},
		// Selection (Service selects Pods by label).
		{"service-selects-pod", "SELECTS", astronv1alpha1.LabelSelectorStrategy, service, pod},
		// Configuration mounts (ConfigMap/Secret consumed by a Pod).
		{"configmap-mounts-pod", "MOUNTS", astronv1alpha1.VolumeMountStrategy, configMap, pod},
		{"secret-mounts-pod", "MOUNTS", astronv1alpha1.VolumeMountStrategy, secret, pod},
		// Storage mounts (PersistentVolumeClaim consumed by a Pod via a volume).
		{"pvc-mounts-pod", "MOUNTS", astronv1alpha1.VolumeMountStrategy, pvc, pod},
		// Traffic routing (Ingress/HTTPRoute forward to a Service via backendRefs).
		{"ingress-routes-service", "ROUTES", astronv1alpha1.ServiceBackendStrategy, ingress, service},
		{"httproute-routes-service", "ROUTES", astronv1alpha1.ServiceBackendStrategy, httpRoute, service},
		// Gateway attachment (HTTPRoute attaches to a Gateway via parentRefs).
		{"gateway-routes-httproute", "ROUTES", astronv1alpha1.GatewayParentStrategy, gateway, httpRoute},
		// Identity (Pod runs under a ServiceAccount, via spec.serviceAccountName).
		{"serviceaccount-runs-pod", "RUNS", astronv1alpha1.ServiceAccountStrategy, serviceAccount, pod},
		// RBAC: roles grant permissions through bindings to ServiceAccounts.
		{"role-grants-rolebinding", "GRANTS", astronv1alpha1.RoleRefStrategy, role, roleBinding},
		{"clusterrole-grants-clusterrolebinding", "GRANTS", astronv1alpha1.RoleRefStrategy, clusterRole, clusterRoleBinding},
		{"clusterrole-grants-rolebinding", "GRANTS", astronv1alpha1.RoleRefStrategy, clusterRole, roleBinding},
		{"rolebinding-binds-serviceaccount", "BINDS", astronv1alpha1.BindingSubjectStrategy, roleBinding, serviceAccount},
		{"clusterrolebinding-binds-serviceaccount", "BINDS", astronv1alpha1.BindingSubjectStrategy, clusterRoleBinding, serviceAccount},
	}

	var rules []astronv1alpha1.RelationshipRule
	for _, c := range candidates {
		if !present[c.from.Kind] || !present[c.to.Kind] {
			continue
		}
		rules = append(rules, astronv1alpha1.RelationshipRule{
			Name:     c.name,
			Type:     c.relType,
			Strategy: c.strategy,
			From:     c.from,
			To:       c.to,
		})
	}
	return rules
}
