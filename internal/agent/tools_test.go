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

package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/project-astron/astron/internal/rag"
)

func fixedTool(name string) Tool {
	return Tool{
		Spec: rag.ToolSpec{Name: name},
		Invoke: func(context.Context, json.RawMessage) (string, error) {
			return name + "-result", nil
		},
	}
}

func TestToolSetSpecs(t *testing.T) {
	ts := ToolSet{fixedTool("a"), fixedTool("b")}
	specs := ts.Specs()
	if len(specs) != 2 || specs[0].Name != "a" || specs[1].Name != "b" {
		t.Fatalf("unexpected specs: %+v", specs)
	}
}

func TestToolSetFind(t *testing.T) {
	ts := ToolSet{fixedTool("a"), fixedTool("b")}

	tool, ok := ts.find("b")
	if !ok {
		t.Fatal("expected to find tool b")
	}
	out, err := tool.Invoke(context.Background(), nil)
	if err != nil || out != "b-result" {
		t.Fatalf("Invoke = %q, %v", out, err)
	}

	if _, ok := ts.find("missing"); ok {
		t.Fatal("did not expect to find an unregistered tool")
	}
}
