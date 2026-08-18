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

package rag

import (
	"context"
	"encoding/json"
	"testing"
)

// TestFakeChatCompleteWithToolsFallsBackWithoutScript verifies that with no
// ToolScript or ToolCallFunc configured, CompleteWithTools behaves like
// Complete wrapped in a content-only Reply (the default echo behavior).
func TestFakeChatCompleteWithToolsFallsBackWithoutScript(t *testing.T) {
	f := &FakeChat{}
	reply, err := f.CompleteWithTools(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("CompleteWithTools: %v", err)
	}
	if reply.Content != "hi" || len(reply.ToolCalls) != 0 {
		t.Fatalf("unexpected reply: %+v", reply)
	}
}

// TestFakeChatCompleteWithToolsScript verifies ToolScript is consumed in
// order across successive calls, then falls back once exhausted.
func TestFakeChatCompleteWithToolsScript(t *testing.T) {
	f := &FakeChat{
		Reply: "fallback",
		ToolScript: []Reply{
			{ToolCalls: []ToolCall{{ID: "call_1", Name: "search_cluster_graph", Arguments: json.RawMessage(`{}`)}}},
			{Content: "final answer"},
		},
	}

	first, err := f.CompleteWithTools(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("first CompleteWithTools: %v", err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != "search_cluster_graph" {
		t.Fatalf("unexpected first reply: %+v", first)
	}

	second, err := f.CompleteWithTools(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("second CompleteWithTools: %v", err)
	}
	if second.Content != "final answer" {
		t.Fatalf("unexpected second reply: %+v", second)
	}

	// Script exhausted: falls back to Reply.
	third, err := f.CompleteWithTools(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("third CompleteWithTools: %v", err)
	}
	if third.Content != "fallback" || len(third.ToolCalls) != 0 {
		t.Fatalf("unexpected third reply: %+v", third)
	}
}

// TestFakeChatCompleteWithToolsFunc verifies ToolCallFunc takes precedence
// over ToolScript and is invoked with the live messages/tools on every call.
func TestFakeChatCompleteWithToolsFunc(t *testing.T) {
	var gotTools []ToolSpec
	f := &FakeChat{
		ToolScript: []Reply{{Content: "should not be used"}},
		ToolCallFunc: func(messages []Message, tools []ToolSpec) Reply {
			gotTools = tools
			return Reply{Content: "computed: " + messages[len(messages)-1].Content}
		},
	}
	tools := []ToolSpec{{Name: "query_graph"}}
	reply, err := f.CompleteWithTools(context.Background(), []Message{{Role: RoleUser, Content: "q"}}, tools)
	if err != nil {
		t.Fatalf("CompleteWithTools: %v", err)
	}
	if reply.Content != "computed: q" {
		t.Fatalf("Content = %q", reply.Content)
	}
	if len(gotTools) != 1 || gotTools[0].Name != "query_graph" {
		t.Fatalf("tools not passed through: %+v", gotTools)
	}
}

// TestFakeChatWithModelCopiesScriptCursor verifies WithModel is a plain value
// copy: the returned instance shares the script but starts from the cursor
// position at the time of the copy, and the two instances then advance
// independently.
func TestFakeChatWithModelCopiesScriptCursor(t *testing.T) {
	f := &FakeChat{ToolScript: []Reply{{Content: "one"}, {Content: "two"}, {Content: "three"}}}
	if reply, err := f.CompleteWithTools(context.Background(), nil, nil); err != nil || reply.Content != "one" {
		t.Fatalf("first call = %+v, err %v", reply, err)
	}

	// Copied after the first call: inherits the cursor at index 1.
	other := f.WithModel("other-model").(*FakeChat)
	if reply, err := other.CompleteWithTools(context.Background(), nil, nil); err != nil || reply.Content != "two" {
		t.Fatalf("copy's first call = %+v, err %v", reply, err)
	}

	// The two instances now advance independently.
	if reply, err := f.CompleteWithTools(context.Background(), nil, nil); err != nil || reply.Content != "two" {
		t.Fatalf("original's second call = %+v, err %v", reply, err)
	}
	if reply, err := other.CompleteWithTools(context.Background(), nil, nil); err != nil || reply.Content != "three" {
		t.Fatalf("copy's second call = %+v, err %v", reply, err)
	}
}
