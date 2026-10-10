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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt"

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

// NewJTI mints the identifier of one issued token. The platform records it on
// the session, which is what makes a token revocable: a token whose jti is no
// longer the session's is refused, whether or not it has expired.
func NewJTI() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("minting a token id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// SessionClaimsOf builds the claims for one session.
//
// It reads the session the platform recorded rather than anything the caller
// says, so what a token carries and what the platform believes cannot drift:
// the scope a session has is the scope its object has.
//
// Everything it cannot state is an error rather than a default. A session
// without an expiry, without a UID, or in a mode this surface does not know
// would otherwise become a token that establishes something nobody described.
func SessionClaimsOf(session *appv1.AgentSession, jti string, now time.Time) (SessionClaims, error) {
	if session == nil {
		return SessionClaims{}, fmt.Errorf("no session was given")
	}
	if session.UID == "" {
		return SessionClaims{}, fmt.Errorf("the session has no UID, so a token for it could not be tied to it")
	}
	if jti == "" {
		return SessionClaims{}, fmt.Errorf("no token id was given, so the token could not be revoked")
	}
	if session.Spec.Mode != appv1.SessionModeReadOnly && session.Spec.Mode != appv1.SessionModeSandbox {
		return SessionClaims{}, fmt.Errorf("the session carries the unknown mode %q", session.Spec.Mode)
	}
	if session.Status.ExpiresAt == nil || session.Status.ExpiresAt.IsZero() {
		return SessionClaims{}, fmt.Errorf("the session has no expiry, and a session without one is not trusted")
	}

	// A sandbox session writes somewhere, so it has to say where. A read-only
	// session has no sandbox and is not asked for one.
	if session.Spec.Mode == appv1.SessionModeSandbox && session.Status.SandboxNamespaceUID == "" {
		return SessionClaims{}, fmt.Errorf("the session may write in a sandbox but records no sandbox namespace")
	}

	tools := make([]string, 0, len(session.Spec.Tools))
	for _, tool := range session.Spec.Tools {
		tools = append(tools, string(tool))
	}

	return SessionClaims{
		SessionUID:          string(session.UID),
		GroupUID:            session.Spec.GroupRef.UID,
		SandboxNamespaceUID: session.Status.SandboxNamespaceUID,
		Mode:                Mode(session.Spec.Mode),
		Tools:               tools,
		StandardClaims: jwt.StandardClaims{
			Audience:  Audience,
			Id:        jti,
			IssuedAt:  now.Unix(),
			ExpiresAt: session.Status.ExpiresAt.Unix(),
			Subject:   session.Spec.Owner.User,
		},
	}, nil
}
