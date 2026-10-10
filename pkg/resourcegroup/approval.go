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
	"fmt"

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

// Result is the outcome of one check the platform ran while a change was being
// prepared. It is the API's own type rather than a copy of its values: the
// platform records these on the ChangeRequest and these rules read them back,
// and two spellings of the same three values would eventually disagree about
// whether a check nobody ran counts as a pass.
type Result = appv1.EvidenceResult

const (
	// ResultPassed is a check that ran and passed.
	ResultPassed = appv1.EvidencePassed

	// ResultFailed is a check that ran and failed.
	ResultFailed = appv1.EvidenceFailed

	// ResultNotRun is a check that was never run.
	ResultNotRun = appv1.EvidenceNotRun
)

// Evidence is what the platform observed while a change was being prepared.
//
// It is not decoration. A change request without it asks a person to approve
// something nobody checked, and the reviewer's whole job is to approve what was
// validated rather than what was described.
type Evidence struct {
	// DryRun is the server's answer to applying the change.
	DryRun Result

	// Policy is the sandbox policy check.
	Policy Result

	// SmokeTest is the job that ran inside the sandbox.
	SmokeTest Result

	// QuotaEstimated records that the change's quota delta and the room left in
	// the group were worked out. A change that would exhaust a quota is one a
	// reviewer has to see before landing it, not after.
	//
	// It is the one field that does not mirror appv1.Evidence: that type records
	// the numbers themselves, and this is the question a reviewer's rule asks of
	// them, which is whether they were worked out at all.
	QuotaEstimated bool
}

// Approval is what a change needs before a person may land it.
type Approval struct {
	// Approvers is how many distinct people must approve it.
	Approvers int

	// Separated says the requester's own approval does not count.
	Separated bool
}

// ApprovalFor answers what a change in this environment needs.
//
// The environment is a threshold and only a threshold: it can ask for more
// people, and it never decides who may write. A value this rule does not know
// is answered with production's requirement, because the alternative is a typo
// in a label quietly removing two reviewers.
func ApprovalFor(environment appv1.Environment) Approval {
	switch environment {
	case appv1.EnvironmentDev:
		return Approval{Approvers: 0, Separated: false}
	case appv1.EnvironmentStaging:
		return Approval{Approvers: 1, Separated: true}
	default:
		return Approval{Approvers: 2, Separated: true}
	}
}

// Change is what a reviewer needs to know about one change request.
type Change struct {
	// Requester is who proposed it.
	Requester string

	// Environment is where the group sits.
	Environment appv1.Environment

	// Approvers are the people who have approved it so far.
	Approvers []string

	// Evidence is what was validated before it was proposed.
	Evidence Evidence
}

// Verdict is whether a change may be landed, and why not when it may not.
type Verdict struct {
	MayLand bool
	Reason  string
}

// Review answers whether one change may be landed.
//
// The evidence is read first: a change nobody validated is not made acceptable
// by enough approvals, and saying so in that order keeps the reason a reviewer
// reads the one that matters.
func Review(change Change) Verdict {
	if verdict := reviewEvidence(change.Evidence); !verdict.MayLand {
		return verdict
	}

	approval := ApprovalFor(change.Environment)
	approvers := distinctApprovers(change.Approvers, change.Requester, approval.Separated)

	if len(approvers) < approval.Approvers {
		return Verdict{Reason: shortOfApprovals(change, approval, len(approvers))}
	}

	if approval.Approvers == 0 {
		return Verdict{MayLand: true, Reason: "this environment needs no approval, and the change was validated"}
	}
	return Verdict{MayLand: true, Reason: fmt.Sprintf("%d approvals are recorded", len(approvers))}
}

func reviewEvidence(evidence Evidence) Verdict {
	checks := []struct {
		name   string
		result Result
	}{
		{"the server-side dry run", evidence.DryRun},
		{"the policy check", evidence.Policy},
		{"the smoke test", evidence.SmokeTest},
	}

	for _, check := range checks {
		switch check.result {
		case ResultPassed:
			continue
		case ResultFailed:
			return Verdict{Reason: fmt.Sprintf("%s failed, so what a reviewer would approve is not what was tested", check.name)}
		default:
			return Verdict{Reason: fmt.Sprintf("%s was never run, so nothing supports this change", check.name)}
		}
	}

	if !evidence.QuotaEstimated {
		return Verdict{Reason: "the change's quota effect was never worked out, so a reviewer cannot see what it costs"}
	}

	return Verdict{MayLand: true}
}

// distinctApprovers counts the people whose approval counts, which is not the
// same as counting approvals: one person approving twice is one approval, and
// in an environment that separates duties the requester is not an approver at
// all.
func distinctApprovers(approvers []string, requester string, separated bool) []string {
	seen := make(map[string]bool, len(approvers))
	out := make([]string, 0, len(approvers))

	for _, approver := range approvers {
		if approver == "" || seen[approver] {
			continue
		}
		if separated && approver == requester {
			continue
		}
		seen[approver] = true
		out = append(out, approver)
	}

	return out
}

func shortOfApprovals(change Change, approval Approval, have int) string {
	missing := approval.Approvers - have

	who := ""
	if approval.Separated {
		who = " from people other than the requester"
	}

	return fmt.Sprintf("%d more approval(s) needed%s: this environment asks for %d, and %d count",
		missing, who, approval.Approvers, have)
}
