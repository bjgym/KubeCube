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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	quotav1 "github.com/kubecube-io/kubecube/pkg/apis/quota/v1"
	"github.com/kubecube-io/kubecube/pkg/clog"
	quotapkg "github.com/kubecube-io/kubecube/pkg/quota"
)

// CubeResourceQuotaReconciler releases the finalizer that keeps a member's copy
// of a CubeResourceQuota in Terminating.
//
// A member's CubeResourceQuota is not created by a controller on the member:
// the sync manager mirrors the pivot's object into the member, finalizers
// included (pkg/warden/syncmgr/resources.go). Its removal, however, had no owner
// there. The pivot's controller-manager (pkg/ctrlmgr/controllers/quota) watches
// the pivot's own copy, and the warden quota controller in this package
// reconciles v1.ResourceQuota, not this type. A tenant or space torn down on the
// pivot therefore left the member's copy deleting forever: the finalizer never
// went away, and every later sync of a same-named object failed with
//
//	object is being deleted: cuberesourcequota.quota.kubecube.io "<name>" already exists
//
// because the terminating object still held the name.
type CubeResourceQuotaReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func newCubeResourceQuotaReconciler(mgr ctrl.Manager) (*CubeResourceQuotaReconciler, error) {
	return &CubeResourceQuotaReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}, nil
}

func (r *CubeResourceQuotaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cubeQuota := &quotav1.CubeResourceQuota{}
	if err := r.Get(ctx, req.NamespacedName, cubeQuota); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only a deletion is this controller's business, and only the removal below
	// is deliberate: the finalizer is added on the pivot and reaches the member
	// with the synced object. Adding it here would create a finalizer with no
	// pivot object behind it, which is the same stuck state in a new place.
	if cubeQuota.DeletionTimestamp == nil {
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(cubeQuota, quotapkg.Finalizer) {
		return ctrl.Result{}, nil
	}

	clog.Info("Remove finalizer from deleting CubeResourceQuota %v on the member cluster", cubeQuota.Name)

	// No parent, tenant or project is consulted first, and that is the point:
	// the pivot-side object a lookup would resolve was torn down before the
	// member's copy could finish, so any lookup would fail and return before the
	// removal, leaving the object in Terminating forever. This finalizer guards
	// this object's own deletion and this reconcile is the only member-side code
	// that can drop it, so it is always dropped and the object is let go.
	controllerutil.RemoveFinalizer(cubeQuota, quotapkg.Finalizer)
	if err := r.Update(ctx, cubeQuota); err != nil {
		clog.Warn("remove finalizer from CubeResourceQuota %v failed: %v", cubeQuota.Name, err)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupCubeResourceQuotaWithManager registers the member-side controller that
// finishes the deletion of a synced CubeResourceQuota.
//
// It runs only on a member (see localmgr.setupControllersWithManager): on the
// pivot, ctrlmgr's CubeResourceQuota controller owns the same object and also
// flushes parent status while removing the finalizer, so a second writer there
// would only race it.
func SetupCubeResourceQuotaWithManager(mgr ctrl.Manager) error {
	r, err := newCubeResourceQuotaReconciler(mgr)
	if err != nil {
		return err
	}

	// A synced CubeResourceQuota arrives with the pivot's finalizer already on
	// it, so only an object that is being deleted needs a reconcile. Filtering
	// the rest out keeps the sync manager's periodic updates from driving a
	// no-op reconcile for every quota.
	deletingOnly := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetDeletionTimestamp() != nil
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectNew.GetDeletionTimestamp() != nil
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&quotav1.CubeResourceQuota{}).
		WithEventFilter(deletingOnly).
		Complete(r)
}
