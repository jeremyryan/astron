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

// linkOptions holds the flags shared by the links subcommands.
type linkOptions struct {
	*options
	// relType is the relationship type of the link; empty selects the server
	// default (CUSTOM).
	relType string
	// note is the free-text note set by "links update".
	note string
}

// newLinksCmd builds the "links" command group.
func newLinksCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "links",
		Short: "Manage user-defined links between graph nodes",
		Long: "links manages user-created edges between resource nodes in a\n" +
			"projection's graph. These edges live alongside the derived\n" +
			"relationships and survive resyncs.\n\n" +
			"Node ids are the graph node identifiers, as shown by\n" +
			"\"astron graph -o json\" or the search commands.",
	}
	cmd.AddCommand(newLinksAddCmd(opts))
	cmd.AddCommand(newLinksRmCmd(opts))
	cmd.AddCommand(newLinksUpdateCmd(opts))
	return cmd
}

// newLinksAddCmd builds "links add <namespace> <projection> <from> <to>".
func newLinksAddCmd(opts *options) *cobra.Command {
	lopts := &linkOptions{options: opts}

	cmd := &cobra.Command{
		Use:     "add <namespace> <projection> <from-id> <to-id>",
		Aliases: []string{"create"},
		Short:   "Create a link between two graph nodes",
		Args:    cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(lopts.options)
			if err != nil {
				return err
			}
			created, err := client.AddLink(cmd.Context(), args[0], args[1], LinkRequest{
				From: args[2], To: args[3], Type: lopts.relType,
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "link %s -[%s]-> %s created\n",
				created.From, created.Type, created.To)
			return err
		},
	}

	cmd.Flags().StringVar(&lopts.relType, "type", "",
		"Relationship type of the link (defaults to CUSTOM)")

	return cmd
}

// newLinksRmCmd builds "links rm <namespace> <projection> <from> <to>".
func newLinksRmCmd(opts *options) *cobra.Command {
	lopts := &linkOptions{options: opts}

	cmd := &cobra.Command{
		Use:     "rm <namespace> <projection> <from-id> <to-id>",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete a user-created link between two graph nodes",
		Args:    cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(lopts.options)
			if err != nil {
				return err
			}
			if err := client.DeleteLink(cmd.Context(), args[0], args[1], args[2], args[3], lopts.relType); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "link %s -> %s deleted\n", args[2], args[3])
			return err
		},
	}

	cmd.Flags().StringVar(&lopts.relType, "type", "",
		"Relationship type of the link (defaults to CUSTOM)")

	return cmd
}

// newLinksUpdateCmd builds "links update <namespace> <projection> <from> <to>".
func newLinksUpdateCmd(opts *options) *cobra.Command {
	lopts := &linkOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "update <namespace> <projection> <from-id> <to-id>",
		Short: "Update the note on a user-created link",
		Args:  cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient(lopts.options)
			if err != nil {
				return err
			}
			updated, err := client.UpdateLink(cmd.Context(), args[0], args[1], LinkRequest{
				From: args[2], To: args[3], Type: lopts.relType, Note: lopts.note,
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "link %s -[%s]-> %s updated\n",
				updated.From, updated.Type, updated.To)
			return err
		},
	}

	cmd.Flags().StringVar(&lopts.relType, "type", "",
		"Relationship type of the link (defaults to CUSTOM)")
	cmd.Flags().StringVar(&lopts.note, "note", "",
		"Free-text note to set on the link")

	return cmd
}
