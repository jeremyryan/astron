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
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

// describeOptions holds the flags for "projections describe".
type describeOptions struct {
	*options
	kube kubeOptions
}

// newProjectionsDescribeCmd builds "projections describe <namespace> <name>".
func newProjectionsDescribeCmd(opts *options) *cobra.Command {
	dopts := &describeOptions{options: opts}

	cmd := &cobra.Command{
		Use:     "describe <namespace> <name>",
		Aliases: []string{"get"},
		Short:   "Show the spec and status of a GraphProjection",
		Long: "describe prints a detailed summary of a GraphProjection: its scope,\n" +
			"captured resource kinds, relationship rules, GraphRAG configuration and\n" +
			"current status (phase, conditions, graph size, sync times).\n\n" +
			"Use -o json to print the full object.\n\n" +
			"It talks directly to the Kubernetes API (via your kubeconfig), not the\n" +
			"Astron read API, so the --server flag does not apply here.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := dopts.kube.restConfig()
			if err != nil {
				return err
			}
			dyn, err := dynamic.NewForConfig(cfg)
			if err != nil {
				return fmt.Errorf("creating dynamic client: %w", err)
			}
			return describeProjection(cmd, dopts, dyn, args[0], args[1])
		},
	}

	cmd.Flags().StringVar(&dopts.kube.kubeconfig, "kubeconfig", "",
		"Path to the kubeconfig file (defaults to KUBECONFIG or ~/.kube/config)")
	cmd.Flags().StringVar(&dopts.kube.context, "context", "",
		"Name of the kubeconfig context to use")

	return cmd
}

// describeProjection fetches the projection and renders it. It is split out so
// it can be unit-tested with a fake dynamic client.
func describeProjection(cmd *cobra.Command, dopts *describeOptions, dyn dynamic.Interface, namespace, name string) error {
	u, err := dyn.Resource(graphProjectionGVR).Namespace(namespace).Get(cmd.Context(), name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("fetching GraphProjection %s/%s: %w", namespace, name, err)
	}

	if dopts.output == outputJSON {
		return printJSON(cmd.OutOrStdout(), u.Object)
	}

	// Round-trip through JSON into the typed object for structured access.
	var p astronv1alpha1.GraphProjection
	data, err := json.Marshal(u.Object)
	if err != nil {
		return fmt.Errorf("encoding projection: %w", err)
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("decoding projection: %w", err)
	}

	return printProjectionDescription(cmd.OutOrStdout(), &p)
}

// printProjectionDescription renders a kubectl-describe-style summary.
func printProjectionDescription(w io.Writer, p *astronv1alpha1.GraphProjection) error {
	pr := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	pr("Name:         %s", p.Name)
	pr("Namespace:    %s", p.Namespace)

	pr("Neo4j:")
	pr("  URI:        %s", p.Spec.Neo4j.URI)
	pr("  Database:   %s", dash(p.Spec.Neo4j.Database))
	pr("  Secret:     %s", qualifiedName(p.Spec.Neo4j.AuthSecretRef.Namespace, p.Spec.Neo4j.AuthSecretRef.Name))

	pr("Scope:")
	switch {
	case p.Spec.Scope.OwnNamespaceOnly:
		pr("  Namespaces: (own namespace only)")
	case len(p.Spec.Scope.Namespaces) > 0:
		pr("  Namespaces: %s", strings.Join(p.Spec.Scope.Namespaces, ", "))
	default:
		pr("  Namespaces: (all)")
	}
	if kinds := selectorKinds(p.Spec.Scope.Resources); len(kinds) > 0 {
		pr("  Resources:  %s", strings.Join(kinds, ", "))
	} else {
		pr("  Resources:  (built-in default set)")
	}

	if len(p.Spec.Relationships) > 0 {
		pr("Relationships:")
		for _, r := range p.Spec.Relationships {
			pr("  %s: %s %s -> %s (%s)", r.Name, r.Type, r.From.Kind, r.To.Kind, r.Strategy)
		}
	} else {
		pr("Relationships: (built-in default set)")
	}

	if p.Spec.ResyncInterval != nil {
		pr("Resync:       %s", p.Spec.ResyncInterval.Duration)
	}

	if rag := p.Spec.GraphRAG; rag != nil && rag.Enabled {
		pr("GraphRAG:")
		pr("  Embedding:  %s %s", dash(rag.Embedding.Provider), dash(rag.Embedding.Model))
		if rag.Chat != nil && rag.Chat.Enabled {
			pr("  Chat:       %s %s", dash(rag.Chat.Provider), dash(rag.Chat.Model))
		} else {
			pr("  Chat:       (disabled)")
		}
	} else {
		pr("GraphRAG:     (disabled)")
	}

	pr("Status:")
	pr("  Phase:      %s", dash(p.Status.Phase))
	pr("  Graph:      %d nodes, %d edges", p.Status.NodeCount, p.Status.RelationshipCount)
	if p.Status.LastSyncTime != nil {
		pr("  Last Sync:  %s", p.Status.LastSyncTime.Format("2006-01-02T15:04:05Z07:00"))
	}
	if p.Status.EmbeddedNodeCount > 0 {
		pr("  Embedded:   %d nodes", p.Status.EmbeddedNodeCount)
	}
	if p.Status.LastEmbeddingTime != nil {
		pr("  Last Embed: %s", p.Status.LastEmbeddingTime.Format("2006-01-02T15:04:05Z07:00"))
	}
	if len(p.Status.Conditions) > 0 {
		pr("  Conditions:")
		for _, c := range p.Status.Conditions {
			msg := c.Message
			if msg != "" {
				msg = ": " + msg
			}
			pr("    %s=%s (%s)%s", c.Type, c.Status, c.Reason, msg)
		}
	}
	return nil
}

// selectorKinds renders resource selectors as "group/Kind" (or "Kind" for the
// core group) labels.
func selectorKinds(selectors []astronv1alpha1.ResourceSelector) []string {
	out := make([]string, 0, len(selectors))
	for _, s := range selectors {
		if s.Group == "" {
			out = append(out, s.Kind)
			continue
		}
		out = append(out, s.Group+"/"+s.Kind)
	}
	return out
}

// newViewsDescribeCmd builds "views describe <namespace> <name>".
func newViewsDescribeCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:     "describe <namespace> <name>",
		Aliases: []string{"get"},
		Short:   "Show the details and filters of a GraphView",
		Long: "describe prints a detailed summary of a GraphView: the projection it\n" +
			"filters, its display metadata, and its kind, namespace and label\n" +
			"filters.\n\nUse -o json to print the full object.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(opts)
			if err != nil {
				return err
			}
			view, err := client.GetView(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return printJSON(cmd.OutOrStdout(), view)
			}
			return printViewDescription(cmd.OutOrStdout(), view)
		},
	}
}

// printViewDescription renders a describe-style summary of a GraphView.
func printViewDescription(w io.Writer, v View) error {
	pr := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format+"\n", args...) }

	pr("Name:         %s", v.Name)
	pr("Namespace:    %s", v.Namespace)
	pr("Display Name: %s", dash(v.DisplayName))
	if v.Description != "" {
		pr("Description:  %s", v.Description)
	}
	pr("Projection:   %s", projectionLabel(v))

	pr("Filters:")
	mode := v.Filters.KindMode
	if mode == "" {
		mode = "hide"
	}
	pr("  Kind Mode:  %s", mode)
	if len(v.Filters.VisibleKinds) > 0 {
		pr("  Visible:    %s", strings.Join(v.Filters.VisibleKinds, ", "))
	}
	if len(v.Filters.HiddenKinds) > 0 {
		pr("  Hidden:     %s", strings.Join(v.Filters.HiddenKinds, ", "))
	}
	if len(v.Filters.HiddenNamespaces) > 0 {
		pr("  Hidden NS:  %s", strings.Join(v.Filters.HiddenNamespaces, ", "))
	}
	if len(v.Filters.LabelFilters) > 0 {
		labels := make([]string, 0, len(v.Filters.LabelFilters))
		for _, lf := range v.Filters.LabelFilters {
			if lf.Value == "" {
				labels = append(labels, lf.Key)
				continue
			}
			labels = append(labels, lf.Key+"="+lf.Value)
		}
		pr("  Labels:     %s (%s)", strings.Join(labels, ", "), dash(v.Filters.LabelMode))
	}
	if v.Filters.MaxDistance != nil {
		pr("  Max Dist:   %d", *v.Filters.MaxDistance)
	}
	return nil
}
