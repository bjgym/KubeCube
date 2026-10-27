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

package authorization

import (
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// builtinRoleNames are the ClusterRoles the chart ships under
// kubecube-chart/templates/kubecube/rbac/buildin/. They are the roles the
// platform itself binds, and nothing in the platform would recreate one.
var builtinRoleNames = sets.New[string](
	constants.PlatformAdmin,
	constants.TenantAdmin,
	constants.ProjectAdmin,
	constants.Reviewer,
	constants.TenantAdminCluster,
	constants.ProjectAdminCluster,
)

// isBuiltinRole reports whether a ClusterRole belongs to the deployment.
//
// The authority is the aggregationRule, not the name: every role in the chart's
// buildin directory carries one and takes its rules from the roles labelled for
// it, and no role this API writes ever has one, because setRoleItems derives a
// plain rule set from the catalogue. The names are kept as well, because a
// built-in role whose aggregationRule was overwritten by an earlier write is
// still a built-in role — the aggregation controller would put the rules back —
// and deleting platform-admin takes the console away from everybody, including
// whoever would have to restore it.
func isBuiltinRole(role *rbacv1.ClusterRole) bool {
	if role == nil {
		return false
	}
	if role.AggregationRule != nil {
		return true
	}
	return builtinRoleNames.Has(role.Name)
}

// isManagedRole reports whether a ClusterRole is one this API writes.
//
// The label is the one the role list selects by, so a role a console can show is
// exactly a role this endpoint may remove. Anything else — cluster-admin above
// all — is not this endpoint's to delete, however it is named: the name is a
// path parameter, and without this check any ClusterRole in the cluster could be
// removed through the console's own authorization API.
func isManagedRole(role *rbacv1.ClusterRole) bool {
	if role == nil {
		return false
	}
	return role.Labels[constants.RbacLabel] == "true"
}

// roleRemovalRefusal says whether one role may be removed, and why not.
//
// It is a decision over the role and the number of subjects bound to it, so what
// the endpoint refuses can be read, and tested, without an API server. The order
// is the order the reasons matter: a built-in role is refused for being built
// in, not for happening to have holders.
func roleRemovalRefusal(role *rbacv1.ClusterRole, holders int) error {
	if role == nil {
		return fmt.Errorf("there is no role to delete")
	}
	if isBuiltinRole(role) {
		return fmt.Errorf("role %s is built in and cannot be deleted", role.Name)
	}
	if !isManagedRole(role) {
		return fmt.Errorf("%s is not a role this platform manages and cannot be deleted here", role.Name)
	}
	if holders > 0 {
		return fmt.Errorf("role %s is held by %d subject(s); remove those bindings before deleting the role",
			role.Name, holders)
	}
	return nil
}

// roleHolders reports the subjects bound to one ClusterRole.
//
// "In use" is a question about bindings, so it is answered from the bindings:
// every ClusterRoleBinding, and every RoleBinding in the cluster whose roleRef
// is this ClusterRole. Both are needed — a role granted at a tenant or a project
// is written by warden as a namespaced RoleBinding pointing at the ClusterRole
// (pkg/warden/localmgr/controllers/user/user_controller.go, refreshNsBinding),
// which is where most custom roles are used, while a platform-level role is
// granted by a ClusterRoleBinding.
//
// The resolver's UsersFor is deliberately not the probe here. It maps every
// subject back to a User CR and returns an aggregated error for a subject that
// has none, so a role bound to a ServiceAccount — or to any identity the
// platform does not know as a user — cannot be read at all through it. That
// would fail closed on the roles the console opens first, and it answers "which
// users hold this" rather than "is this still granted to anybody".
func (h *handler) roleHolders(name string) (sets.Set[string], error) {
	holders := sets.New[string]()

	clusterRoleBindings, err := h.ListClusterRoleBindings()
	if err != nil {
		return nil, err
	}
	for _, binding := range clusterRoleBindings {
		if !refersToClusterRole(binding.RoleRef, name) {
			continue
		}
		for _, subject := range binding.Subjects {
			holders.Insert(subject.Name)
		}
	}

	roleBindings, err := h.ListRoleBindings("")
	if err != nil {
		return nil, err
	}
	for _, binding := range roleBindings {
		if !refersToClusterRole(binding.RoleRef, name) {
			continue
		}
		for _, subject := range binding.Subjects {
			holders.Insert(subject.Name)
		}
	}

	return holders, nil
}

// refersToClusterRole reports whether a binding's roleRef is this ClusterRole.
// A RoleBinding to a Role of the same name is a different role and not this
// one's use.
func refersToClusterRole(ref rbacv1.RoleRef, name string) bool {
	return ref.Kind == constants.K8sKindClusterRole && ref.Name == name
}
