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
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// Adoption works out the ownership labels a namespace should carry when it
// carries none, so that the platform can own a tree it did not build.
//
// Ownership is decided from the best evidence available — the labels the
// namespace already carries, then its name, then its parent — and written once.
// The name convention is consulted exactly here; after a namespace is adopted
// the label is the authority and the name is only a convention, which is what
// lets the readers stop reading names. A namespace the platform cannot place is
// left unmanaged rather than guessed at, because a guess would decide who may
// reach it.
//
// parent is the namespace the subnamespace annotation points at, when there is
// one. It matters because a project's tenant and a space's project are only
// recorded there.
func Adoption(ns *corev1.Namespace, parent *corev1.Namespace) (labels map[string]string, adopt bool) {
	if ns == nil {
		return nil, false
	}

	// The authority is already present, so nothing is derived over it. This
	// asks about the label rather than about resolved ownership on purpose:
	// resolving falls back to the name, and a namespace that is only
	// name-identifiable is exactly the one that still needs labelling.
	if ns.Labels[ownership.Label] != "" {
		return nil, false
	}

	// The HNC labels are the best evidence available: they already say who owns
	// this namespace. The project is read first, because a project namespace
	// and a space carry both labels and the project is the narrower fact.
	if project := ns.Labels[constants.HncProjectLabel]; project != "" {
		if tenant := ns.Labels[constants.HncTenantLabel]; tenant != "" {
			return ownership.ProjectLabels(tenant, project), true
		}
	}
	if tenant := ns.Labels[constants.HncTenantLabel]; tenant != "" {
		return ownership.TenantLabels(tenant), true
	}

	name := ns.GetName()

	// a tenant namespace names itself
	if tenant, ok := trimPrefix(name, constants.TenantNsPrefix); ok {
		return ownership.TenantLabels(tenant), true
	}

	// a project namespace names itself, and takes its tenant from the namespace
	// it was created in
	if project, ok := trimPrefix(name, constants.ProjectNsPrefix); ok {
		tenant, ok := tenantOf(parent)
		if !ok {
			return nil, false
		}
		return ownership.ProjectLabels(tenant, project), true
	}

	// anything else belongs to whatever owns its parent. Only a project owner
	// is inherited: a namespace created directly under a tenant namespace is
	// not thereby a tenant namespace.
	if kind, owner, ok := ownershipOf(parent); ok && kind == ownership.KindProject {
		tenant, ok := tenantOf(parent)
		if !ok {
			return nil, false
		}
		return ownership.ProjectLabels(tenant, owner), true
	}

	return nil, false
}

func trimPrefix(name, prefix string) (string, bool) {
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(name, prefix)
	if rest == "" {
		return "", false
	}
	return rest, true
}

// tenantOf and ownershipOf tolerate a missing parent, which is the common case
// for a namespace that is not part of the tree at all.
func tenantOf(parent *corev1.Namespace) (string, bool) {
	if parent == nil {
		return "", false
	}
	return ownership.TenantOf(parent)
}

func ownershipOf(parent *corev1.Namespace) (ownership.Kind, string, bool) {
	if parent == nil {
		return "", "", false
	}
	return ownership.Of(parent)
}
