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

// ragOptions holds the flags shared by the GraphRAG commands (search, ask,
// query).
type ragOptions struct {
	*options

	// topK bounds the number of seed nodes selected by vector similarity.
	topK int
	// hops is how far the subgraph is expanded around the seeds; negative means
	// the server default.
	hops int
	// edgeTypes restricts expansion to the given relationship types.
	edgeTypes []string
	// kinds restricts search seeds to the given resource kinds.
	kinds []string
	// namespaces restricts search seeds to the given namespaces.
	namespaces []string
	// model overrides the projection's configured chat model (ask/query).
	model string
	// showContext prints the retrieval context that grounded an answer.
	showContext bool
	// showCards prints the card text of each search result.
	showCards bool
	// showCypher prints the generated Cypher of a query.
	showCypher bool
}

// hopsPtr converts the --hops flag into the request's optional hops field,
// where nil selects the server-side default.
func (r *ragOptions) hopsPtr() *int {
	if r.hops < 0 {
		return nil
	}
	h := r.hops
	return &h
}

// newSearchCmd builds "search <namespace> <projection> <query>...".
func newSearchCmd(opts *options) *cobra.Command {
	ropts := &ragOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "search <namespace> <projection> <query>...",
		Short: "Semantic search over a projection's resource graph",
		Long: "search embeds the query, finds the most similar resource nodes in the\n" +
			"projection's graph, and prints them with their similarity scores.\n\n" +
			"The projection must have GraphRAG enabled (spec.graphRAG) so resource\n" +
			"embeddings are available. The query may be given as one or more\n" +
			"arguments, which are joined with spaces.\n\n" +
			"Use --show-cards to also print the natural-language card of each\n" +
			"result, and -o json to print the full retrieval context (seeds, cards\n" +
			"and the connecting subgraph).",
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearch(cmd, ropts, args[0], args[1], strings.Join(args[2:], " "))
		},
	}

	cmd.Flags().IntVar(&ropts.topK, "top-k", 0,
		"Maximum number of matching resources to return (0 means the server default)")
	cmd.Flags().IntVar(&ropts.hops, "hops", -1,
		"Expand the subgraph this many hops around the matches (negative means the server default)")
	cmd.Flags().StringSliceVar(&ropts.edgeTypes, "edge-types", nil,
		"Only follow these relationship types when expanding (e.g. OWNS,SELECTS)")
	cmd.Flags().StringSliceVar(&ropts.kinds, "kinds", nil,
		"Only match resources of these Kinds (e.g. Pod,Service)")
	cmd.Flags().StringSliceVar(&ropts.namespaces, "namespaces", nil,
		"Only match resources in these namespaces")
	cmd.Flags().BoolVar(&ropts.showCards, "show-cards", false,
		"Also print each result's natural-language card")

	return cmd
}

func runSearch(cmd *cobra.Command, ropts *ragOptions, namespace, projection, query string) error {
	client, err := newClient(ropts.options)
	if err != nil {
		return err
	}

	result, err := client.Search(cmd.Context(), namespace, projection, SearchRequest{
		Query:      query,
		TopK:       ropts.topK,
		Hops:       ropts.hopsPtr(),
		EdgeTypes:  ropts.edgeTypes,
		Kinds:      ropts.kinds,
		Namespaces: ropts.namespaces,
	})
	if err != nil {
		return err
	}

	if ropts.output == outputJSON {
		return printJSON(cmd.OutOrStdout(), result)
	}

	out := cmd.OutOrStdout()
	tw := newTabWriter(out)
	_, _ = fmt.Fprintln(tw, "KIND\tNAME\tSCORE")
	for _, s := range result.Seeds {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%.3f\n", s.Kind, s.Name, s.Score)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if ropts.showCards {
		for _, c := range result.Cards {
			_, _ = fmt.Fprintf(out, "\n--- %s %s\n%s\n", c.Kind, qualifiedName(c.Namespace, c.Name),
				strings.TrimSpace(c.Text))
		}
	}
	return nil
}

// newAskCmd builds "ask <namespace> <projection> <question>...".
func newAskCmd(opts *options) *cobra.Command {
	ropts := &ragOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "ask <namespace> <projection> <question>...",
		Short: "Ask a natural-language question about a projection's cluster state",
		Long: "ask answers a question using retrieval-augmented generation: relevant\n" +
			"resources are retrieved from the projection's graph and a chat model\n" +
			"generates an answer grounded in them.\n\n" +
			"The projection must have GraphRAG and a chat model configured\n" +
			"(spec.graphRAG.chat). The question may be given as one or more\n" +
			"arguments, which are joined with spaces.\n\n" +
			"Use --show-context to also print the resources the answer was grounded\n" +
			"in, and -o json to print the full response including the retrieval\n" +
			"context.",
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAsk(cmd, ropts, args[0], args[1], strings.Join(args[2:], " "))
		},
	}

	cmd.Flags().IntVar(&ropts.topK, "top-k", 0,
		"Maximum number of resources retrieved as context (0 means the server default)")
	cmd.Flags().IntVar(&ropts.hops, "hops", -1,
		"Expand the context this many hops around the matches (negative means the server default)")
	cmd.Flags().StringSliceVar(&ropts.edgeTypes, "edge-types", nil,
		"Only follow these relationship types when expanding (e.g. OWNS,SELECTS)")
	cmd.Flags().StringVar(&ropts.model, "model", "",
		"Chat model to use (must be allowed by the projection; defaults to its configured model)")
	cmd.Flags().BoolVar(&ropts.showContext, "show-context", false,
		"Also print the resources the answer was grounded in")

	return cmd
}

func runAsk(cmd *cobra.Command, ropts *ragOptions, namespace, projection, question string) error {
	client, err := newClient(ropts.options)
	if err != nil {
		return err
	}

	result, err := client.Answer(cmd.Context(), namespace, projection, QuestionRequest{
		Question:  question,
		TopK:      ropts.topK,
		Hops:      ropts.hopsPtr(),
		EdgeTypes: ropts.edgeTypes,
		Model:     ropts.model,
	})
	if err != nil {
		return err
	}

	if ropts.output == outputJSON {
		return printJSON(cmd.OutOrStdout(), result)
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, strings.TrimSpace(result.Answer))

	if ropts.showContext && len(result.Retrieval.Seeds) > 0 {
		_, _ = fmt.Fprintln(out, "\nGrounded in:")
		tw := newTabWriter(out)
		_, _ = fmt.Fprintln(tw, "KIND\tNAME\tSCORE")
		for _, s := range result.Retrieval.Seeds {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%.3f\n", s.Kind, s.Name, s.Score)
		}
		return tw.Flush()
	}
	return nil
}

// newQueryCmd builds "query <namespace> <projection> <question>...".
func newQueryCmd(opts *options) *cobra.Command {
	ropts := &ragOptions{options: opts}

	cmd := &cobra.Command{
		Use:   "query <namespace> <projection> <question>...",
		Short: "Answer a question by running a generated read-only Cypher query",
		Long: "query translates a natural-language question into a guarded, read-only\n" +
			"Cypher query (text-to-Cypher), executes it against the projection's\n" +
			"graph, and prints the resulting rows.\n\n" +
			"The projection must have GraphRAG and a chat model configured\n" +
			"(spec.graphRAG.chat). The question may be given as one or more\n" +
			"arguments, which are joined with spaces.\n\n" +
			"Use --show-cypher to also print the generated query, and -o json to\n" +
			"print the full result.",
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQuery(cmd, ropts, args[0], args[1], strings.Join(args[2:], " "))
		},
	}

	cmd.Flags().StringVar(&ropts.model, "model", "",
		"Chat model to use (must be allowed by the projection; defaults to its configured model)")
	cmd.Flags().BoolVar(&ropts.showCypher, "show-cypher", false,
		"Also print the generated Cypher query")

	return cmd
}

func runQuery(cmd *cobra.Command, ropts *ragOptions, namespace, projection, question string) error {
	client, err := newClient(ropts.options)
	if err != nil {
		return err
	}

	result, err := client.Query(cmd.Context(), namespace, projection, QuestionRequest{
		Question: question,
		Model:    ropts.model,
	})
	if err != nil {
		return err
	}

	if ropts.output == outputJSON {
		return printJSON(cmd.OutOrStdout(), result)
	}

	out := cmd.OutOrStdout()
	if ropts.showCypher {
		_, _ = fmt.Fprintf(out, "%s\n\n", strings.TrimSpace(result.Cypher))
	}
	return printRows(out, result.Rows)
}

// printRows renders query result rows as a table. Columns are the union of the
// row keys, sorted for deterministic output.
func printRows(out io.Writer, rows []map[string]any) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "(no rows)")
		return err
	}

	colSet := map[string]bool{}
	for _, row := range rows {
		for k := range row {
			colSet[k] = true
		}
	}
	cols := make([]string, 0, len(colSet))
	for k := range colSet {
		cols = append(cols, k)
	}
	sort.Strings(cols)

	tw := newTabWriter(out)
	_, _ = fmt.Fprintln(tw, strings.ToUpper(strings.Join(cols, "\t")))
	for _, row := range rows {
		cells := make([]string, 0, len(cols))
		for _, c := range cols {
			cells = append(cells, dash(cellString(row[c])))
		}
		_, _ = fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}

// cellString renders a single row value for table output.
func cellString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		// JSON numbers decode as float64; print integers without a decimal.
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// qualifiedName renders "namespace/name", or just the name for cluster-scoped
// resources.
func qualifiedName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}
