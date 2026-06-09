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

// Package mcp implements a minimal Model Context Protocol (MCP) server that
// exposes Gamera's graph retrieval as agent tools. It speaks JSON-RPC 2.0 over
// a newline-delimited stdio transport and is a thin client of the Gamera read
// API, so it inherits that API's projection scoping and read-only behavior.
//
// The implementation depends only on the standard library.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// protocolVersion is the MCP protocol revision this server implements.
const protocolVersion = "2024-11-05"

// serverName and serverVersion identify this server to MCP clients.
const (
	serverName    = "gamera-mcp"
	serverVersion = "0.1.0"
)

// JSON-RPC 2.0 message types.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes used by this server.
const (
	codeParseError    = -32700
	codeMethodNotFnd  = -32601
	codeInvalidParams = -32602
)

// toolHandler executes a tool and returns its textual result.
type toolHandler func(ctx context.Context, args json.RawMessage) (string, error)

// tool is a registered MCP tool: its advertised schema plus its handler.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	handler     toolHandler    `json:"-"`
}

// Server is an MCP server exposing Gamera retrieval tools over stdio.
type Server struct {
	api   *APIClient
	tools map[string]tool
	order []string // tool names in registration order, for stable tools/list
}

// NewServer builds an MCP server backed by the given Gamera API client.
func NewServer(api *APIClient) *Server {
	s := &Server{api: api, tools: map[string]tool{}}
	s.registerTools()
	return s
}

// register adds a tool to the server.
func (s *Server) register(t tool) {
	s.tools[t.Name] = t
	s.order = append(s.order, t.Name)
}

// Serve runs the JSON-RPC loop, reading newline-delimited requests from in and
// writing responses to out, until in is exhausted or ctx is cancelled.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Allow large messages (graphs can be sizable): up to 16 MiB per line.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	enc := json.NewEncoder(out)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(errorResponse(nil, codeParseError, "parse error"))
			continue
		}

		resp, respond := s.handle(ctx, &req)
		if !respond {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("writing response: %w", err)
		}
	}
	return scanner.Err()
}

// handle dispatches a single request. The second return value is false for
// notifications (no response is written).
func (s *Server) handle(ctx context.Context, req *rpcRequest) (*rpcResponse, bool) {
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		return resultResponse(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
		}), !isNotification

	case "notifications/initialized", "notifications/cancelled":
		// Notifications: never answered.
		return nil, false

	case "ping":
		return resultResponse(req.ID, map[string]any{}), !isNotification

	case "tools/list":
		return resultResponse(req.ID, map[string]any{"tools": s.toolList()}), !isNotification

	case "tools/call":
		if isNotification {
			return nil, false
		}
		return s.handleToolCall(ctx, req), true

	default:
		if isNotification {
			return nil, false
		}
		return errorResponse(req.ID, codeMethodNotFnd, "method not found: "+req.Method), true
	}
}

// toolList returns the registered tools in registration order.
func (s *Server) toolList() []tool {
	out := make([]tool, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.tools[name])
	}
	return out
}

// toolCallParams is the params object of a tools/call request.
type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// handleToolCall executes a tool and wraps its output as MCP tool content.
// Tool execution failures are reported as a tool result with isError=true (per
// the MCP convention), not as a JSON-RPC protocol error.
func (s *Server) handleToolCall(ctx context.Context, req *rpcRequest) *rpcResponse {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, codeInvalidParams, "invalid tools/call params: "+err.Error())
	}
	t, ok := s.tools[params.Name]
	if !ok {
		return toolErrorResponse(req.ID, "unknown tool: "+params.Name)
	}

	text, err := t.handler(ctx, params.Arguments)
	if err != nil {
		return toolErrorResponse(req.ID, err.Error())
	}
	return resultResponse(req.ID, toolTextResult(text, false))
}

// toolTextResult builds an MCP tools/call result with a single text content.
func toolTextResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func resultResponse(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: normalizeID(id), Result: result}
}

func errorResponse(id json.RawMessage, code int, message string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: normalizeID(id), Error: &rpcError{Code: code, Message: message}}
}

// toolErrorResponse reports a tool execution failure as a (successful) RPC
// response carrying an error-flagged tool result.
func toolErrorResponse(id json.RawMessage, message string) *rpcResponse {
	return resultResponse(id, toolTextResult(message, true))
}

// normalizeID ensures the response carries a JSON null id rather than an absent
// one when the request had no id.
func normalizeID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}
