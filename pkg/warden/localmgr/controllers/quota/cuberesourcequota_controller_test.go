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
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	quotav1 "github.com/kubecube-io/kubecube/pkg/apis/quota/v1"
	quotapkg "github.com/kubecube-io/kubecube/pkg/quota"
)

// testStuckMemberQuotaName is the name from the live member cluster: the
// tenant's quota had already been torn down on the pivot, while this copy was
// still deleting four days later.
const testStuckMemberQuotaName = "member-kubeadm-6002.t-verify"

// testStuckMemberQuota is the object the sync manager left behind on the
// member: a CubeResourceQuota deleting, still carrying the pivot's finalizer,
// with used and subResourceQuotas empty because nothing reconciled it there.
func testStuckMemberQuota() *quotav1.CubeResourceQuota {
	now := metav1.Now()

	return &quotav1.CubeResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:              testStuckMemberQuotaName,
			Finalizers:        []string{quotapkg.Finalizer},
			DeletionTimestamp: &now,
		},
		Spec: quotav1.CubeResourceQuotaSpec{
			ParentQuota: "t-verify-quota",
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU: resource.MustParse("1"),
			},
		},
	}
}

// testSyncedMemberQuota is a fresh member-side object, as the sync manager
// would create it once the name is free again.
func testSyncedMemberQuota() *quotav1.CubeResourceQuota {
	return &quotav1.CubeResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: testStuckMemberQuotaName},
		Spec: quotav1.CubeResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU: resource.MustParse("1"),
			},
		},
	}
}

func newTestMemberReconciler(t *testing.T, objects ...client.Object) (*CubeResourceQuotaReconciler, client.Client) {
	t.Helper()

	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()

	return &CubeResourceQuotaReconciler{Client: fakeClient, Scheme: scheme}, fakeClient
}

func assertCubeQuotaFinalizerGone(t *testing.T, c client.Client, name string) {
	t.Helper()

	cubeQuota := &quotav1.CubeResourceQuota{}
	err := c.Get(context.Background(), types.NamespacedName{Name: name}, cubeQuota)
	if errors.IsNotFound(err) {
		// Dropping the last finalizer of a deleting object ends it; the fake
		// client performs that removal, and on a real server it is the same
		// desired end state.
		return
	}
	if err != nil {
		t.Fatalf("cannot read the cubeResourceQuota back: %v", err)
	}

	for _, finalizer := range cubeQuota.Finalizers {
		if finalizer == quotapkg.Finalizer {
			// Error, not Fatal: the test also reports whether the name is free,
			// and the live defect showed both at once.
			t.Errorf("the cubeResourceQuota still carries the finalizer %q, so it stays in Terminating and the name stays taken",
				quotapkg.Finalizer)
		}
	}
}

// Deleting a tenant or space on the pivot removes the parent state the member
// could consult. The member's copy must still lose the finalizer on its own,
// and the name must then be free for the sync manager's next create; otherwise
// the object is stuck forever and the tenant's quota can never be re-established.
func TestMemberReconcileRemovesFinalizerFromADeletingCubeResourceQuota(t *testing.T) {
	cubeQuota := testStuckMemberQuota()
	r, fakeClient := newTestMemberReconciler(t, cubeQuota)

	request := ctrl.Request{NamespacedName: types.NamespacedName{Name: cubeQuota.Name}}
	if _, err := r.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("the reconcile of a deleting cubeResourceQuota failed: %v", err)
	}

	assertCubeQuotaFinalizerGone(t, fakeClient, cubeQuota.Name)

	// The live symptom: while the terminating object held the name, the sync
	// manager's create kept returning AlreadyExists ("object is being deleted: ...
	// already exists"). Once the finalizer is gone the name must be free.
	if err := fakeClient.Create(context.Background(), testSyncedMemberQuota()); err != nil {
		t.Errorf("the sync manager cannot create the cubeResourceQuota again: %v", err)
	}

	// Every pass must be safe: the object is gone now, so a repeated reconcile
	// of the same request is a no-op that returns no error.
	if _, err := r.Reconcile(context.Background(), request); err != nil {
		t.Errorf("an idempotent reconcile of the removed cubeResourceQuota failed: %v", err)
	}
}

// The member must not add the finalizer itself. It would then own the deletion
// of a finalizer the pivot never added, so a pivot-side repair could no longer
// free the object and this controller would be the only thing standing between
// a pivot cleanup and a member left in Terminating.
func TestMemberReconcileLeavesALiveCubeResourceQuotaAlone(t *testing.T) {
	cubeQuota := testSyncedMemberQuota()
	r, fakeClient := newTestMemberReconciler(t, cubeQuota)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cubeQuota.Name},
	}); err != nil {
		t.Fatalf("the reconcile of a live cubeResourceQuota failed: %v", err)
	}

	stored := &quotav1.CubeResourceQuota{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: cubeQuota.Name}, stored); err != nil {
		t.Fatalf("cannot read the cubeResourceQuota back: %v", err)
	}
	if len(stored.Finalizers) != 0 {
		t.Fatalf("a member-side reconcile added finalizers to a live cubeResourceQuota: %v", stored.Finalizers)
	}
}
