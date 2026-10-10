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

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

// Resolve checks the session a token established against the session the
// platform recorded, and answers with the scope the request runs as.
//
// It is the second half of the predicate. The verifier proves the token is the
// platform's; this proves it is still that session's. A token is a photograph
// of a session at the moment it was issued, so everything the platform may have
// changed since is read from the session and not from the token: the phase, the
// mode, the tool list, the expiry, and which token is the current one. A tool
// taken away, a mode downgraded or a session ended therefore takes effect on
// the next request rather than when the token happens to expire.
func Resolve(recorded *appv1.AgentSession, established Session) (Session, error) {
	if recorded == nil {
		return Session{}, fmt.Errorf("the token names a session that does not exist")
	}
	if established.UID != string(recorded.UID) {
		return Session{}, fmt.Errorf("the token names session %s, not %s", established.UID, recorded.UID)
	}
	if recorded.Status.Phase != appv1.SessionPhaseReady {
		return Session{}, fmt.Errorf("the session is %s", recorded.Status.Phase)
	}
	if recorded.Status.TokenJTI == "" || recorded.Status.TokenJTI != established.TokenJTI {
		return Session{}, fmt.Errorf("the token is no longer the session's current one")
	}
	if recorded.Status.ExpiresAt == nil || recorded.Status.ExpiresAt.IsZero() {
		return Session{}, fmt.Errorf("the session has no expiry, and a session without one is not trusted")
	}
	if recorded.Spec.Mode != established.Mode {
		return Session{}, fmt.Errorf("the session's mode is now %q, and the token carries %q",
			recorded.Spec.Mode, established.Mode)
	}
	if recorded.Spec.GroupRef.UID != established.GroupUID {
		return Session{}, fmt.Errorf("the session is scoped to group %s, and the token carries %s",
			recorded.Spec.GroupRef.UID, established.GroupUID)
	}
	if recorded.Spec.Mode == appv1.SessionModeSandbox &&
		recorded.Status.SandboxNamespaceUID != established.SandboxNamespaceUID {
		return Session{}, fmt.Errorf("the session's sandbox is no longer the one the token was issued for")
	}

	tools := make([]string, 0, len(recorded.Spec.Tools))
	for _, tool := range recorded.Spec.Tools {
		tools = append(tools, string(tool))
	}

	return Session{
		UID:                 string(recorded.UID),
		TokenJTI:            recorded.Status.TokenJTI,
		GroupUID:            recorded.Spec.GroupRef.UID,
		SandboxNamespaceUID: recorded.Status.SandboxNamespaceUID,
		Mode:                recorded.Spec.Mode,
		Tools:               tools,
		ExpiresAt:           recorded.Status.ExpiresAt.Time,
	}, nil
}
