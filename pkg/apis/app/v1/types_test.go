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

package v1

import (
	"encoding/json"
	"testing"
)

// The shapes below are the ones the design note writes down. Reading them into
// the types pins two things at once: the JSON spelling of every field, which the
// CRD, the token and the platform all depend on, and the fact that what the
// design describes is what these types accept.

func TestResourceGroupReadsTheDesignedShape(t *testing.T) {
	document := `{
	  "spec": {
	    "displayName": "Shop web",
	    "description": "the storefront",
	    "cluster": "member-a",
	    "scope": {"allowedKinds": ["Deployment", "Service"]},
	    "agent": {"defaultMode": "readonly", "maxSessionTTL": "4h", "sandboxQuotaCap": {"cpu": "1"}}
	  },
	  "status": {
	    "phase": "Ready",
	    "groupUID": "8f2c-4d91",
	    "environment": "prod",
	    "members": {"declared": 1, "derived": 2, "unlabelledWarnings": 0},
	    "observedGeneration": 3
	  }
	}`

	group := ResourceGroup{}
	if err := json.Unmarshal([]byte(document), &group); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if group.Spec.DisplayName != "Shop web" || group.Spec.Cluster != "member-a" {
		t.Errorf("spec = %+v, want the document's fields", group.Spec)
	}
	if len(group.Spec.Scope.AllowedKinds) != 2 || group.Spec.Scope.AllowedKinds[1] != "Service" {
		t.Errorf("scope = %+v, want the allowed kinds", group.Spec.Scope)
	}
	if group.Spec.Agent.DefaultMode != SessionModeReadOnly {
		t.Errorf("defaultMode = %q, want readonly", group.Spec.Agent.DefaultMode)
	}
	if group.Spec.Agent.MaxSessionTTL == nil || group.Spec.Agent.MaxSessionTTL.Duration.String() != "4h0m0s" {
		t.Errorf("maxSessionTTL = %v, want four hours", group.Spec.Agent.MaxSessionTTL)
	}
	if group.Status.GroupUID != "8f2c-4d91" || group.Status.Environment != EnvironmentProd {
		t.Errorf("status = %+v, want the scope key and the environment", group.Status)
	}
	if group.Status.Members.Derived != 2 || group.Status.ObservedGeneration != 3 {
		t.Errorf("status = %+v, want the member counts", group.Status)
	}
}

func TestAgentSessionReadsTheDesignedShape(t *testing.T) {
	document := `{
	  "spec": {
	    "owner": {"user": "alice", "client": "claude-desktop"},
	    "groupRef": {"name": "shop-web", "uid": "8f2c-4d91", "cluster": "member-a", "namespace": "kubecube-project-demo-space1"},
	    "mode": "sandbox",
	    "tools": ["describe_group", "sandbox_apply"],
	    "sandbox": {"ttl": "2h", "quotaCap": {"cpu": "1"}}
	  },
	  "status": {
	    "phase": "Ready",
	    "expiresAt": "2026-10-10T14:00:00Z",
	    "sandboxNamespace": "kubecube-agent-sess-7f3a",
	    "sandboxNamespaceUID": "3d91-77aa",
	    "tokenJTI": "jti-1",
	    "lastUsedAt": "2026-10-10T12:00:00Z",
	    "actions": 37,
	    "usedQuota": {"cpu": "500m"}
	  }
	}`

	session := AgentSession{}
	if err := json.Unmarshal([]byte(document), &session); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if session.Spec.Owner.User != "alice" || session.Spec.GroupRef.UID != "8f2c-4d91" {
		t.Errorf("spec = %+v, want the owner and the group", session.Spec)
	}
	if session.Spec.Mode != SessionModeSandbox || len(session.Spec.Tools) != 2 {
		t.Errorf("spec = %+v, want the mode and the allowlist", session.Spec)
	}
	if session.Status.SandboxNamespace != "kubecube-agent-sess-7f3a" ||
		session.Status.SandboxNamespaceUID != "3d91-77aa" {
		t.Errorf("status = %+v, want the sandbox and its UID", session.Status)
	}
	if session.Status.TokenJTI != "jti-1" || session.Status.Actions != 37 {
		t.Errorf("status = %+v, want the token id and the action count", session.Status)
	}
	if session.Status.ExpiresAt == nil || session.Status.ExpiresAt.IsZero() {
		t.Error("expiresAt is empty, want the session's expiry")
	}
	if _, ok := session.Status.UsedQuota["cpu"]; !ok {
		t.Errorf("usedQuota = %v, want the recorded usage", session.Status.UsedQuota)
	}
}

func TestChangeRequestReadsTheDesignedShape(t *testing.T) {
	document := `{
	  "spec": {
	    "groupRef": {"name": "shop-web", "uid": "8f2c-4d91", "cluster": "member-a", "namespace": "kubecube-project-demo-space1"},
	    "sessionRef": "sess-7f3a",
	    "changeSet": {"configMapRef": "change-0001", "digest": "sha256:abc"},
	    "evidence": {
	      "dryRun": "passed",
	      "policy": "passed",
	      "quota": {"delta": {"cpu": "500m"}, "remaining": {"cpu": "2"}},
	      "smokeTest": "passed"
	    }
	  },
	  "status": {"phase": "Pending", "message": "waiting for a second approver"}
	}`

	request := ChangeRequest{}
	if err := json.Unmarshal([]byte(document), &request); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if request.Spec.SessionRef != "sess-7f3a" || request.Spec.ChangeSet.Digest != "sha256:abc" {
		t.Errorf("spec = %+v, want the session and the digest", request.Spec)
	}
	if request.Spec.Evidence.DryRun != EvidencePassed || request.Spec.Evidence.SmokeTest != EvidencePassed {
		t.Errorf("evidence = %+v, want the checks recorded as passed", request.Spec.Evidence)
	}
	if _, ok := request.Spec.Evidence.Quota.Delta["cpu"]; !ok {
		t.Errorf("quota = %+v, want the estimated delta", request.Spec.Evidence.Quota)
	}
	if request.Status.Phase != ChangeRequestPhasePending {
		t.Errorf("phase = %q, want Pending", request.Status.Phase)
	}
}

// The values are the wire format, the enum in the CRD and what the rules
// compare against, so a rename is a break rather than a tidy-up.
func TestEnumValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"group pending", string(GroupPhasePending), "Pending"},
		{"group ready", string(GroupPhaseReady), "Ready"},
		{"group degraded", string(GroupPhaseDegraded), "Degraded"},
		{"session pending", string(SessionPhasePending), "Pending"},
		{"session ready", string(SessionPhaseReady), "Ready"},
		{"session expired", string(SessionPhaseExpired), "Expired"},
		{"session revoked", string(SessionPhaseRevoked), "Revoked"},
		{"mode readonly", string(SessionModeReadOnly), "readonly"},
		{"mode sandbox", string(SessionModeSandbox), "sandbox"},
		{"environment prod", string(EnvironmentProd), "prod"},
		{"environment staging", string(EnvironmentStaging), "staging"},
		{"environment dev", string(EnvironmentDev), "dev"},
		{"change pending", string(ChangeRequestPhasePending), "Pending"},
		{"change accepted", string(ChangeRequestPhaseAccepted), "Accepted"},
		{"change rejected", string(ChangeRequestPhaseRejected), "Rejected"},
		{"change applied", string(ChangeRequestPhaseApplied), "Applied"},
		{"evidence passed", string(EvidencePassed), "passed"},
		{"evidence failed", string(EvidenceFailed), "failed"},
		{"evidence not run", string(EvidenceNotRun), "not-run"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != tt.want {
				t.Errorf("value = %q, want %q", tt.value, tt.want)
			}
		})
	}
}

// A field nobody filled in must not read as a pass: an empty result is a check
// that was not recorded, and the rules treat anything that is not "passed" as
// not passed.
func TestAnEmptyEvidenceResultIsNotAPass(t *testing.T) {
	empty := EvidenceResult("")

	if empty == EvidencePassed {
		t.Fatal("an empty result equals passed")
	}
	if empty != EvidenceNotRun {
		t.Log("an empty result is distinct from not-run, which the rules already read as not passed")
	}
}
