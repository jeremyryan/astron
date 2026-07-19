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
	"fmt"

	"github.com/spf13/cobra"
)

// statusResult is the JSON shape of the status command's output.
type statusResult struct {
	Server      string `json:"server"`
	Healthy     bool   `json:"healthy"`
	Error       string `json:"error,omitempty"`
	Projections int    `json:"projections"`
	// Ready counts projections whose phase is "Ready".
	Ready int `json:"ready"`
	// TotalNodes and TotalEdges aggregate the graph sizes across projections.
	TotalNodes int64 `json:"totalNodes"`
	TotalEdges int64 `json:"totalEdges"`
}

// newStatusCmd builds the "status" command.
func newStatusCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check connectivity to the Astron API and summarize projection health",
		Long: "status pings the Astron read API's health endpoint and, when reachable,\n" +
			"prints a summary of the projections it manages: how many exist, how\n" +
			"many are Ready, and the total graph size.\n\n" +
			"It exits non-zero when the server is unreachable or unhealthy, so it\n" +
			"can be used as a smoke test in scripts and CI.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(cmd, opts)
		},
	}
}

func runStatus(cmd *cobra.Command, opts *options) error {
	client, err := newClient(opts)
	if err != nil {
		return err
	}

	result := statusResult{Server: client.baseURL}
	if err := client.Health(cmd.Context()); err != nil {
		result.Error = err.Error()
		if opts.output == outputJSON {
			_ = printJSON(cmd.OutOrStdout(), result)
		}
		return fmt.Errorf("astron API at %s is not healthy: %w", client.baseURL, err)
	}
	result.Healthy = true

	if err := summarizeProjections(cmd.Context(), client, &result); err != nil {
		return err
	}

	if opts.output == outputJSON {
		return printJSON(cmd.OutOrStdout(), result)
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "server:      %s\n", result.Server)
	_, _ = fmt.Fprintf(out, "healthy:     true\n")
	_, _ = fmt.Fprintf(out, "projections: %d (%d ready)\n", result.Projections, result.Ready)
	_, _ = fmt.Fprintf(out, "graph:       %d nodes, %d edges\n", result.TotalNodes, result.TotalEdges)
	return nil
}

// summarizeProjections aggregates projection counts and graph sizes into the
// status result.
func summarizeProjections(ctx context.Context, client *Client, result *statusResult) error {
	projections, err := client.ListProjections(ctx)
	if err != nil {
		return fmt.Errorf("listing projections: %w", err)
	}
	result.Projections = len(projections)
	for _, p := range projections {
		if p.Phase == "Ready" {
			result.Ready++
		}
		result.TotalNodes += p.NodeCount
		result.TotalEdges += p.RelationshipCount
	}
	return nil
}
