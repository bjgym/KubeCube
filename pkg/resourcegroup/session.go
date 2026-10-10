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
	"time"

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
	"github.com/kubecube-io/kubecube/pkg/ownership"
)

// SandboxLabel marks the namespace a session writes in, and carries the
// session's UID. It is what the admission backstop scopes itself by, so a
// namespace without it is not a sandbox whatever else it carries.
const SandboxLabel = "kubecube.io/agent-sandbox"

// sandboxPrefix names a sandbox namespace after its session.
const sandboxPrefix = "kubecube-agent-"

// SandboxNamespaceName is what one session's sandbox namespace is called.
//
// The name is derived rather than chosen so that a session and its sandbox
// cannot drift apart, and so an operator reading a namespace list can tell
// where it came from.
func SandboxNamespaceName(sessionName string) string {
	return sandboxPrefix + sessionName
}

// SandboxNamespaceLabels are the labels a sandbox namespace carries.
//
// It is owned by the project the session was created for, so the ResourceQuota
// webhook — which selects on the owner label — admits it under that project's
// quota, and the tenant key still answers a tenant-scoped question. Its level is
// sandbox rather than space, so the readers that enumerate a project's spaces
// never offer it to a user as somewhere to work.
func SandboxNamespaceLabels(tenant, project, sessionUID string) map[string]string {
	labels := ownership.SandboxLabels(tenant, project)
	labels[SandboxLabel] = sessionUID
	return labels
}

// SessionState is what a controller knows about one session when it decides
// whether the session is still alive.
type SessionState struct {
	Name       string
	Phase      appv1.SessionPhase
	ExpiresAt  time.Time
	LastUsedAt time.Time
}

// SessionAction is what a controller must do about one session.
type SessionAction string

const (
	// SessionKeep leaves a live session alone.
	SessionKeep SessionAction = "keep"

	// SessionExpire marks a session expired, which invalidates its token, and
	// hands its sandbox to the next pass to delete.
	SessionExpire SessionAction = "expire"

	// SessionDestroySandbox deletes a sandbox whose session is already over.
	SessionDestroySandbox SessionAction = "destroy-sandbox"
)

// SessionPlan is the decision for one session, with the reason a person would
// need to understand why their session ended.
type SessionPlan struct {
	Name   string
	Action SessionAction
	Reason string
}

// EvaluateSessions decides what to do about each session at one moment.
//
// It takes the moment as an argument rather than reading a clock, so the same
// rule can be tested at a boundary instead of around one.
//
// Reclamation is driven from here and not from the tenancy cascade: a session's
// sandbox must be deleted by the controller that knows about the session, so a
// cascade that runs while a controller is down cannot orphan one.
func EvaluateSessions(states []SessionState, idleTimeout time.Duration, now time.Time) []SessionPlan {
	plans := make([]SessionPlan, 0, len(states))

	for _, state := range states {
		plans = append(plans, evaluateSession(state, idleTimeout, now))
	}

	return plans
}

func evaluateSession(state SessionState, idleTimeout time.Duration, now time.Time) SessionPlan {
	plan := func(action SessionAction, reason string) SessionPlan {
		return SessionPlan{Name: state.Name, Action: action, Reason: reason}
	}

	switch state.Phase {
	case appv1.SessionPhaseRevoked:
		return plan(SessionDestroySandbox, "the session was revoked")

	case appv1.SessionPhaseExpired:
		return plan(SessionDestroySandbox, "the session is over and its sandbox is still here")

	case appv1.SessionPhasePending:
		// The session is being set up; its sandbox may not exist yet.
		return plan(SessionKeep, "the session is still being established")

	case appv1.SessionPhaseReady:
		switch {
		case state.ExpiresAt.IsZero():
			return plan(SessionExpire, "the session has no expiry, and a session without one is not trusted")
		case now.After(state.ExpiresAt):
			return plan(SessionExpire, "the session reached its expiry")
		case idleTimeout > 0 && !state.LastUsedAt.IsZero() && now.After(state.LastUsedAt.Add(idleTimeout)):
			return plan(SessionExpire, "the session was idle for longer than its timeout")
		default:
			return plan(SessionKeep, "the session is alive")
		}

	default:
		return plan(SessionExpire, "the session carries a phase this rule does not know")
	}
}
