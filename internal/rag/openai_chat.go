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
	"strings"
	"time"
)

// OpenAIChatConfig configures an OpenAIChat.
type OpenAIChatConfig struct {
	APIKey      string
	Model       string
	BaseURL     string
	Temperature float64
	HTTPClient  *http.Client
}

// OpenAIChat is a Chat backed by an OpenAI-compatible chat-completions API
// (OpenAI, Azure OpenAI, or Ollama's /v1 endpoint).
type OpenAIChat struct {
	cfg    OpenAIChatConfig
	client *http.Client
}

// compile-time assertion that OpenAIChat satisfies Chat.
var _ Chat = (*OpenAIChat)(nil)

// NewOpenAIChat constructs an OpenAIChat, validating required fields without
// performing network I/O.
func NewOpenAIChat(cfg OpenAIChatConfig) (*OpenAIChat, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai chat: APIKey is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("openai chat: Model is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultOpenAIBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &OpenAIChat{cfg: cfg, client: client}, nil
}

// Model returns the configured model name.
func (c *OpenAIChat) Model() string { return c.cfg.Model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Complete calls the chat-completions endpoint and returns the assistant reply.
func (c *OpenAIChat) Complete(ctx context.Context, messages []Message) (string, error) {
	msgs := make([]chatMessage, len(messages))
	for i, m := range messages {
		msgs[i] = chatMessage{Role: string(m.Role), Content: m.Content}
	}
	body, err := json.Marshal(chatRequest{Model: c.cfg.Model, Messages: msgs, Temperature: c.cfg.Temperature})
	if err != nil {
		return "", fmt.Errorf("openai chat: marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("openai chat: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai chat: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", fmt.Errorf("openai chat: reading response: %w", err)
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("openai chat: decoding response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return "", fmt.Errorf("openai chat: api error (status %d): %s", resp.StatusCode, parsed.Error.Message)
		}
		return "", fmt.Errorf("openai chat: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openai chat: response contained no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}
