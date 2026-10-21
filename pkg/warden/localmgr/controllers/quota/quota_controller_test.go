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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	quotav1 "github.com/kubecube-io/kubecube/pkg/apis/quota/v1"
	quotapkg "github.com/kubecube-io/kubecube/pkg/quota"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

const (
	testParentQuotaName = "member-kubeadm-6002.t-verify"
	testQuotaName       = "member-kubeadm-6002.t-verify.p-verify.onboard-e2e"
	testQuotaNamespace  = "onboard-e2e"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot register core types: %v", err)
	}
	if err := quotav1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot register quota types: %v", err)
	}

	return scheme
}

// testDeletingQuota is the live object: a space ResourceQuota marked for
// deletion that still carries the warden finalizer.
func testDeletingQuota() *corev1.ResourceQuota {
	now := metav1.Now()

	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:              testQuotaName,
			Namespace:         testQuotaNamespace,
			Labels:            map[string]string{constants.CubeQuotaLabel: testParentQuotaName},
			Finalizers:        []string{quotapkg.Finalizer},
			DeletionTimestamp: &now,
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsStorage: resource.MustParse("10Gi"),
			},
		},
	}
}

func testQuotaParent() *quotav1.CubeResourceQuota {
	return &quotav1.CubeResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: testParentQuotaName},
		Spec: quotav1.CubeResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsStorage: resource.MustParse("100Gi"),
			},
		},
		Status: quotav1.CubeResourceQuotaStatus{
			Used: corev1.ResourceList{
				corev1.ResourceRequestsStorage: resource.MustParse("10Gi"),
			},
			SubResourceQuotas: []string{testQuotaName + "." + testQuotaNamespace + ".quota"},
		},
	}
}

func newTestReconciler(t *testing.T, localObjects, pivotObjects []client.Object) (*QuotaReconciler, client.Client, client.Client) {
	t.Helper()

	scheme := testScheme(t)
	localClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(localObjects...).Build()
	pivotClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&quotav1.CubeResourceQuota{}).
		WithObjects(pivotObjects...).
		Build()

	return &QuotaReconciler{Client: localClient, pivotClient: pivotClient, Scheme: scheme}, localClient, pivotClient
}

func assertFinalizerGone(t *testing.T, c client.Client, name, namespace string) {
	t.Helper()

	quotaObj := &corev1.ResourceQuota{}
	err := c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, quotaObj)
	if errors.IsNotFound(err) {
		// The fake client does not run the API server's final deletion, but if
		// it did the object would be gone, which is the same desired end state.
		return
	}
	if err != nil {
		t.Fatalf("cannot read the resourceQuota back: %v", err)
	}

	for _, finalizer := range quotaObj.Finalizers {
		if finalizer == quotapkg.Finalizer {
			t.Fatalf("the resourceQuota still carries the finalizer %q, so the namespace cannot terminate",
				quotapkg.Finalizer)
		}
	}
}

// Deleting a space removes its CubeResourceQuota and its ResourceQuota without
// ordering guarantees. When the parent is already absent, the finalizer must
// still be removed; otherwise the ResourceQuota keeps it forever and the
// namespace stays in Terminating.
func TestReconcileRemovesFinalizerWhenTheCubeResourceQuotaIsGone(t *testing.T) {
	quotaObj := testDeletingQuota()
	// The pivot cluster holds only the project quotas now; the space's parent
	// has already been deleted, which is what the live cluster showed.
	r, localClient, _ := newTestReconciler(t, []client.Object{quotaObj}, nil)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: quotaObj.Name, Namespace: quotaObj.Namespace},
	})
	if err != nil {
		t.Fatalf("the reconcile of a deleting resourceQuota failed: %v", err)
	}

	assertFinalizerGone(t, localClient, quotaObj.Name, quotaObj.Namespace)
}

// When the parent still exists the finalizer removal keeps flushing its status:
// the fix must not turn the flush into dead code.
func TestReconcileFlushesParentStatusWhenTheCubeResourceQuotaExists(t *testing.T) {
	quotaObj := testDeletingQuota()
	parent := testQuotaParent()
	r, localClient, pivotClient := newTestReconciler(t, []client.Object{quotaObj}, []client.Object{parent})

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: quotaObj.Name, Namespace: quotaObj.Namespace},
	})
	if err != nil {
		t.Fatalf("the reconcile of a deleting resourceQuota failed: %v", err)
	}

	assertFinalizerGone(t, localClient, quotaObj.Name, quotaObj.Namespace)

	updated := &quotav1.CubeResourceQuota{}
	if err := pivotClient.Get(context.Background(), types.NamespacedName{Name: parent.Name}, updated); err != nil {
		t.Fatalf("cannot read the parent quota back: %v", err)
	}
	if len(updated.Status.SubResourceQuotas) != 0 {
		t.Errorf("the parent still lists the deleted sub quota: %v", updated.Status.SubResourceQuotas)
	}
}
