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

// completeProjectionArgs returns a ValidArgsFunction completing
// "<namespace> <projection>" positional arguments from the read API. Failures
// (e.g. unreachable server) silently disable completion.
func completeProjectionArgs(opts *options) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveDefault
		}
		client, err := newClient(opts)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		projections, err := client.ListProjections(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		seen := map[string]bool{}
		var out []cobra.Completion
		for _, p := range projections {
			switch len(args) {
			case 0: // completing the namespace
				if !seen[p.Namespace] {
					seen[p.Namespace] = true
					out = append(out, p.Namespace)
				}
			case 1: // completing the projection name within the namespace
				if p.Namespace == args[0] && !seen[p.Name] {
					seen[p.Name] = true
					out = append(out, p.Name)
				}
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeViewArgs returns a ValidArgsFunction completing
// "<namespace> <view>" positional arguments from the read API.
func completeViewArgs(opts *options) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveDefault
		}
		client, err := newClient(opts)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		views, err := client.ListViews(cmd.Context(), "", "")
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		seen := map[string]bool{}
		var out []cobra.Completion
		for _, v := range views {
			switch len(args) {
			case 0:
				if !seen[v.Namespace] {
					seen[v.Namespace] = true
					out = append(out, v.Namespace)
				}
			case 1:
				if v.Namespace == args[0] && !seen[v.Name] {
					seen[v.Name] = true
					out = append(out, v.Name)
				}
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
