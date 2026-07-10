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
type FakeChat struct {
	// Reply, when non-empty, is returned for every completion.
	Reply string
	// ReplyFunc, when set, computes the reply from the messages. It takes
	// precedence over Reply.
	ReplyFunc func([]Message) string
	// ModelName, when non-empty, is reported by Model (default "fake").
	ModelName string
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

// compile-time assertions that FakeChat satisfies Chat and ModelSelector.
var (
	_ Chat          = (*FakeChat)(nil)
	_ ModelSelector = (*FakeChat)(nil)
)
