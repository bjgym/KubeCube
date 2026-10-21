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
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

const (
	testParentName = "c1.t1"
	testQuotaName  = "c1.t1.p1.space-a"
	testQuotaNs    = "space-a"
)

// testParentQuota has a storage limit the space quota below does not set, which
// is the shape that made the live webhook answer "less resource(requests.storage)
// but parent quota had ..." to a finalizer removal.
func testParentQuota() *quotav1.CubeResourceQuota {
	return &quotav1.CubeResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: testParentName},
		Spec: quotav1.CubeResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU:     resource.MustParse("2"),
				corev1.ResourceRequestsStorage: resource.MustParse("10Gi"),
			},
		},
		Status: quotav1.CubeResourceQuotaStatus{
			Used: corev1.ResourceList{
				corev1.ResourceRequestsCPU:     resource.MustParse("1"),
				corev1.ResourceRequestsStorage: resource.MustParse("1Gi"),
			},
		},
	}
}

func testQuota() *corev1.ResourceQuota {
	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testQuotaName,
			Namespace: testQuotaNs,
			Labels:    map[string]string{constants.CubeQuotaLabel: testParentName},
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU: resource.MustParse("1"),
			},
		},
	}
}

func newTestValidator(t *testing.T, objects ...client.Object) *ResourceQuotaValidator {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot register core types: %v", err)
	}
	if err := quotav1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot register quota types: %v", err)
	}

	pivotClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	localClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	return NewValidator(pivotClient, localClient, admission.NewDecoder(scheme))
}

func rawExtension(t *testing.T, obj *corev1.ResourceQuota) runtime.RawExtension {
	t.Helper()

	obj.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "ResourceQuota"}
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("cannot encode the resourceQuota: %v", err)
	}

	return runtime.RawExtension{Raw: raw}
}

func updateRequest(t *testing.T, oldQuota, currentQuota *corev1.ResourceQuota) admission.Request {
	t.Helper()

	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Update,
		Name:      currentQuota.Name,
		Namespace: currentQuota.Namespace,
		Object:    rawExtension(t, currentQuota),
		OldObject: rawExtension(t, oldQuota),
	}}
}

// Removing the finalizer of a ResourceQuota whose deletion has begun must be
// admitted even though the same spec would fail the parent quota comparison.
// The namespace cannot finish terminating while this finalizer exists, so a
// denial here leaves a state no client can repair.
func TestUpdateOfADeletingResourceQuotaIsAllowed(t *testing.T) {
	validator := newTestValidator(t, testParentQuota())

	oldQuota := testQuota()
	currentQuota := testQuota()
	now := metav1.Now()
	currentQuota.DeletionTimestamp = &now

	resp := validator.Handle(context.Background(), updateRequest(t, oldQuota, currentQuota))
	if !resp.Allowed {
		t.Fatalf("an update of a ResourceQuota being deleted was denied: %v", resp.Result)
	}
}

// A finalizer removal is an update even when the object has no deletion
// timestamp yet (for example a manual repair), and it changes nothing but
// metadata.finalizers, so it must be admitted.
func TestFinalizerOnlyUpdateIsAllowed(t *testing.T) {
	validator := newTestValidator(t, testParentQuota())

	oldQuota := testQuota()
	oldQuota.Finalizers = []string{quotapkg.Finalizer}
	currentQuota := testQuota()

	resp := validator.Handle(context.Background(), updateRequest(t, oldQuota, currentQuota))
	if !resp.Allowed {
		t.Fatalf("an update that only removes the finalizer was denied: %v", resp.Result)
	}
}

// The allowance is for the repair, not a hole: raising the quota above the
// parent's remaining capacity must still be denied.
func TestOverQuotaUpdateIsStillDenied(t *testing.T) {
	validator := newTestValidator(t, testParentQuota())

	oldQuota := testQuota()
	oldQuota.Spec.Hard[corev1.ResourceRequestsStorage] = resource.MustParse("1Gi")
	currentQuota := oldQuota.DeepCopy()
	currentQuota.Spec.Hard[corev1.ResourceRequestsCPU] = resource.MustParse("5")

	resp := validator.Handle(context.Background(), updateRequest(t, oldQuota, currentQuota))
	if resp.Allowed {
		t.Fatal("an update that exceeds the parent quota was allowed")
	}
	if resp.Result == nil {
		t.Fatal("the over-quota update was not denied by the quota check")
	}
}

func TestDeleteOfAResourceQuotaIsAllowed(t *testing.T) {
	validator := newTestValidator(t, testParentQuota())

	oldQuota := testQuota()
	resp := validator.Handle(context.Background(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Delete,
		Name:      oldQuota.Name,
		Namespace: oldQuota.Namespace,
		OldObject: rawExtension(t, oldQuota),
	}})
	if !resp.Allowed {
		t.Fatalf("a DELETE of a ResourceQuota was denied: %v", resp.Result)
	}
}
