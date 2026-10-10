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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

func readyAgentSession() *appv1.AgentSession {
	return &appv1.AgentSession{
		ObjectMeta: metav1.ObjectMeta{Name: "sess-7f3a", Namespace: "kubecube-project-demo-space1", UID: types.UID("sess-uid")},
		Spec: appv1.AgentSessionSpec{
			Owner:    appv1.SessionOwner{User: "alice", Client: "claude-desktop"},
			GroupRef: appv1.GroupRef{Name: "shop-web", UID: "8f2c-4d91", Cluster: "member-a", Namespace: "kubecube-project-demo-space1"},
			Mode:     appv1.SessionModeSandbox,
			Tools:    []appv1.ToolName{appv1.ToolDescribeGroup, appv1.ToolSandboxApply},
		},
		Status: appv1.AgentSessionStatus{
			Phase:               appv1.SessionPhaseReady,
			ExpiresAt:           &metav1.Time{Time: time.Now().Add(time.Hour)},
			SandboxNamespace:    "kubecube-agent-sess-7f3a",
			SandboxNamespaceUID: "3d91-77aa",
			TokenJTI:            "jti-1",
		},
	}
}

func TestSessionClaimsOf(t *testing.T) {
	now := time.Now()
	claims, err := SessionClaimsOf(readyAgentSession(), "jti-1", now)
	if err != nil {
		t.Fatalf("SessionClaimsOf: %v", err)
	}

	if claims.SessionUID != "sess-uid" || claims.GroupUID != "8f2c-4d91" {
		t.Errorf("claims = %+v, want the session's identity", claims)
	}
	if claims.SandboxNamespaceUID != "3d91-77aa" || claims.Mode != ModeSandbox {
		t.Errorf("claims = %+v, want the session's sandbox and mode", claims)
	}
	if len(claims.Tools) != 2 || claims.Tools[1] != string(appv1.ToolSandboxApply) {
		t.Errorf("tools = %v, want the session's allowlist", claims.Tools)
	}
	if claims.Audience != Audience || claims.Subject != "alice" {
		t.Errorf("claims = %+v, want this surface's audience and the owner as the subject", claims)
	}
	if claims.ExpiresAt != readyAgentSession().Status.ExpiresAt.Unix() {
		t.Errorf("expiry = %d, want the session's own expiry", claims.ExpiresAt)
	}
}

// The token the platform issues has to establish exactly the session the
// platform recorded: this is the one place the two halves meet.
func TestIssuedTokenEstablishesTheRecordedSession(t *testing.T) {
	session := readyAgentSession()

	claims, err := SessionClaimsOf(session, "jti-1", time.Now())
	if err != nil {
		t.Fatalf("SessionClaimsOf: %v", err)
	}

	token, err := SignSession(testSecret, claims)
	if err != nil {
		t.Fatalf("SignSession: %v", err)
	}

	established, err := (Verifier{Secret: testSecret}).Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if established.UID != string(session.UID) ||
		established.GroupUID != session.Spec.GroupRef.UID ||
		established.SandboxNamespaceUID != session.Status.SandboxNamespaceUID ||
		established.Mode != Mode(session.Spec.Mode) ||
		len(established.Tools) != len(session.Spec.Tools) {
		t.Errorf("established = %+v, want the session the platform recorded", established)
	}

	// And the scope gate agrees: the sandbox tool is reachable, an ungranted one
	// is not.
	if err := established.Authorize(mustTool(t, "sandbox_apply")); err != nil {
		t.Errorf("Authorize(sandbox_apply) = %v, want it granted", err)
	}
	if err := established.Authorize(mustTool(t, "get_logs")); err == nil {
		t.Error("Authorize(get_logs) = nil, want a refusal: the session was not granted it")
	}
}

func TestSessionClaimsOfRefuses(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*appv1.AgentSession)
		jti     string
		wantErr bool
	}{
		{name: "a ready sandbox session", change: func(*appv1.AgentSession) {}},
		{
			name: "a read-only session without a sandbox",
			change: func(s *appv1.AgentSession) {
				s.Spec.Mode = appv1.SessionModeReadOnly
				s.Status.SandboxNamespaceUID = ""
			},
			wantErr: false,
		},
		{
			name:    "a sandbox session with no sandbox recorded",
			change:  func(s *appv1.AgentSession) { s.Status.SandboxNamespaceUID = "" },
			wantErr: true,
		},
		{
			name:    "a session with no expiry",
			change:  func(s *appv1.AgentSession) { s.Status.ExpiresAt = nil },
			wantErr: true,
		},
		{
			name:    "a session with no UID",
			change:  func(s *appv1.AgentSession) { s.UID = "" },
			wantErr: true,
		},
		{
			name:    "a session in a mode this surface does not know",
			change:  func(s *appv1.AgentSession) { s.Spec.Mode = appv1.SessionMode("administrator") },
			wantErr: true,
		},
		{
			name:    "no token id to revoke it by",
			change:  func(*appv1.AgentSession) {},
			jti:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := readyAgentSession()
			tt.change(session)

			jti := tt.jti
			if jti == "" && !tt.wantErr {
				jti = "jti-1"
			}

			_, err := SessionClaimsOf(session, jti, time.Now())
			if tt.wantErr && err == nil {
				t.Fatal("SessionClaimsOf = nil, want a refusal")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("SessionClaimsOf: %v", err)
			}
		})
	}
}

func TestSessionClaimsOfRefusesNoSession(t *testing.T) {
	if _, err := SessionClaimsOf(nil, "jti-1", time.Now()); err == nil {
		t.Fatal("SessionClaimsOf(nil) = nil, want a refusal")
	}
}

func TestNewJTIDiffers(t *testing.T) {
	first, err := NewJTI()
	if err != nil {
		t.Fatalf("NewJTI: %v", err)
	}
	second, err := NewJTI()
	if err != nil {
		t.Fatalf("NewJTI: %v", err)
	}

	if first == "" || first == second {
		t.Errorf("ids = %q and %q, want two different non-empty ids", first, second)
	}
}

func mustTool(t *testing.T, name string) Tool {
	t.Helper()

	tool, ok := Lookup(name)
	if !ok {
		t.Fatalf("no such tool: %s", name)
	}
	return tool
}
