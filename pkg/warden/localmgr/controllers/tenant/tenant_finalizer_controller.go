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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	tenantv1 "github.com/kubecube-io/kubecube/pkg/apis/tenant/v1"
	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// TenantFinalizerReconciler releases the finalizer that keeps a member's copy of
// a Tenant in Terminating.
//
// A member's Tenant is not created by a controller there: the sync manager
// mirrors the pivot's object into the member, finalizers included
// (pkg/warden/syncmgr/register.go creates and updates the local object from the
// pivot's, and trimObjMeta only clears the resource version). The finalizer
// itself has a single owner, the pivot's tenancy controller
// (pkg/ctrlmgr/controllers/tenancy), which adds it and removes it once the
// tenant's projects are gone. Nothing on the member was meant to hold it and
// nothing there removed it, so a tenant torn down on the pivot left the member's
// copy deleting forever and kept the name:
//
//	object is being deleted: tenants.tenant.kubecube.io "t-verify" already exists
//
// The pivot does update the finalizer away just before it deletes its tenant,
// but the member only sees whatever the sync observed: when the delete is what
// the sync sees first, the finalizer-less version is never applied and the
// member keeps a finalizer whose owner no longer exists in any cluster it can
// read.
type TenantFinalizerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func newTenantFinalizerReconciler(mgr ctrl.Manager) (*TenantFinalizerReconciler, error) {
	return &TenantFinalizerReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}, nil
}

func (r *TenantFinalizerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	tenant := &tenantv1.Tenant{}
	if err := r.Get(ctx, req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only a deletion is this controller's business. Adding the finalizer on the
	// member would create a guard whose cascade lives only on the pivot, which is
	// the same stuck object in a new place; which tenant may go, and when, stays
	// the pivot's decision.
	if tenant.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(tenant, constants.TenantFinalizer) {
		return ctrl.Result{}, nil
	}

	clog.Info("Remove finalizer from deleting tenant %v on the member cluster", tenant.Name)

	// Nothing is consulted before the removal, and that is the point: the pivot
	// object and the projects the finalizer waited for are exactly what is gone
	// whenever the member's copy is left behind, so any lookup would fail and
	// return before the removal. kubecube.io/force-delete does not help here
	// either: the only code that reads it is the member's tenant webhook, on the
	// DELETE request, to decide whether to admit the deletion at all
	// (pkg/warden/localmgr/webhooks/tenant). It never removed a finalizer, so it
	// admitted a DELETE that then could not finish. This reconcile is what makes
	// that admitted DELETE complete, which is also what makes the annotation an
	// escape hatch rather than a dead end.
	controllerutil.RemoveFinalizer(tenant, constants.TenantFinalizer)
	if err := r.Update(ctx, tenant); err != nil {
		clog.Warn("remove finalizer from tenant %v failed: %v", tenant.Name, err)
		return ctrl.Result{}, err
	}

	// The cleanup the member already owns is not skipped by releasing the object:
	// the tenant controller in this package deletes the tenant namespace and its
	// quotas when it sees the tenant gone (deleteTenant, on IsNotFound), so this
	// removal is what lets that path run instead of never being reached.
	return ctrl.Result{}, nil
}

// SetupTenantFinalizerWithManager registers the member-side controller that
// finishes the deletion of a synced Tenant.
//
// It runs only on a member (see localmgr.setupControllersWithManager): on the
// pivot the tenancy controller owns the same finalizer and must keep waiting for
// the tenant's projects, so releasing it there would skip the cascade.
func SetupTenantFinalizerWithManager(mgr ctrl.Manager) error {
	r, err := newTenantFinalizerReconciler(mgr)
	if err != nil {
		return err
	}

	// A synced Tenant arrives with the pivot's finalizer already on it, so only
	// an object that is being deleted needs a reconcile. Filtering the rest out
	// keeps the sync manager's periodic updates from driving a no-op reconcile
	// for every tenant.
	deletingOnly := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return !e.Object.GetDeletionTimestamp().IsZero()
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return !e.ObjectNew.GetDeletionTimestamp().IsZero()
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&tenantv1.Tenant{}).
		WithEventFilter(deletingOnly).
		Complete(r)
}
