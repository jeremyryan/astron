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
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNeo4jConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "neo4j.yaml")
	content := "uri: neo4j://file:7687\ndatabase: filedb\nusername: fileuser\npassword: filepass\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// File only.
	cfg, err := LoadNeo4jConfig(Neo4jConfig{}, file)
	if err != nil {
		t.Fatalf("LoadNeo4jConfig: %v", err)
	}
	want := Neo4jConfig{URI: "neo4j://file:7687", Database: "filedb", Username: "fileuser", Password: "filepass"}
	if cfg != want {
		t.Errorf("file-only config = %+v, want %+v", cfg, want)
	}

	// Env overrides file.
	t.Setenv(EnvNeo4jURI, "neo4j://env:7687")
	t.Setenv(EnvNeo4jPassword, "envpass")
	cfg, err = LoadNeo4jConfig(Neo4jConfig{}, file)
	if err != nil {
		t.Fatalf("LoadNeo4jConfig: %v", err)
	}
	if cfg.URI != "neo4j://env:7687" || cfg.Password != "envpass" {
		t.Errorf("env should override file: %+v", cfg)
	}
	if cfg.Database != "filedb" || cfg.Username != "fileuser" {
		t.Errorf("unset env vars should keep file values: %+v", cfg)
	}

	// Flags override env and file.
	cfg, err = LoadNeo4jConfig(Neo4jConfig{URI: "neo4j://flag:7687", Database: "flagdb"}, file)
	if err != nil {
		t.Fatalf("LoadNeo4jConfig: %v", err)
	}
	if cfg.URI != "neo4j://flag:7687" || cfg.Database != "flagdb" {
		t.Errorf("flags should override env and file: %+v", cfg)
	}
	if cfg.Password != "envpass" {
		t.Errorf("password should fall through to env: %+v", cfg)
	}
}

func TestLoadNeo4jConfigDefaultsAndErrors(t *testing.T) {
	// Database defaults when nothing sets it.
	cfg, err := LoadNeo4jConfig(Neo4jConfig{URI: "neo4j://x:7687"}, "")
	if err != nil {
		t.Fatalf("LoadNeo4jConfig: %v", err)
	}
	if cfg.Database != DefaultNeo4jDatabase {
		t.Errorf("database = %q, want default %q", cfg.Database, DefaultNeo4jDatabase)
	}

	// Missing file is an error.
	if _, err := LoadNeo4jConfig(Neo4jConfig{}, filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("expected error for a missing config file")
	}

	// Unknown keys in the file are rejected (typo protection).
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("url: oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadNeo4jConfig(Neo4jConfig{}, bad); err == nil {
		t.Error("expected error for unknown config file keys")
	}
}
