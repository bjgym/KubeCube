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
)

const (
	testCubeQuotaName       = "p1-quota"
	testCubeParentQuotaName = "t1-quota"
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

func testDeletingCubeQuota() *quotav1.CubeResourceQuota {
	now := metav1.Now()

	return &quotav1.CubeResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:              testCubeQuotaName,
			Finalizers:        []string{quotapkg.Finalizer},
			DeletionTimestamp: &now,
		},
		Spec: quotav1.CubeResourceQuotaSpec{
			ParentQuota: testCubeParentQuotaName,
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU: resource.MustParse("1"),
			},
		},
	}
}

func testCubeQuotaParent() *quotav1.CubeResourceQuota {
	return &quotav1.CubeResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: testCubeParentQuotaName},
		Spec: quotav1.CubeResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU: resource.MustParse("10"),
			},
		},
		Status: quotav1.CubeResourceQuotaStatus{
			Used: corev1.ResourceList{
				corev1.ResourceRequestsCPU: resource.MustParse("1"),
			},
			SubResourceQuotas: []string{testCubeQuotaName + ".quota"},
		},
	}
}

func newTestReconciler(t *testing.T, objects ...client.Object) (*CubeResourceQuotaReconciler, client.Client) {
	t.Helper()

	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&quotav1.CubeResourceQuota{}).
		WithObjects(objects...).
		Build()

	return &CubeResourceQuotaReconciler{Client: fakeClient, Scheme: scheme}, fakeClient
}

func assertFinalizerGone(t *testing.T, c client.Client, name string) {
	t.Helper()

	cubeQuota := &quotav1.CubeResourceQuota{}
	err := c.Get(context.Background(), types.NamespacedName{Name: name}, cubeQuota)
	if errors.IsNotFound(err) {
		return
	}
	if err != nil {
		t.Fatalf("cannot read the cubeResourceQuota back: %v", err)
	}

	for _, finalizer := range cubeQuota.Finalizers {
		if finalizer == quotapkg.Finalizer {
			t.Fatalf("the cubeResourceQuota still carries the finalizer %q and cannot terminate",
				quotapkg.Finalizer)
		}
	}
}

// A CubeResourceQuota can lose its parent before it is removed itself. The
// finalizer guards its own deletion, so it has to go even when the parent
// lookup fails.
func TestReconcileRemovesFinalizerWhenTheParentCubeResourceQuotaIsGone(t *testing.T) {
	cubeQuota := testDeletingCubeQuota()
	r, fakeClient := newTestReconciler(t, cubeQuota)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cubeQuota.Name},
	})
	if err != nil {
		t.Fatalf("the reconcile of a deleting cubeResourceQuota failed: %v", err)
	}

	assertFinalizerGone(t, fakeClient, cubeQuota.Name)
}

// The parent flush still runs while the parent exists.
func TestReconcileFlushesParentStatusWhenTheParentCubeResourceQuotaExists(t *testing.T) {
	cubeQuota := testDeletingCubeQuota()
	parent := testCubeQuotaParent()
	r, fakeClient := newTestReconciler(t, cubeQuota, parent)

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cubeQuota.Name},
	})
	if err != nil {
		t.Fatalf("the reconcile of a deleting cubeResourceQuota failed: %v", err)
	}

	assertFinalizerGone(t, fakeClient, cubeQuota.Name)

	updated := &quotav1.CubeResourceQuota{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: parent.Name}, updated); err != nil {
		t.Fatalf("cannot read the parent quota back: %v", err)
	}
	if len(updated.Status.SubResourceQuotas) != 0 {
		t.Errorf("the parent still lists the deleted sub quota: %v", updated.Status.SubResourceQuotas)
	}
}
