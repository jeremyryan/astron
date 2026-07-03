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

// Package cli implements the gamera command-line client. It is a thin client
// over the operator's read API (see internal/api), used to inspect projections
// and the graph they materialize from a terminal.
package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// defaultServer is the read API/UI base URL the CLI talks to when --server is
// not provided. It matches the address the operator's API server binds to and
// the port-forward documented in the README.
const defaultServer = "http://localhost:8082"

// options holds the global flags shared by all subcommands.
type options struct {
	// server is the base URL of the Gamera read API.
	server string
	// timeout bounds each request made to the API.
	timeout time.Duration
	// output selects the rendering format ("table" or "json").
	output string
}

// newRootCmd builds the root command and wires up the global flags and
// subcommands. It is exported via Execute for the binary entrypoint and used
// directly by tests.
func newRootCmd() *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "gamera",
		Short: "Inspect Project Gamera projections and graphs",
		Long: "gamera is the command-line client for Project Gamera.\n\n" +
			"It talks to a running operator's read API to list GraphProjections\n" +
			"and explore the resource graph they materialize into Neo4J.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			switch opts.output {
			case outputTable, outputJSON:
				return nil
			default:
				return fmt.Errorf("invalid --output %q: must be %q or %q",
					opts.output, outputTable, outputJSON)
			}
		},
	}

	cmd.PersistentFlags().StringVar(&opts.server, "server", defaultServer,
		"Base URL of the Gamera read API")
	cmd.PersistentFlags().DurationVar(&opts.timeout, "timeout", 30*time.Second,
		"Timeout for requests to the API")
	cmd.PersistentFlags().StringVarP(&opts.output, "output", "o", "table",
		"Output format: table or json")

	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newProjectionsCmd(opts))
	cmd.AddCommand(newViewsCmd(opts))
	cmd.AddCommand(newGraphCmd(opts))

	return cmd
}

// Execute runs the root command and exits the process with a non-zero status on
// error.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
