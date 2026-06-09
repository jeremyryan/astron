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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// defaultOpenAIBaseURL is the OpenAI embeddings API base. It is overridable so
// the same client works with Azure OpenAI and OpenAI-compatible servers such as
// Ollama (http://host:11434/v1).
const defaultOpenAIBaseURL = "https://api.openai.com/v1"

// OpenAIConfig configures an OpenAIEmbedder.
type OpenAIConfig struct {
	// APIKey authenticates to the embeddings endpoint. Sent as a Bearer token.
	APIKey string
	// Model is the embedding model name, e.g. "text-embedding-3-small".
	Model string
	// BaseURL overrides the API base (for Azure OpenAI or Ollama). When empty,
	// the public OpenAI endpoint is used.
	BaseURL string
	// Dimensions, when non-zero, requests vectors of this length from models
	// that support dimension reduction (e.g. text-embedding-3-*). It is also
	// reported by Dimensions().
	Dimensions int
	// HTTPClient is the client used for requests. When nil, a client with a
	// sensible timeout is used.
	HTTPClient *http.Client
}

// OpenAIEmbedder is an Embedder backed by an OpenAI-compatible embeddings API.
type OpenAIEmbedder struct {
	cfg    OpenAIConfig
	client *http.Client
}

// compile-time assertion that OpenAIEmbedder satisfies Embedder.
var _ Embedder = (*OpenAIEmbedder)(nil)

// NewOpenAIEmbedder constructs an OpenAIEmbedder. It validates required fields
// but performs no network I/O.
func NewOpenAIEmbedder(cfg OpenAIConfig) (*OpenAIEmbedder, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai embedder: APIKey is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("openai embedder: Model is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultOpenAIBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OpenAIEmbedder{cfg: cfg, client: client}, nil
}

// Dimensions reports the configured vector length, or 0 when left to the model
// default (discoverable from the first Embed result).
func (e *OpenAIEmbedder) Dimensions() int { return e.cfg.Dimensions }

// Model returns the configured model name.
func (e *OpenAIEmbedder) Model() string { return e.cfg.Model }

// embedRequest is the JSON body of an embeddings request.
type embedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// embedResponse is the relevant subset of an embeddings response.
type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Embed calls the embeddings endpoint and returns one vector per input text, in
// input order (the response is re-sorted by index defensively).
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([]Embedding, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(embedRequest{
		Model:      e.cfg.Model,
		Input:      texts,
		Dimensions: e.cfg.Dimensions,
	})
	if err != nil {
		return nil, fmt.Errorf("openai embedder: marshaling request: %w", err)
	}

	url := e.cfg.BaseURL + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai embedder: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embedder: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("openai embedder: reading response: %w", err)
	}

	var parsed embedResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("openai embedder: decoding response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return nil, fmt.Errorf("openai embedder: api error (status %d): %s", resp.StatusCode, parsed.Error.Message)
		}
		return nil, fmt.Errorf("openai embedder: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("openai embedder: expected %d embeddings, got %d", len(texts), len(parsed.Data))
	}

	// Re-order by the response index so output aligns with input order.
	sort.Slice(parsed.Data, func(i, j int) bool { return parsed.Data[i].Index < parsed.Data[j].Index })
	out := make([]Embedding, len(parsed.Data))
	for i, d := range parsed.Data {
		out[i] = Embedding(d.Embedding)
	}
	return out, nil
}
