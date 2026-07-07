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

package mcp

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

// APIClient is a thin HTTP client for the Astron read API. The MCP server uses
// it to serve retrieval tools, so it inherits the API's projection scoping and
// read-only guarantees.
type APIClient struct {
	baseURL string
	http    *http.Client
}

// NewAPIClient builds a client for the Astron read API at baseURL
// (e.g. "http://localhost:8082").
func NewAPIClient(baseURL string, httpClient *http.Client) *APIClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &APIClient{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// get performs a GET and returns the raw response body, erroring on non-2xx.
func (c *APIClient) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// postJSON performs a POST with a JSON body and returns the raw response body.
func (c *APIClient) postJSON(ctx context.Context, path string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c *APIClient) do(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling astron API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("reading astron API response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("astron API %s %s: status %d: %s",
			req.Method, req.URL.Path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// ListProjections returns the projections JSON.
func (c *APIClient) ListProjections(ctx context.Context) ([]byte, error) {
	return c.get(ctx, "/api/projections", nil)
}

// Search runs hybrid retrieval against a projection and returns the result JSON.
func (c *APIClient) Search(ctx context.Context, namespace, name string, body any) ([]byte, error) {
	path := fmt.Sprintf("/api/projections/%s/%s/rag/search", url.PathEscape(namespace), url.PathEscape(name))
	return c.postJSON(ctx, path, body)
}

// Neighborhood runs structural retrieval around a resource and returns the
// result JSON.
func (c *APIClient) Neighborhood(ctx context.Context, namespace, name string, body any) ([]byte, error) {
	path := fmt.Sprintf("/api/projections/%s/%s/rag/neighborhood", url.PathEscape(namespace), url.PathEscape(name))
	return c.postJSON(ctx, path, body)
}

// Query runs text-to-Cypher against a projection and returns the result JSON.
func (c *APIClient) Query(ctx context.Context, namespace, name string, body any) ([]byte, error) {
	path := fmt.Sprintf("/api/projections/%s/%s/rag/query", url.PathEscape(namespace), url.PathEscape(name))
	return c.postJSON(ctx, path, body)
}

// Answer runs retrieval-augmented question answering and returns the JSON.
func (c *APIClient) Answer(ctx context.Context, namespace, name string, body any) ([]byte, error) {
	path := fmt.Sprintf("/api/projections/%s/%s/rag/answer", url.PathEscape(namespace), url.PathEscape(name))
	return c.postJSON(ctx, path, body)
}

// ResourceYAML returns the YAML for a single live resource.
func (c *APIClient) ResourceYAML(ctx context.Context, query url.Values) ([]byte, error) {
	return c.get(ctx, "/api/resource", query)
}
