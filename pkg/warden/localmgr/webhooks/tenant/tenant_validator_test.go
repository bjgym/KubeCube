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

package tenant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	tenantv1 "github.com/kubecube-io/kubecube/pkg/apis/tenant/v1"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

const testTenantName = "t-verify"

func testTenant() *tenantv1.Tenant {
	return &tenantv1.Tenant{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testTenantName,
			Annotations: map[string]string{constants.SyncAnnotation: "1"},
			Finalizers:  []string{constants.TenantFinalizer},
		},
		Spec: tenantv1.TenantSpec{Namespace: constants.TenantNsPrefix + testTenantName},
	}
}

func newTestValidator(t *testing.T, isMember bool) *Validator {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := tenantv1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot register tenant types: %v", err)
	}

	return NewValidator(nil, isMember, admission.NewDecoder(scheme))
}

func rawTenant(t *testing.T, tenant *tenantv1.Tenant) runtime.RawExtension {
	t.Helper()

	tenant.TypeMeta = metav1.TypeMeta{APIVersion: "tenant.kubecube.io/v1", Kind: "Tenant"}
	raw, err := json.Marshal(tenant)
	if err != nil {
		t.Fatalf("cannot encode the tenant: %v", err)
	}

	return runtime.RawExtension{Raw: raw}
}

func deleteRequest(t *testing.T, tenant *tenantv1.Tenant) admission.Request {
	t.Helper()

	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Delete,
		Name:      tenant.Name,
		OldObject: rawTenant(t, tenant),
	}}
}

// This is the gate the force-delete annotation exists for: a member must not let
// its copy of a tenant be deleted directly, because the pivot owns that decision.
// The escape hatch is the same DELETE with kubecube.io/force-delete set, which is
// what the sync manager does when the pivot's object is gone. The annotation only
// ever admitted the request; the finalizer removal that lets the request finish
// lives in the member-side reconciler (pkg/warden/localmgr/controllers/tenant).
func TestMemberDeleteOfATenantWithoutTheForceDeleteAnnotationIsRefused(t *testing.T) {
	validator := newTestValidator(t, true)

	resp := validator.Handle(context.Background(), deleteRequest(t, testTenant()))
	if resp.Allowed {
		t.Fatal("a member allowed a tenant DELETE without the force-delete annotation")
	}
	if resp.Result == nil || !strings.Contains(resp.Result.Message, "member cluster does not allow deletion of tenant") {
		t.Fatalf("the refusal did not name the member-cluster policy: %v", resp.Result)
	}
}

func TestMemberDeleteOfATenantWithTheForceDeleteAnnotationIsAllowed(t *testing.T) {
	validator := newTestValidator(t, true)

	tenant := testTenant()
	tenant.Annotations[constants.ForceDeleteAnnotation] = "true"

	resp := validator.Handle(context.Background(), deleteRequest(t, tenant))
	if !resp.Allowed {
		t.Fatalf("the force-delete path was refused on a member: %v", resp.Result)
	}
}

func TestPivotDeleteOfATenantIsAllowed(t *testing.T) {
	validator := newTestValidator(t, false)

	resp := validator.Handle(context.Background(), deleteRequest(t, testTenant()))
	if !resp.Allowed {
		t.Fatalf("a pivot tenant DELETE was refused: %v", resp.Result)
	}
}

// Removing the finalizer is an UPDATE, and it is the only repair for a tenant
// that is already terminating. The member webhook must never deny it, or the
// deletion admitted above could still never finish.
func TestMemberUpdateThatRemovesTheFinalizerIsAllowed(t *testing.T) {
	validator := newTestValidator(t, true)

	oldTenant := testTenant()
	currentTenant := testTenant()
	currentTenant.Finalizers = nil

	resp := validator.Handle(context.Background(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Update,
		Name:      currentTenant.Name,
		Object:    rawTenant(t, currentTenant),
		OldObject: rawTenant(t, oldTenant),
	}})
	if !resp.Allowed {
		t.Fatalf("the finalizer removal on a member was denied: %v", resp.Result)
	}
}
