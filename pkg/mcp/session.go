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
	"fmt"
	"time"

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

// Mode is what a session is allowed to be. It is the API's own type rather than
// a copy of its values: the platform records a session's mode on the
// AgentSession, this surface carries it in a token, and two spellings of the
// same two values would eventually disagree — a session whose token says a mode
// the surface does not know is refused for no reason a reader could find.
type Mode = appv1.SessionMode

const (
	// ModeReadOnly reads and proposes.
	ModeReadOnly = appv1.SessionModeReadOnly

	// ModeSandbox reads, proposes and writes inside its own sandbox.
	ModeSandbox = appv1.SessionModeSandbox
)

// Session is the scope a token carries: which group it is bound to, which
// sandbox it may write in, and which tools it was granted.
//
// It is built from the token and the AgentSession the platform recorded, and
// nothing here may widen it. A field the platform did not set is empty, and an
// empty field refuses rather than allows.
type Session struct {
	// UID is the session object's UID. An empty one means no session was
	// established, which is a refusal rather than an anonymous session.
	UID string

	// TokenJTI is the id of the token this session was established from. The
	// platform compares it with the id recorded on the session, which is what
	// makes a token revocable before it expires.
	TokenJTI string

	// GroupUID is the group the session is scoped to. It is a UID rather than a
	// name so that recreating a name cannot inherit a scope.
	GroupUID string

	// SandboxNamespaceUID binds the session to one namespace object, so
	// deleting the namespace and recreating the same name invalidates it.
	SandboxNamespaceUID string

	// Mode is what the session may do.
	Mode Mode

	// Tools is the allowlist. A tool absent from it is refused.
	Tools []string

	// ExpiresAt is when the session stops being usable. A zero time means the
	// platform did not set one, which is treated as expired.
	ExpiresAt time.Time

	// Revoked ends a session before its expiry.
	Revoked bool
}

// Refusal is why a session may not call a tool. It is a value rather than a
// bare error so a caller can tell a refusal from a failure of the tool itself.
type Refusal struct {
	Tool   string
	Reason string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("tool %s is refused: %s", r.Tool, r.Reason)
}

// Authorize decides whether this session may call a tool.
//
// It only narrows: every rule below can refuse a call the platform would
// otherwise allow, and none can allow one the platform refuses. The platform
// checks the same facts again on the request it receives, because this surface
// is replaceable and the boundary is not.
func (s Session) Authorize(tool Tool) error {
	switch {
	case s.UID == "":
		return &Refusal{Tool: tool.Name, Reason: "no session was established"}
	case s.Revoked:
		return &Refusal{Tool: tool.Name, Reason: "the session was revoked"}
	case s.ExpiresAt.IsZero():
		return &Refusal{Tool: tool.Name, Reason: "the session has no expiry, and a session without one is not trusted"}
	case time.Now().After(s.ExpiresAt):
		return &Refusal{Tool: tool.Name, Reason: "the session expired"}
	case !s.grants(tool.Name):
		return &Refusal{Tool: tool.Name, Reason: "the session is not granted this tool"}
	case !s.modeAllows(tool.Tier):
		return &Refusal{
			Tool:   tool.Name,
			Reason: fmt.Sprintf("a %s session may not call a %s tool", s.Mode, tool.Tier),
		}
	}

	return nil
}

func (s Session) grants(name string) bool {
	for _, granted := range s.Tools {
		if granted == name {
			return true
		}
	}
	return false
}

// modeAllows keeps the tiers a mode may reach. An unknown mode allows nothing,
// so a mode this server does not know cannot become a way in.
func (s Session) modeAllows(tier Tier) bool {
	switch s.Mode {
	case ModeReadOnly:
		return tier == TierRead || tier == TierPropose
	case ModeSandbox:
		return tier == TierRead || tier == TierPropose || tier == TierSandboxWrite || tier == TierSubmit
	default:
		return false
	}
}
