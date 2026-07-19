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

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	astronv1alpha1 "github.com/project-astron/astron/api/v1alpha1"
)

func TestDescribeProjection(t *testing.T) {
	p := astronv1alpha1.GraphProjection{
		TypeMeta:   metav1.TypeMeta{APIVersion: astronv1alpha1.GroupVersion.String(), Kind: "GraphProjection"},
		ObjectMeta: metav1.ObjectMeta{Namespace: demoNS, Name: "web"},
		Spec: astronv1alpha1.GraphProjectionSpec{
			Neo4j: astronv1alpha1.Neo4jConnection{
				URI:           "neo4j://x:7687",
				Database:      "neo4j",
				AuthSecretRef: astronv1alpha1.SecretReference{Name: "creds"},
			},
			Scope: astronv1alpha1.ProjectionScope{
				Namespaces: []string{demoNS},
				Resources: []astronv1alpha1.ResourceSelector{
					{Group: "apps", Version: "v1", Kind: "Deployment"},
					{Group: "", Version: "v1", Kind: "Pod"},
				},
			},
			Relationships: []astronv1alpha1.RelationshipRule{
				{Name: "deployment-owns-pod", Type: "OWNS",
					From:     astronv1alpha1.ResourceSelector{Kind: "Deployment"},
					To:       astronv1alpha1.ResourceSelector{Kind: "Pod"},
					Strategy: astronv1alpha1.OwnerReferenceStrategy},
			},
			GraphRAG: &astronv1alpha1.GraphRAGSpec{
				Enabled:   true,
				Embedding: astronv1alpha1.EmbeddingConfig{Provider: "openai", Model: "text-embedding-3-small"},
			},
		},
		Status: astronv1alpha1.GraphProjectionStatus{
			Phase:             "Ready",
			NodeCount:         12,
			RelationshipCount: 30,
			Conditions: []metav1.Condition{
				{Type: "Available", Status: metav1.ConditionTrue, Reason: "Synced", Message: "all good"},
			},
		},
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}

	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{graphProjectionGVR: "GraphProjectionList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, u)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)

	dopts := &describeOptions{options: &options{output: outputTable}}
	if err := describeProjection(cmd, dopts, dyn, demoNS, "web"); err != nil {
		t.Fatalf("describeProjection: %v", err)
	}

	for _, want := range []string{
		"Name:         web",
		"URI:        neo4j://x:7687",
		"Namespaces: demo",
		"Resources:  apps/Deployment, Pod",
		"deployment-owns-pod: OWNS Deployment -> Pod (OwnerReference)",
		"Embedding:  openai text-embedding-3-small",
		"Chat:       (disabled)",
		"Phase:      Ready",
		"Graph:      12 nodes, 30 edges",
		"Available=True (Synced): all good",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("describe output missing %q:\n%s", want, out.String())
		}
	}

	// JSON output prints the raw object.
	out.Reset()
	dopts.output = outputJSON
	if err := describeProjection(cmd, dopts, dyn, demoNS, "web"); err != nil {
		t.Fatalf("describeProjection json: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(out.Bytes(), &obj); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if obj["kind"] != "GraphProjection" {
		t.Errorf("unexpected JSON kind: %v", obj["kind"])
	}

	// A missing projection errors clearly.
	if err := describeProjection(cmd, dopts, dyn, demoNS, "absent"); err == nil ||
		!strings.Contains(err.Error(), "fetching GraphProjection") {
		t.Fatalf("expected fetch error, got %v", err)
	}
}

func TestViewsDescribe(t *testing.T) {
	maxDist := int32(3)
	view := View{
		Namespace: "astron", Name: "web-compute",
		DisplayName: "Compute", Description: "Workloads",
		ProjectionRef: ViewProjectionRef{Name: "web", Namespace: "astron"},
		Filters: ViewFilters{
			KindMode:         "show",
			VisibleKinds:     []string{"Deployment", "Pod"},
			HiddenNamespaces: []string{"kube-system"},
			LabelFilters:     []ViewLabelFilter{{Key: "app", Value: "web"}},
			LabelMode:        "any",
			MaxDistance:      &maxDist,
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != apiViewsPath {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]View{view})
	}))
	defer srv.Close()

	out, err := runCmd(t, "--server", srv.URL, "views", "describe", "astron", "web-compute")
	if err != nil {
		t.Fatalf("views describe failed: %v", err)
	}
	for _, want := range []string{
		"Name:         web-compute",
		"Display Name: Compute",
		"Projection:   astron/web",
		"Kind Mode:  show",
		"Visible:    Deployment, Pod",
		"Hidden NS:  kube-system",
		"Labels:     app=web (any)",
		"Max Dist:   3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("describe output missing %q:\n%s", want, out)
		}
	}

	// Unknown views error clearly.
	if _, err := runCmd(t, "--server", srv.URL, "views", "describe", "astron", "absent"); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
