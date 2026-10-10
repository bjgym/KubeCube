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

package resourcegroup

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/labels"

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
	"github.com/kubecube-io/kubecube/pkg/ownership"
)

func TestSandboxNamespaceName(t *testing.T) {
	if got := SandboxNamespaceName("sess-7f3a"); got != "kubecube-agent-sess-7f3a" {
		t.Errorf("SandboxNamespaceName = %q, want the session's name behind the prefix", got)
	}
}

// The labels are what makes a sandbox work without a special case anywhere: the
// owner label brings it under the project's quota, and the level keeps it out
// of the readers that enumerate a project's spaces.
func TestSandboxNamespaceLabels(t *testing.T) {
	got := SandboxNamespaceLabels("t1", "p1", "sess-7f3a")

	if got[SandboxLabel] != "sess-7f3a" {
		t.Errorf("sandbox label = %q, want the session's UID", got[SandboxLabel])
	}

	set := labels.Set(got)
	if !ownership.ManagedSelector().Matches(set) {
		t.Error("the sandbox does not match the selector the ResourceQuota webhook uses, so its quota would not apply")
	}
	if ownership.SpaceSelector("p1").Matches(set) {
		t.Error("the sandbox matches the project's space selector, so it would be listed as a space")
	}
	if !ownership.Selector(ownership.KindProject, "p1").Matches(set) {
		t.Error("the sandbox is not owned by its project")
	}
}

func TestEvaluateSessions(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	hour := time.Hour

	tests := []struct {
		name        string
		state       SessionState
		idleTimeout time.Duration
		want        SessionAction
	}{
		{
			name:  "a session inside its expiry that was just used",
			state: SessionState{Phase: appv1.SessionPhaseReady, ExpiresAt: now.Add(hour), LastUsedAt: now.Add(-time.Minute)},
			want:  SessionKeep,
		},
		{
			name:  "a session past its expiry",
			state: SessionState{Phase: appv1.SessionPhaseReady, ExpiresAt: now.Add(-time.Minute), LastUsedAt: now},
			want:  SessionExpire,
		},
		{
			name:  "a session with no expiry at all",
			state: SessionState{Phase: appv1.SessionPhaseReady, LastUsedAt: now},
			want:  SessionExpire,
		},
		{
			name:        "a session idle past its timeout",
			state:       SessionState{Phase: appv1.SessionPhaseReady, ExpiresAt: now.Add(hour), LastUsedAt: now.Add(-2 * hour)},
			idleTimeout: hour,
			want:        SessionExpire,
		},
		{
			name:        "a session idle inside its timeout",
			state:       SessionState{Phase: appv1.SessionPhaseReady, ExpiresAt: now.Add(hour), LastUsedAt: now.Add(-time.Minute)},
			idleTimeout: hour,
			want:        SessionKeep,
		},
		{
			name:  "a session that was never used is not idle",
			state: SessionState{Phase: appv1.SessionPhaseReady, ExpiresAt: now.Add(hour)},
			want:  SessionKeep,
		},
		{
			name:        "idle reclamation switched off",
			state:       SessionState{Phase: appv1.SessionPhaseReady, ExpiresAt: now.Add(hour), LastUsedAt: now.Add(-100 * hour)},
			idleTimeout: 0,
			want:        SessionKeep,
		},
		{
			name:  "a revoked session",
			state: SessionState{Phase: appv1.SessionPhaseRevoked, ExpiresAt: now.Add(hour)},
			want:  SessionDestroySandbox,
		},
		{
			name:  "a session whose sandbox outlived it",
			state: SessionState{Phase: appv1.SessionPhaseExpired, ExpiresAt: now.Add(-hour)},
			want:  SessionDestroySandbox,
		},
		{
			name:  "a session still being established",
			state: SessionState{Phase: appv1.SessionPhasePending, ExpiresAt: now.Add(hour)},
			want:  SessionKeep,
		},
		{
			name:  "a phase this rule does not know",
			state: SessionState{Phase: appv1.SessionPhase("paused"), ExpiresAt: now.Add(hour)},
			want:  SessionExpire,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.state
			state.Name = "sess-7f3a"

			plans := EvaluateSessions([]SessionState{state}, tt.idleTimeout, now)
			if len(plans) != 1 {
				t.Fatalf("plans = %v, want one per session", plans)
			}
			if plans[0].Action != tt.want {
				t.Errorf("action = %q (%s), want %q", plans[0].Action, plans[0].Reason, tt.want)
			}
			if plans[0].Reason == "" {
				t.Error("the plan carries no reason, and a session ending without one is a support ticket")
			}
		})
	}
}

func TestEvaluateSessionsKeepsTheOrder(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	plans := EvaluateSessions([]SessionState{
		{Name: "first", Phase: appv1.SessionPhaseReady, ExpiresAt: now.Add(time.Hour)},
		{Name: "second", Phase: appv1.SessionPhaseExpired},
	}, 0, now)

	if len(plans) != 2 || plans[0].Name != "first" || plans[1].Name != "second" {
		t.Fatalf("plans = %v, want one per session in the order given", plans)
	}
}
