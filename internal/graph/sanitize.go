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
	"fmt"
	"regexp"
)

// relTypePattern restricts relationship types to a safe identifier form. Since
// Cypher does not allow relationship types to be parameterized, the type must
// be interpolated directly into the query; validating it prevents injection.
var relTypePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// sanitizeRelType validates a relationship type before it is interpolated into
// a Cypher query. It returns an error for empty or unsafe values.
func sanitizeRelType(relType string) (string, error) {
	if relType == "" {
		return "", fmt.Errorf("relationship type must not be empty")
	}
	if !relTypePattern.MatchString(relType) {
		return "", fmt.Errorf("invalid relationship type %q: must match %s", relType, relTypePattern.String())
	}
	return relType, nil
}
