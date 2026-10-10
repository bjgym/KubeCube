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

func TestNextPhase(t *testing.T) {
	tests := []struct {
		name      string
		phase     appv1.ChangeRequestPhase
		event     ChangeEvent
		want      appv1.ChangeRequestPhase
		reasonHas string
	}{
		{
			name:  "a pending change is accepted",
			phase: appv1.ChangeRequestPhasePending, event: EventAccept,
			want: appv1.ChangeRequestPhaseAccepted,
		},
		{
			name:  "a pending change is rejected",
			phase: appv1.ChangeRequestPhasePending, event: EventReject,
			want: appv1.ChangeRequestPhaseRejected,
		},
		{
			name:  "an accepted change is landed",
			phase: appv1.ChangeRequestPhaseAccepted, event: EventApply,
			want: appv1.ChangeRequestPhaseApplied,
		},
		{
			name:  "landing a change nobody accepted",
			phase: appv1.ChangeRequestPhasePending, event: EventApply,
			reasonHas: "accepted before it is landed",
		},
		{
			name:  "rejecting a change that was already accepted",
			phase: appv1.ChangeRequestPhaseAccepted, event: EventReject,
			reasonHas: "propose another change instead",
		},
		{
			name:  "accepting a change twice",
			phase: appv1.ChangeRequestPhaseAccepted, event: EventAccept,
			reasonHas: "already accepted",
		},
		{
			name:  "landing a change that was landed",
			phase: appv1.ChangeRequestPhaseApplied, event: EventApply,
			reasonHas: "another change request",
		},
		{
			name:  "reopening a rejected change",
			phase: appv1.ChangeRequestPhaseRejected, event: EventAccept,
			reasonHas: "not reopened",
		},
		{
			name:  "an event this rule does not know",
			phase: appv1.ChangeRequestPhasePending, event: ChangeEvent("rollback"),
			reasonHas: "not one this rule knows",
		},
		{
			name:  "a phase this rule does not know",
			phase: appv1.ChangeRequestPhase("Drafting"), event: EventAccept,
			reasonHas: "not one this rule knows",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextPhase(tt.phase, tt.event)

			if tt.reasonHas == "" {
				if err != nil {
					t.Fatalf("NextPhase: %v", err)
				}
				if got != tt.want {
					t.Errorf("phase = %q, want %q", got, tt.want)
				}
				return
			}

			if err == nil {
				t.Fatalf("NextPhase = %q, want a refusal", got)
			}
			if !strings.Contains(err.Error(), tt.reasonHas) {
				t.Errorf("error = %q, want it to carry %q", err.Error(), tt.reasonHas)
			}
		})
	}
}

// The two rules that carry the design: nothing lands without someone having
// accepted it, and both ends of the record are terminal.
func TestNextPhaseKeepsTheRecordHonest(t *testing.T) {
	if _, err := NextPhase(appv1.ChangeRequestPhasePending, EventApply); err == nil {
		t.Error("a change reached Applied without being accepted, so the record would not say who took responsibility")
	}

	for _, phase := range []appv1.ChangeRequestPhase{appv1.ChangeRequestPhaseApplied, appv1.ChangeRequestPhaseRejected} {
		for _, event := range []ChangeEvent{EventAccept, EventReject, EventApply} {
			if got, err := NextPhase(phase, event); err == nil {
				t.Errorf("NextPhase(%q, %q) = %q, want a refusal: the phase is terminal", phase, event, got)
			}
		}
	}
}
