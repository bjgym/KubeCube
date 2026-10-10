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

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

// establishedFrom signs a token for a recorded session and verifies it, which is
// how a request reaches Resolve.
func establishedFrom(t *testing.T, session *appv1.AgentSession) Session {
	t.Helper()

	claims, err := SessionClaimsOf(session, string(session.Status.TokenJTI), time.Now())
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
	return established
}

func TestResolve(t *testing.T) {
	session := readyAgentSession()
	established := establishedFrom(t, session)

	resolved, err := Resolve(session, established)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if resolved.UID != established.UID || resolved.TokenJTI != established.TokenJTI {
		t.Errorf("resolved = %+v, want the session's identity", resolved)
	}
	if resolved.Mode != session.Spec.Mode || resolved.GroupUID != session.Spec.GroupRef.UID {
		t.Errorf("resolved = %+v, want the session's scope", resolved)
	}
	if resolved.ExpiresAt.IsZero() {
		t.Error("resolved has no expiry, want the session's")
	}
}

func TestResolveRefuses(t *testing.T) {
	tests := []struct {
		name   string
		change func(*appv1.AgentSession)
	}{
		{
			name:   "a session that is no longer ready",
			change: func(s *appv1.AgentSession) { s.Status.Phase = appv1.SessionPhaseExpired },
		},
		{
			name:   "a token that is no longer the current one",
			change: func(s *appv1.AgentSession) { s.Status.TokenJTI = "jti-2" },
		},
		{
			name:   "a session whose token id was cleared",
			change: func(s *appv1.AgentSession) { s.Status.TokenJTI = "" },
		},
		{
			name:   "a session that lost its expiry",
			change: func(s *appv1.AgentSession) { s.Status.ExpiresAt = nil },
		},
		{
			name:   "a session whose mode changed",
			change: func(s *appv1.AgentSession) { s.Spec.Mode = appv1.SessionModeReadOnly },
		},
		{
			name:   "a session moved to another group",
			change: func(s *appv1.AgentSession) { s.Spec.GroupRef.UID = "another-group-uid" },
		},
		{
			name:   "a session whose sandbox was replaced",
			change: func(s *appv1.AgentSession) { s.Status.SandboxNamespaceUID = "another-namespace-uid" },
		},
		{
			name:   "a session whose uid changed",
			change: func(s *appv1.AgentSession) { s.UID = "another-session-uid" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorded := readyAgentSession()
			established := establishedFrom(t, recorded)

			// The change happens after the token was issued, which is the case
			// this rule exists for.
			tt.change(recorded)

			if resolved, err := Resolve(recorded, established); err == nil {
				t.Fatalf("Resolve = %+v, want a refusal", resolved)
			}
		})
	}
}

func TestResolveRefusesNoSession(t *testing.T) {
	if _, err := Resolve(nil, Session{UID: "sess-uid"}); err == nil {
		t.Fatal("Resolve(nil) = nil, want a refusal")
	}
}

// The session is the authority for what a session may call: a tool taken away
// stops being reachable on the next request, not when the token expires.
func TestResolveTakesTheToolListFromTheSession(t *testing.T) {
	session := readyAgentSession()
	established := establishedFrom(t, session)

	session.Spec.Tools = []appv1.ToolName{appv1.ToolDescribeGroup}

	resolved, err := Resolve(session, established)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := resolved.Authorize(mustTool(t, "sandbox_apply")); err == nil {
		t.Error("Authorize(sandbox_apply) = nil, want a refusal: the tool was taken away")
	}
	if err := resolved.Authorize(mustTool(t, "describe_group")); err != nil {
		t.Errorf("Authorize(describe_group) = %v, want it still granted", err)
	}
}
