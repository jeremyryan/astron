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
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
)

// newViewsCmd builds the "views" command group.
// kindModeShow is the GraphView filter mode that shows only the listed kinds
// (allow-list), as opposed to hiding listed kinds.
const kindModeShow = "show"

func newViewsCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "views",
		Aliases: []string{"view"},
		Short:   "Work with GraphViews",
	}
	cmd.AddCommand(newViewsListCmd(opts))
	cmd.AddCommand(newViewsAddCmd(opts))
	cmd.AddCommand(newViewsDefaultsCmd(opts))
	cmd.AddCommand(newViewsRmCmd(opts))
	cmd.AddCommand(newViewsGenerateCmd(opts))
	cmd.AddCommand(newViewsNewCmd(opts))
	cmd.AddCommand(newViewsUpdateCmd(opts))
	cmd.AddCommand(newViewsDescribeCmd(opts))
	return cmd
}

// viewsUpdateOptions holds the flags for "views update".
type viewsUpdateOptions struct {
	*options

	addKinds    []string
	removeKinds []string
	displayName string
	description string
}

// newViewsUpdateCmd builds "views update <namespace> <name>".
func newViewsUpdateCmd(opts *options) *cobra.Command {
	uopts := &viewsUpdateOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "update <namespace> <name>",
		Short: "Modify an existing GraphView",
		Long: "update modifies an existing GraphView in place.\n\n" +
			"--add-kinds makes the given resource kinds visible in the view and\n" +
			"--remove-kinds hides them, honoring the view's kind mode: for an\n" +
			"allow-list view (kindMode \"show\") the visible-kinds list is edited,\n" +
			"and for a deny-list view the hidden-kinds list is edited. Both flags\n" +
			"accept comma-separated lists and may be repeated.\n\n" +
			"--display-name and --description replace the view's metadata when set.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runViewsUpdate(cmd, uopts, args[0], args[1])
		},
	}

	cmd.Flags().StringSliceVar(&uopts.addKinds, "add-kinds", nil,
		"Resource Kinds to make visible in the view (e.g. Pod,Service)")
	cmd.Flags().StringSliceVar(&uopts.removeKinds, "remove-kinds", nil,
		"Resource Kinds to hide from the view (e.g. Secret)")
	cmd.Flags().StringVar(&uopts.displayName, "display-name", "",
		"New human-friendly name for the view")
	cmd.Flags().StringVar(&uopts.description, "description", "",
		"New description for the view")

	return cmd
}

func runViewsUpdate(cmd *cobra.Command, uopts *viewsUpdateOptions, namespace, name string) error {
	add := parseKindArgs(uopts.addKinds)
	remove := parseKindArgs(uopts.removeKinds)
	if len(add) == 0 && len(remove) == 0 && uopts.displayName == "" && uopts.description == "" {
		return fmt.Errorf("nothing to update: pass --add-kinds, --remove-kinds, --display-name and/or --description")
	}
	if overlap := intersect(add, remove); len(overlap) > 0 {
		return fmt.Errorf("kinds cannot be both added and removed: %s", strings.Join(overlap, ", "))
	}

	client, err := newClient(uopts.options)
	if err != nil {
		return err
	}

	view, err := client.GetView(cmd.Context(), namespace, name)
	if err != nil {
		return err
	}

	updateViewKinds(&view, add, remove)
	if uopts.displayName != "" {
		view.DisplayName = uopts.displayName
	}
	if uopts.description != "" {
		view.Description = uopts.description
	}

	updated, err := client.UpdateView(cmd.Context(), view)
	if err != nil {
		return fmt.Errorf("updating view %q: %w", name, err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"graphview.astron.astron.io/%s configured in namespace %s\n", updated.Name, updated.Namespace)
	return err
}

// updateViewKinds applies kind additions/removals to a view's filters,
// honoring its kind mode: allow-list views (kindMode "show") edit
// visibleKinds, deny-list views edit hiddenKinds.
func updateViewKinds(v *View, add, remove []string) {
	if strings.EqualFold(v.Filters.KindMode, "show") {
		// Allow-list: adding shows a kind, removing hides it.
		v.Filters.VisibleKinds = addKinds(removeKinds(v.Filters.VisibleKinds, remove), add)
		return
	}
	// Deny-list: adding un-hides a kind, removing hides it.
	v.Filters.HiddenKinds = addKinds(removeKinds(v.Filters.HiddenKinds, add), remove)
}

// addKinds returns kinds with the additions merged in, de-duplicated and
// sorted.
func addKinds(kinds, add []string) []string {
	return parseKindArgs(append(append([]string(nil), kinds...), add...))
}

// removeKinds returns kinds without the removed entries (case-insensitive).
func removeKinds(kinds, remove []string) []string {
	drop := map[string]bool{}
	for _, k := range remove {
		drop[strings.ToLower(k)] = true
	}
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if !drop[strings.ToLower(k)] {
			out = append(out, k)
		}
	}
	return out
}

// intersect returns the entries present in both lists (case-insensitive),
// using the spelling from a.
func intersect(a, b []string) []string {
	inB := map[string]bool{}
	for _, k := range b {
		inB[strings.ToLower(k)] = true
	}
	var out []string
	for _, k := range a {
		if inB[strings.ToLower(k)] {
			out = append(out, k)
		}
	}
	return out
}

// viewsNewOptions holds the flags for "views new".
type viewsNewOptions struct {
	*options

	// displayName is the human-friendly name shown in the UI (defaults to the
	// view's resource name).
	displayName string
	// description is an optional free-form description of the view.
	description string
}

// newViewsNewCmd builds "views new <namespace> <projection> <name> <kind>...".
func newViewsNewCmd(opts *options) *cobra.Command {
	nopts := &viewsNewOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "new <namespace> <projection> <name> <kind>...",
		Short: "Create a custom GraphView showing a chosen set of resource kinds",
		Long: "new creates a GraphView with the given name for an existing\n" +
			"GraphProjection, showing only the specified resource kinds.\n\n" +
			"Kinds are given as one or more arguments and may also be\n" +
			"comma-separated (e.g. \"Pod,Service\" or \"Pod Service\"). The view uses\n" +
			"an allow-list (kindMode \"show\"), so only the listed kinds are visible.",
		Args: cobra.MinimumNArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runViewsNew(cmd, nopts, args[0], args[1], args[2], args[3:])
		},
	}

	cmd.Flags().StringVar(&nopts.displayName, "display-name", "",
		"Human-friendly name shown in the UI (defaults to the view name)")
	cmd.Flags().StringVar(&nopts.description, "description", "",
		"Free-form description of what the view shows")

	return cmd
}

// runViewsNew creates a GraphView with the given name that shows exactly the
// requested resource kinds.
func runViewsNew(cmd *cobra.Command, nopts *viewsNewOptions, namespace, projection, name string, kindArgs []string) error {
	kinds := parseKindArgs(kindArgs)
	if len(kinds) == 0 {
		return fmt.Errorf("at least one resource kind must be given")
	}

	displayName := nopts.displayName
	if displayName == "" {
		displayName = name
	}

	client, err := newClient(nopts.options)
	if err != nil {
		return err
	}

	created, err := client.CreateView(cmd.Context(), View{
		Namespace:   namespace,
		Name:        name,
		DisplayName: displayName,
		Description: nopts.description,
		ProjectionRef: ViewProjectionRef{
			Name:      projection,
			Namespace: namespace,
		},
		Filters: ViewFilters{
			KindMode:     kindModeShow,
			VisibleKinds: kinds,
		},
	})
	if err != nil {
		return fmt.Errorf("creating view %q: %w", name, err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"graphview.astron.astron.io/%s created in namespace %s\n", created.Name, created.Namespace)
	return err
}

// parseKindArgs flattens kind arguments (each possibly comma-separated) into a
// de-duplicated, sorted list of resource kinds.
func parseKindArgs(args []string) []string {
	seen := map[string]bool{}
	var kinds []string
	for _, arg := range args {
		for part := range strings.SplitSeq(arg, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			kinds = append(kinds, part)
		}
	}
	sort.Strings(kinds)
	return kinds
}

// viewsGenerateOptions holds the flags for "views generate".
type viewsGenerateOptions struct {
	*options
	kube kubeOptions

	// outputFile, when set (and not "-"), writes the manifests to a file instead
	// of stdout.
	outputFile string
	// apply creates/updates the GraphViews in the cluster instead of emitting
	// their manifests.
	apply bool
}

// newViewsGenerateCmd builds "views generate <namespace> <projection> <view>...".
func newViewsGenerateCmd(opts *options) *cobra.Command {
	gopts := &viewsGenerateOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "generate <namespace> <projection> <view>...",
		Short: "Generate GraphView manifests for a GraphProjection",
		Long: "generate produces GraphView manifests for one or more of the built-in\n" +
			"default views, filtering an existing GraphProjection in a namespace.\n\n" +
			"Each view is one of the default views: " + strings.Join(defaultViewNames(), ", ") + ",\n" +
			"or 'defaults' for all of them. View names are case-insensitive. Each\n" +
			"GraphView is named \"<projection>-<view>\" (e.g. web-compute). Run\n" +
			"\"astron views defaults\" to see what each default view shows.\n\n" +
			"By default the manifests are written to stdout as a multi-document YAML\n" +
			"stream. Use --output-file to write them to a file, or --apply to\n" +
			"create/update the GraphViews in the cluster (via your kubeconfig)\n" +
			"instead of emitting YAML.",
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runViewsGenerate(cmd, gopts, args[0], args[1], args[2:])
		},
	}

	cmd.Flags().StringVarP(&gopts.outputFile, "output-file", "f", "",
		"Write the generated manifests to this file instead of stdout (\"-\" means stdout)")
	cmd.Flags().BoolVar(&gopts.apply, "apply", false,
		"Create/update the GraphViews in the cluster instead of emitting their manifests")
	cmd.Flags().StringVar(&gopts.kube.kubeconfig, "kubeconfig", "",
		"Path to the kubeconfig file used with --apply (defaults to KUBECONFIG or ~/.kube/config)")
	cmd.Flags().StringVar(&gopts.kube.context, "context", "",
		"Name of the kubeconfig context to use with --apply")

	return cmd
}

// runViewsGenerate resolves the requested view names and emits (or applies) a
// GraphView manifest for each one.
func runViewsGenerate(cmd *cobra.Command, gopts *viewsGenerateOptions, namespace, projection string, viewNames []string) error {
	if gopts.apply && gopts.outputFile != "" {
		return fmt.Errorf("--apply cannot be combined with --output-file")
	}

	views, err := parseViewSelection(strings.Join(viewNames, ","))
	if err != nil {
		return err
	}

	manifests := make([]viewManifest, 0, len(views))
	for _, v := range views {
		manifests = append(manifests, buildViewManifest(namespace, projection, v))
	}

	if gopts.apply {
		cfg, cfgErr := gopts.kube.restConfig()
		if cfgErr != nil {
			return cfgErr
		}
		dyn, dynErr := dynamic.NewForConfig(cfg)
		if dynErr != nil {
			return fmt.Errorf("creating dynamic client: %w", dynErr)
		}
		for _, m := range manifests {
			if err := applyView(cmd, dyn, m); err != nil {
				return err
			}
		}
		return nil
	}

	docs := make([]any, 0, len(manifests))
	for _, m := range manifests {
		docs = append(docs, m)
	}
	return writeDocuments(cmd, gopts.outputFile, docs...)
}

// newViewsRmCmd builds "views rm <namespace> <name>...".
func newViewsRmCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <namespace> <name>...",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete one or more GraphViews",
		Long: "rm deletes the named GraphViews from a namespace.\n\n" +
			"Use \"views list\" to see the existing GraphViews and their names.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runViewsRm(cmd, opts, args[0], args[1:])
		},
	}
}

// runViewsRm deletes each named GraphView, continuing past individual failures
// and reporting them together.
func runViewsRm(cmd *cobra.Command, opts *options, namespace string, names []string) error {
	client, err := newClient(opts)
	if err != nil {
		return err
	}

	var errs []error
	for _, name := range names {
		if err := client.DeleteView(cmd.Context(), namespace, name); err != nil {
			errs = append(errs, fmt.Errorf("deleting view %q: %w", name, err))
			continue
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"graphview.astron.astron.io/%s deleted from namespace %s\n", name, namespace)
	}
	return errors.Join(errs...)
}

// defaultViewInfo is the JSON-friendly shape of a built-in view definition, as
// rendered by "views defaults".
type defaultViewInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kinds       []string `json:"kinds"`
}

// newViewsDefaultsCmd builds "views defaults". It is purely local: the built-in
// view definitions are compiled into the CLI, so no API or cluster access is
// needed.
func newViewsDefaultsCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "defaults",
		Short: "List the built-in default views",
		Long: "defaults shows the pre-defined views built into astron, with the\n" +
			"resource kinds each one makes visible.\n\n" +
			"These are the names accepted by \"views add\" and by the --views flag of\n" +
			"\"projections generate\". The command is local and does not contact the\n" +
			"API server.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runViewsDefaults(cmd, opts)
		},
	}
}

func runViewsDefaults(cmd *cobra.Command, opts *options) error {
	infos := make([]defaultViewInfo, 0, len(defaultViewCategories))
	for _, cat := range defaultViewCategories {
		infos = append(infos, defaultViewInfo{
			Name:        cat.displayName,
			Description: cat.description,
			// visibleKindsFor mirrors exactly what a created GraphView would show,
			// including the always-visible kinds.
			Kinds: visibleKindsFor(cat),
		})
	}

	if opts.output == outputJSON {
		return printJSON(cmd.OutOrStdout(), infos)
	}

	tw := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(tw, "NAME\tDESCRIPTION\tKINDS")
	for _, info := range infos {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
			info.Name, info.Description, strings.Join(info.Kinds, ", "))
	}
	return tw.Flush()
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
			"GraphView is named \"<projection>-<view>\" (e.g. web-compute).\n\n" +
			"Run \"astron views defaults\" to see what each default view shows.",
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
