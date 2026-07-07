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

package graph

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// queryTimeout bounds how long a single read-only query may run, protecting the
// database from expensive or runaway LLM-generated Cypher.
const queryTimeout = 10 * time.Second

// projectionParam is the Cypher parameter name under which the owning
// projection id is exposed to read-only queries, so generated Cypher can (and
// is instructed to) scope itself with `{_projection: $projection}`.
const projectionParam = "projection"

// forbiddenClause matches Cypher clauses that write or invoke procedures. It is
// used as defense-in-depth on top of running queries in a read-only
// transaction (which the server also enforces). Blocking CALL prevents
// procedure invocation (e.g. apoc.*, db.*, dbms.*) entirely.
var forbiddenClause = regexp.MustCompile(`(?i)\b(CREATE|MERGE|DELETE|DETACH|SET|REMOVE|DROP|FOREACH|CALL|LOAD|USE|GRANT|REVOKE|START|TERMINATE)\b`)

// returnClause requires a query to project rows, ensuring it is a read.
var returnClause = regexp.MustCompile(`(?i)\bRETURN\b`)

// QueryStore is an optional capability for stores that can execute guarded,
// read-only ad-hoc queries (used by text-to-Cypher). Like VectorStore it is
// separate from Store so the feature stays additive and callers can degrade
// gracefully.
type QueryStore interface {
	// ReadOnlyQuery validates and executes a single read-only Cypher statement
	// against the projection, returning the result rows as JSON-friendly maps.
	// The owning projection id is provided to the query as $projection. The
	// query runs in a read-only transaction with a statement timeout; writes and
	// procedure calls are rejected.
	ReadOnlyQuery(ctx context.Context, projection ProjectionID, cypher string, params map[string]any) ([]map[string]any, error)
}

// compile-time assertion that Neo4jStore satisfies QueryStore.
var _ QueryStore = (*Neo4jStore)(nil)

// ValidateReadOnlyCypher checks that a Cypher statement is a single read-only
// query: no write/DDL/procedure clauses, no statement chaining, and a RETURN.
// It returns a descriptive error when the statement is rejected.
func ValidateReadOnlyCypher(cypher string) error {
	trimmed := strings.TrimSpace(cypher)
	if trimmed == "" {
		return fmt.Errorf("empty query")
	}

	// Allow a single trailing semicolon; reject anything that chains statements.
	body := strings.TrimRight(trimmed, "; \t\r\n")
	if strings.Contains(body, ";") {
		return fmt.Errorf("multiple statements are not allowed")
	}

	if loc := forbiddenClause.FindString(body); loc != "" {
		return fmt.Errorf("query contains a forbidden clause %q: only read-only queries are allowed", strings.ToUpper(loc))
	}
	if !returnClause.MatchString(body) {
		return fmt.Errorf("query must contain a RETURN clause")
	}
	return nil
}

// ReadOnlyQuery validates and runs a read-only Cypher query for a projection.
func (s *Neo4jStore) ReadOnlyQuery(ctx context.Context, projection ProjectionID, cypher string, params map[string]any) ([]map[string]any, error) {
	if err := ValidateReadOnlyCypher(cypher); err != nil {
		return nil, fmt.Errorf("rejected query: %w", err)
	}

	// Merge caller params with the injected projection scope. The projection
	// parameter is reserved and always set by us.
	merged := make(map[string]any, len(params)+1)
	for k, v := range params {
		if k == projectionParam {
			continue
		}
		merged[k] = v
	}
	merged[projectionParam] = string(projection)

	sess := s.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: s.database,
		AccessMode:   neo4j.AccessModeRead,
	})
	defer func() { _ = sess.Close(ctx) }()

	result, err := sess.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		res, err := tx.Run(ctx, cypher, merged)
		if err != nil {
			return nil, err
		}
		records, err := res.Collect(ctx)
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]any, 0, len(records))
		for _, rec := range records {
			row := make(map[string]any, len(rec.Keys))
			for i, key := range rec.Keys {
				row[key] = convertValue(rec.Values[i])
			}
			rows = append(rows, row)
		}
		return rows, nil
	}, neo4j.WithTxTimeout(queryTimeout))
	if err != nil {
		return nil, fmt.Errorf("executing read-only query for projection %q: %w", projection, err)
	}
	return result.([]map[string]any), nil
}

// convertValue converts neo4j graph values into plain, JSON-serializable Go
// values, recursing through containers. Internal bookkeeping properties are
// stripped from nodes and relationships.
func convertValue(v any) any {
	switch val := v.(type) {
	case neo4j.Node:
		return map[string]any{
			"labels":     val.Labels,
			"properties": stripInternal(convertProps(val.Props)),
		}
	case neo4j.Relationship:
		return map[string]any{
			"type":       val.Type,
			"properties": stripInternal(convertProps(val.Props)),
		}
	case []any:
		out := make([]any, len(val))
		for i, e := range val {
			out[i] = convertValue(e)
		}
		return out
	case map[string]any:
		return convertProps(val)
	default:
		return val
	}
}

func convertProps(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	for k, v := range props {
		out[k] = convertValue(v)
	}
	return out
}

// stripInternal removes Astron/vector bookkeeping keys from a property map so
// query results stay clean and don't leak large embedding vectors.
func stripInternal(props map[string]any) map[string]any {
	for _, k := range []string{
		"_key", projectionProperty, syncTokenProperty, manualProperty,
		embeddingProperty, cardProperty, cardHashProperty, embeddingModelProperty,
	} {
		delete(props, k)
	}
	return props
}
