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

// Command openapi writes the generated OpenAPI 3 document (YAML) for the Gamera
// HTTP API. With no -o flag it prints to stdout. Regenerate the checked-in spec
// with `make openapi` (or `go generate ./...`).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/project-gamera/gamera/internal/api"
)

func main() {
	out := flag.String("o", "", "output file (default: stdout)")
	flag.Parse()

	spec, err := api.OpenAPIYAML()
	if err != nil {
		fmt.Fprintln(os.Stderr, "generating openapi spec:", err)
		os.Exit(1)
	}
	if *out == "" {
		if _, err := os.Stdout.Write(spec); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(*out, spec, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
