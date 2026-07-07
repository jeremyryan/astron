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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

// newProjectionsCmd builds the "projections" command group.
func newProjectionsCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projections",
		Aliases: []string{"projection", "proj"},
		Short:   "Work with GraphProjections",
	}
	cmd.AddCommand(newProjectionsListCmd(opts))
	cmd.AddCommand(newGenerateCmd(opts))
	cmd.AddCommand(newProjectionsRemoveCmd(opts))
	cmd.AddCommand(newProjectionsUpdateCmd(opts))
	return cmd
}

// removeOptions holds the flags for "projections rm".
type removeOptions struct {
	*options
	kube kubeOptions
}

// newProjectionsRemoveCmd builds "projections rm <namespace> <name>".
func newProjectionsRemoveCmd(opts *options) *cobra.Command {
	ropts := &removeOptions{options: opts}

	cmd := &cobra.Command{
		Use:     "rm <namespace> <name>",
		Aliases: []string{"remove", "delete", "del"},
		Short:   "Delete a GraphProjection from a namespace",
		Long: "rm deletes the named GraphProjection from the given namespace.\n\n" +
			"It talks directly to the Kubernetes API (via your kubeconfig), not the\n" +
			"Astron read API, so the --server flag does not apply here.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ropts.kube.restConfig()
			if err != nil {
				return err
			}
			dyn, err := dynamic.NewForConfig(cfg)
			if err != nil {
				return fmt.Errorf("creating dynamic client: %w", err)
			}
			return deleteProjection(cmd, dyn, args[0], args[1])
		},
	}

	cmd.Flags().StringVar(&ropts.kube.kubeconfig, "kubeconfig", "",
		"Path to the kubeconfig file (defaults to KUBECONFIG or ~/.kube/config)")
	cmd.Flags().StringVar(&ropts.kube.context, "context", "",
		"Name of the kubeconfig context to use")

	return cmd
}

// deleteProjection deletes the named GraphProjection from the namespace via the
// dynamic client. It is split out so it can be unit-tested with a fake client.
func deleteProjection(cmd *cobra.Command, dyn dynamic.Interface, namespace, name string) error {
	ri := dyn.Resource(graphProjectionGVR).Namespace(namespace)
	if err := ri.Delete(cmd.Context(), name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("GraphProjection %s/%s not found", namespace, name)
		}
		return fmt.Errorf("deleting GraphProjection %s/%s: %w", namespace, name, err)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "graphprojection.astron.astron.io/%s deleted from namespace %s\n", name, namespace)
	return err
}

// newProjectionsListCmd builds "projections list".
func newProjectionsListCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List GraphProjections and their node/edge counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newClient(opts)
			if err != nil {
				return err
			}
			projections, err := client.ListProjections(cmd.Context())
			if err != nil {
				return err
			}

			if opts.output == outputJSON {
				return printJSON(cmd.OutOrStdout(), projections)
			}

			tw := newTabWriter(cmd.OutOrStdout())
			_, _ = fmt.Fprintln(tw, "NAMESPACE\tNAME\tPHASE\tNODES\tEDGES")
			for _, p := range projections {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\n",
					p.Namespace, p.Name, dash(p.Phase), p.NodeCount, p.RelationshipCount)
			}
			return tw.Flush()
		},
	}
}
