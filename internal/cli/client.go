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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// apiViewsPath is the read API's Views collection endpoint.
const apiViewsPath = "/api/views"

// Projection mirrors the read API's projection summary
// (see internal/api projectionDTO).
type Projection struct {
	UID               string `json:"uid"`
	Namespace         string `json:"namespace"`
	Name              string `json:"name"`
	Phase             string `json:"phase,omitempty"`
	NodeCount         int64  `json:"nodeCount"`
	RelationshipCount int64  `json:"relationshipCount"`
}

// ViewProjectionRef mirrors the read API's projectionRef on a view
// (see internal/api projectionRefDTO).
type ViewProjectionRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// ViewLabelFilter mirrors a single label filter (see internal/api labelFilterDTO).
type ViewLabelFilter struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// ViewFilters mirrors the read API's view filters (see internal/api viewFiltersDTO).
type ViewFilters struct {
	KindMode         string            `json:"kindMode,omitempty"`
	HiddenKinds      []string          `json:"hiddenKinds,omitempty"`
	VisibleKinds     []string          `json:"visibleKinds,omitempty"`
	HiddenNamespaces []string          `json:"hiddenNamespaces,omitempty"`
	LabelFilters     []ViewLabelFilter `json:"labelFilters,omitempty"`
	LabelMode        string            `json:"labelMode,omitempty"`
	MaxDistance      *int32            `json:"maxDistance,omitempty"`
	GroupByNamespace *bool             `json:"groupByNamespace,omitempty"`
}

// View mirrors the read API's saved GraphView (see internal/api viewDTO).
type View struct {
	Namespace     string            `json:"namespace"`
	Name          string            `json:"name"`
	UID           string            `json:"uid,omitempty"`
	DisplayName   string            `json:"displayName,omitempty"`
	Description   string            `json:"description,omitempty"`
	ProjectionRef ViewProjectionRef `json:"projectionRef"`
	Filters       ViewFilters       `json:"filters"`
}

// Node mirrors the read API's graph node (see internal/api nodeDTO).
type Node struct {
	ID         string         `json:"id"`
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Namespace  string         `json:"namespace,omitempty"`
	Name       string         `json:"name"`
	Properties map[string]any `json:"properties,omitempty"`
}

// Edge mirrors the read API's graph relationship (see internal/api edgeDTO).
type Edge struct {
	ID         string         `json:"id"`
	Source     string         `json:"source"`
	Target     string         `json:"target"`
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
}

// Graph mirrors the read API's full projection graph (see internal/api graphDTO).
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Client is a thin HTTP client for the Gamera read API.
type Client struct {
	baseURL string
	http    *http.Client
}

// newClient builds a Client from the global options.
func newClient(opts *options) (*Client, error) {
	base := strings.TrimRight(opts.server, "/")
	if base == "" {
		return nil, fmt.Errorf("server URL must not be empty")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("invalid server URL %q: %w", opts.server, err)
	}
	timeout := opts.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{baseURL: base, http: &http.Client{Timeout: timeout}}, nil
}

// ListProjections returns all GraphProjections known to the operator.
func (c *Client) ListProjections(ctx context.Context) ([]Projection, error) {
	var out []Projection
	if err := c.getJSON(ctx, "/api/projections", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListViews returns the saved GraphViews known to the operator. When
// projectionNamespace and/or projectionName are non-empty, the server narrows
// the result to views referencing that GraphProjection.
func (c *Client) ListViews(ctx context.Context, projectionNamespace, projectionName string) ([]View, error) {
	path := apiViewsPath
	q := url.Values{}
	if projectionNamespace != "" {
		q.Set("projectionNamespace", projectionNamespace)
	}
	if projectionName != "" {
		q.Set("projectionName", projectionName)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out []View
	if err := c.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateView creates a new GraphView and returns the server's representation of
// the created object.
func (c *Client) CreateView(ctx context.Context, v View) (View, error) {
	var out View
	if err := c.postJSON(ctx, apiViewsPath, v, &out); err != nil {
		return View{}, err
	}
	return out, nil
}

// Graph returns the materialized graph for a single projection.
func (c *Client) Graph(ctx context.Context, namespace, name string) (Graph, error) {
	var out Graph
	path := fmt.Sprintf("/api/projections/%s/%s/graph",
		url.PathEscape(namespace), url.PathEscape(name))
	if err := c.getJSON(ctx, path, &out); err != nil {
		return Graph{}, err
	}
	return out, nil
}

// getJSON issues a GET request and decodes a JSON response body into v.
func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response from %s: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, apiError(resp.StatusCode, body))
	}

	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decoding response from %s: %w", path, err)
	}
	return nil
}

// postJSON issues a POST request with a JSON body and decodes a JSON response
// body into v. It accepts any 2xx status.
func (c *Client) postJSON(ctx context.Context, path string, body, v any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding request for %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response from %s: %w", path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", path, apiError(resp.StatusCode, respBody))
	}

	if v == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, v); err != nil {
		return fmt.Errorf("decoding response from %s: %w", path, err)
	}
	return nil
}

// apiError renders a server error response, preferring the API's structured
// {"error": "..."} body when present.
func apiError(status int, body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return fmt.Sprintf("%d %s: %s", status, http.StatusText(status), payload.Error)
	}
	return fmt.Sprintf("%d %s", status, http.StatusText(status))
}
