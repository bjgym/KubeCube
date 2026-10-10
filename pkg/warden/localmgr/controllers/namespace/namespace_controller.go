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
// ownership label, and strips the protocol fields they were described with.
//
// The adoption half exists because the platform did not always build its own
// tree: the tenant namespace was created by hand and the project namespace was
// materialised by HNC from an anchor, and neither carried an ownership label.
// Without it the labels the readers migrated to would never reach the top of the
// tree.
//
// The cleanup half is what makes the old fields safe to stop reading. Nothing
// writes them any more, so a namespace that carries them is one created before
// the migration; this controller is the only thing that removes them, and it
// does so on the same pass that gives the namespace an owner, which is why a
// namespace is never left with neither.
type Reconciler struct {
	client.Client

	// reader reads straight from the API server, so that listing the objects HNC
	// propagated into a namespace does not put every Secret in the cluster into
	// the cache.
	reader client.Reader
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

	patch := ns.DeepCopy()

	if _, _, owned := ownership.Of(ns); !owned {
		labels, adopt := Adoption(ns, r.parentOf(ctx, ns))
		if !adopt {
			// A namespace that cannot be placed is left exactly as it is: it
			// has no owner label, so stripping the fields that say where it
			// might belong would destroy the only evidence there is.
			clog.Debug("namespace %v carries no ownership label and could not be placed; leaving it unmanaged", ns.Name)
			reportInconsistency(ns.Name, ns.Labels)
			return ctrl.Result{}, nil
		}

		if patch.Labels == nil {
			patch.Labels = map[string]string{}
		}
		for k, v := range labels {
			patch.Labels[k] = v
		}

		clog.Info("adopted namespace %v as %v in tenant %v", ns.Name, labels[ownership.Label], labels[ownership.TenantKey])
	}

	reportInconsistency(ns.Name, patch.Labels)

	stripped := stripRetired(patch)
	if stripped == 0 {
		return ctrl.Result{}, r.stripPropagated(ctx, ns.Name)
	}

	if err := r.Patch(ctx, patch, client.MergeFrom(ns)); err != nil {
		return ctrl.Result{}, err
	}

	clog.Info("removed %v retired protocol field(s) from namespace %v", stripped, ns.Name)
	return ctrl.Result{}, r.stripPropagated(ctx, ns.Name)
}

// reportInconsistency says so when a namespace's ownership labels disagree with
// each other.
//
// The three keys are written together from one input, so they cannot drift on
// their own; a namespace that fails this was written by something else, or by a
// version that did not yet write all three. The readers believe the owner, so
// what a human has to look at is the derived keys that disagree with it.
func reportInconsistency(name string, labels map[string]string) {
	if reason := ownership.Inconsistency(labels); reason != "" {
		clog.Warn("namespace %v %s", name, reason)
	}
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
		Complete(&Reconciler{Client: mgr.GetClient(), reader: mgr.GetAPIReader()})
}
