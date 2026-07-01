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
	return cmd
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
	fmt.Fprintln(tw, "NAMESPACE\tNAME\tPROJECTION\tDISPLAY NAME")
	for _, v := range views {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
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
