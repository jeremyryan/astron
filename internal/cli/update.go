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
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	gamerav1alpha1 "github.com/project-gamera/gamera/api/v1alpha1"
)

// updateOptions holds the flags for "projections update".
type updateOptions struct {
	*options
	kube kubeOptions

	// add / remove are the resource types to add to or remove from the
	// projection's scope.resources allow-list.
	add    []string
	remove []string
}

// newProjectionsUpdateCmd builds "projections update <namespace> <name>".
func newProjectionsUpdateCmd(opts *options) *cobra.Command {
	uopts := &updateOptions{options: opts}

	cmd := &cobra.Command{
		Use:     "update <namespace> <name>",
		Aliases: []string{"edit"},
		Short:   "Add or remove resource types on an existing GraphProjection",
		Long: "update modifies the scope.resources allow-list of an existing\n" +
			"GraphProjection, adding and/or removing one or more resource types.\n\n" +
			"Resource types are given as \"[group/]version/Kind\" (e.g. apps/v1/Deployment\n" +
			"or v1/Pod) for --add; --remove may use the same form or just the Kind.\n" +
			"Both flags accept a comma-separated list and may be repeated.\n\n" +
			"Note: when the projection has no explicit resource list (it captures the\n" +
			"built-in default set), adding resources creates an explicit allow-list\n" +
			"containing only the added kinds.\n\n" +
			"It talks directly to the Kubernetes API (via your kubeconfig), not the\n" +
			"Gamera read API, so the --server flag does not apply here.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, uopts, args[0], args[1])
		},
	}

	cmd.Flags().StringVar(&uopts.kube.kubeconfig, "kubeconfig", "",
		"Path to the kubeconfig file (defaults to KUBECONFIG or ~/.kube/config)")
	cmd.Flags().StringVar(&uopts.kube.context, "context", "",
		"Name of the kubeconfig context to use")
	cmd.Flags().StringSliceVar(&uopts.add, "add", nil,
		"Resource type(s) to add, as [group/]version/Kind (repeatable, comma-separated)")
	cmd.Flags().StringSliceVar(&uopts.remove, "remove", nil,
		"Resource type(s) to remove, as [group/]version/Kind or Kind (repeatable, comma-separated)")

	return cmd
}

// runUpdate fetches the projection, applies the requested resource changes and
// writes it back.
func runUpdate(cmd *cobra.Command, uopts *updateOptions, namespace, name string) error {
	if len(uopts.add) == 0 && len(uopts.remove) == 0 {
		return fmt.Errorf("nothing to do: specify at least one --add or --remove")
	}

	add, err := parseResourceSelectors(uopts.add)
	if err != nil {
		return err
	}
	removeKinds, err := parseRemoveKinds(uopts.remove)
	if err != nil {
		return err
	}

	cfg, err := uopts.kube.restConfig()
	if err != nil {
		return err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("creating dynamic client: %w", err)
	}

	return updateProjectionResources(cmd, dyn, namespace, name, add, removeKinds)
}

// updateProjectionResources fetches the named GraphProjection, applies the add
// and remove changes to its scope.resources list, and updates it in place. It is
// split out so it can be unit-tested with a fake dynamic client.
func updateProjectionResources(cmd *cobra.Command, dyn dynamic.Interface, namespace, name string, add []gamerav1alpha1.ResourceSelector, removeKinds map[string]bool) error {
	ctx := cmd.Context()
	ri := dyn.Resource(graphProjectionGVR).Namespace(namespace)

	obj, err := ri.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("GraphProjection %s/%s not found", namespace, name)
		}
		return fmt.Errorf("fetching GraphProjection %s/%s: %w", namespace, name, err)
	}

	existing, err := readResourceSelectors(obj)
	if err != nil {
		return fmt.Errorf("reading resources of GraphProjection %s/%s: %w", namespace, name, err)
	}

	updated, added, removed := applyResourceChanges(existing, add, removeKinds)
	if added == 0 && removed == 0 {
		_, ferr := fmt.Fprintf(cmd.OutOrStdout(),
			"graphprojection.gamera.gamera.io/%s unchanged in namespace %s\n", name, namespace)
		return ferr
	}

	if err := writeResourceSelectors(obj, updated); err != nil {
		return fmt.Errorf("updating resources of GraphProjection %s/%s: %w", namespace, name, err)
	}
	if _, err := ri.Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating GraphProjection %s/%s: %w", namespace, name, err)
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"graphprojection.gamera.gamera.io/%s updated in namespace %s (+%d/-%d resources, %d total)\n",
		name, namespace, added, removed, len(updated))
	return err
}

// applyResourceChanges returns the resource list after removing the given kinds
// and adding/overriding the given selectors (deduplicated by kind). It also
// reports how many entries were effectively added and removed.
func applyResourceChanges(existing, add []gamerav1alpha1.ResourceSelector, removeKinds map[string]bool) (result []gamerav1alpha1.ResourceSelector, added, removed int) {
	out := make([]gamerav1alpha1.ResourceSelector, 0, len(existing)+len(add))
	for _, r := range existing {
		if removeKinds[r.Kind] {
			removed++
			continue
		}
		out = append(out, r)
	}
	for _, a := range add {
		replaced := false
		for i := range out {
			if out[i].Kind == a.Kind {
				if out[i] != a {
					out[i] = a
					added++
				}
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, a)
			added++
		}
	}
	return out, added, removed
}

// readResourceSelectors reads spec.scope.resources from an unstructured
// GraphProjection into a typed slice.
func readResourceSelectors(obj *unstructured.Unstructured) ([]gamerav1alpha1.ResourceSelector, error) {
	raw, found, err := unstructured.NestedSlice(obj.Object, "spec", "scope", "resources")
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	out := make([]gamerav1alpha1.ResourceSelector, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unexpected resource entry %T", item)
		}
		group, _, _ := unstructured.NestedString(m, "group")
		version, _, _ := unstructured.NestedString(m, "version")
		kind, _, _ := unstructured.NestedString(m, "kind")
		out = append(out, gamerav1alpha1.ResourceSelector{Group: group, Version: version, Kind: kind})
	}
	return out, nil
}

// writeResourceSelectors writes the typed slice back to spec.scope.resources on
// the unstructured object, omitting empty group/version fields.
func writeResourceSelectors(obj *unstructured.Unstructured, selectors []gamerav1alpha1.ResourceSelector) error {
	raw := make([]any, 0, len(selectors))
	for _, s := range selectors {
		m := map[string]any{"kind": s.Kind}
		if s.Group != "" {
			m["group"] = s.Group
		}
		if s.Version != "" {
			m["version"] = s.Version
		}
		raw = append(raw, m)
	}
	return unstructured.SetNestedSlice(obj.Object, raw, "spec", "scope", "resources")
}

// parseResourceSelectors parses a list of "[group/]version/Kind" (or "Kind")
// strings into ResourceSelectors.
func parseResourceSelectors(specs []string) ([]gamerav1alpha1.ResourceSelector, error) {
	out := make([]gamerav1alpha1.ResourceSelector, 0, len(specs))
	for _, s := range specs {
		sel, err := parseResourceSelector(s)
		if err != nil {
			return nil, err
		}
		out = append(out, sel)
	}
	return out, nil
}

// parseRemoveKinds parses the --remove values (each "[group/]version/Kind" or a
// bare "Kind") into a set of kinds to remove.
func parseRemoveKinds(specs []string) (map[string]bool, error) {
	out := make(map[string]bool, len(specs))
	for _, s := range specs {
		sel, err := parseResourceSelector(s)
		if err != nil {
			return nil, err
		}
		out[sel.Kind] = true
	}
	return out, nil
}

// parseResourceSelector parses a single resource-type string. Accepted forms:
//
//	Kind                    -> {Kind}
//	version/Kind            -> {Version, Kind}
//	group/version/Kind      -> {Group, Version, Kind}
//
// The core API group is written as an empty group, so "v1/Pod" and "/v1/Pod"
// are equivalent.
func parseResourceSelector(spec string) (gamerav1alpha1.ResourceSelector, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return gamerav1alpha1.ResourceSelector{}, fmt.Errorf("empty resource type")
	}
	parts := strings.Split(spec, "/")
	var sel gamerav1alpha1.ResourceSelector
	switch len(parts) {
	case 1:
		sel.Kind = parts[0]
	case 2:
		sel.Version = parts[0]
		sel.Kind = parts[1]
	case 3:
		sel.Group = parts[0]
		sel.Version = parts[1]
		sel.Kind = parts[2]
	default:
		return gamerav1alpha1.ResourceSelector{}, fmt.Errorf("invalid resource type %q: expected [group/]version/Kind or Kind", spec)
	}
	if sel.Kind == "" {
		return gamerav1alpha1.ResourceSelector{}, fmt.Errorf("invalid resource type %q: missing Kind", spec)
	}
	return sel, nil
}
