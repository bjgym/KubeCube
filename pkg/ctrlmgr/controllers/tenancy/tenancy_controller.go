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

// Package tenancy cascades a tenant's deletion to the projects it owns.
//
// The tenant and project custom resources live on the pivot, and their
// disappearance is what removes the namespaces on the member clusters: syncmgr
// copies the absence of the pivot's object down, and warden's own controllers
// then delete the tenant namespace and the project namespaces. Nothing deleted
// the projects of a tenant, so the deletion webhooks refused the tenant delete
// instead and the tree had to be walked bottom-up by hand.
//
// The cascade is driven by a finalizer rather than by noticing that the object
// is already gone. A controller that reacts to absence cannot be trusted: if it
// is down when the object is deleted, the list it takes when it restarts does
// not contain that object, so the cascade never runs and nothing records that
// it did not.
package tenancy

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	tenantv1 "github.com/kubecube-io/kubecube/pkg/apis/tenant/v1"
	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/ctrlmgr/options"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// tenantFinalizer holds a tenant until the projects it owns are gone. The name
// follows the cluster controller's.
const tenantFinalizer = "tenant.finalizers.kubecube.io"

// cascadeInterval bounds how often a tenant whose projects are still going is
// looked at again. The projects delete on their own, so this waits rather than
// blocks a worker.
const cascadeInterval = 3 * time.Second

//+kubebuilder:rbac:groups=tenant.kubecube.io,resources=tenants,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=tenant.kubecube.io,resources=tenants/finalizers,verbs=update
//+kubebuilder:rbac:groups=tenant.kubecube.io,resources=projects,verbs=get;list;watch;delete

// Reconciler cascades a tenant's deletion to its projects.
type Reconciler struct {
	client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	tenant := tenantv1.Tenant{}
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// the finalizer goes on before anything else, so that a deletion which
	// happens while this controller is down is still cascaded
	if tenant.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.ensureFinalizer(ctx, &tenant)
	}

	return r.cascade(ctx, &tenant)
}

// cascade deletes the tenant's projects and holds the tenant until they are
// gone.
//
// Releasing the tenant only then is what orders the whole tree: syncmgr removes
// the member cluster's copy after the pivot's object is gone, and warden's
// tenant controller then deletes the tenant namespace — which is the parent of
// the project namespaces, so it cannot go before them.
func (r *Reconciler) cascade(ctx context.Context, tenant *tenantv1.Tenant) (ctrl.Result, error) {
	projects := tenantv1.ProjectList{}
	if err := r.List(ctx, &projects, client.MatchingLabels{constants.TenantLabel: tenant.Name}); err != nil {
		return ctrl.Result{}, err
	}

	for i := range projects.Items {
		if !projects.Items[i].DeletionTimestamp.IsZero() {
			continue
		}
		clog.Info("tenancy: deleting project %s with its tenant %s", projects.Items[i].Name, tenant.Name)
		if err := r.Delete(ctx, &projects.Items[i]); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	if len(projects.Items) > 0 {
		return ctrl.Result{RequeueAfter: cascadeInterval}, nil
	}

	return ctrl.Result{}, r.removeFinalizer(ctx, tenant)
}

// projectMapFunc maps a project to the tenant that owns it, so that the last
// project leaving releases its tenant promptly rather than on the next poll.
func (r *Reconciler) projectMapFunc(_ context.Context, obj client.Object) []reconcile.Request {
	tenant := obj.GetLabels()[constants.TenantLabel]
	if tenant == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: tenant}}}
}

func (r *Reconciler) ensureFinalizer(ctx context.Context, tenant *tenantv1.Tenant) error {
	if controllerutil.ContainsFinalizer(tenant, tenantFinalizer) {
		return nil
	}

	controllerutil.AddFinalizer(tenant, tenantFinalizer)
	if err := r.Update(ctx, tenant); err != nil {
		clog.Error("tenancy: cannot add the finalizer to tenant %s: %v", tenant.Name, err)
		return err
	}

	return nil
}

func (r *Reconciler) removeFinalizer(ctx context.Context, tenant *tenantv1.Tenant) error {
	if !controllerutil.ContainsFinalizer(tenant, tenantFinalizer) {
		return nil
	}

	clog.Info("tenancy: tenant %s has no projects left, releasing it", tenant.Name)
	controllerutil.RemoveFinalizer(tenant, tenantFinalizer)
	if err := r.Update(ctx, tenant); err != nil {
		clog.Error("tenancy: cannot remove the finalizer from tenant %s: %v", tenant.Name, err)
		return err
	}

	return nil
}

func SetupWithManager(mgr ctrl.Manager, _ *options.Options) error {
	r := &Reconciler{Client: mgr.GetClient()}

	return ctrl.NewControllerManagedBy(mgr).
		For(&tenantv1.Tenant{}).
		// a project leaving is what lets a tenant finish deleting
		Watches(&tenantv1.Project{}, handler.EnqueueRequestsFromMapFunc(r.projectMapFunc)).
		Complete(r)
}
