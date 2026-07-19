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
	"os"

	"sigs.k8s.io/yaml"
)

// Environment variables consulted by LoadNeo4jConfig. They mirror the
// corresponding command-line flags and override values from the configuration
// file.
const (
	EnvNeo4jURI      = "ASTRON_NEO4J_URI"
	EnvNeo4jDatabase = "ASTRON_NEO4J_DATABASE"
	EnvNeo4jUsername = "ASTRON_NEO4J_USERNAME"
	EnvNeo4jPassword = "ASTRON_NEO4J_PASSWORD"
)

// DefaultNeo4jDatabase is used when no database name is configured.
const DefaultNeo4jDatabase = "neo4j"

// LoadNeo4jConfig assembles the controller-wide Neo4J connection settings
// shared by every GraphProjection. Values are merged with the following
// precedence (highest wins):
//
//  1. Command-line flags (the non-empty fields of flagCfg)
//  2. Environment variables (ASTRON_NEO4J_URI, ASTRON_NEO4J_DATABASE,
//     ASTRON_NEO4J_USERNAME, ASTRON_NEO4J_PASSWORD)
//  3. A YAML configuration file (uri/database/username/password keys),
//     typically mounted from a ConfigMap
//
// The database defaults to "neo4j" when unset. An empty configFile is
// ignored; a configFile that cannot be read or parsed is an error.
func LoadNeo4jConfig(flagCfg Neo4jConfig, configFile string) (Neo4jConfig, error) {
	var cfg Neo4jConfig

	if configFile != "" {
		raw, err := os.ReadFile(configFile)
		if err != nil {
			return Neo4jConfig{}, fmt.Errorf("reading Neo4J config file %q: %w", configFile, err)
		}
		var file struct {
			URI      string `json:"uri"`
			Database string `json:"database"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := yaml.UnmarshalStrict(raw, &file); err != nil {
			return Neo4jConfig{}, fmt.Errorf("parsing Neo4J config file %q: %w", configFile, err)
		}
		cfg = Neo4jConfig{URI: file.URI, Database: file.Database, Username: file.Username, Password: file.Password}
	}

	overlay := func(dst *string, value string) {
		if value != "" {
			*dst = value
		}
	}
	overlay(&cfg.URI, os.Getenv(EnvNeo4jURI))
	overlay(&cfg.Database, os.Getenv(EnvNeo4jDatabase))
	overlay(&cfg.Username, os.Getenv(EnvNeo4jUsername))
	overlay(&cfg.Password, os.Getenv(EnvNeo4jPassword))

	overlay(&cfg.URI, flagCfg.URI)
	overlay(&cfg.Database, flagCfg.Database)
	overlay(&cfg.Username, flagCfg.Username)
	overlay(&cfg.Password, flagCfg.Password)

	if cfg.Database == "" {
		cfg.Database = DefaultNeo4jDatabase
	}
	return cfg, nil
}
