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
	"strings"

	"github.com/spf13/cobra"
)

// newDocsCmd builds the "docs" command.
func newDocsCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "docs",
		Short: "Print the URL of the Astron API documentation",
		Long: "docs prints the URL of the interactive API documentation served by the\n" +
			"Astron read API (a Redoc rendering of its OpenAPI specification).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			base := strings.TrimRight(opts.server, "/")
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s/api/docs\n", base)
			return err
		},
	}
}
