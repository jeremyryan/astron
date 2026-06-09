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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/project-gamera/gamera/internal/mcp"
)

// mcpServerCommand is the subcommand name that runs the MCP server instead of
// the operator.
const mcpServerCommand = "mcp-server"

// runMCPServer runs the Gamera MCP server over stdio. It is a thin client of the
// Gamera read API, so it requires a reachable API endpoint (the operator's
// --api-bind-address, by default port-forwarded or in-cluster).
//
// Logs go to stderr so they never corrupt the JSON-RPC stream on stdout.
func runMCPServer(args []string) {
	fs := flag.NewFlagSet(mcpServerCommand, flag.ExitOnError)
	apiBaseURL := fs.String("api-base-url", defaultAPIBaseURL(),
		"Base URL of the Gamera read API (e.g. http://localhost:8082). "+
			"Defaults to $GAMERA_API_URL or http://localhost:8082.")
	_ = fs.Parse(args)

	client := mcp.NewAPIClient(*apiBaseURL, nil)
	server := mcp.NewServer(client)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "gamera mcp-server: serving over stdio, API at %s\n", *apiBaseURL)
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "gamera mcp-server: %v\n", err)
		os.Exit(1)
	}
}

// defaultAPIBaseURL resolves the default Gamera API URL from the environment.
func defaultAPIBaseURL() string {
	if v := os.Getenv("GAMERA_API_URL"); v != "" {
		return v
	}
	return "http://localhost:8082"
}
