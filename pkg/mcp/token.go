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
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt"
)

// Audience is what a session token's aud must carry. It is a separate value
// from the console's tokens so that a token issued for a person cannot be spent
// here, and a token issued here cannot be spent there.
const Audience = "mcp"

// SessionClaims is what a session token carries. The platform signs it; this
// package verifies it, and both sides share the type so the vocabulary cannot
// drift between them.
type SessionClaims struct {
	// SessionUID is the AgentSession the token was issued for.
	SessionUID string `json:"sessionUID"`

	// GroupUID is the group the session is scoped to.
	GroupUID string `json:"groupUID"`

	// SandboxNamespaceUID binds the token to one namespace object, so deleting
	// the namespace and recreating the same name does not hand the session a
	// fresh sandbox.
	SandboxNamespaceUID string `json:"sandboxNamespaceUID"`

	// Mode and Tools are the session's scope, copied from the AgentSession the
	// platform recorded.
	Mode  Mode     `json:"mode"`
	Tools []string `json:"tools"`

	// StandardClaims carries aud, exp and iss, which the JWT library already
	// models. Declaring an audience field here as well would shadow the
	// embedded one, and a caller that set it would be silently ignored.
	jwt.StandardClaims
}

// Verifier turns a token into the scope of a request.
type Verifier struct {
	// Secret is the platform's signing secret. It is the same secret the
	// console tokens use, so a deployment has one place to rotate.
	Secret string
}

// SignSession issues a token for a session. The platform calls it; keeping it
// here is what stops the claim names from existing in two places.
//
// It fills in the audience when the caller leaves it out, because this function
// only ever issues tokens for this surface, and a token that surface would
// refuse is not a token worth signing.
func SignSession(secret string, claims SessionClaims) (string, error) {
	if claims.Audience == "" {
		claims.Audience = Audience
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("signing the session token: %w", err)
	}
	return signed, nil
}

// Verify checks a token and returns the session it establishes.
//
// Every check refuses rather than defaults: a token without an audience, an
// expiry, a session id or a known mode establishes nothing, because a session
// this server cannot describe is one it cannot bound.
func (v Verifier) Verify(token string) (Session, error) {
	if token == "" {
		return Session{}, fmt.Errorf("no token was presented")
	}
	if v.Secret == "" {
		return Session{}, fmt.Errorf("this server has no signing secret, so it cannot verify anything")
	}

	claims := &SessionClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (interface{}, error) {
		return []byte(v.Secret), nil
	})
	if err != nil {
		return Session{}, fmt.Errorf("the token could not be verified")
	}
	if !parsed.Valid {
		return Session{}, fmt.Errorf("the token is not valid")
	}

	switch {
	case claims.Audience != Audience:
		return Session{}, fmt.Errorf("the token was issued for %q, not for this surface", claims.Audience)
	case claims.SessionUID == "":
		return Session{}, fmt.Errorf("the token names no session")
	case claims.ExpiresAt == 0:
		return Session{}, fmt.Errorf("the token has no expiry")
	case claims.Id == "":
		return Session{}, fmt.Errorf("the token has no id, so it could never be revoked")
	case claims.Mode != ModeReadOnly && claims.Mode != ModeSandbox:
		return Session{}, fmt.Errorf("the token carries the unknown mode %q", claims.Mode)
	}

	return Session{
		UID:                 claims.SessionUID,
		TokenJTI:            claims.Id,
		GroupUID:            claims.GroupUID,
		SandboxNamespaceUID: claims.SandboxNamespaceUID,
		Mode:                claims.Mode,
		Tools:               claims.Tools,
		ExpiresAt:           time.Unix(claims.ExpiresAt, 0),
	}, nil
}

// BearerToken reads the bearer token a request presents. It is the transport's
// half of establishing a session: the server takes the token out of the header,
// and the verifier decides whether it is the platform's.
func BearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", fmt.Errorf("no Authorization header was sent")
	}

	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", fmt.Errorf("the Authorization header is not a bearer token")
	}

	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", fmt.Errorf("the Authorization header carries an empty token")
	}

	return token, nil
}

// Session verifies one token and returns the session it establishes. It is what
// a deployment wires into Server.SessionFrom.
func (v Verifier) Session(token string) (Session, error) {
	return v.Verify(token)
}
