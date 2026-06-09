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
	"encoding/json"
	"io"
	"text/tabwriter"
)

// Supported values for the --output flag.
const (
	outputTable = "table"
	outputJSON  = "json"
)

// newTabWriter returns a tabwriter configured for the CLI's table output.
func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
}

// printJSON writes v as indented JSON to w.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// dash returns "-" for empty strings, so table cells are never blank.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
