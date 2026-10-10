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

package tenancy

import (
	"context"
	"testing"

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

const tenantName = "t1"

func tenant(name string) *tenantv1.Tenant {
	return &tenantv1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func project(name, ownedBy string) *tenantv1.Project {
	labels := map[string]string{}
	if ownedBy != "" {
		labels[constants.TenantLabel] = ownedBy
	}
	return &tenantv1.Project{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func newReconciler(t *testing.T, objects ...client.Object) (*Reconciler, client.Client) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := tenantv1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot build the scheme: %v", err)
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &Reconciler{Client: fakeClient}, fakeClient
}

func mustReconcile(t *testing.T, r *Reconciler, name string) ctrl.Result {
	t.Helper()

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
	if err != nil {
		t.Fatalf("reconcile of %s failed: %v", name, err)
	}
	return result
}

func TestReconcileAddsTheFinalizer(t *testing.T) {
	r, fakeClient := newReconciler(t, tenant(tenantName))
	mustReconcile(t, r, tenantName)

	got := &tenantv1.Tenant{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: tenantName}, got); err != nil {
		t.Fatalf("cannot read the tenant back: %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, tenantFinalizer) {
		t.Error("the tenant has no finalizer, so a deletion that happens while this controller is down would never be cascaded")
	}
}

// A tenant that is not being deleted must not take its projects with it.
func TestReconcileLeavesALivingTenantsProjectsAlone(t *testing.T) {
	r, fakeClient := newReconciler(t, tenant(tenantName), project("p1", tenantName))
	mustReconcile(t, r, tenantName)

	got := &tenantv1.Project{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "p1"}, got); err != nil {
		t.Fatalf("a living tenant's project was disturbed: %v", err)
	}
	if !got.DeletionTimestamp.IsZero() {
		t.Error("a living tenant's project was deleted")
	}
}

func TestCascadeDeletesProjectsAndHoldsTheTenant(t *testing.T) {
	r, fakeClient := newReconciler(t, tenant(tenantName), project("p1", tenantName), project("p2", tenantName))
	ctx := context.Background()

	// the finalizer goes on first, as it does in a real cluster
	mustReconcile(t, r, tenantName)

	if err := fakeClient.Delete(ctx, tenant(tenantName)); err != nil {
		t.Fatalf("cannot delete the tenant: %v", err)
	}

	result := mustReconcile(t, r, tenantName)
	if result.RequeueAfter == 0 {
		t.Error("the tenant was not held while its projects were going")
	}

	for _, name := range []string{"p1", "p2"} {
		err := fakeClient.Get(ctx, types.NamespacedName{Name: name}, &tenantv1.Project{})
		if !errors.IsNotFound(err) {
			t.Errorf("project %s was not deleted with its tenant (err = %v)", name, err)
		}
	}

	held := &tenantv1.Tenant{}
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: tenantName}, held); err != nil {
		t.Fatalf("the tenant is gone while its projects were still there: %v", err)
	}
	if !controllerutil.ContainsFinalizer(held, tenantFinalizer) {
		t.Error("the tenant was released in the same pass that deleted its projects, so the projects were not waited for")
	}
}

// The whole point of holding the tenant is that releasing it is what lets the
// member cluster delete the namespaces, and the tenant namespace cannot go
// before the project namespaces do.
func TestCascadeReleasesTheTenantOnlyOnceItsProjectsAreGone(t *testing.T) {
	r, fakeClient := newReconciler(t, tenant(tenantName), project("p1", tenantName))
	ctx := context.Background()

	mustReconcile(t, r, tenantName)
	if err := fakeClient.Delete(ctx, tenant(tenantName)); err != nil {
		t.Fatalf("cannot delete the tenant: %v", err)
	}

	// one pass deletes the project, the next finds nothing left and releases
	mustReconcile(t, r, tenantName)
	mustReconcile(t, r, tenantName)

	got := &tenantv1.Tenant{}
	err := fakeClient.Get(ctx, types.NamespacedName{Name: tenantName}, got)
	if errors.IsNotFound(err) {
		// released and collected, which is the whole sequence working
		return
	}
	if err != nil {
		t.Fatalf("cannot read the tenant back: %v", err)
	}
	if controllerutil.ContainsFinalizer(got, tenantFinalizer) {
		t.Error("the tenant is still held after its projects are gone, so it could never be deleted")
	}
}

func TestProjectMapFuncMapsToItsTenant(t *testing.T) {
	r, _ := newReconciler(t)
	ctx := context.Background()

	requests := r.projectMapFunc(ctx, project("p1", tenantName))
	if len(requests) != 1 || requests[0].Name != tenantName {
		t.Errorf("a project mapped to %v, want the tenant %s", requests, tenantName)
	}

	if got := r.projectMapFunc(ctx, project("p1", "")); len(got) != 0 {
		t.Errorf("a project with no tenant mapped to %v, want nothing", got)
	}
}
