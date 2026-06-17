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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
)

// graphProjectionGVR is the resource used to apply generated manifests.
var graphProjectionGVR = schema.GroupVersionResource{
	Group:    gamerav1alpha1.GroupVersion.Group,
	Version:  gamerav1alpha1.GroupVersion.Version,
	Resource: "graphprojections",
}

// generateOptions holds the flags for "projections generate".
type generateOptions struct {
	*options
	kube kubeOptions

	name              string
	neo4jURI          string
	neo4jDatabase     string
	neo4jSecret       string
	neo4jSecretNS     string
	resyncInterval    string
	withRelationships bool
	exclude           []string
	// allResources includes every namespaced kind that has instances, rather
	// than just the standard common set.
	allResources bool

	// outputFile, when set (and not "-"), writes the manifest to a file instead
	// of stdout.
	outputFile string
	// apply creates/updates the GraphProjection in the cluster instead of
	// emitting its manifest.
	apply bool
}

// projectionManifest is a YAML-friendly wrapper used to emit a clean
// GraphProjection manifest (without the status subresource or server-managed
// metadata). It reuses the real Spec type so it stays in sync with the API.
type projectionManifest struct {
	APIVersion string                             `json:"apiVersion"`
	Kind       string                             `json:"kind"`
	Metadata   manifestMeta                       `json:"metadata"`
	Spec       gamerav1alpha1.GraphProjectionSpec `json:"spec"`
}

type manifestMeta struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// newGenerateCmd builds "projections generate <namespace>".
func newGenerateCmd(opts *options) *cobra.Command {
	gopts := &generateOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "generate <namespace>",
		Short: "Generate a GraphProjection for the resource types in a namespace",
		Long: "generate inspects a namespace in the cluster and produces a\n" +
			"GraphProjection manifest scoped to that namespace.\n\n" +
			"By default it includes only the standard set of common resource kinds\n" +
			"(workloads, Services, ConfigMaps/Secrets, PVCs, Ingress, ...) that\n" +
			"currently have at least one instance in the namespace. Pass\n" +
			"--all-resources to include every namespaced kind that has instances.\n\n" +
			"By default the manifest is written to stdout. Use --output-file to write\n" +
			"it to a file, or --apply to create/update the GraphProjection in the\n" +
			"cluster instead of emitting YAML.\n\n" +
			"It talks directly to the Kubernetes API (via your kubeconfig), not the\n" +
			"Gamera read API, so the --server flag does not apply here.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGenerate(cmd, gopts, args[0])
		},
	}

	cmd.Flags().StringVar(&gopts.kube.kubeconfig, "kubeconfig", "",
		"Path to the kubeconfig file (defaults to KUBECONFIG or ~/.kube/config)")
	cmd.Flags().StringVar(&gopts.kube.context, "context", "",
		"Name of the kubeconfig context to use")
	cmd.Flags().StringVar(&gopts.name, "name", "",
		"Name of the generated GraphProjection (defaults to the namespace)")
	cmd.Flags().StringVar(&gopts.neo4jURI, "neo4j-uri", "neo4j://gamera-neo4j.gamera.svc:7687",
		"Neo4J connection URI to write into the manifest")
	cmd.Flags().StringVar(&gopts.neo4jDatabase, "neo4j-database", "neo4j",
		"Neo4J database name")
	cmd.Flags().StringVar(&gopts.neo4jSecret, "neo4j-secret", "neo4j-credentials",
		"Name of the Secret holding Neo4J credentials")
	cmd.Flags().StringVar(&gopts.neo4jSecretNS, "neo4j-secret-namespace", "",
		"Namespace of the Neo4J credentials Secret (defaults to the projection's namespace)")
	cmd.Flags().StringVar(&gopts.resyncInterval, "resync-interval", "5m",
		"Full reconciliation interval to set on the projection")
	cmd.Flags().BoolVar(&gopts.withRelationships, "with-relationships", true,
		"Include well-known relationship rules (OWNS/SELECTS/MOUNTS) for the discovered kinds")
	cmd.Flags().StringSliceVar(&gopts.exclude, "exclude", nil,
		"Resource Kinds to exclude from the projection (e.g. Event,EndpointSlice)")
	cmd.Flags().BoolVar(&gopts.allResources, "all-resources", false,
		"Include every namespaced kind that has instances, instead of the standard common set")
	cmd.Flags().StringVarP(&gopts.outputFile, "output-file", "f", "",
		"Write the generated manifest to this file instead of stdout (\"-\" means stdout)")
	cmd.Flags().BoolVar(&gopts.apply, "apply", false,
		"Create/update the GraphProjection in the cluster instead of emitting its manifest")

	return cmd
}

func runGenerate(cmd *cobra.Command, gopts *generateOptions, namespace string) error {
	if gopts.apply && gopts.outputFile != "" {
		return fmt.Errorf("--apply cannot be combined with --output-file")
	}

	cfg, err := gopts.kube.restConfig()
	if err != nil {
		return err
	}
	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return fmt.Errorf("creating discovery client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	selectors, err := discoverKinds(cmd.Context(), disco, dyn, namespace, gopts.exclude, !gopts.allResources)
	if err != nil {
		return err
	}
	if len(selectors) == 0 {
		if !gopts.allResources {
			return fmt.Errorf("no standard resource kinds with instances found in namespace %q (try --all-resources)", namespace)
		}
		return fmt.Errorf("no resource types with instances found in namespace %q", namespace)
	}

	manifest := buildManifest(gopts, namespace, selectors)

	if gopts.apply {
		return applyProjection(cmd, dyn, manifest)
	}
	return writeManifest(cmd, gopts.outputFile, manifest)
}

// writeManifest marshals the manifest to YAML and writes it to the given path,
// or to stdout when path is empty or "-".
func writeManifest(cmd *cobra.Command, path string, manifest projectionManifest) error {
	out, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshaling manifest: %w", err)
	}
	if path == "" || path == "-" {
		_, err = fmt.Fprint(cmd.OutOrStdout(), string(out))
		return err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s\n", path)
	return nil
}

// applyProjection creates the generated GraphProjection in the cluster, or
// updates it in place when one with the same name already exists.
func applyProjection(cmd *cobra.Command, dyn dynamic.Interface, manifest projectionManifest) error {
	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}
	u := &unstructured.Unstructured{}
	if uErr := u.UnmarshalJSON(data); uErr != nil {
		return fmt.Errorf("decoding manifest: %w", uErr)
	}

	ctx := cmd.Context()
	ns := manifest.Metadata.Namespace
	name := manifest.Metadata.Name
	ri := dyn.Resource(graphProjectionGVR).Namespace(ns)

	verb := "created"
	if _, err := ri.Create(ctx, u, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating GraphProjection %s/%s: %w", ns, name, err)
		}
		// Already present: carry over the resourceVersion and update in place.
		existing, getErr := ri.Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("fetching existing GraphProjection %s/%s: %w", ns, name, getErr)
		}
		u.SetResourceVersion(existing.GetResourceVersion())
		if _, updErr := ri.Update(ctx, u, metav1.UpdateOptions{}); updErr != nil {
			return fmt.Errorf("updating GraphProjection %s/%s: %w", ns, name, updErr)
		}
		verb = "configured"
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "graphprojection.gamera.gamera.io/%s %s in namespace %s\n", name, verb, ns)
	return err
}

// discoverKinds enumerates the namespaced, listable resource types in the
// cluster and returns selectors for those that currently have at least one
// instance in the given namespace. Subresources and excluded kinds are skipped.
func discoverKinds(ctx context.Context, disco discovery.DiscoveryInterface, dyn dynamic.Interface, namespace string, exclude []string, standardOnly bool) ([]gamerav1alpha1.ResourceSelector, error) {
	lists, err := disco.ServerPreferredNamespacedResources()
	if err != nil && len(lists) == 0 {
		return nil, fmt.Errorf("discovering namespaced resources: %w", err)
	}
	return selectNamespacedKinds(ctx, lists, dyn, namespace, exclude, standardOnly)
}

// selectNamespacedKinds filters discovered resource lists to the namespaced,
// listable kinds that have at least one instance in the namespace. It is split
// out from discovery so it can be unit-tested with a fake dynamic client.
func selectNamespacedKinds(ctx context.Context, lists []*metav1.APIResourceList, dyn dynamic.Interface, namespace string, exclude []string, standardOnly bool) ([]gamerav1alpha1.ResourceSelector, error) {
	excluded := map[string]bool{}
	for _, k := range exclude {
		excluded[strings.ToLower(k)] = true
	}

	var standard map[string]bool
	if standardOnly {
		standard = standardKindSet()
	}

	seen := map[string]bool{}
	var selectors []gamerav1alpha1.ResourceSelector

	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, res := range list.APIResources {
			if strings.Contains(res.Name, "/") { // subresource
				continue
			}
			if !canList(res.Verbs) {
				continue
			}
			if excluded[strings.ToLower(res.Kind)] || seen[res.Kind] {
				continue
			}
			if standard != nil && !standard[kindKey(gv.Group, res.Kind)] {
				continue
			}

			gvr := schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: res.Name}
			has, listErr := hasInstances(ctx, dyn, gvr, namespace)
			if listErr != nil {
				// A type we cannot list (RBAC, conversion errors) is skipped rather
				// than failing the whole command.
				continue
			}
			if !has {
				continue
			}

			seen[res.Kind] = true
			selectors = append(selectors, gamerav1alpha1.ResourceSelector{
				Group:   gv.Group,
				Version: gv.Version,
				Kind:    res.Kind,
			})
		}
	}

	sortSelectors(selectors)
	return selectors, nil
}

// hasInstances reports whether the given resource has at least one object in the
// namespace, fetching a single item to keep the request cheap.
func hasInstances(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace string) (bool, error) {
	list, err := dyn.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return false, err
	}
	return len(list.Items) > 0, nil
}

// canList reports whether a resource's verbs include "list".
func canList(verbs metav1.Verbs) bool {
	return slices.Contains(verbs, "list")
}

// kindKey is the lookup key used for the standard-kind set, "<group>/<kind>"
// lowercased (group empty for the core API group).
func kindKey(group, kind string) string {
	return strings.ToLower(group + "/" + kind)
}

// standardKindSet is the default set of common resource kinds included by
// "projections generate" unless --all-resources is given. It mirrors the
// operator's built-in defaults (workloads, config, storage, networking) and
// deliberately excludes noisy/low-signal kinds like Events, EndpointSlices,
// Leases and ControllerRevisions.
func standardKindSet() map[string]bool {
	kinds := []schema.GroupKind{
		{Group: "apps", Kind: "Deployment"},
		{Group: "apps", Kind: "StatefulSet"},
		{Group: "apps", Kind: "DaemonSet"},
		{Group: "apps", Kind: "ReplicaSet"},
		{Group: "batch", Kind: "Job"},
		{Group: "batch", Kind: "CronJob"},
		{Group: "", Kind: "Pod"},
		{Group: "", Kind: "Service"},
		{Group: "", Kind: "ConfigMap"},
		{Group: "", Kind: "Secret"},
		{Group: "", Kind: "PersistentVolumeClaim"},
		{Group: "", Kind: "ServiceAccount"},
		{Group: "networking.k8s.io", Kind: "Ingress"},
		{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute"},
		{Group: "gateway.networking.k8s.io", Kind: "Gateway"},
	}
	set := make(map[string]bool, len(kinds))
	for _, gk := range kinds {
		set[kindKey(gk.Group, gk.Kind)] = true
	}
	return set
}

// buildManifest assembles the GraphProjection manifest from discovered kinds.
func buildManifest(gopts *generateOptions, namespace string, selectors []gamerav1alpha1.ResourceSelector) projectionManifest {
	name := gopts.name
	if name == "" {
		name = namespace
	}

	spec := gamerav1alpha1.GraphProjectionSpec{
		Neo4j: gamerav1alpha1.Neo4jConnection{
			URI:      gopts.neo4jURI,
			Database: gopts.neo4jDatabase,
			AuthSecretRef: gamerav1alpha1.SecretReference{
				Name:      gopts.neo4jSecret,
				Namespace: gopts.neo4jSecretNS,
			},
		},
		Scope: gamerav1alpha1.ProjectionScope{
			Namespaces: []string{namespace},
			Resources:  selectors,
		},
	}

	if d, err := parseDuration(gopts.resyncInterval); err == nil && d != nil {
		spec.ResyncInterval = d
	}

	if gopts.withRelationships {
		spec.Relationships = buildRelationships(selectors)
	}

	return projectionManifest{
		APIVersion: gamerav1alpha1.GroupVersion.String(),
		Kind:       "GraphProjection",
		Metadata:   manifestMeta{Name: name, Namespace: namespace},
		Spec:       spec,
	}
}

// parseDuration parses a duration flag into a metav1.Duration pointer, returning
// nil for an empty value.
func parseDuration(s string) (*metav1.Duration, error) {
	if s == "" {
		return nil, nil
	}
	var d metav1.Duration
	if err := d.UnmarshalJSON(fmt.Appendf(nil, "%q", s)); err != nil {
		return nil, err
	}
	return &d, nil
}

// sortSelectors orders selectors by group then kind for stable output.
func sortSelectors(selectors []gamerav1alpha1.ResourceSelector) {
	sort.Slice(selectors, func(i, j int) bool {
		if selectors[i].Group != selectors[j].Group {
			return selectors[i].Group < selectors[j].Group
		}
		return selectors[i].Kind < selectors[j].Kind
	})
}
