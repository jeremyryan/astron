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

// newProjectionsCmd builds the "projections" command group.
func newProjectionsCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projections",
		Aliases: []string{"projection", "proj"},
		Short:   "Work with GraphProjections",
	}
	cmd.AddCommand(newProjectionsListCmd(opts))
	cmd.AddCommand(newGenerateCmd(opts))
	return cmd
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
