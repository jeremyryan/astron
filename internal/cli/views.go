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
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newViewsCmd builds the "views" command group.
func newViewsCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "views",
		Aliases: []string{"view"},
		Short:   "Work with GraphViews",
	}
	cmd.AddCommand(newViewsListCmd(opts))
	cmd.AddCommand(newViewsAddCmd(opts))
	return cmd
}

// newViewsAddCmd builds "views add <namespace> <projection> <view>...".
func newViewsAddCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "add <namespace> <projection> <view>...",
		Aliases: []string{"create"},
		Short:   "Create one or more default GraphViews for a GraphProjection",
		Long: "add creates one or more of the built-in GraphViews for an existing\n" +
			"GraphProjection in a namespace.\n\n" +
			"Each view is one of the default views: " + strings.Join(defaultViewNames(), ", ") + ".\n" +
			"View names are case-insensitive and one or more may be given. Each created\n" +
			"GraphView is named \"<projection>-<view>\" (e.g. web-compute).",
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runViewsAdd(cmd, opts, args[0], args[1], args[2:])
		},
	}
	return cmd
}

// runViewsAdd validates the requested view names and creates a GraphView for
// each one, referencing the given projection.
func runViewsAdd(cmd *cobra.Command, opts *options, namespace, projection string, viewNames []string) error {
	// Resolve and de-duplicate the requested views up front so an unknown name
	// fails before anything is created.
	seen := map[string]bool{}
	var views []defaultViewCategory
	for _, n := range viewNames {
		cat, ok := lookupDefaultView(n)
		if !ok {
			return fmt.Errorf("unknown view %q: must be one of %s", n, strings.Join(defaultViewNames(), ", "))
		}
		if seen[cat.displayName] {
			continue
		}
		seen[cat.displayName] = true
		views = append(views, cat)
	}

	client, err := newClient(opts)
	if err != nil {
		return err
	}

	var errs []error
	for _, cat := range views {
		v := buildDefaultView(namespace, projection, cat)
		created, err := client.CreateView(cmd.Context(), v)
		if err != nil {
			errs = append(errs, fmt.Errorf("creating view %q: %w", cat.displayName, err))
			continue
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"graphview.astron.astron.io/%s created in namespace %s\n", created.Name, created.Namespace)
	}
	return errors.Join(errs...)
}

// viewsListOptions holds the flags for "views list".
type viewsListOptions struct {
	*options
	// namespace narrows the result to GraphViews in this namespace.
	namespace string
	// projection narrows the result to GraphViews referencing this
	// GraphProjection (by name); requires namespace to identify the projection.
	projection string
}

// newViewsListCmd builds "views list".
func newViewsListCmd(opts *options) *cobra.Command {
	vopts := &viewsListOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List GraphViews defined in the cluster",
		Long: "list shows the saved GraphViews and the GraphProjection each one\n" +
			"filters.\n\n" +
			"Use --namespace to narrow the list to GraphViews in a single namespace,\n" +
			"and --projection to narrow it to the GraphViews associated with a\n" +
			"specific GraphProjection (--projection requires --namespace to identify\n" +
			"the projection).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runViewsList(cmd, vopts)
		},
	}

	cmd.Flags().StringVarP(&vopts.namespace, "namespace", "n", "",
		"Only show GraphViews in this namespace")
	cmd.Flags().StringVar(&vopts.projection, "projection", "",
		"Only show GraphViews associated with this GraphProjection (requires --namespace)")

	return cmd
}

func runViewsList(cmd *cobra.Command, vopts *viewsListOptions) error {
	if vopts.projection != "" && vopts.namespace == "" {
		return fmt.Errorf("--projection requires --namespace to identify the projection")
	}

	client, err := newClient(vopts.options)
	if err != nil {
		return err
	}

	var views []View
	if vopts.projection != "" {
		// Narrow to views referencing the given projection (name + namespace),
		// filtered server-side.
		views, err = client.ListViews(cmd.Context(), vopts.namespace, vopts.projection)
	} else {
		// Otherwise fetch all and, when requested, keep only those whose own
		// namespace matches.
		views, err = client.ListViews(cmd.Context(), "", "")
	}
	if err != nil {
		return err
	}
	if vopts.projection == "" && vopts.namespace != "" {
		views = filterViewsByNamespace(views, vopts.namespace)
	}

	if vopts.output == outputJSON {
		return printJSON(cmd.OutOrStdout(), views)
	}

	tw := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(tw, "NAMESPACE\tNAME\tPROJECTION\tDISPLAY NAME")
	for _, v := range views {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			v.Namespace, v.Name, projectionLabel(v), dash(v.DisplayName))
	}
	return tw.Flush()
}

// filterViewsByNamespace keeps only the views whose own namespace matches ns.
func filterViewsByNamespace(views []View, ns string) []View {
	out := make([]View, 0, len(views))
	for _, v := range views {
		if v.Namespace == ns {
			out = append(out, v)
		}
	}
	return out
}

// projectionLabel renders a view's projectionRef as "namespace/name". A
// projectionRef without a namespace defaults to the view's own namespace.
func projectionLabel(v View) string {
	ns := v.ProjectionRef.Namespace
	if ns == "" {
		ns = v.Namespace
	}
	return fmt.Sprintf("%s/%s", ns, v.ProjectionRef.Name)
}
