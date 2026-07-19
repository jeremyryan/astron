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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

func completionServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/projections":
			_ = json.NewEncoder(w).Encode([]Projection{
				{Namespace: "demo", Name: "web"},
				{Namespace: "demo", Name: "batch"},
				{Namespace: "other", Name: "infra"},
			})
		case apiViewsPath:
			_ = json.NewEncoder(w).Encode([]View{
				{Namespace: "demo", Name: "web-compute"},
				{Namespace: "other", Name: "infra-view"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "x"}
	cmd.SetContext(context.Background())
	return cmd
}

func TestCompleteProjectionArgs(t *testing.T) {
	srv := completionServer(t)
	defer srv.Close()
	fn := completeProjectionArgs(&options{server: srv.URL})
	cmd := newCompletionCmd()

	// First arg: unique namespaces.
	got, directive := fn(cmd, nil, "")
	if !slices.Contains(got, "demo") || !slices.Contains(got, "other") || len(got) != 2 {
		t.Errorf("namespace completions = %v", got)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("unexpected directive: %v", directive)
	}

	// Second arg: projections within the chosen namespace.
	got, _ = fn(cmd, []string{"demo"}, "")
	if !slices.Contains(got, "web") || !slices.Contains(got, "batch") || slices.Contains(got, "infra") {
		t.Errorf("projection completions = %v", got)
	}

	// Later args: default completion.
	if _, directive := fn(cmd, []string{"demo", "web"}, ""); directive != cobra.ShellCompDirectiveDefault {
		t.Errorf("expected default directive for later args, got %v", directive)
	}
}

func TestCompleteViewArgs(t *testing.T) {
	srv := completionServer(t)
	defer srv.Close()
	fn := completeViewArgs(&options{server: srv.URL})
	cmd := newCompletionCmd()

	got, _ := fn(cmd, []string{"demo"}, "")
	if !slices.Contains(got, "web-compute") || slices.Contains(got, "infra-view") {
		t.Errorf("view completions = %v", got)
	}
}

func TestCompletionUnreachableServer(t *testing.T) {
	fn := completeProjectionArgs(&options{server: "http://127.0.0.1:1"})
	if _, directive := fn(newCompletionCmd(), nil, ""); directive != cobra.ShellCompDirectiveError {
		t.Errorf("expected error directive, got %v", directive)
	}
}
