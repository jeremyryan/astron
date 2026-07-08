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

// Command astron is the command-line client for Astron. It talks to a
// running operator's read API to inspect projections and the graph they
// materialize.
package main

import (
	"github.com/project-astron/astron/internal/cli"
)

func main() {
	cli.Execute()
}
