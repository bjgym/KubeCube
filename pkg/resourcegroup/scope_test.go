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

const sandboxNS = "kubecube-agent-sess-7f3a"

func sandboxScope() Scope {
	return Scope{
		Session:          "sess-7f3a",
		GroupUID:         groupUID,
		GroupNamespace:   groupNS,
		SandboxNamespace: sandboxNS,
		Mode:             appv1.SessionModeSandbox,
		Members: []Ref{
			{APIVersion: "apps/v1", Kind: "Deployment", Namespace: groupNS, Name: "web"},
			{APIVersion: "v1", Kind: "Pod", Namespace: groupNS, Name: "web-6d4f-x2k"},
			{APIVersion: "v1", Kind: "Secret", Namespace: groupNS, Name: "credentials"},
		},
	}
}

func readOnlyScope() Scope {
	s := sandboxScope()
	s.Mode = appv1.SessionModeReadOnly
	return s
}

func TestDecideAbstainsForASubjectThatIsNotASession(t *testing.T) {
	scope := Scope{}

	access := scope.Decide(Request{
		Verb: VerbGet,
		Ref:  Ref{Kind: "Deployment", Namespace: groupNS, Name: "web"},
	})

	if access.Decided {
		t.Fatalf("access = %+v, want this package to abstain rather than allow", access)
	}
}

func TestDecideReads(t *testing.T) {
	tests := []struct {
		name    string
		scope   Scope
		request Request
		allowed bool
	}{
		{
			name:  "a declared member",
			scope: sandboxScope(),
			request: Request{Verb: VerbGet, Ref: Ref{
				APIVersion: "apps/v1", Kind: "Deployment", Namespace: groupNS, Name: "web"}},
			allowed: true,
		},
		{
			name:  "a derived member",
			scope: sandboxScope(),
			request: Request{Verb: VerbList, Ref: Ref{
				APIVersion: "v1", Kind: "Pod", Namespace: groupNS, Name: "web-6d4f-x2k"}},
			allowed: true,
		},
		{
			name:  "an object inside the sandbox",
			scope: sandboxScope(),
			request: Request{Verb: VerbGet, Ref: Ref{
				APIVersion: "v1", Kind: "ConfigMap", Namespace: sandboxNS, Name: "draft"}},
			allowed: true,
		},
		{
			name:  "an object in the group's namespace that is not a member",
			scope: sandboxScope(),
			request: Request{Verb: VerbGet, Ref: Ref{
				APIVersion: "apps/v1", Kind: "Deployment", Namespace: groupNS, Name: "someone-elses"}},
		},
		{
			name:  "an object in another namespace",
			scope: sandboxScope(),
			request: Request{Verb: VerbGet, Ref: Ref{
				APIVersion: "apps/v1", Kind: "Deployment", Namespace: "another-space", Name: "web"}},
		},
		{
			name:  "a member that is a Secret",
			scope: sandboxScope(),
			request: Request{Verb: VerbGet, Ref: Ref{
				APIVersion: "v1", Kind: "Secret", Namespace: groupNS, Name: "credentials"}},
		},
		{
			name:  "a Secret inside the sandbox",
			scope: sandboxScope(),
			request: Request{Verb: VerbGet, Ref: Ref{
				APIVersion: "v1", Kind: "Secret", Namespace: sandboxNS, Name: "draft"}},
		},
		{
			name:  "the logs of a member",
			scope: sandboxScope(),
			request: Request{Verb: VerbLogs, Ref: Ref{
				APIVersion: "v1", Kind: "Pod", Namespace: groupNS, Name: "web-6d4f-x2k"}},
			allowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := tt.scope.Decide(tt.request)
			if !access.Decided {
				t.Fatalf("access = %+v, want a decision", access)
			}
			if access.Allowed != tt.allowed {
				t.Fatalf("allowed = %v (%s), want %v", access.Allowed, access.Reason, tt.allowed)
			}
		})
	}
}

func TestDecideWrites(t *testing.T) {
	tests := []struct {
		name    string
		scope   Scope
		request Request
		allowed bool
	}{
		{
			name:  "a write inside the sandbox",
			scope: sandboxScope(),
			request: Request{Verb: VerbCreate, Ref: Ref{
				APIVersion: "v1", Kind: "ConfigMap", Namespace: sandboxNS, Name: "draft"}},
			allowed: true,
		},
		{
			name:  "a member of the production group",
			scope: sandboxScope(),
			request: Request{Verb: VerbPatch, Ref: Ref{
				APIVersion: "apps/v1", Kind: "Deployment", Namespace: groupNS, Name: "web"}},
		},
		{
			name:  "a read-only session writing in its own sandbox",
			scope: readOnlyScope(),
			request: Request{Verb: VerbCreate, Ref: Ref{
				APIVersion: "v1", Kind: "ConfigMap", Namespace: sandboxNS, Name: "draft"}},
		},
		{
			name:  "exec into a member",
			scope: sandboxScope(),
			request: Request{Verb: VerbExec, Ref: Ref{
				APIVersion: "v1", Kind: "Pod", Namespace: groupNS, Name: "web-6d4f-x2k"}},
		},
		{
			name:  "port-forward into the sandbox",
			scope: sandboxScope(),
			request: Request{Verb: VerbPortForward, Ref: Ref{
				APIVersion: "v1", Kind: "Pod", Namespace: sandboxNS, Name: "draft"}},
			allowed: true,
		},
		{
			name:  "scaling a workload in the sandbox",
			scope: sandboxScope(),
			request: Request{Verb: VerbScale, Ref: Ref{
				APIVersion: "apps/v1", Kind: "Deployment", Namespace: sandboxNS, Name: "web"}},
			allowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := tt.scope.Decide(tt.request)
			if access.Allowed != tt.allowed {
				t.Fatalf("allowed = %v (%s), want %v", access.Allowed, access.Reason, tt.allowed)
			}
		})
	}
}

func TestDecideRefusesWhatItDoesNotKnow(t *testing.T) {
	scope := sandboxScope()

	clusterScoped := scope.Decide(Request{
		Verb: VerbGet,
		Ref:  Ref{APIVersion: "v1", Kind: "Namespace", Name: "someone-elses"},
	})
	if clusterScoped.Allowed || !strings.Contains(clusterScoped.Reason, "cluster-scoped") {
		t.Errorf("cluster-scoped access = %+v, want a refusal", clusterScoped)
	}

	unknownVerb := scope.Decide(Request{
		Verb: Verb("impersonate"),
		Ref:  Ref{APIVersion: "v1", Kind: "ConfigMap", Namespace: sandboxNS, Name: "draft"},
	})
	if unknownVerb.Allowed || !strings.Contains(unknownVerb.Reason, "does not know") {
		t.Errorf("unknown verb access = %+v, want a refusal", unknownVerb)
	}
}

// A mode this package does not know must write nowhere, the same way an unknown
// tool tier is reachable from no mode on the MCP surface.
func TestDecideRefusesAnUnknownMode(t *testing.T) {
	scope := sandboxScope()
	scope.Mode = appv1.SessionMode("administrator")

	access := scope.Decide(Request{
		Verb: VerbCreate,
		Ref:  Ref{APIVersion: "v1", Kind: "ConfigMap", Namespace: sandboxNS, Name: "draft"},
	})

	if access.Allowed {
		t.Fatalf("access = %+v, want a refusal", access)
	}
}
