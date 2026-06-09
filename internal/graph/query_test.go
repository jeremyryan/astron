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

import "testing"

func TestValidateReadOnlyCypherAccepts(t *testing.T) {
	valid := []string{
		"MATCH (p:Pod {_projection: $projection}) RETURN p.name",
		"MATCH (p:Pod {_projection: $projection}) RETURN count(p) AS n",
		"  match (n) where n._projection = $projection return n limit 10  ",
		"MATCH (a)-[:OWNS]->(b) WHERE a._projection = $projection RETURN a, b;",
	}
	for _, q := range valid {
		if err := ValidateReadOnlyCypher(q); err != nil {
			t.Errorf("expected %q to be accepted, got: %v", q, err)
		}
	}
}

func TestValidateReadOnlyCypherRejects(t *testing.T) {
	cases := map[string]string{
		"empty":           "   ",
		"no return":       "MATCH (p:Pod) WHERE p._projection = $projection",
		"create":          "CREATE (p:Pod {name:'x'}) RETURN p",
		"merge":           "MERGE (p:Pod {name:'x'}) RETURN p",
		"delete":          "MATCH (p:Pod) DELETE p RETURN 1",
		"detach delete":   "MATCH (p:Pod) DETACH DELETE p RETURN 1",
		"set":             "MATCH (p:Pod) SET p.x = 1 RETURN p",
		"remove":          "MATCH (p:Pod) REMOVE p.x RETURN p",
		"call procedure":  "CALL db.labels() YIELD label RETURN label",
		"apoc":            "CALL apoc.cypher.run('MATCH (n) RETURN n', {}) YIELD value RETURN value",
		"load csv":        "LOAD CSV FROM 'file:///x.csv' AS row RETURN row",
		"multi statement": "MATCH (p:Pod) RETURN p; MATCH (s:Service) RETURN s",
		"use database":    "USE neo4j MATCH (p:Pod) RETURN p",
	}
	for name, q := range cases {
		if err := ValidateReadOnlyCypher(q); err == nil {
			t.Errorf("%s: expected %q to be rejected", name, q)
		}
	}
}

func TestConvertValueStripsInternalNodeProps(t *testing.T) {
	props := map[string]any{
		"name":             "web",
		"kind":             "Pod",
		"_key":             "proj|uid",
		projectionProperty: "proj",
		syncTokenProperty:  "tok",
		embeddingProperty:  []any{0.1, 0.2},
		cardProperty:       "Pod web ...",
	}
	out := stripInternal(convertProps(props))

	if out["name"] != "web" || out["kind"] != "Pod" {
		t.Errorf("expected identity props preserved, got %#v", out)
	}
	for _, hidden := range []string{"_key", projectionProperty, syncTokenProperty, embeddingProperty, cardProperty} {
		if _, ok := out[hidden]; ok {
			t.Errorf("expected internal prop %q to be stripped", hidden)
		}
	}
}
