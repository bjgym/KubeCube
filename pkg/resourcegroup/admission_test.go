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
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func request(operation admissionv1.Operation, subresource, namespace, name string) admission.Request {
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation:   operation,
		SubResource: subresource,
		Namespace:   namespace,
		Name:        name,
		Kind:        metav1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
	}}
}

func TestRequestOf(t *testing.T) {
	tests := []struct {
		name string
		req  admission.Request
		verb Verb
	}{
		{
			name: "a create",
			req:  request(admissionv1.Create, "", groupNS, "web"),
			verb: VerbCreate,
		},
		{
			name: "an update",
			req:  request(admissionv1.Update, "", groupNS, "web"),
			verb: VerbUpdate,
		},
		{
			name: "a status update is an update",
			req:  request(admissionv1.Update, "status", groupNS, "web"),
			verb: VerbUpdate,
		},
		{
			name: "a delete",
			req:  request(admissionv1.Delete, "", groupNS, "web"),
			verb: VerbDelete,
		},
		{
			name: "an exec",
			req:  request(admissionv1.Connect, "exec", groupNS, "web-6d4f-x2k"),
			verb: VerbExec,
		},
		{
			name: "an attach",
			req:  request(admissionv1.Connect, "attach", groupNS, "web-6d4f-x2k"),
			verb: VerbAttach,
		},
		{
			name: "a port-forward",
			req:  request(admissionv1.Connect, "portforward", groupNS, "web-6d4f-x2k"),
			verb: VerbPortForward,
		},
		{
			name: "a log read",
			req:  request(admissionv1.Connect, "log", groupNS, "web-6d4f-x2k"),
			verb: VerbLogs,
		},
		{
			name: "a scale",
			req:  request(admissionv1.Update, "scale", groupNS, "web"),
			verb: VerbScale,
		},
		{
			name: "an eviction",
			req:  request(admissionv1.Create, "eviction", groupNS, "web-6d4f-x2k"),
			verb: VerbEvict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RequestOf(tt.req)
			if err != nil {
				t.Fatalf("RequestOf: %v", err)
			}
			if got.Verb != tt.verb {
				t.Errorf("verb = %q, want %q", got.Verb, tt.verb)
			}
			if got.Ref.Namespace != tt.req.Namespace || got.Ref.Kind != tt.req.Kind.Kind {
				t.Errorf("ref = %+v, want the request's object", got.Ref)
			}
		})
	}
}

func TestRequestOfReadsTheNameFromACreate(t *testing.T) {
	req := request(admissionv1.Create, "", groupNS, "")
	req.Object = runtime.RawExtension{Raw: []byte(`{"metadata":{"name":"web","namespace":"` + groupNS + `"}}`)}

	got, err := RequestOf(req)
	if err != nil {
		t.Fatalf("RequestOf: %v", err)
	}
	if got.Ref.Name != "web" {
		t.Errorf("name = %q, want the name the object carries", got.Ref.Name)
	}
}

func TestRequestOfRefusesWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name string
		req  admission.Request
	}{
		{
			name: "a create that carries no object",
			req:  request(admissionv1.Create, "", groupNS, ""),
		},
		{
			name: "a create whose object has no name",
			req: func() admission.Request {
				r := request(admissionv1.Create, "", groupNS, "")
				r.Object = runtime.RawExtension{Raw: []byte(`{"metadata":{"namespace":"x"}}`)}
				return r
			}(),
		},
		{
			name: "a connect to a subresource this rule does not know",
			req:  request(admissionv1.Connect, "proxy", groupNS, "web"),
		},
		{
			name: "an operation this rule does not know",
			req:  request(admissionv1.Operation("REPLACE"), "", groupNS, "web"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := RequestOf(tt.req); err == nil {
				t.Fatalf("RequestOf = %+v, want a refusal", got)
			}
		})
	}
}

// The backstop and the API layer have to reach the same answer from different
// inputs: this is the cluster-side half of that pairing.
func TestAdmissionRequestMeetsTheSameScopeRule(t *testing.T) {
	scope := sandboxScope()

	// An exec into a production member is refused even though the pod is a
	// member: reaching into a running container is a write, and a write outside
	// the sandbox is refused.
	execIntoMember := request(admissionv1.Connect, "exec", groupNS, "web-6d4f-x2k")
	execIntoMember.Kind = metav1.GroupVersionKind{Version: "v1", Kind: "Pod"}

	req, err := RequestOf(execIntoMember)
	if err != nil {
		t.Fatalf("RequestOf: %v", err)
	}
	if access := scope.Decide(req); access.Allowed {
		t.Errorf("access = %+v, want the exec refused", access)
	}

	// The same exec inside the session's sandbox is allowed.
	inSandbox := request(admissionv1.Connect, "exec", sandboxNS, "draft")
	inSandbox.Kind = metav1.GroupVersionKind{Version: "v1", Kind: "Pod"}

	req, err = RequestOf(inSandbox)
	if err != nil {
		t.Fatalf("RequestOf: %v", err)
	}
	if access := scope.Decide(req); !access.Allowed {
		t.Errorf("access = %+v, want the exec in the sandbox allowed", access)
	}
}
