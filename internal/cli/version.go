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
	"github.com/spf13/cobra"
)

// These values are overridden at build time via -ldflags, e.g.:
//
//	go build -ldflags "-X github.com/project-astron/astron/internal/cli.version=v0.1.0"
var (
	// version is the CLI release version.
	version = "dev"
	// commit is the git commit the binary was built from.
	commit = "none"
	// date is the build timestamp.
	date = "unknown"
)

// newVersionCmd builds the "version" subcommand.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the astron CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("astron %s (commit %s, built %s)\n", version, commit, date)
			return nil
		},
	}
}
