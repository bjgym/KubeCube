/*
Copyright 2021 KubeCube Authors

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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ProtocolVersion is the revision of the Model Context Protocol this server
// speaks.
const ProtocolVersion = "2025-06-18"

// maxMessageBytes bounds one request, so a client cannot make the server hold
// an unbounded body.
const maxMessageBytes = 1 << 20

// Handler serves one tool call. The platform API implements it, and the server
// holds no cluster credential of its own.
type Handler interface {
	Call(ctx context.Context, session Session, tool Tool, arguments json.RawMessage) (any, error)
}

// Server dispatches JSON-RPC messages from an MCP client.
type Server struct {
	// Session is the scope every call is checked against when SessionFrom is
	// not set.
	Session Session

	// SessionFrom resolves the session of one request from the bearer token it
	// presented, which is where a token is verified. A deployment sets it; a
	// test that wants one fixed session leaves it nil, and a deployment must
	// not, because Session alone is the same scope for every caller.
	SessionFrom func(token string) (Session, error)

	// Handler serves the tools.
	Handler Handler

	// Name and Version are what the server reports when a client initializes.
	Name    string
	Version string
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC error codes, from the specification.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Handle answers one message and returns the reply, or nil for a notification,
// which by definition has none.
//
// A refused call is a result carrying isError rather than a protocol error: the
// call was understood and answered, and the model is meant to read the reason
// and try something else. A malformed request is a protocol error, because
// there is nothing to answer.
func (s *Server) Handle(ctx context.Context, message []byte) []byte {
	return s.handle(ctx, s.Session, message)
}

func (s *Server) handle(ctx context.Context, session Session, message []byte) []byte {
	var req request
	if err := json.Unmarshal(message, &req); err != nil {
		return marshal(response{JSONRPC: "2.0", Error: &rpcError{Code: codeParseError, Message: "the message is not JSON"}})
	}
	if req.Method == "" {
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: codeInvalidRequest, Message: "the message has no method"}})
	}

	// A notification carries no id and gets no reply.
	if len(req.ID) == 0 {
		return nil
	}

	switch req.Method {
	case "initialize":
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": s.Name, "version": s.Version},
		}})

	case "ping":
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}})

	case "tools/list":
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{"tools": s.advertised()}})

	case "tools/call":
		return s.call(ctx, session, req)

	default:
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code:    codeMethodNotFound,
			Message: fmt.Sprintf("this server does not serve %s", req.Method),
		}})
	}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (s *Server) call(ctx context.Context, session Session, req request) []byte {
	var params callParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code:    codeInvalidParams,
			Message: "tools/call needs a tool name",
		}})
	}

	tool, ok := Lookup(params.Name)
	if !ok {
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code:    codeInvalidParams,
			Message: fmt.Sprintf("this server does not serve the tool %s", params.Name),
		}})
	}

	if err := session.Authorize(tool); err != nil {
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Result: toolResult(err.Error(), true)})
	}

	if s.Handler == nil {
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Result: toolResult("this server has no platform to call yet", true)})
	}

	result, err := s.Handler.Call(ctx, session, tool, params.Arguments)
	if err != nil {
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Result: toolResult(err.Error(), true)})
	}

	text, err := json.Marshal(result)
	if err != nil {
		return marshal(response{JSONRPC: "2.0", ID: req.ID, Result: toolResult("the result could not be encoded", true)})
	}

	return marshal(response{JSONRPC: "2.0", ID: req.ID, Result: toolResult(string(text), false)})
}

func (s *Server) advertised() []map[string]interface{} {
	tools := Catalogue()

	out := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
		})
	}

	return out
}

func toolResult(text string, isError bool) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func marshal(v interface{}) []byte {
	out, err := json.Marshal(v)
	if err != nil {
		// The values marshalled here are built by this package, so a failure
		// means a programming error rather than bad input.
		return []byte(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"the reply could not be encoded"}}`)
	}
	return out
}

// ServeHTTP serves the streamable HTTP transport: one JSON-RPC message per
// POST, answered with JSON.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "this endpoint takes POST", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxMessageBytes))
	if err != nil {
		http.Error(w, "the request body could not be read", http.StatusBadRequest)
		return
	}

	session := s.Session
	ctx := r.Context()
	if s.SessionFrom != nil {
		token, err := BearerToken(r)
		if err != nil {
			http.Error(w, "the request carried no bearer token", http.StatusUnauthorized)
			return
		}

		resolved, err := s.SessionFrom(token)
		if err != nil {
			http.Error(w, "the session could not be established", http.StatusUnauthorized)
			return
		}
		session = resolved

		// The token travels with the request, so a tool can present it to the
		// platform: this server holds no credential of its own, and the platform
		// authorizes the session rather than trusting the surface.
		ctx = WithToken(ctx, token)
	}

	reply := s.handle(ctx, session, body)
	if reply == nil {
		// A notification is answered with nothing at all.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(reply)
}
