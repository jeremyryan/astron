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

import "context"

// FakeChat is a deterministic, dependency-free Chat for tests and offline
// development. By default it echoes the last user message; a fixed Reply or a
// ReplyFunc can be supplied to script responses (e.g. to return canned Cypher).
//
// FakeChat also implements ToolCaller, so it can drive deterministic,
// token-free tests of tool-using agent loops: ToolScript supplies a sequence
// of Replies (e.g. a tool call, then a final answer), or ToolCallFunc computes
// one from the live messages and tools.
type FakeChat struct {
	// Reply, when non-empty, is returned for every completion.
	Reply string
	// ReplyFunc, when set, computes the reply from the messages. It takes
	// precedence over Reply.
	ReplyFunc func([]Message) string
	// ModelName, when non-empty, is reported by Model (default "fake").
	ModelName string

	// ToolScript is a sequence of Replies returned by CompleteWithTools, one
	// per call, in order (e.g. a first call requesting a tool, then a second
	// call with the final answer once the tool's result is in the transcript).
	// Once exhausted, CompleteWithTools falls back to Complete/ReplyFunc/Reply
	// wrapped in a content-only Reply.
	ToolScript []Reply
	// ToolCallFunc, when set, computes the CompleteWithTools reply from the
	// messages and advertised tools. It takes precedence over ToolScript.
	ToolCallFunc func(messages []Message, tools []ToolSpec) Reply

	// toolStep tracks how far into ToolScript this instance has progressed.
	toolStep int
}

// NewFakeChat returns a FakeChat that always returns the given reply (or echoes
// the last user message when reply is empty).
func NewFakeChat(reply string) *FakeChat {
	return &FakeChat{Reply: reply}
}

// Complete returns the scripted reply.
func (f *FakeChat) Complete(_ context.Context, messages []Message) (string, error) {
	if f.ReplyFunc != nil {
		return f.ReplyFunc(messages), nil
	}
	if f.Reply != "" {
		return f.Reply, nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			return messages[i].Content, nil
		}
	}
	return "", nil
}

// Model identifies the fake chat model.
func (f *FakeChat) Model() string {
	if f.ModelName != "" {
		return f.ModelName
	}
	return string(ProviderFake)
}

// WithModel returns a copy of the fake reporting a different model name, so
// per-request model selection can be exercised in tests and offline setups.
func (f *FakeChat) WithModel(model string) Chat {
	cp := *f
	cp.ModelName = model
	return &cp
}

// CompleteWithTools returns the next scripted Reply. With ToolCallFunc set,
// every call is computed from the live messages/tools. With ToolScript set,
// calls are consumed from it in order; once exhausted (or when neither is
// set), it falls back to a content-only Reply from Complete.
func (f *FakeChat) CompleteWithTools(ctx context.Context, messages []Message, tools []ToolSpec) (Reply, error) {
	if f.ToolCallFunc != nil {
		return f.ToolCallFunc(messages, tools), nil
	}
	if f.toolStep < len(f.ToolScript) {
		reply := f.ToolScript[f.toolStep]
		f.toolStep++
		return reply, nil
	}
	content, err := f.Complete(ctx, messages)
	if err != nil {
		return Reply{}, err
	}
	return Reply{Content: content}, nil
}

// compile-time assertions that FakeChat satisfies Chat, ModelSelector and
// ToolCaller.
var (
	_ Chat          = (*FakeChat)(nil)
	_ ModelSelector = (*FakeChat)(nil)
	_ ToolCaller    = (*FakeChat)(nil)
)
