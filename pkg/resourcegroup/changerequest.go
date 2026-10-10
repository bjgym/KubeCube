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

// ChangeEvent is what happens to a change request.
type ChangeEvent string

const (
	// EventAccept is a person taking responsibility for the change, which is
	// what makes it landable. It is the step Review gates.
	EventAccept ChangeEvent = "accept"

	// EventReject is a person declining it.
	EventReject ChangeEvent = "reject"

	// EventApply is a person having landed it through an existing channel — a
	// Helm upgrade, a pipeline, the console's YAML editing, an operator. The
	// platform does not apply anything itself, and there is no event for it
	// doing so.
	EventApply ChangeEvent = "apply"
)

// NextPhase answers what a change request becomes when an event happens.
//
// The rule that matters is that applying is not reachable from pending: a
// change is accepted before it is landed, so the record says who took
// responsibility for it rather than only that it happened. Both ends are
// terminal, because a landed change is not un-landed by moving this record —
// that is another change request.
func NextPhase(phase appv1.ChangeRequestPhase, event ChangeEvent) (appv1.ChangeRequestPhase, error) {
	switch phase {
	case appv1.ChangeRequestPhasePending:
		switch event {
		case EventAccept:
			return appv1.ChangeRequestPhaseAccepted, nil
		case EventReject:
			return appv1.ChangeRequestPhaseRejected, nil
		case EventApply:
			return "", fmt.Errorf("a change is accepted before it is landed, so that the record says who took responsibility for it")
		default:
			return "", fmt.Errorf("the event %q is not one this rule knows", event)
		}

	case appv1.ChangeRequestPhaseAccepted:
		switch event {
		case EventApply:
			return appv1.ChangeRequestPhaseApplied, nil
		case EventReject:
			return "", fmt.Errorf("an accepted change is not rejected afterwards: propose another change instead")
		case EventAccept:
			return "", fmt.Errorf("the change is already accepted")
		default:
			return "", fmt.Errorf("the event %q is not one this rule knows", event)
		}

	case appv1.ChangeRequestPhaseApplied:
		return "", fmt.Errorf("the change was landed, and what was landed is changed by another change request")

	case appv1.ChangeRequestPhaseRejected:
		return "", fmt.Errorf("the change was rejected, and a rejected change is not reopened")

	default:
		return "", fmt.Errorf("the phase %q is not one this rule knows", phase)
	}
}
