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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ListModels returns the chat model identifiers the provider in cfg makes
// available, using the provider credentials server-side. For OpenAI-compatible
// backends (OpenAI, Ollama's /v1 endpoint) it queries GET {base}/models. Azure
// OpenAI cannot enumerate deployments through the data-plane API, so only the
// configured model is returned.
func ListModels(ctx context.Context, cfg ChatConfig) ([]string, error) {
	switch Provider(strings.ToLower(string(cfg.Provider))) {
	case "", ProviderFake:
		return []string{"fake"}, nil
	case ProviderAzureOpenAI:
		if cfg.Model == "" {
			return nil, nil
		}
		return []string{cfg.Model}, nil
	case ProviderOpenAI, ProviderOllama:
		return listOpenAIModels(ctx, cfg)
	default:
		return nil, fmt.Errorf("chat models: unsupported provider %q", cfg.Provider)
	}
}

// openAIModelsResponse is the shape of GET /models on OpenAI-compatible APIs.
type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// listOpenAIModels enumerates models from an OpenAI-compatible endpoint.
func listOpenAIModels(ctx context.Context, cfg ChatConfig) ([]string, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = defaultOpenAIBaseURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("chat models: building request: %w", err)
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat models: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("chat models: reading response: %w", err)
	}

	var parsed openAIModelsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("chat models: decoding response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return nil, fmt.Errorf("chat models: provider returned status %d: %s", resp.StatusCode, msg)
	}

	models := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}
