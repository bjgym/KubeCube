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
	"strings"
	"testing"

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

func completeEvidence() Evidence {
	return Evidence{
		DryRun:         ResultPassed,
		Policy:         ResultPassed,
		SmokeTest:      ResultPassed,
		QuotaEstimated: true,
	}
}

func TestApprovalFor(t *testing.T) {
	tests := []struct {
		environment appv1.Environment
		approvers   int
		separated   bool
	}{
		{environment: appv1.EnvironmentDev, approvers: 0},
		{environment: appv1.EnvironmentStaging, approvers: 1, separated: true},
		{environment: appv1.EnvironmentProd, approvers: 2, separated: true},
		// A value this rule does not know is answered with production's
		// requirement: the alternative is a typo in a label quietly removing
		// two reviewers.
		{environment: appv1.Environment("production"), approvers: 2, separated: true},
		{environment: appv1.Environment(""), approvers: 2, separated: true},
	}

	for _, tt := range tests {
		t.Run(string(tt.environment), func(t *testing.T) {
			got := ApprovalFor(tt.environment)
			if got.Approvers != tt.approvers || got.Separated != tt.separated {
				t.Errorf("ApprovalFor(%q) = %+v, want %d approvers separated=%v",
					tt.environment, got, tt.approvers, tt.separated)
			}
		})
	}
}

func TestReviewApprovals(t *testing.T) {
	tests := []struct {
		name      string
		change    Change
		mayLand   bool
		reasonHas string
	}{
		{
			name:    "a validated change in dev lands on the requester's word",
			change:  Change{Requester: "alice", Environment: appv1.EnvironmentDev, Evidence: completeEvidence()},
			mayLand: true,
		},
		{
			name: "a validated change in staging with one other approver",
			change: Change{Requester: "alice", Environment: appv1.EnvironmentStaging,
				Approvers: []string{"bob"}, Evidence: completeEvidence()},
			mayLand: true,
		},
		{
			name: "a validated change in prod with two other approvers",
			change: Change{Requester: "alice", Environment: appv1.EnvironmentProd,
				Approvers: []string{"bob", "carol"}, Evidence: completeEvidence()},
			mayLand: true,
		},
		{
			name: "the requester approving their own change in staging",
			change: Change{Requester: "alice", Environment: appv1.EnvironmentStaging,
				Approvers: []string{"alice"}, Evidence: completeEvidence()},
			reasonHas: "other than the requester",
		},
		{
			name: "the requester's approval not counting towards prod's two",
			change: Change{Requester: "alice", Environment: appv1.EnvironmentProd,
				Approvers: []string{"alice", "bob"}, Evidence: completeEvidence()},
			reasonHas: "1 more approval",
		},
		{
			name: "one person approving twice",
			change: Change{Requester: "alice", Environment: appv1.EnvironmentProd,
				Approvers: []string{"bob", "bob"}, Evidence: completeEvidence()},
			reasonHas: "1 more approval",
		},
		{
			name: "an environment nobody labelled",
			change: Change{Requester: "alice", Environment: appv1.Environment(""),
				Approvers: []string{"bob"}, Evidence: completeEvidence()},
			reasonHas: "1 more approval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := Review(tt.change)
			if verdict.MayLand != tt.mayLand {
				t.Fatalf("MayLand = %v (%s), want %v", verdict.MayLand, verdict.Reason, tt.mayLand)
			}
			if verdict.Reason == "" {
				t.Error("the verdict carries no reason")
			}
			if tt.reasonHas != "" && !strings.Contains(verdict.Reason, tt.reasonHas) {
				t.Errorf("reason = %q, want it to carry %q", verdict.Reason, tt.reasonHas)
			}
		})
	}
}

// A change nobody validated is not made acceptable by enough approvals, and the
// reason a reviewer reads is the one about the evidence.
func TestReviewEvidence(t *testing.T) {
	tests := []struct {
		name      string
		evidence  Evidence
		reasonHas string
	}{
		{
			name:      "a dry run that failed",
			evidence:  Evidence{DryRun: ResultFailed, Policy: ResultPassed, SmokeTest: ResultPassed, QuotaEstimated: true},
			reasonHas: "dry run failed",
		},
		{
			name:      "a policy check that was never run",
			evidence:  Evidence{DryRun: ResultPassed, Policy: ResultNotRun, SmokeTest: ResultPassed, QuotaEstimated: true},
			reasonHas: "policy check was never run",
		},
		{
			name:      "a smoke test that failed",
			evidence:  Evidence{DryRun: ResultPassed, Policy: ResultPassed, SmokeTest: ResultFailed, QuotaEstimated: true},
			reasonHas: "smoke test failed",
		},
		{
			name:      "evidence nobody filled in",
			evidence:  Evidence{},
			reasonHas: "dry run was never run",
		},
		{
			name:      "a quota effect nobody worked out",
			evidence:  Evidence{DryRun: ResultPassed, Policy: ResultPassed, SmokeTest: ResultPassed},
			reasonHas: "quota effect was never worked out",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			change := Change{
				Requester:   "alice",
				Environment: appv1.EnvironmentDev,
				Evidence:    tt.evidence,
			}

			verdict := Review(change)
			if verdict.MayLand {
				t.Fatalf("MayLand = true (%s), want a refusal", verdict.Reason)
			}
			if !strings.Contains(verdict.Reason, tt.reasonHas) {
				t.Errorf("reason = %q, want it to carry %q", verdict.Reason, tt.reasonHas)
			}
		})
	}
}

// The evidence is read before the approvals, so a change with neither is
// refused for the reason that matters rather than for the count.
func TestReviewReadsTheEvidenceFirst(t *testing.T) {
	verdict := Review(Change{
		Requester:   "alice",
		Environment: appv1.EnvironmentProd,
		Evidence:    Evidence{},
	})

	if verdict.MayLand {
		t.Fatal("MayLand = true, want a refusal")
	}
	if strings.Contains(verdict.Reason, "approval") {
		t.Errorf("reason = %q, want the evidence named rather than the approvals", verdict.Reason)
	}
}
