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

package namespace

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// Reconciler adopts namespaces the platform should own but that carry no
// ownership label.
//
// It exists because the platform does not build its own tree. In a default
// deployment the tenant namespace is created by hand and the project namespace
// is materialised by HNC from an anchor, and neither carries an ownership
// label; KubeCube's own creation paths for them sit behind an environment
// variable that no manifest sets. Without this controller the labels that the
// readers are migrating to would never reach the top of the tree, and removing
// HNC would leave the tenant and project levels unowned.
type Reconciler struct {
	client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, req.NamespacedName, ns); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// a namespace on its way out needs nothing
	if !ns.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if _, _, owned := ownership.Of(ns); owned {
		return ctrl.Result{}, nil
	}

	labels, adopt := Adoption(ns, r.parentOf(ctx, ns))
	if !adopt {
		clog.Debug("namespace %v carries no ownership label and could not be placed; leaving it unmanaged", ns.Name)
		return ctrl.Result{}, nil
	}

	patch := ns.DeepCopy()
	if patch.Labels == nil {
		patch.Labels = map[string]string{}
	}
	for k, v := range labels {
		patch.Labels[k] = v
	}

	if err := r.Patch(ctx, patch, client.MergeFrom(ns)); err != nil {
		return ctrl.Result{}, err
	}

	clog.Info("adopted namespace %v as %v in tenant %v", ns.Name, labels[ownership.Label], labels[ownership.TenantKey])
	return ctrl.Result{}, nil
}

// parentOf resolves the namespace the subnamespace annotation points at, which
// is where a project's tenant and a space's project are recorded. A missing
// parent is not an error: it is what a namespace outside the tree looks like.
func (r *Reconciler) parentOf(ctx context.Context, ns *corev1.Namespace) *corev1.Namespace {
	parentName := ns.Annotations[constants.HncAnnotation]
	if parentName == "" {
		return nil
	}

	parent := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: parentName}, parent); err != nil {
		clog.Debug("namespace %v points at parent %v which could not be read: %v", ns.Name, parentName, err)
		return nil
	}
	return parent
}

// SetupWithManager sets up the controller with the Manager.
func SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Complete(&Reconciler{Client: mgr.GetClient()})
}
