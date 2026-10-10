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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func sandboxNamespace() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: sandboxNS,
			UID:  types.UID("3d91-77aa"),
			Labels: map[string]string{
				SandboxLabel: "sess-7f3a",
			},
		},
	}
}

func TestSandboxOf(t *testing.T) {
	sandbox, err := SandboxOf(sandboxNamespace())
	if err != nil {
		t.Fatalf("SandboxOf: %v", err)
	}

	if sandbox.Name != sandboxNS || sandbox.UID != "3d91-77aa" || sandbox.SessionUID != "sess-7f3a" {
		t.Errorf("sandbox = %+v, want the namespace's facts", sandbox)
	}
}

// A namespace without the marker is not a sandbox. Reading it as "nothing to
// restrict" would leave the writes that most need checking unguarded.
func TestSandboxOfRefusesANamespaceThatIsNotASandbox(t *testing.T) {
	plain := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kubecube-project-demo-space1"}}

	if sandbox, err := SandboxOf(plain); err == nil {
		t.Fatalf("SandboxOf = %+v, want a refusal", sandbox)
	}
	if _, err := SandboxOf(nil); err == nil {
		t.Fatal("SandboxOf(nil) = nil, want a refusal")
	}
}

func TestBackstopAllowsAnOrdinaryWrite(t *testing.T) {
	sandbox, err := SandboxOf(sandboxNamespace())
	if err != nil {
		t.Fatalf("SandboxOf: %v", err)
	}
	backstop := Backstop{Sandbox: sandbox}

	draft := obj("apps/v1", "Deployment", sandboxNS, "web", nil)
	request := Request{Verb: VerbCreate, Ref: RefOf(draft)}

	if err := backstop.Decide(request, nil, draft); err != nil {
		t.Fatalf("Decide = %v, want the write allowed", err)
	}
}

// The one thing the cluster refuses on its own: a session putting an object
// into a group. The API layer allows a write inside the sandbox, so this is the
// layer that has to say no.
func TestBackstopRefusesMembership(t *testing.T) {
	sandbox, err := SandboxOf(sandboxNamespace())
	if err != nil {
		t.Fatalf("SandboxOf: %v", err)
	}
	backstop := Backstop{Sandbox: sandbox}

	plain := obj("apps/v1", "Deployment", sandboxNS, "web", nil)
	labelled := obj("apps/v1", "Deployment", sandboxNS, "web", map[string]string{Label: groupUID})
	other := obj("apps/v1", "Deployment", sandboxNS, "web", map[string]string{Label: "another-group-uid"})

	tests := []struct {
		name string
		verb Verb
		old  *unstructured.Unstructured
		new  *unstructured.Unstructured
		want string
	}{
		{
			name: "a create that puts the object in a group",
			verb: VerbCreate, new: labelled,
			want: "put the object in group",
		},
		{
			name: "an update that puts the object in a group",
			verb: VerbUpdate, old: plain, new: labelled,
			want: "put the object in group",
		},
		{
			name: "an update that takes the object out of its group",
			verb: VerbUpdate, old: labelled, new: plain,
			want: "take the object out of group",
		},
		{
			name: "an update that moves the object to another group",
			verb: VerbUpdate, old: labelled, new: other,
			want: "move the object from group",
		},
		{
			name: "a patch that arrives with the object as it would be",
			verb: VerbPatch, old: plain, new: labelled,
			want: "put the object in group",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := Request{Verb: tt.verb, Ref: RefOf(tt.new)}

			err := backstop.Decide(request, tt.old, tt.new)
			if err == nil {
				t.Fatal("Decide = nil, want a refusal")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to say %q", err.Error(), tt.want)
			}
			if !strings.Contains(err.Error(), "a person declares membership") {
				t.Errorf("error = %q, want it to say who may declare membership", err.Error())
			}
		})
	}
}

// A write that leaves membership exactly as it was is not a declaration, so an
// object that somehow carries a label is not wedged.
func TestBackstopAllowsAWriteThatLeavesMembershipAlone(t *testing.T) {
	sandbox, err := SandboxOf(sandboxNamespace())
	if err != nil {
		t.Fatalf("SandboxOf: %v", err)
	}
	backstop := Backstop{Sandbox: sandbox}

	labelled := obj("apps/v1", "Deployment", sandboxNS, "web", map[string]string{Label: groupUID})
	alsoLabelled := obj("apps/v1", "Deployment", sandboxNS, "web", map[string]string{Label: groupUID, "app": "web"})

	request := Request{Verb: VerbUpdate, Ref: RefOf(alsoLabelled)}
	if err := backstop.Decide(request, labelled, alsoLabelled); err != nil {
		t.Fatalf("Decide = %v, want the write allowed", err)
	}
}

func TestBackstopAllowsADelete(t *testing.T) {
	sandbox, err := SandboxOf(sandboxNamespace())
	if err != nil {
		t.Fatalf("SandboxOf: %v", err)
	}
	backstop := Backstop{Sandbox: sandbox}

	labelled := obj("apps/v1", "Deployment", sandboxNS, "web", map[string]string{Label: groupUID})
	request := Request{Verb: VerbDelete, Ref: RefOf(labelled)}

	if err := backstop.Decide(request, labelled, nil); err != nil {
		t.Fatalf("Decide = %v, want the delete allowed", err)
	}
}

func TestBackstopRefusesWhatItCannotScope(t *testing.T) {
	sandbox, err := SandboxOf(sandboxNamespace())
	if err != nil {
		t.Fatalf("SandboxOf: %v", err)
	}

	tests := []struct {
		name     string
		backstop Backstop
		request  Request
		want     string
	}{
		{
			name:     "a cluster-scoped write",
			backstop: Backstop{Sandbox: sandbox},
			request: Request{Verb: VerbUpdate, Ref: Ref{
				APIVersion: "v1", Kind: "Namespace", Name: sandboxNS}},
			want: "cluster-scoped",
		},
		{
			name:     "a write in another namespace",
			backstop: Backstop{Sandbox: sandbox},
			request: Request{Verb: VerbCreate, Ref: Ref{
				APIVersion: "v1", Kind: "ConfigMap", Namespace: "another-space", Name: "draft"}},
			want: "outside the sandbox",
		},
		{
			name:     "a rule built without a sandbox",
			backstop: Backstop{},
			request: Request{Verb: VerbCreate, Ref: Ref{
				APIVersion: "v1", Kind: "ConfigMap", Namespace: sandboxNS, Name: "draft"}},
			want: "without a sandbox namespace",
		},
		{
			name:     "a sandbox that names no session",
			backstop: Backstop{Sandbox: Sandbox{Name: sandboxNS, UID: "3d91-77aa"}},
			request: Request{Verb: VerbCreate, Ref: Ref{
				APIVersion: "v1", Kind: "ConfigMap", Namespace: sandboxNS, Name: "draft"}},
			want: "names no session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.backstop.Decide(tt.request, nil, nil)
			if err == nil {
				t.Fatal("Decide = nil, want a refusal")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to say %q", err.Error(), tt.want)
			}
		})
	}
}

// The two layers answer different questions, and this is the gap between them:
// the scope rule allows any write inside the sandbox, and the cluster refuses
// the one write that would change who owns what.
func TestTheScopeRuleAndTheBackstopTogether(t *testing.T) {
	scope := sandboxScope()

	draft := obj("apps/v1", "Deployment", sandboxNS, "web", nil)
	request := Request{Verb: VerbCreate, Ref: RefOf(draft)}

	if access := scope.Decide(request); !access.Allowed {
		t.Fatalf("the scope rule refused an ordinary write in the sandbox: %s", access.Reason)
	}

	labelled := obj("apps/v1", "Deployment", sandboxNS, "web", map[string]string{Label: groupUID})
	request = Request{Verb: VerbCreate, Ref: RefOf(labelled)}

	if access := scope.Decide(request); !access.Allowed {
		t.Fatalf("the scope rule refused a write in the sandbox: %s", access.Reason)
	}

	sandbox, err := SandboxOf(sandboxNamespace())
	if err != nil {
		t.Fatalf("SandboxOf: %v", err)
	}
	if err := (Backstop{Sandbox: sandbox}).Decide(request, nil, labelled); err == nil {
		t.Fatal("the cluster allowed a session to declare membership")
	}
}
