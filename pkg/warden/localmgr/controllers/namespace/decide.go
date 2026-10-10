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
// Ownership is decided from the best evidence available — the HNC labels a
// namespace already carries, then its name, then its project-owned parent — and
// written once. The name convention is consulted exactly here; after a
// namespace is adopted the label is the authority and the name is only a
// convention, which is what lets the readers stop reading names. A namespace
// the platform cannot place is left unmanaged rather than guessed at, because a
// guess would decide who may reach it.
//
// parent is the namespace the subnamespace annotation points at, when there is
// one. It matters because a project's tenant and a space's project are only
// recorded there.
func Adoption(ns *corev1.Namespace, parent *corev1.Namespace) (map[string]string, bool) {
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

	name := ns.GetName()

	// a tenant namespace names itself
	if tenant, ok := trimPrefix(name, constants.TenantNsPrefix); ok {
		return ownership.TenantLabels(tenant), true
	}

	// a project namespace names itself, and takes its tenant from the labels it
	// carries or from the namespace it was created in
	if selfProject, ok := trimPrefix(name, constants.ProjectNsPrefix); ok {
		tenant, ok := tenantOf(ns, parent)
		if !ok {
			return nil, false
		}
		return ownership.ProjectLabels(tenant, selfProject), true
	}

	// a namespace carrying the HNC project label is owned by that project. The
	// level is what it sits under, because HNC's tree is tenant, then project,
	// then space, and only the parent says which of the last two this is.
	if project := ns.Labels[constants.HncProjectLabel]; project != "" {
		tenant, ok := tenantOf(ns, parent)
		if !ok {
			return nil, false
		}
		if parent != nil && strings.HasPrefix(parent.GetName(), constants.TenantNsPrefix) {
			return ownership.ProjectLabels(tenant, project), true
		}
		return ownership.SpaceLabels(tenant, project), true
	}

	// a namespace carrying only the HNC tenant label is the tenant's own
	if tenant := ns.Labels[constants.HncTenantLabel]; tenant != "" {
		return ownership.TenantLabels(tenant), true
	}

	// anything else belongs to whatever owns its parent, and is a space. Only a
	// project owner is inherited: a namespace created directly under a tenant
	// namespace is not thereby a project or a tenant.
	if kind, owner, ok := ownershipOf(parent); ok && kind == ownership.KindProject {
		tenant, ok := tenantOf(ns, parent)
		if !ok {
			return nil, false
		}
		return ownership.SpaceLabels(tenant, owner), true
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

// tenantOf reads the tenant from the namespace itself and then from its parent,
// which is where a project namespace's tenant is recorded.
func tenantOf(ns *corev1.Namespace, parent *corev1.Namespace) (string, bool) {
	if tenant, ok := tenantNameOf(ns); ok {
		return tenant, true
	}
	return tenantNameOf(parent)
}

// tenantNameOf resolves a namespace's tenant from its labels and, for one that
// predates them, from its name. The name convention is evidence here and
// nowhere else: a hand-built tenant namespace carries nothing else, and the
// label this adoption writes is what every reader consults afterwards.
func tenantNameOf(ns *corev1.Namespace) (string, bool) {
	if ns == nil {
		return "", false
	}

	if tenant, ok := ownership.TenantOf(ns); ok {
		return tenant, true
	}
	if tenant := ns.Labels[constants.HncTenantLabel]; tenant != "" {
		return tenant, true
	}
	return trimPrefix(ns.GetName(), constants.TenantNsPrefix)
}

// ownershipOf resolves the owner of a namespace the same way, so that a space
// under a project namespace that has not been adopted yet is still placed.
func ownershipOf(parent *corev1.Namespace) (ownership.Kind, string, bool) {
	if parent == nil {
		return "", "", false
	}

	if kind, name, ok := ownership.Of(parent); ok {
		return kind, name, true
	}
	if project := parent.Labels[constants.HncProjectLabel]; project != "" {
		return ownership.KindProject, project, true
	}
	if tenant := parent.Labels[constants.HncTenantLabel]; tenant != "" {
		return ownership.KindTenant, tenant, true
	}

	return "", "", false
}
