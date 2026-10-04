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
	"slices"

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

	certManagerGroup   = "cert-manager.io"
	certificate        = astronv1alpha1.ResourceSelector{Group: certManagerGroup, Version: "v1", Kind: "Certificate"}
	certificateRequest = astronv1alpha1.ResourceSelector{Group: certManagerGroup, Version: "v1", Kind: "CertificateRequest"}
	issuer             = astronv1alpha1.ResourceSelector{Group: certManagerGroup, Version: "v1", Kind: "Issuer"}
	clusterIssuer      = astronv1alpha1.ResourceSelector{Group: certManagerGroup, Version: "v1", Kind: "ClusterIssuer"}

	certManagerACMEGroup = "acme.cert-manager.io"
	acmeOrder            = astronv1alpha1.ResourceSelector{Group: certManagerACMEGroup, Version: "v1", Kind: "Order"}
	acmeChallenge        = astronv1alpha1.ResourceSelector{Group: certManagerACMEGroup, Version: "v1", Kind: "Challenge"}
)

// buildRelationships returns the subset of well-known relationship rules whose
// endpoint kinds are both present among the discovered selectors. This keeps the
// generated manifest's relationships consistent with its captured resources.
func buildRelationships(selectors []astronv1alpha1.ResourceSelector) []astronv1alpha1.RelationshipRule {
	present := presentKinds(selectors)

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

// fieldRefCandidate describes a potential FieldReference relationship rule
// for a crdRelationshipPack: a From kind, an ordered list of possible To
// kinds (for a reference field that may point at more than one kind, e.g.
// cert-manager's issuerRef), and the fieldRef configuration to use.
type fieldRefCandidate struct {
	name, relType string
	from          astronv1alpha1.ResourceSelector
	// toCandidates are tried in order; the rule is only emitted if at least one
	// is present among the discovered selectors, and the first present one
	// becomes the rule's static `to` (the fallback target kind used when
	// fieldRef.kindPath doesn't resolve a candidate).
	toCandidates []astronv1alpha1.ResourceSelector
	// strategy defaults to FieldReference (using fieldRef) when empty; set it
	// to emit a rule using another built-in strategy (e.g. OwnerReference),
	// in which case fieldRef is unused.
	strategy astronv1alpha1.RelationshipStrategy
	fieldRef astronv1alpha1.FieldReferenceSpec
}

// crdRelationshipPack is a named, shippable bundle of FieldReference
// relationship rules for a well-known CRD-defined API group (see
// docs/relationships.md). It is applied automatically by buildCRDRelationships
// when at least one Kind from the pack's group is among the discovered/included
// selectors (e.g. via --include-group cert-manager.io), the same way
// --include-group lets you pull in a whole group's Kinds without naming them
// individually.
type crdRelationshipPack struct {
	// groups are the API groups the pack covers; it applies when at least one
	// Kind from any of them is among the selectors.
	groups []string
	rules  []fieldRefCandidate
}

// crdRelationshipPacks is the built-in registry of known CRD relationship
// packs. cert-manager is the first and, for now, only entry; see
// docs/relationship-extensibility-design.md for the FieldReference strategy
// this is built on, and the "relationship packs" follow-on it anticipates.
var crdRelationshipPacks = []crdRelationshipPack{
	{
		groups: []string{certManagerGroup, certManagerACMEGroup},
		rules: []fieldRefCandidate{
			{
				name: "certificate-uses-secret", relType: "USES_SECRET",
				from: certificate, toCandidates: []astronv1alpha1.ResourceSelector{secret},
				fieldRef: astronv1alpha1.FieldReferenceSpec{NamePath: "spec.secretName"},
			},
			{
				name: "certificate-issued-by", relType: "ISSUED_BY",
				from: certificate, toCandidates: []astronv1alpha1.ResourceSelector{issuer, clusterIssuer},
				fieldRef: astronv1alpha1.FieldReferenceSpec{
					NamePath: "spec.issuerRef.name",
					KindPath: "spec.issuerRef.kind",
				},
			},
			{
				name: "certificaterequest-issued-by", relType: "ISSUED_BY",
				from: certificateRequest, toCandidates: []astronv1alpha1.ResourceSelector{issuer, clusterIssuer},
				fieldRef: astronv1alpha1.FieldReferenceSpec{
					NamePath: "spec.issuerRef.name",
					KindPath: "spec.issuerRef.kind",
				},
			},
			// ACME issuance chain: cert-manager sets ownerReferences
			// CertificateRequest -> Order -> Challenge.
			{
				name: "certificaterequest-owns-order", relType: "OWNS", strategy: astronv1alpha1.OwnerReferenceStrategy,
				from: certificateRequest, toCandidates: []astronv1alpha1.ResourceSelector{acmeOrder},
			},
			{
				name: "order-owns-challenge", relType: "OWNS", strategy: astronv1alpha1.OwnerReferenceStrategy,
				from: acmeOrder, toCandidates: []astronv1alpha1.ResourceSelector{acmeChallenge},
			},
		},
	},
}

// buildCRDRelationships returns FieldReference relationship rules from any
// known relationship pack (crdRelationshipPacks) whose API group has at least
// one Kind among the discovered selectors. Each candidate rule within a
// matching pack is further gated the same way buildRelationships gates its
// own candidates: both endpoints must actually be present, so a pack whose
// group is only partially included (e.g. Certificate without Issuer or
// ClusterIssuer) doesn't emit a rule with a dangling target.
func buildCRDRelationships(selectors []astronv1alpha1.ResourceSelector) []astronv1alpha1.RelationshipRule {
	present := presentKinds(selectors)
	groupPresent := map[string]bool{}
	for _, s := range selectors {
		groupPresent[s.Group] = true
	}

	var rules []astronv1alpha1.RelationshipRule
	for _, pack := range crdRelationshipPacks {
		if !slices.ContainsFunc(pack.groups, func(g string) bool { return groupPresent[g] }) {
			continue
		}
		for _, c := range pack.rules {
			to, ok := gateRule(c.from, c.toCandidates, present)
			if !ok {
				continue
			}
			rule := astronv1alpha1.RelationshipRule{
				Name:     c.name,
				Type:     c.relType,
				Strategy: astronv1alpha1.FieldReferenceStrategy,
				From:     c.from,
				To:       to,
			}
			if c.strategy != "" {
				rule.Strategy = c.strategy
			} else {
				fieldRef := c.fieldRef // copy: each rule needs its own pointer
				rule.FieldRef = &fieldRef
			}
			rules = append(rules, rule)
		}
	}
	return rules
}

// presentKinds indexes a resource selector list by Kind, for the
// endpoint-presence gating buildRelationships, buildCRDRelationships and
// buildPackRelationships all perform.
func presentKinds(selectors []astronv1alpha1.ResourceSelector) map[string]bool {
	present := make(map[string]bool, len(selectors))
	for _, s := range selectors {
		present[s.Kind] = true
	}
	return present
}

// gateRule reports whether a from/toCandidates pair should produce a rule
// given which kinds are present (from must be present, and at least one
// toCandidate must be), returning the resolved static "to" selector: the
// first present candidate, used as the rule's fallback target kind (relevant
// when a FieldReference rule's kindPath can resolve to more than one of
// them).
func gateRule(
	from astronv1alpha1.ResourceSelector, toCandidates []astronv1alpha1.ResourceSelector, present map[string]bool,
) (astronv1alpha1.ResourceSelector, bool) {
	if !present[from.Kind] {
		return astronv1alpha1.ResourceSelector{}, false
	}
	for _, c := range toCandidates {
		if present[c.Kind] {
			return c, true
		}
	}
	return astronv1alpha1.ResourceSelector{}, false
}
