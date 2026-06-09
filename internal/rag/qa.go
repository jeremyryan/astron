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
	"fmt"
	"strings"
)

// CypherMessages builds the chat messages that ask a model to translate a
// natural-language question into a single read-only Cypher query grounded in
// the given schema. The model is instructed to scope results to the projection
// via the $projection parameter and to return only the query.
func CypherMessages(schema, question string) []Message {
	system := `You translate natural-language questions about a Kubernetes cluster
into a single read-only Neo4J Cypher query.

Rules:
- Output ONLY the Cypher query, with no prose and no Markdown code fences.
- Generate exactly one statement. It MUST be read-only: never use CREATE, MERGE,
  DELETE, SET, REMOVE, or CALL.
- The query MUST include a RETURN clause.
- Scope every matched K8sResource to the current projection by matching the
  property ` + "`_projection: $projection`" + ` on each node, e.g.
  MATCH (p:Pod {_projection: $projection}).
- Use only the labels, properties and relationship types described in the schema.

Schema:
` + schema

	user := "Question: " + strings.TrimSpace(question)
	return []Message{
		{Role: RoleSystem, Content: system},
		{Role: RoleUser, Content: user},
	}
}

// ExtractCypher cleans a model's reply into a bare Cypher statement: it strips
// Markdown code fences (``` or ```cypher) and surrounding whitespace.
func ExtractCypher(reply string) string {
	s := strings.TrimSpace(reply)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening fence line (which may carry a language hint).
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:]
	} else {
		s = strings.TrimPrefix(s, "```")
	}
	// Drop a trailing closing fence.
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// AnswerMessages builds the chat messages that ask a model to answer a question
// using only the supplied retrieval context (resource cards and relationships),
// citing the resources it relies on.
func AnswerMessages(question string, cards []Card, relationships []string) []Message {
	var ctx strings.Builder
	ctx.WriteString("Resources:\n")
	if len(cards) == 0 {
		ctx.WriteString("(none)\n")
	}
	for _, c := range cards {
		ctx.WriteString("- " + c.Text + "\n")
	}
	if len(relationships) > 0 {
		ctx.WriteString("\nRelationships:\n")
		for _, r := range relationships {
			ctx.WriteString("- " + r + "\n")
		}
	}

	system := `You are a Kubernetes cluster assistant. Answer the user's question
using ONLY the provided context about cluster resources and their
relationships. If the context is insufficient, say so. Cite the specific
resources (kind and name) you used in your answer. Be concise.`

	user := fmt.Sprintf("Context:\n%s\nQuestion: %s", ctx.String(), strings.TrimSpace(question))
	return []Message{
		{Role: RoleSystem, Content: system},
		{Role: RoleUser, Content: user},
	}
}
