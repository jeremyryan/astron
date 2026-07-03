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
	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
)

// known group/version/kind shorthands used when emitting relationship rules.
var (
	pod            = gamerav1alpha1.ResourceSelector{Version: "v1", Kind: "Pod"}
	service        = gamerav1alpha1.ResourceSelector{Version: "v1", Kind: "Service"}
	configMap      = gamerav1alpha1.ResourceSelector{Version: "v1", Kind: "ConfigMap"}
	secret         = gamerav1alpha1.ResourceSelector{Version: "v1", Kind: "Secret"}
	serviceAccount = gamerav1alpha1.ResourceSelector{Version: "v1", Kind: "ServiceAccount"}

	deployment  = gamerav1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "Deployment"}
	replicaSet  = gamerav1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
	statefulSet = gamerav1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "StatefulSet"}
	daemonSet   = gamerav1alpha1.ResourceSelector{Group: "apps", Version: "v1", Kind: "DaemonSet"}
	job         = gamerav1alpha1.ResourceSelector{Group: "batch", Version: "v1", Kind: "Job"}
	cronJob     = gamerav1alpha1.ResourceSelector{Group: "batch", Version: "v1", Kind: "CronJob"}
)

// buildRelationships returns the subset of well-known relationship rules whose
// endpoint kinds are both present among the discovered selectors. This keeps the
// generated manifest's relationships consistent with its captured resources.
func buildRelationships(selectors []gamerav1alpha1.ResourceSelector) []gamerav1alpha1.RelationshipRule {
	present := map[string]bool{}
	for _, s := range selectors {
		present[s.Kind] = true
	}

	type candidate struct {
		name     string
		relType  string
		strategy gamerav1alpha1.RelationshipStrategy
		from, to gamerav1alpha1.ResourceSelector
	}

	candidates := []candidate{
		// Ownership (derived from ownerReferences).
		{"deployment-owns-replicaset", "OWNS", gamerav1alpha1.OwnerReferenceStrategy, deployment, replicaSet},
		{"replicaset-owns-pod", "OWNS", gamerav1alpha1.OwnerReferenceStrategy, replicaSet, pod},
		{"statefulset-owns-pod", "OWNS", gamerav1alpha1.OwnerReferenceStrategy, statefulSet, pod},
		{"daemonset-owns-pod", "OWNS", gamerav1alpha1.OwnerReferenceStrategy, daemonSet, pod},
		{"job-owns-pod", "OWNS", gamerav1alpha1.OwnerReferenceStrategy, job, pod},
		{"cronjob-owns-job", "OWNS", gamerav1alpha1.OwnerReferenceStrategy, cronJob, job},
		// Selection (Service selects Pods by label).
		{"service-selects-pod", "SELECTS", gamerav1alpha1.LabelSelectorStrategy, service, pod},
		// Configuration mounts (ConfigMap/Secret consumed by a Pod).
		{"configmap-mounts-pod", "MOUNTS", gamerav1alpha1.VolumeMountStrategy, configMap, pod},
		{"secret-mounts-pod", "MOUNTS", gamerav1alpha1.VolumeMountStrategy, secret, pod},
		// Identity (Pod runs under a ServiceAccount, via spec.serviceAccountName).
		{"serviceaccount-runs-pod", "RUNS", gamerav1alpha1.ServiceAccountStrategy, serviceAccount, pod},
	}

	var rules []gamerav1alpha1.RelationshipRule
	for _, c := range candidates {
		if !present[c.from.Kind] || !present[c.to.Kind] {
			continue
		}
		rules = append(rules, gamerav1alpha1.RelationshipRule{
			Name:     c.name,
			Type:     c.relType,
			Strategy: c.strategy,
			From:     c.from,
			To:       c.to,
		})
	}
	return rules
}
