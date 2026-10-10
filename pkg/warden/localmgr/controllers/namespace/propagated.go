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
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// stripPropagated removes the marker HNC left on the objects it copied into a
// namespace.
//
// Cleaning the namespace's own fields is not enough: HNC propagated whole
// objects down the tree, so a space can still hold the RoleBindings and Secrets
// it copied there, each carrying hnc.x-k8s.io/inherited-from. That label is
// load-bearing in exactly one place — syncmgr refuses to sync an object that
// carries it — so leaving it behind would quietly exempt those objects from
// syncing for good.
//
// The object is kept and only the marker goes. A RoleBinding in a space is
// legitimate either way, and the platform writes its own, so deleting the copy
// would be deleting something the platform may have since adopted.
//
// It is driven by the namespace event rather than by watching RoleBindings and
// Secrets, because watching Secrets means caching every Secret in the cluster,
// which is part of what made HNC heavy in the first place.
func (r *Reconciler) stripPropagated(ctx context.Context, namespace string) error {
	selector := client.MatchingLabelsSelector{Selector: inheritedSelector()}

	bindings := &rbacv1.RoleBindingList{}
	if err := r.reader.List(ctx, bindings, client.InNamespace(namespace), selector); err != nil {
		return err
	}
	for i := range bindings.Items {
		if err := r.stripInherited(ctx, &bindings.Items[i]); err != nil {
			return err
		}
	}

	secrets := &corev1.SecretList{}
	if err := r.reader.List(ctx, secrets, client.InNamespace(namespace), selector); err != nil {
		return err
	}
	for i := range secrets.Items {
		if err := r.stripInherited(ctx, &secrets.Items[i]); err != nil {
			return err
		}
	}

	return nil
}

// stripInherited removes the marker from one object, and does nothing when it is
// already gone, so that a reconcile writes only when it has something to change.
func (r *Reconciler) stripInherited(ctx context.Context, obj client.Object) error {
	marked := obj.GetLabels()
	if _, ok := marked[constants.HncInherited]; !ok {
		return nil
	}

	original, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return nil
	}

	patch := client.MergeFrom(original)
	delete(marked, constants.HncInherited)
	obj.SetLabels(marked)

	clog.Info("removed the hnc inheritance marker from %v/%v", obj.GetNamespace(), obj.GetName())
	return r.Patch(ctx, obj, patch)
}

// inheritedSelector matches on the marker's presence rather than its value: HNC
// set it to the namespace it inherited from, which the cleanup has no use for.
func inheritedSelector() labels.Selector {
	req, err := labels.NewRequirement(constants.HncInherited, selection.Exists, nil)
	if err != nil {
		// a requirement on a constant key with no values cannot fail to build
		return labels.Nothing()
	}
	return labels.NewSelector().Add(*req)
}
