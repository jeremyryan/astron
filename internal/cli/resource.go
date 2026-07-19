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

// resourceOptions holds the flags for the resource command.
type resourceOptions struct {
	*options
	// namespace of the resource; empty for cluster-scoped resources.
	namespace string
}

// newResourceCmd builds "resource <apiVersion>/<Kind> <name>".
func newResourceCmd(opts *options) *cobra.Command {
	ropts := &resourceOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "resource <apiVersion>/<Kind> <name>",
		Short: "Fetch the live manifest of a resource as YAML",
		Long: "resource fetches a single resource from the cluster through the Astron\n" +
			"read API and prints its manifest as YAML, with noisy server-managed\n" +
			"fields (managedFields, last-applied annotation) stripped.\n\n" +
			"The resource type is given as \"[group/]version/Kind\", e.g. v1/Pod or\n" +
			"apps/v1/Deployment. Use -n/--namespace for namespaced resources; omit\n" +
			"it for cluster-scoped ones.\n\n" +
			"This is handy for inspecting a node found via \"astron graph\" or\n" +
			"\"astron search\" without switching kubectl contexts.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResource(cmd, ropts, args[0], args[1])
		},
	}

	cmd.Flags().StringVarP(&ropts.namespace, "namespace", "n", "",
		"Namespace of the resource (omit for cluster-scoped resources)")

	return cmd
}

func runResource(cmd *cobra.Command, ropts *resourceOptions, typeRef, name string) error {
	apiVersion, kind, err := parseTypeRef(typeRef)
	if err != nil {
		return err
	}

	client, err := newClient(ropts.options)
	if err != nil {
		return err
	}

	data, err := client.ResourceYAML(cmd.Context(), apiVersion, kind, ropts.namespace, name)
	if err != nil {
		return err
	}
	_, err = cmd.OutOrStdout().Write(data)
	return err
}

// parseTypeRef splits "[group/]version/Kind" into its apiVersion and Kind
// parts, e.g. "apps/v1/Deployment" -> ("apps/v1", "Deployment") and
// "v1/Pod" -> ("v1", "Pod").
func parseTypeRef(ref string) (apiVersion, kind string, err error) {
	idx := strings.LastIndex(ref, "/")
	if idx <= 0 || idx == len(ref)-1 {
		return "", "", fmt.Errorf("invalid resource type %q: expected [group/]version/Kind (e.g. v1/Pod or apps/v1/Deployment)", ref)
	}
	return ref[:idx], ref[idx+1:], nil
}
