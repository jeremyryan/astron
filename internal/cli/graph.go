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
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// Graph-specific output formats accepted by --format, in addition to the
// global table/json formats.
const (
	formatDOT     = "dot"
	formatMermaid = "mermaid"
)

// graphOptions holds the flags specific to the graph command.
type graphOptions struct {
	*options
	// kind, when set, restricts displayed nodes (and the edges touching them)
	// to the given resource Kind, e.g. "Pod".
	kind string
	// edgesOnly / nodesOnly restrict the table output to a single section.
	edgesOnly bool
	nodesOnly bool
	// format selects how the graph is rendered: "table" (the default) prints
	// human-readable node and edge tables; "json" prints a single JSON document
	// containing all nodes and edges; "dot" prints a Graphviz digraph; and
	// "mermaid" prints a Mermaid flowchart. When empty it falls back to the
	// global --output flag.
	format string
}

// newGraphCmd builds the "graph" command.
func newGraphCmd(opts *options) *cobra.Command {
	gopts := &graphOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "graph <namespace> <name>",
		Short: "Show the resource graph materialized by a projection",
		Long: "graph fetches the nodes and edges a GraphProjection has materialized\n" +
			"and prints them. The projection is identified by its namespace and name\n" +
			"(see \"astron projections list\").\n\n" +
			"Use --format table (the default) to print human-readable node and edge\n" +
			"tables, or --format json to print a single JSON document containing all\n" +
			"nodes and edges.\n\n" +
			"For rendering, --format dot emits a Graphviz digraph (pipe it into\n" +
			"\"dot -Tsvg\") and --format mermaid emits a Mermaid flowchart suitable\n" +
			"for embedding in Markdown.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := gopts.resolveFormat()
			if err != nil {
				return err
			}
			if gopts.edgesOnly && gopts.nodesOnly {
				return fmt.Errorf("--edges-only and --nodes-only are mutually exclusive")
			}
			client, err := newClient(gopts.options)
			if err != nil {
				return err
			}
			g, err := client.Graph(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			g = filterGraph(g, gopts.kind)

			switch format {
			case outputJSON:
				return printJSON(cmd.OutOrStdout(), g)
			case formatDOT:
				return renderDOT(cmd.OutOrStdout(), g)
			case formatMermaid:
				return renderMermaid(cmd.OutOrStdout(), g)
			default:
				return printGraphTable(cmd, g, gopts)
			}
		},
	}

	cmd.Flags().StringVar(&gopts.kind, "kind", "",
		"Only show nodes of this Kind (and edges touching them), e.g. Pod")
	cmd.Flags().BoolVar(&gopts.edgesOnly, "edges-only", false, "Only print the edges section")
	cmd.Flags().BoolVar(&gopts.nodesOnly, "nodes-only", false, "Only print the nodes section")
	cmd.Flags().StringVar(&gopts.format, "format", "",
		"Output format for the graph: table, json, dot or mermaid (defaults to the global --output)")

	return cmd
}

// resolveFormat determines the effective output format for the graph command.
// An explicit --format wins; otherwise it falls back to the global --output
// flag. It returns an error for any unsupported value.
func (o *graphOptions) resolveFormat() (string, error) {
	format := o.format
	if format == "" {
		format = o.output
	}
	switch format {
	case outputTable, outputJSON, formatDOT, formatMermaid:
		return format, nil
	default:
		return "", fmt.Errorf("invalid --format %q: must be %q, %q, %q or %q",
			o.format, outputTable, outputJSON, formatDOT, formatMermaid)
	}
}

// renderDOT writes the graph as a Graphviz digraph. Node identifiers are the
// graph node ids (quoted and escaped); labels are "Kind ns/name". Output is
// sorted for determinism.
func renderDOT(w io.Writer, g Graph) error {
	byID := make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	sortNodes(g.Nodes)
	sortEdges(g.Edges, byID)

	var b strings.Builder
	b.WriteString("digraph {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box];\n")
	for _, n := range g.Nodes {
		fmt.Fprintf(&b, "  %s [label=%s];\n", dotQuote(n.ID), dotQuote(nodeLabel(byID, n.ID)))
	}
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %s -> %s [label=%s];\n", dotQuote(e.Source), dotQuote(e.Target), dotQuote(e.Type))
	}
	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// dotQuote renders a DOT double-quoted string, escaping backslashes and
// quotes.
func dotQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// renderMermaid writes the graph as a Mermaid flowchart. Nodes get synthetic
// identifiers (n0, n1, ...) since Mermaid ids cannot contain arbitrary
// characters; labels are "Kind ns/name". Output is sorted for determinism.
func renderMermaid(w io.Writer, g Graph) error {
	byID := make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	sortNodes(g.Nodes)
	sortEdges(g.Edges, byID)

	ids := make(map[string]string, len(g.Nodes))
	var b strings.Builder
	b.WriteString("graph LR\n")
	for i, n := range g.Nodes {
		id := fmt.Sprintf("n%d", i)
		ids[n.ID] = id
		fmt.Fprintf(&b, "  %s[%q]\n", id, mermaidLabel(nodeLabel(byID, n.ID)))
	}
	for _, e := range g.Edges {
		src, sok := ids[e.Source]
		dst, dok := ids[e.Target]
		if !sok || !dok {
			// Skip edges pointing at nodes outside the (possibly filtered) graph.
			continue
		}
		fmt.Fprintf(&b, "  %s -->|%s| %s\n", src, mermaidLabel(e.Type), dst)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// mermaidLabel sanitizes a label for Mermaid output, where double quotes and
// pipes are structural characters.
func mermaidLabel(s string) string {
	s = strings.ReplaceAll(s, `"`, "'")
	return strings.ReplaceAll(s, "|", "/")
}

// filterGraph restricts the graph to nodes of the given kind (case-insensitive)
// and the edges that connect two retained nodes. An empty kind returns the
// graph unchanged.
func filterGraph(g Graph, kind string) Graph {
	if kind == "" {
		return g
	}
	keep := map[string]bool{}
	nodes := make([]Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if strings.EqualFold(n.Kind, kind) {
			nodes = append(nodes, n)
			keep[n.ID] = true
		}
	}
	edges := make([]Edge, 0, len(g.Edges))
	for _, e := range g.Edges {
		if keep[e.Source] && keep[e.Target] {
			edges = append(edges, e)
		}
	}
	return Graph{Nodes: nodes, Edges: edges}
}

// printGraphTable renders the graph as human-readable node and edge tables.
func printGraphTable(cmd *cobra.Command, g Graph, gopts *graphOptions) error {
	w := cmd.OutOrStdout()
	byID := make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}

	if !gopts.edgesOnly {
		sortNodes(g.Nodes)
		tw := newTabWriter(w)
		_, _ = fmt.Fprintln(tw, "KIND\tNAMESPACE\tNAME")
		for _, n := range g.Nodes {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", dash(n.Kind), dash(n.Namespace), n.Name)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	if !gopts.edgesOnly && !gopts.nodesOnly {
		_, _ = fmt.Fprintln(w)
	}

	if !gopts.nodesOnly {
		sortEdges(g.Edges, byID)
		tw := newTabWriter(w)
		_, _ = fmt.Fprintln(tw, "TYPE\tFROM\tTO")
		for _, e := range g.Edges {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				e.Type, nodeLabel(byID, e.Source), nodeLabel(byID, e.Target))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// nodeLabel renders a readable endpoint label for an edge. When the endpoint
// node is present in the graph it is shown as "Kind ns/name" (or "Kind name"
// for cluster-scoped objects); otherwise the raw id is used as a fallback.
func nodeLabel(byID map[string]Node, id string) string {
	n, ok := byID[id]
	if !ok {
		return id
	}
	if n.Namespace == "" {
		return fmt.Sprintf("%s %s", n.Kind, n.Name)
	}
	return fmt.Sprintf("%s %s/%s", n.Kind, n.Namespace, n.Name)
}

// sortNodes orders nodes by kind, namespace, then name for stable output.
func sortNodes(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
}

// sortEdges orders edges by type, then by their (resolved) endpoint labels.
func sortEdges(edges []Edge, byID map[string]Node) {
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if la, lb := nodeLabel(byID, a.Source), nodeLabel(byID, b.Source); la != lb {
			return la < lb
		}
		return nodeLabel(byID, a.Target) < nodeLabel(byID, b.Target)
	})
}
