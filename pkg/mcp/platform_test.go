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
)

func platformFor(t *testing.T, handler http.HandlerFunc) Platform {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return Platform{BaseURL: server.URL}
}

func TestDoPresentsTheSessionToken(t *testing.T) {
	var presented string
	var contentType string

	platform := platformFor(t, func(w http.ResponseWriter, r *http.Request) {
		presented = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"ResourceGroup","metadata":{"name":"shop-web"}}`))
	})

	var out map[string]interface{}
	ctx := WithToken(context.Background(), "session-token")

	err := platform.Do(ctx, http.MethodGet, "/api/v1/cube/resource-groups/demo/shop-web", nil, &out)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	if presented != "Bearer session-token" {
		t.Errorf("Authorization = %q, want the session's token", presented)
	}
	if contentType != "" {
		t.Errorf("Content-Type = %q, want none on a call with no body", contentType)
	}
	if out["kind"] != "ResourceGroup" {
		t.Errorf("answer = %v, want the platform's answer decoded", out)
	}
}

func TestDoSendsABody(t *testing.T) {
	var got map[string]interface{}

	platform := platformFor(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("the body could not be read: %v", err)
		}
		_, _ = w.Write([]byte(`{}`))
	})

	ctx := WithToken(context.Background(), "session-token")
	body := map[string]interface{}{"manifests": []string{"a"}}

	if err := platform.Do(ctx, http.MethodPost, "/api/v1/cube/sandbox/apply", body, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if got["manifests"] == nil {
		t.Errorf("body = %v, want the arguments sent", got)
	}
}

// A call that reaches the platform without the session's token would be
// answered as an anonymous caller, and whatever came back would be nobody's
// scope. It is refused here instead of sent.
func TestDoRefusesWithoutAToken(t *testing.T) {
	called := false
	platform := platformFor(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{}`))
	})

	err := platform.Do(context.Background(), http.MethodGet, "/api/v1/cube/resource-groups", nil, nil)
	if err == nil {
		t.Fatal("Do = nil, want a refusal")
	}
	if called {
		t.Error("the platform was called without a token")
	}
}

func TestDoRefusesWithoutAPlatform(t *testing.T) {
	ctx := WithToken(context.Background(), "session-token")

	if err := (Platform{}).Do(ctx, http.MethodGet, "/anything", nil, nil); err == nil {
		t.Fatal("Do = nil, want a refusal")
	}
}

func TestDoSeparatesARefusalFromAFailure(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		refusal  bool
		contains string
	}{
		{
			name: "a token the platform does not accept", status: http.StatusUnauthorized,
			body: `{"code":401,"message":"Unauthorized"}`, refusal: true,
			contains: "did not accept this session's token",
		},
		{
			name: "a call outside the session's scope", status: http.StatusForbidden,
			body: `{"code":403,"message":"the object is not a member of this group"}`, refusal: true,
			contains: "not a member of this group",
		},
		{
			name: "an object the platform does not have", status: http.StatusNotFound,
			body: `{"code":404,"message":"deployment not found."}`, refusal: true,
			contains: "deployment not found.",
		},
		{
			name: "a conflict", status: http.StatusConflict,
			body: `{"code":409,"message":"the group already holds this object"}`, refusal: true,
			contains: "already holds",
		},
		{
			name: "a refusal with no message", status: http.StatusForbidden,
			body: `not json at all`, refusal: true,
			contains: "declined the call",
		},
		{
			name: "the platform breaking", status: http.StatusInternalServerError,
			body: `{"code":500,"message":"internal error"}`, refusal: false,
			contains: "internal error",
		},
		{
			name: "the platform rate limiting", status: http.StatusTooManyRequests,
			body: `{}`, refusal: false,
			contains: "rate limiting",
		},
		{
			name: "a malformed call", status: http.StatusBadRequest,
			body: `{"code":400,"message":"Param name is missing."}`, refusal: false,
			contains: "Param name is missing.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform := platformFor(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})

			ctx := WithToken(context.Background(), "session-token")
			err := platform.Do(ctx, http.MethodGet, "/anything", nil, nil)
			if err == nil {
				t.Fatal("Do = nil, want an error")
			}

			var refusal *PlatformRefusal
			var failure *PlatformFailure

			if tt.refusal {
				if !errors.As(err, &refusal) {
					t.Fatalf("Do = %v, want a refusal the model can read as an answer", err)
				}
				if errors.As(err, &failure) {
					t.Error("a refusal was also reported as a failure")
				}
			} else {
				if !errors.As(err, &failure) {
					t.Fatalf("Do = %v, want a failure rather than a refusal", err)
				}
				if errors.As(err, &refusal) {
					t.Error("a failure was also reported as a refusal")
				}
			}

			if !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("error = %q, want it to carry %q", err.Error(), tt.contains)
			}
		})
	}
}

func TestDoReportsAnUnreachablePlatform(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()

	platform := Platform{BaseURL: server.URL}
	ctx := WithToken(context.Background(), "session-token")

	err := platform.Do(ctx, http.MethodGet, "/anything", nil, nil)

	var failure *PlatformFailure
	if !errors.As(err, &failure) {
		t.Fatalf("Do = %v, want a failure", err)
	}
	if failure.Status != 0 {
		t.Errorf("status = %d, want none for a call that never arrived", failure.Status)
	}
	if !strings.Contains(err.Error(), "could not be reached") {
		t.Errorf("error = %q, want it to say the platform was unreachable", err.Error())
	}
}

func TestDoReportsAnAnswerItCannotRead(t *testing.T) {
	platform := platformFor(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`this is not json`))
	})

	ctx := WithToken(context.Background(), "session-token")
	var out map[string]interface{}

	err := platform.Do(ctx, http.MethodGet, "/anything", nil, &out)

	var failure *PlatformFailure
	if !errors.As(err, &failure) {
		t.Fatalf("Do = %v, want a failure", err)
	}
}

// The routes are the part of the platform's API this surface does not define
// yet. A tool without one must say so rather than invent an endpoint that looks
// like a working one.
func TestCallRefusesAToolWithNoRoute(t *testing.T) {
	platform := Platform{BaseURL: "http://platform.invalid"}
	ctx := WithToken(context.Background(), "session-token")

	_, err := platform.Call(ctx, readySession(), mustTool(t, "describe_group"), nil)
	if err == nil {
		t.Fatal("Call = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "not defined yet") {
		t.Errorf("error = %q, want it to say the route is not defined", err.Error())
	}
}

func TestCallFollowsARoute(t *testing.T) {
	var path string

	platform := platformFor(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"group":"shop-web"}`))
	})
	platform.Routes = map[string]Route{
		"describe_group": {
			Method: http.MethodGet,
			Path: func(session Session, _ json.RawMessage) (string, error) {
				return "/api/v1/cube/resource-groups/by-uid/" + session.GroupUID, nil
			},
		},
	}

	ctx := WithToken(context.Background(), "session-token")
	result, err := platform.Call(ctx, readySession(), mustTool(t, "describe_group"), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if path != "/api/v1/cube/resource-groups/by-uid/"+readySession().GroupUID {
		t.Errorf("path = %q, want the route's path", path)
	}
	answer, ok := result.(map[string]interface{})
	if !ok || answer["group"] != "shop-web" {
		t.Fatalf("result = %#v, want the platform's answer", result)
	}
}

func TestCallReportsAPathItCannotBuild(t *testing.T) {
	platform := Platform{BaseURL: "http://platform.invalid"}
	platform.Routes = map[string]Route{
		"describe_group": {
			Method: http.MethodGet,
			Path: func(Session, json.RawMessage) (string, error) {
				return "", errors.New("no group was named")
			},
		},
	}

	ctx := WithToken(context.Background(), "session-token")
	_, err := platform.Call(ctx, readySession(), mustTool(t, "describe_group"), nil)
	if err == nil {
		t.Fatal("Call = nil, want an error")
	}
	if !strings.Contains(err.Error(), "no group was named") {
		t.Errorf("error = %q, want it to carry the reason", err.Error())
	}
}
