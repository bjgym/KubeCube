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

package quota

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	quotav1 "github.com/kubecube-io/kubecube/pkg/apis/quota/v1"
	quotapkg "github.com/kubecube-io/kubecube/pkg/quota"
)

const (
	testParentQuotaName = "t1-quota"
	testQuotaName       = "p1-quota"
)

func testParentQuota() *quotav1.CubeResourceQuota {
	return &quotav1.CubeResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: testParentQuotaName},
		Spec: quotav1.CubeResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU: resource.MustParse("2"),
			},
		},
		Status: quotav1.CubeResourceQuotaStatus{
			Used: corev1.ResourceList{
				corev1.ResourceRequestsCPU: resource.MustParse("1"),
			},
		},
	}
}

func testQuota() *quotav1.CubeResourceQuota {
	return &quotav1.CubeResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: testQuotaName},
		Spec: quotav1.CubeResourceQuotaSpec{
			ParentQuota: testParentQuotaName,
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU: resource.MustParse("1"),
			},
		},
	}
}

func newTestValidator(t *testing.T, objects ...client.Object) *CubeResourceQuotaValidator {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot register core types: %v", err)
	}
	if err := quotav1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot register quota types: %v", err)
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()

	return NewCubeResourceQuotaValidator(fakeClient, admission.NewDecoder(scheme))
}

func rawExtension(t *testing.T, obj *quotav1.CubeResourceQuota) runtime.RawExtension {
	t.Helper()

	obj.TypeMeta = metav1.TypeMeta{APIVersion: quotav1.GroupVersion.String(), Kind: "CubeResourceQuota"}
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("cannot encode the cubeResourceQuota: %v", err)
	}

	return runtime.RawExtension{Raw: raw}
}

func updateRequest(t *testing.T, oldQuota, currentQuota *quotav1.CubeResourceQuota) admission.Request {
	t.Helper()

	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Update,
		Name:      currentQuota.Name,
		Object:    rawExtension(t, currentQuota),
		OldObject: rawExtension(t, oldQuota),
	}}
}

// The finalizer of a CubeResourceQuota is removed with an update of an object
// that is already being deleted. When the parent is gone the quota comparison
// errors out, so the update has to be admitted before it — otherwise this
// object, and whatever blocks on it, can never finish terminating.
func TestUpdateOfADeletingCubeResourceQuotaIsAllowed(t *testing.T) {
	validator := newTestValidator(t)

	oldQuota := testQuota()
	currentQuota := testQuota()
	now := metav1.Now()
	currentQuota.DeletionTimestamp = &now

	resp := validator.Handle(context.Background(), updateRequest(t, oldQuota, currentQuota))
	if !resp.Allowed {
		t.Fatalf("an update of a CubeResourceQuota being deleted was denied: %v", resp.Result)
	}
}

func TestFinalizerOnlyUpdateOfACubeResourceQuotaIsAllowed(t *testing.T) {
	validator := newTestValidator(t)

	oldQuota := testQuota()
	oldQuota.Finalizers = []string{quotapkg.Finalizer}
	currentQuota := testQuota()

	resp := validator.Handle(context.Background(), updateRequest(t, oldQuota, currentQuota))
	if !resp.Allowed {
		t.Fatalf("an update that only removes the finalizer was denied: %v", resp.Result)
	}
}

// The deletion allowance must not let a live CubeResourceQuota grow past its
// parent.
func TestOverQuotaUpdateOfACubeResourceQuotaIsStillDenied(t *testing.T) {
	validator := newTestValidator(t, testParentQuota())

	oldQuota := testQuota()
	currentQuota := oldQuota.DeepCopy()
	currentQuota.Spec.Hard[corev1.ResourceRequestsCPU] = resource.MustParse("5")

	resp := validator.Handle(context.Background(), updateRequest(t, oldQuota, currentQuota))
	if resp.Allowed {
		t.Fatal("an update that exceeds the parent quota was allowed")
	}
}
