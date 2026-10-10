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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
)

const testSecret = "a-secret-only-the-platform-and-this-server-share"

func signWith(t *testing.T, secret string, claims SessionClaims) string {
	t.Helper()

	token, err := SignSession(secret, claims)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return token
}

// signRaw signs without the issuer's defaults, for the tokens this package has
// to refuse: a claim the issuer always fills in cannot be tested through it.
func signRaw(t *testing.T, secret string, claims SessionClaims) string {
	t.Helper()

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return token
}

func validClaims() SessionClaims {
	return SessionClaims{
		SessionUID:          "sess-7f3a",
		GroupUID:            "8f2c-4d91",
		SandboxNamespaceUID: "3d91-77aa",
		Mode:                ModeSandbox,
		Tools:               []string{"describe_group"},
		StandardClaims: jwt.StandardClaims{
			Audience:  Audience,
			Id:        "jti-1",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		},
	}
}

func TestSignSessionFillsInTheAudience(t *testing.T) {
	claims := validClaims()
	claims.Audience = ""

	verifier := Verifier{Secret: testSecret}
	if _, err := verifier.Verify(signWith(t, testSecret, claims)); err != nil {
		t.Fatalf("Verify: %v, want the issuer to have filled the audience in", err)
	}
}

func TestVerifyAcceptsASessionToken(t *testing.T) {
	verifier := Verifier{Secret: testSecret}

	session, err := verifier.Verify(signWith(t, testSecret, validClaims()))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if session.UID != "sess-7f3a" || session.GroupUID != "8f2c-4d91" {
		t.Errorf("session = %+v, want the token's identity", session)
	}
	if session.SandboxNamespaceUID != "3d91-77aa" || session.Mode != ModeSandbox {
		t.Errorf("session = %+v, want the token's sandbox and mode", session)
	}
	if len(session.Tools) != 1 || session.Tools[0] != "describe_group" {
		t.Errorf("tools = %v, want the token's allowlist", session.Tools)
	}
	if session.ExpiresAt.IsZero() {
		t.Error("expiry is zero, want the token's expiry")
	}
}

func TestVerifyRefuses(t *testing.T) {
	tests := []struct {
		name  string
		token func(t *testing.T) string
	}{
		{
			name: "a token signed with another secret",
			token: func(t *testing.T) string {
				return signWith(t, "another-secret", validClaims())
			},
		},
		{
			name: "a token issued for the console",
			token: func(t *testing.T) string {
				claims := validClaims()
				claims.Audience = "kubecube"
				return signWith(t, testSecret, claims)
			},
		},
		{
			name: "a token without an id to revoke it by",
			token: func(t *testing.T) string {
				claims := validClaims()
				claims.Id = ""
				return signRaw(t, testSecret, claims)
			},
		},
		{
			name: "a token without an audience",
			token: func(t *testing.T) string {
				claims := validClaims()
				claims.Audience = ""
				return signRaw(t, testSecret, claims)
			},
		},
		{
			name: "an expired token",
			token: func(t *testing.T) string {
				claims := validClaims()
				claims.ExpiresAt = time.Now().Add(-time.Minute).Unix()
				return signWith(t, testSecret, claims)
			},
		},
		{
			name: "a token without an expiry",
			token: func(t *testing.T) string {
				claims := validClaims()
				claims.ExpiresAt = 0
				return signWith(t, testSecret, claims)
			},
		},
		{
			name: "a token that names no session",
			token: func(t *testing.T) string {
				claims := validClaims()
				claims.SessionUID = ""
				return signWith(t, testSecret, claims)
			},
		},
		{
			name: "a token carrying an unknown mode",
			token: func(t *testing.T) string {
				claims := validClaims()
				claims.Mode = Mode("administrator")
				return signWith(t, testSecret, claims)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := Verifier{Secret: testSecret}
			if session, err := verifier.Verify(tt.token(t)); err == nil {
				t.Fatalf("Verify = %+v, want a refusal", session)
			}
		})
	}
}

func TestVerifyRefusesWithoutASecret(t *testing.T) {
	verifier := Verifier{}

	if _, err := verifier.Verify(signWith(t, testSecret, validClaims())); err == nil {
		t.Fatal("Verify = nil, want a refusal: a server without a secret cannot verify anything")
	}
}

func TestBearerTokenAndSession(t *testing.T) {
	verifier := Verifier{Secret: testSecret}
	token := signWith(t, testSecret, validClaims())

	tests := []struct {
		name    string
		header  string
		refused bool
	}{
		{name: "a bearer token", header: "Bearer " + token},
		{name: "no header", header: "", refused: true},
		{name: "another scheme", header: "Basic " + token, refused: true},
		{name: "a header without a token", header: "Bearer", refused: true},
		{name: "a token that does not verify", header: "Bearer not-a-token", refused: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tt.header != "" {
				request.Header.Set("Authorization", tt.header)
			}

			presented, err := BearerToken(request)
			if err != nil {
				if !tt.refused {
					t.Fatalf("BearerToken: %v", err)
				}
				return
			}

			session, err := verifier.Session(presented)
			if tt.refused {
				if err == nil {
					t.Fatalf("Session = %+v, want a refusal", session)
				}
				return
			}

			if err != nil {
				t.Fatalf("Session: %v", err)
			}
			if session.UID != "sess-7f3a" {
				t.Errorf("session = %+v, want the token's session", session)
			}
		})
	}
}
