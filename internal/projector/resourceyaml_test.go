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

package projector

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// newResourceYAMLFixture builds a Projector with a fake dynamic client seeded
// with one ConfigMap carrying the noisy fields ResourceYAML is expected to
// strip (managedFields, the last-applied annotation), alongside a real one.
func newResourceYAMLFixture(t *testing.T) *Projector {
	t.Helper()
	cmGVK := schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	mapper.Add(cmGVK, meta.RESTScopeNamespace)

	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	})

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "app-config",
			"namespace": "shop",
			"annotations": map[string]any{
				"kubectl.kubernetes.io/last-applied-configuration": `{"apiVersion":"v1"}`,
				"keep-me": "yes",
			},
			"managedFields": []any{map[string]any{"manager": "kubectl"}},
		},
		"data": map[string]any{"key": "value"},
	}}
	if _, err := dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).
		Namespace("shop").Create(context.Background(), obj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seeding fake ConfigMap: %v", err)
	}

	return New(Options{
		ID:      "proj-yaml",
		Dynamic: dyn,
		Mapper:  mapper,
		Store:   &retrievalStore{data: sampleGraph()},
	})
}

func TestResourceYAMLStripsNoisyFields(t *testing.T) {
	p := newResourceYAMLFixture(t)
	out, err := p.ResourceYAML(context.Background(), "v1", "ConfigMap", "shop", "app-config")
	if err != nil {
		t.Fatalf("ResourceYAML: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "managedFields") {
		t.Errorf("managedFields not stripped: %s", s)
	}
	if strings.Contains(s, "last-applied-configuration") {
		t.Errorf("last-applied annotation not stripped: %s", s)
	}
	if !strings.Contains(s, "keep-me") {
		t.Errorf("unrelated annotation should be kept: %s", s)
	}
	if !strings.Contains(s, "app-config") {
		t.Errorf("expected the resource's own name in output: %s", s)
	}
}

func TestResourceYAMLNotFound(t *testing.T) {
	p := newResourceYAMLFixture(t)
	if _, err := p.ResourceYAML(context.Background(), "v1", "ConfigMap", "shop", "missing"); err == nil {
		t.Fatal("expected an error for a missing resource")
	}
}

func TestResourceYAMLInvalidAPIVersion(t *testing.T) {
	p := newResourceYAMLFixture(t)
	if _, err := p.ResourceYAML(context.Background(), "not a version", "ConfigMap", "shop", "app-config"); err == nil {
		t.Fatal("expected an error for an invalid apiVersion")
	}
}

func TestResourceYAMLUnknownKind(t *testing.T) {
	p := newResourceYAMLFixture(t)
	if _, err := p.ResourceYAML(context.Background(), "v1", "NoSuchKind", "shop", "app-config"); err == nil {
		t.Fatal("expected an error for an unmapped kind")
	}
}

func TestResourceYAMLUnavailableWithoutClient(t *testing.T) {
	p := New(Options{ID: "proj-yaml", Store: &retrievalStore{data: sampleGraph()}})
	if _, err := p.ResourceYAML(context.Background(), "v1", "ConfigMap", "shop", "app-config"); err != ErrLiveResourceUnavailable {
		t.Fatalf("err = %v, want ErrLiveResourceUnavailable", err)
	}
}

// TestToolResourceYAML verifies the get_resource_yaml tool handler parses
// arguments, delegates to ResourceYAML, and requires the mandatory fields.
func TestToolResourceYAML(t *testing.T) {
	p := newResourceYAMLFixture(t)

	out, err := p.toolResourceYAML(context.Background(),
		json.RawMessage(`{"apiVersion":"v1","kind":"ConfigMap","namespace":"shop","name":"app-config"}`))
	if err != nil {
		t.Fatalf("toolResourceYAML: %v", err)
	}
	if !strings.Contains(out, "app-config") {
		t.Errorf("unexpected output: %s", out)
	}

	if _, err := p.toolResourceYAML(context.Background(), json.RawMessage(`{"kind":"ConfigMap","name":"app-config"}`)); err == nil {
		t.Fatal("expected an error for a missing apiVersion")
	}
}
