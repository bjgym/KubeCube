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

package controllers

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	tenantv1 "github.com/kubecube-io/kubecube/pkg/apis/tenant/v1"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

const (
	// testTenantName and testProjectName are the objects from the live member
	// cluster: the tenant had already been torn down on the pivot, while this
	// copy was still deleting six days later.
	testTenantName  = "t-verify"
	testProjectName = "p-verify"
)

// testStuckMemberTenant is the live object: a Tenant whose pivot object is gone,
// still deleting, still carrying the pivot's finalizer, and annotated with the
// force-delete flag the sync manager sets before its DELETE
// (pkg/warden/syncmgr/register.go, updateSpecialObjForDelete).
func testStuckMemberTenant() *tenantv1.Tenant {
	now := metav1.NewTime(time.Date(2026, 10, 16, 7, 27, 41, 0, time.UTC))

	return &tenantv1.Tenant{
		ObjectMeta: metav1.ObjectMeta{
			Name: testTenantName,
			Annotations: map[string]string{
				constants.ForceDeleteAnnotation: "true",
				constants.SyncAnnotation:        "1",
			},
			Finalizers:        []string{constants.TenantFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: tenantv1.TenantSpec{
			DisplayName: "verification tenant",
			Namespace:   constants.TenantNsPrefix + testTenantName,
		},
	}
}

// testSyncedMemberTenant is what the sync manager creates once the name is free
// again: a fresh copy of the pivot's tenant, with no deletion timestamp.
func testSyncedMemberTenant() *tenantv1.Tenant {
	return &tenantv1.Tenant{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testTenantName,
			Annotations: map[string]string{constants.SyncAnnotation: "1"},
		},
		Spec: tenantv1.TenantSpec{Namespace: constants.TenantNsPrefix + testTenantName},
	}
}

func testTenantScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := tenantv1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot register tenant types: %v", err)
	}
	return scheme
}

func newTestTenantFinalizerReconciler(t *testing.T, objects ...client.Object) (*TenantFinalizerReconciler, client.Client) {
	t.Helper()

	scheme := testTenantScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()

	return &TenantFinalizerReconciler{Client: fakeClient, Scheme: scheme}, fakeClient
}

func reconcileMemberTenant(t *testing.T, r *TenantFinalizerReconciler) {
	t.Helper()

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: testTenantName},
	}); err != nil {
		t.Fatalf("the reconcile of the deleting tenant failed: %v", err)
	}
}

func assertTenantFinalizerGone(t *testing.T, c client.Client) {
	t.Helper()

	stored := &tenantv1.Tenant{}
	err := c.Get(context.Background(), types.NamespacedName{Name: testTenantName}, stored)
	if errors.IsNotFound(err) {
		// Dropping the last finalizer of a deleting object ends it; the fake
		// client performs that removal, and on a real server it is the same
		// desired end state.
		return
	}
	if err != nil {
		t.Fatalf("cannot read the tenant back: %v", err)
	}

	for _, finalizer := range stored.Finalizers {
		if finalizer == constants.TenantFinalizer {
			// Error, not Fatal: the test also reports whether the name is free,
			// and the live defect showed both at once.
			t.Errorf("the tenant still carries the finalizer %q, so it stays in Terminating and the name stays taken",
				constants.TenantFinalizer)
		}
	}
}

// The pivot object this copy mirrors is already gone, so there is no parent,
// project or pivot state left to consult. The member must release the finalizer
// on its own, and the name must then be free for the sync manager's next create;
// otherwise the tenant can never be re-established on the member.
func TestMemberReconcileReleasesAStuckTenant(t *testing.T) {
	r, fakeClient := newTestTenantFinalizerReconciler(t, testStuckMemberTenant())

	reconcileMemberTenant(t, r)
	assertTenantFinalizerGone(t, fakeClient)

	// The live symptom: while the terminating object held the name, the sync
	// manager's create kept returning AlreadyExists -- "object is being deleted:
	// tenants.tenant.kubecube.io \"t-verify\" already exists" on a real server.
	if err := fakeClient.Create(context.Background(), testSyncedMemberTenant()); err != nil {
		t.Errorf("the sync manager cannot create the tenant again: %v", err)
	}
}

// Every pass must be safe. Once the tenant is gone a repeated reconcile of the
// same request is a no-op, and an object that no longer carries this finalizer is
// not written at all.
func TestMemberReconcileIsIdempotent(t *testing.T) {
	r, fakeClient := newTestTenantFinalizerReconciler(t, testStuckMemberTenant())

	reconcileMemberTenant(t, r)
	assertTenantFinalizerGone(t, fakeClient)

	// The object is gone now; the same request must return no error.
	reconcileMemberTenant(t, r)

	// A deleting object held by a different finalizer is not this finalizer's to
	// drop, so the reconcile must not write to it.
	held := testStuckMemberTenant()
	held.Finalizers = []string{"example.com/other"}
	heldReconciler, heldClient := newTestTenantFinalizerReconciler(t, held)

	before := &tenantv1.Tenant{}
	if err := heldClient.Get(context.Background(), types.NamespacedName{Name: testTenantName}, before); err != nil {
		t.Fatalf("cannot read the held tenant: %v", err)
	}
	reconcileMemberTenant(t, heldReconciler)
	after := &tenantv1.Tenant{}
	if err := heldClient.Get(context.Background(), types.NamespacedName{Name: testTenantName}, after); err != nil {
		t.Fatalf("cannot read the held tenant back: %v", err)
	}
	if after.ResourceVersion != before.ResourceVersion {
		t.Errorf("a reconcile that had nothing to remove wrote to the tenant: resourceVersion %s -> %s",
			before.ResourceVersion, after.ResourceVersion)
	}
}

// Only the stuck case resolves. A tenant that is still alive is the pivot's to
// release, and it may still own projects; the member must neither drop the
// finalizer nor disturb those projects, or a deletion the pivot is still
// cascading would be cut short on the member.
func TestMemberReconcileLeavesALiveTenantWithProjectsAlone(t *testing.T) {
	live := &tenantv1.Tenant{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testTenantName,
			Annotations: map[string]string{constants.SyncAnnotation: "1"},
			Finalizers:  []string{constants.TenantFinalizer},
		},
		Spec: tenantv1.TenantSpec{Namespace: constants.TenantNsPrefix + testTenantName},
	}
	project := &tenantv1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:   testProjectName,
			Labels: map[string]string{constants.TenantLabel: testTenantName},
		},
		Spec: tenantv1.ProjectSpec{Namespace: constants.ProjectNsPrefix + testProjectName},
	}

	r, fakeClient := newTestTenantFinalizerReconciler(t, live, project)
	reconcileMemberTenant(t, r)

	stored := &tenantv1.Tenant{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: testTenantName}, stored); err != nil {
		t.Fatalf("a live tenant was deleted: %v", err)
	}
	if !controllerutil.ContainsFinalizer(stored, constants.TenantFinalizer) {
		t.Error("a live tenant lost the finalizer the pivot's cascade waits on")
	}

	storedProject := &tenantv1.Project{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: testProjectName}, storedProject); err != nil {
		t.Fatalf("a live tenant's project was disturbed: %v", err)
	}
	if !storedProject.DeletionTimestamp.IsZero() {
		t.Error("the member released a tenant whose projects still exist")
	}
}
