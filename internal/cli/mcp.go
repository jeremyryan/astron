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
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/project-gamera/gamera/internal/mcp"
)

// newMCPServerCmd builds the "mcp-server" command, which exposes the Gamera read
// API as Model Context Protocol tools over stdio for local LLM agents.
func newMCPServerCmd(opts *options) *cobra.Command {
	var apiBaseURL string
	cmd := &cobra.Command{
		Use:   "mcp-server",
		Short: "Serve Gamera retrieval as Model Context Protocol (MCP) tools over stdio",
		Long: "mcp-server exposes the Gamera read API as Model Context Protocol (MCP)\n" +
			"tools over a stdio transport, for use by local LLM agents.\n\n" +
			"It is a thin client of the read API. The API base URL is taken from\n" +
			"--api-base-url, falling back to the global --server flag and then\n" +
			"$GAMERA_API_URL. Logs go to stderr to keep stdout clean for the JSON-RPC\n" +
			"protocol.",
		Args: cobra.NoArgs,
		// The MCP protocol owns stdout, so keep cobra's usage/error text off it.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			baseURL := apiBaseURL
			if baseURL == "" {
				baseURL = resolveMCPBaseURL(opts)
			}
			server := mcp.NewServer(mcp.NewAPIClient(baseURL, nil))

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "gamera mcp-server: serving over stdio, API at %s\n", baseURL)
			if err := server.Serve(ctx, cmd.InOrStdin(), cmd.OutOrStdout()); err != nil && ctx.Err() == nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&apiBaseURL, "api-base-url", "",
		"Base URL of the Gamera read API (defaults to --server, then $GAMERA_API_URL)")
	return cmd
}

// resolveMCPBaseURL returns the read API base URL for the MCP server. It uses
// the global --server flag, falling back to $GAMERA_API_URL when --server is
// left at its default so the documented environment override still works.
func resolveMCPBaseURL(opts *options) string {
	if opts.server == defaultServer {
		if v := os.Getenv("GAMERA_API_URL"); v != "" {
			return v
		}
	}
	return opts.server
}
