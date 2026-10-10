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
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
	"github.com/kubecube-io/kubecube/pkg/resourcegroup"
)

const (
	chainGroupNS   = "kubecube-project-demo-space1"
	chainGroupUID  = "8f2c-4d91"
	chainSandboxNS = "kubecube-agent-sess-7f3a"
)

func chainObject(apiVersion, kind, namespace, name string, labels map[string]string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
	}}
	if len(labels) > 0 {
		set := make(map[string]interface{}, len(labels))
		for k, v := range labels {
			set[k] = v
		}
		_ = unstructured.SetNestedMap(object.Object, set, "metadata", "labels")
	}
	return object
}

func own(child *unstructured.Unstructured, apiVersion, kind, name string) {
	_ = unstructured.SetNestedSlice(child.Object, []interface{}{
		map[string]interface{}{"apiVersion": apiVersion, "kind": kind, "name": name, "uid": name + "-uid"},
	}, "metadata", "ownerReferences")
}

// The whole chain, in the order a request meets it: the platform issues a token
// for a session it recorded, the surface verifies and resolves it, the scope
// gate narrows what the session may ask for, and the platform's rules decide
// what it may reach — the same rules whether the request arrives as a route or
// as an admission request.
func TestTheAgentScopeChain(t *testing.T) {
	session := readyAgentSession()
	session.Spec.Tools = appv1Tools()

	established := establishedFrom(t, session)

	resolved, err := Resolve(session, established)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The surface's gate: a granted tool passes, an ungranted one does not.
	if err := resolved.Authorize(mustTool(t, "describe_group")); err != nil {
		t.Errorf("Authorize(describe_group) = %v, want it granted", err)
	}
	if err := resolved.Authorize(mustTool(t, "get_logs")); err == nil {
		t.Error("Authorize(get_logs) = nil, want a refusal")
	}

	// The platform's membership, from the objects it observed.
	deployment := chainObject("apps/v1", "Deployment", chainGroupNS, "web", map[string]string{resourcegroup.Label: chainGroupUID})
	replicaset := chainObject("apps/v1", "ReplicaSet", chainGroupNS, "web-6d4f", nil)
	pod := chainObject("v1", "Pod", chainGroupNS, "web-6d4f-x2k", nil)
	secret := chainObject("v1", "Secret", chainGroupNS, "credentials", map[string]string{resourcegroup.Label: chainGroupUID})
	unrelated := chainObject("v1", "ConfigMap", chainGroupNS, "someone-elses", nil)
	own(replicaset, "apps/v1", "Deployment", "web")
	own(pod, "apps/v1", "ReplicaSet", "web-6d4f")

	inventory := resourcegroup.TakeInventory(
		[]*unstructured.Unstructured{deployment, replicaset, pod, secret, unrelated},
		chainGroupUID,
	)

	scope := resourcegroup.Scope{
		Session:          resolved.UID,
		GroupUID:         resolved.GroupUID,
		GroupNamespace:   chainGroupNS,
		SandboxNamespace: session.Status.SandboxNamespace,
		Mode:             resolved.Mode,
		Members:          inventory.Members(),
	}

	member := resourcegroup.Ref{APIVersion: "apps/v1", Kind: "Deployment", Namespace: chainGroupNS, Name: "web"}
	derived := resourcegroup.Ref{APIVersion: "v1", Kind: "Pod", Namespace: chainGroupNS, Name: "web-6d4f-x2k"}
	credentials := resourcegroup.Ref{APIVersion: "v1", Kind: "Secret", Namespace: chainGroupNS, Name: "credentials"}
	elsewhere := resourcegroup.Ref{APIVersion: "v1", Kind: "ConfigMap", Namespace: chainGroupNS, Name: "someone-elses"}
	draft := resourcegroup.Ref{APIVersion: "v1", Kind: "ConfigMap", Namespace: chainSandboxNS, Name: "draft"}

	tests := []struct {
		name    string
		request resourcegroup.Request
		allowed bool
	}{
		{name: "reading a declared member", request: resourcegroup.Request{Verb: resourcegroup.VerbGet, Ref: member}, allowed: true},
		{name: "reading a derived member", request: resourcegroup.Request{Verb: resourcegroup.VerbGet, Ref: derived}, allowed: true},
		{name: "reading an object nobody declared", request: resourcegroup.Request{Verb: resourcegroup.VerbGet, Ref: elsewhere}},
		{name: "reading a Secret member", request: resourcegroup.Request{Verb: resourcegroup.VerbGet, Ref: credentials}},
		{name: "reading the logs of a member", request: resourcegroup.Request{Verb: resourcegroup.VerbLogs, Ref: derived}, allowed: true},
		{name: "writing inside the sandbox", request: resourcegroup.Request{Verb: resourcegroup.VerbCreate, Ref: draft}, allowed: true},
		{name: "writing a member", request: resourcegroup.Request{Verb: resourcegroup.VerbPatch, Ref: member}},
		{name: "exec into a member", request: resourcegroup.Request{Verb: resourcegroup.VerbExec, Ref: derived}},
		{name: "exec inside the sandbox", request: resourcegroup.Request{
			Verb: resourcegroup.VerbExec,
			Ref:  resourcegroup.Ref{APIVersion: "v1", Kind: "Pod", Namespace: chainSandboxNS, Name: "draft"}},
			allowed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := scope.Decide(tt.request)
			if !access.Decided {
				t.Fatalf("access = %+v, want a decision", access)
			}
			if access.Allowed != tt.allowed {
				t.Fatalf("allowed = %v (%s), want %v", access.Allowed, access.Reason, tt.allowed)
			}
		})
	}

	// The cluster-side backstop reaches the same answer from an admission
	// request: an exec into a member is refused, the same exec in the sandbox is
	// allowed.
	execIntoMember := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation:   admissionv1.Connect,
		SubResource: "exec",
		Namespace:   chainGroupNS,
		Name:        "web-6d4f-x2k",
		Kind:        metav1.GroupVersionKind{Version: "v1", Kind: "Pod"},
	}}
	request, err := resourcegroup.RequestOf(execIntoMember)
	if err != nil {
		t.Fatalf("RequestOf: %v", err)
	}
	if access := scope.Decide(request); access.Allowed {
		t.Errorf("access = %+v, want the exec into a member refused at admission too", access)
	}
}

func appv1Tools() []appv1.ToolName {
	return []appv1.ToolName{appv1.ToolDescribeGroup, appv1.ToolSandboxApply}
}
