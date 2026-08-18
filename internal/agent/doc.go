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

// Package agent implements a bounded, tool-using chat loop for answering
// questions about a single Astron GraphProjection: given a rag.Chat that
// supports tool calling (rag.ToolCaller) and a ToolSet of read-only,
// projection-scoped tools, the Runner repeatedly offers the tools to the
// model and executes what it asks for, until it returns a final answer or a
// step budget is exhausted.
//
// This package is transport- and data-layer-agnostic: it only depends on the
// rag tool-calling types. Wiring a ToolSet to an actual Projector (so tools
// really search/query/read the graph) is done by the caller — see
// internal/projector's AnswerWithTools.
package agent
