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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func readySession() Session {
	return Session{
		UID:                 "sess-7f3a",
		GroupUID:            "8f2c-4d91",
		SandboxNamespaceUID: "3d91-77aa",
		Mode:                ModeSandbox,
		Tools:               []string{"describe_group", "sandbox_apply", "propose_change"},
		ExpiresAt:           time.Now().Add(time.Hour),
	}
}

type fakeHandler struct {
	called     bool
	gotSession Session
	gotTool    Tool
	result     any
	err        error
}

func (f *fakeHandler) Call(_ context.Context, session Session, tool Tool, _ json.RawMessage) (any, error) {
	f.called = true
	f.gotSession = session
	f.gotTool = tool
	return f.result, f.err
}

func decode(t *testing.T, reply []byte) map[string]interface{} {
	t.Helper()

	var out map[string]interface{}
	if err := json.Unmarshal(reply, &out); err != nil {
		t.Fatalf("the reply is not JSON (%v): %s", err, reply)
	}
	return out
}

func TestCatalogueIsWellFormed(t *testing.T) {
	want := []string{
		"list_group_resources", "get_resource", "get_events", "get_logs", "describe_group",
		"list_chart_versions", "render_member", "validate_change",
		"sandbox_apply", "sandbox_rollback", "run_smoke_test", "destroy_sandbox",
		"propose_change",
	}

	tools := Catalogue()
	if len(tools) != len(want) {
		t.Fatalf("catalogue has %d tools, want %d", len(tools), len(want))
	}

	tiers := map[Tier]bool{TierRead: true, TierPropose: true, TierSandboxWrite: true, TierSubmit: true}

	for i, tool := range tools {
		if tool.Name != want[i] {
			t.Errorf("tool %d = %s, want %s", i, tool.Name, want[i])
		}
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if !tiers[tool.Tier] {
			t.Errorf("%s has the unknown tier %q", tool.Name, tool.Tier)
		}
		if tool.InputSchema["type"] != "object" {
			t.Errorf("%s does not take an object", tool.Name)
		}
		if tool.InputSchema["additionalProperties"] != false {
			t.Errorf("%s accepts arguments it would ignore", tool.Name)
		}
	}
}

func TestLookup(t *testing.T) {
	if _, ok := Lookup("describe_group"); !ok {
		t.Error("describe_group was not found")
	}
	if _, ok := Lookup("kubectl_apply"); ok {
		t.Error("an unknown tool was found")
	}
}

func TestAuthorize(t *testing.T) {
	tests := []struct {
		name    string
		session Session
		tool    string
		refused bool
	}{
		{
			name:    "a sandbox session may call a granted write tool",
			session: readySession(),
			tool:    "sandbox_apply",
		},
		{
			name:    "a sandbox session may submit",
			session: readySession(),
			tool:    "propose_change",
		},
		{
			name: "a readonly session may read",
			session: Session{
				UID: "sess-1", Mode: ModeReadOnly,
				Tools: []string{"describe_group"}, ExpiresAt: time.Now().Add(time.Hour),
			},
			tool: "describe_group",
		},
		{
			name: "a readonly session may propose",
			session: Session{
				UID: "sess-1", Mode: ModeReadOnly,
				Tools: []string{"validate_change"}, ExpiresAt: time.Now().Add(time.Hour),
			},
			tool: "validate_change",
		},
		{
			name: "a readonly session may not write in a sandbox",
			session: Session{
				UID: "sess-1", Mode: ModeReadOnly,
				Tools: []string{"sandbox_apply"}, ExpiresAt: time.Now().Add(time.Hour),
			},
			tool:    "sandbox_apply",
			refused: true,
		},
		{
			name: "a readonly session may not submit",
			session: Session{
				UID: "sess-1", Mode: ModeReadOnly,
				Tools: []string{"propose_change"}, ExpiresAt: time.Now().Add(time.Hour),
			},
			tool:    "propose_change",
			refused: true,
		},
		{
			name:    "a session that was never established",
			session: Session{Mode: ModeSandbox, Tools: []string{"describe_group"}},
			tool:    "describe_group",
			refused: true,
		},
		{
			name: "a revoked session",
			session: Session{
				UID: "sess-1", Mode: ModeSandbox, Revoked: true,
				Tools: []string{"describe_group"}, ExpiresAt: time.Now().Add(time.Hour),
			},
			tool:    "describe_group",
			refused: true,
		},
		{
			name:    "a session without an expiry",
			session: Session{UID: "sess-1", Mode: ModeSandbox, Tools: []string{"describe_group"}},
			tool:    "describe_group",
			refused: true,
		},
		{
			name: "an expired session",
			session: Session{
				UID: "sess-1", Mode: ModeSandbox,
				Tools: []string{"describe_group"}, ExpiresAt: time.Now().Add(-time.Minute),
			},
			tool:    "describe_group",
			refused: true,
		},
		{
			name:    "a tool the session was not granted",
			session: readySession(),
			tool:    "get_logs",
			refused: true,
		},
		{
			name: "a mode this server does not know",
			session: Session{
				UID: "sess-1", Mode: Mode("administrator"),
				Tools: []string{"describe_group"}, ExpiresAt: time.Now().Add(time.Hour),
			},
			tool:    "describe_group",
			refused: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, ok := Lookup(tt.tool)
			if !ok {
				t.Fatalf("no such tool: %s", tt.tool)
			}

			err := tt.session.Authorize(tool)
			if tt.refused && err == nil {
				t.Fatalf("Authorize = nil, want a refusal")
			}
			if !tt.refused && err != nil {
				t.Fatalf("Authorize = %v, want the call allowed", err)
			}
		})
	}
}

func TestHandleInitialize(t *testing.T) {
	s := &Server{Name: "kubecube-mcp", Version: "dev"}

	reply := decode(t, s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)))

	result, ok := reply["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("reply = %v, want a result", reply)
	}
	if result["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %s", result["protocolVersion"], ProtocolVersion)
	}
	if _, ok := result["capabilities"].(map[string]interface{})["tools"]; !ok {
		t.Errorf("capabilities = %v, want tools advertised", result["capabilities"])
	}
}

func TestHandleToolsList(t *testing.T) {
	s := &Server{Name: "kubecube-mcp", Version: "dev"}

	reply := decode(t, s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)))

	result, _ := reply["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	if len(tools) != len(Catalogue()) {
		t.Fatalf("tools = %d, want %d", len(tools), len(Catalogue()))
	}

	first, _ := tools[0].(map[string]interface{})
	if first["name"] != "list_group_resources" {
		t.Errorf("first tool = %v, want list_group_resources", first["name"])
	}
	if _, ok := first["inputSchema"]; !ok {
		t.Error("a tool was advertised without a schema")
	}
}

func TestHandleToolsCall(t *testing.T) {
	handler := &fakeHandler{result: map[string]interface{}{"members": 3}}
	s := &Server{Session: readySession(), Handler: handler}

	reply := decode(t, s.Handle(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"describe_group","arguments":{}}}`)))

	result, _ := reply["result"].(map[string]interface{})
	if result["isError"] != false {
		t.Fatalf("result = %v, want a successful call", result)
	}
	if text := toolText(t, result); !strings.Contains(text, "members") {
		t.Errorf("text = %q, want the handler's result", text)
	}
	if !handler.called || handler.gotTool.Name != "describe_group" {
		t.Errorf("handler called = %v with %v, want describe_group", handler.called, handler.gotTool.Name)
	}
	if handler.gotSession.UID != "sess-7f3a" {
		t.Errorf("handler got session %q, want the request's session", handler.gotSession.UID)
	}
}

// A refused call must not reach the platform: the surface narrows the scope
// before the platform is asked, so a refusal costs nothing and cannot be
// mistaken for a platform failure.
func TestHandleRefusedCallNeverReachesTheHandler(t *testing.T) {
	handler := &fakeHandler{}
	s := &Server{
		Session: Session{
			UID: "sess-1", Mode: ModeReadOnly,
			Tools: []string{"sandbox_apply"}, ExpiresAt: time.Now().Add(time.Hour),
		},
		Handler: handler,
	}

	reply := decode(t, s.Handle(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"sandbox_apply"}}`)))

	result, _ := reply["result"].(map[string]interface{})
	if result["isError"] != true {
		t.Fatalf("result = %v, want the call refused", result)
	}
	if !strings.Contains(toolText(t, result), "readonly") {
		t.Errorf("text = %q, want the reason to name the mode", toolText(t, result))
	}
	if handler.called {
		t.Error("the handler was called for a refused tool")
	}
}

func TestHandleUnknownMethod(t *testing.T) {
	s := &Server{}

	reply := decode(t, s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`)))

	rpcErr, ok := reply["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("reply = %v, want a protocol error", reply)
	}
	if rpcErr["code"] != float64(codeMethodNotFound) {
		t.Errorf("code = %v, want %d", rpcErr["code"], codeMethodNotFound)
	}
}

func TestHandleUnknownTool(t *testing.T) {
	s := &Server{Session: readySession()}

	reply := decode(t, s.Handle(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"kubectl_apply"}}`)))

	if _, ok := reply["error"].(map[string]interface{}); !ok {
		t.Fatalf("reply = %v, want a protocol error", reply)
	}
}

func TestHandleMalformedMessage(t *testing.T) {
	s := &Server{}

	reply := decode(t, s.Handle(context.Background(), []byte(`{`)))

	rpcErr, ok := reply["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("reply = %v, want a parse error", reply)
	}
	if rpcErr["code"] != float64(codeParseError) {
		t.Errorf("code = %v, want %d", rpcErr["code"], codeParseError)
	}
}

func TestHandleNotificationHasNoReply(t *testing.T) {
	s := &Server{}

	if reply := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); reply != nil {
		t.Fatalf("reply = %s, want none for a notification", reply)
	}
}

func TestHandleWithoutAPlatformSaysSo(t *testing.T) {
	s := &Server{Session: readySession()}

	reply := decode(t, s.Handle(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"describe_group"}}`)))

	result, _ := reply["result"].(map[string]interface{})
	if result["isError"] != true || !strings.Contains(toolText(t, result), "platform") {
		t.Fatalf("result = %v, want a readable failure about the missing platform", result)
	}
}

func TestServeHTTP(t *testing.T) {
	s := &Server{Session: readySession(), Name: "kubecube-mcp", Version: "dev"}
	recorder := httptest.NewRecorder()
	s.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":8,"method":"tools/list"}`)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if ct := recorder.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q, want application/json", ct)
	}
	decode(t, recorder.Body.Bytes())

	recorder = httptest.NewRecorder()
	s.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", recorder.Code)
	}
}

func TestServeHTTPResolvesTheSessionPerRequest(t *testing.T) {
	handler := &fakeHandler{result: "ok"}
	s := &Server{
		SessionFrom: func(token string) (Session, error) {
			if token != "good" {
				return Session{}, errors.New("no token")
			}
			return readySession(), nil
		},
		Handler: handler,
	}

	body := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"describe_group"}}`

	unauthorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	s.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Errorf("status without a token = %d, want 401", unauthorized.Code)
	}
	if handler.called {
		t.Error("the handler was called without a session")
	}

	authorized := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer good")
	s.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("status with a token = %d, want 200", authorized.Code)
	}
	if !handler.called {
		t.Error("the handler was not called with a session")
	}
}

func toolText(t *testing.T, result map[string]interface{}) string {
	t.Helper()

	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("result = %v, want content", result)
	}
	first, _ := content[0].(map[string]interface{})
	text, _ := first["text"].(string)
	return text
}
