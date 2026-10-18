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
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubecube-io/kubecube/pkg/authorizer/rbac"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// managedRole is a role this API writes: the label the role list selects by, and
// no aggregationRule, which is what setRoleItems leaves behind.
func managedRole(name string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				constants.RbacLabel: "true",
				constants.RoleLabel: constants.ClusterRoleProject,
			},
			Annotations: map[string]string{constants.SyncAnnotation: "true"},
		},
	}
}

// builtinRole is one of the chart's roles: it aggregates the rules of the roles
// labelled for it, and holds none of its own.
func builtinRole(name string) *rbacv1.ClusterRole {
	role := managedRole(name)
	role.AggregationRule = &rbacv1.AggregationRule{
		ClusterRoleSelectors: []metav1.LabelSelector{{
			MatchLabels: map[string]string{constants.ProjectAdminAgLabel: "true"},
		}},
	}
	return role
}

// May this role be removed? The three answers the product asks for are here, and
// so are the two the endpoint would otherwise get wrong.
func TestTheRemovalDecision(t *testing.T) {
	tests := []struct {
		name    string
		role    *rbacv1.ClusterRole
		holders int
		refusal string
	}{
		{
			name:    "a built-in role",
			role:    builtinRole(constants.PlatformAdmin),
			refusal: "built in",
		},
		{
			// A built-in role the aggregation controller would refill. Its rules
			// being gone does not make it a custom role.
			name:    "a built-in role whose aggregationRule an earlier write dropped",
			role:    managedRole(constants.PlatformAdmin),
			refusal: "built in",
		},
		{
			name:    "a built-in role the chart labels without a level",
			role:    builtinRole(constants.TenantAdminCluster),
			refusal: "built in",
		},
		{
			name:    "a custom role somebody holds",
			role:    managedRole("auditor"),
			holders: 3,
			refusal: "held by 3 subject(s)",
		},
		{
			name: "a custom role nobody holds",
			role: managedRole("auditor"),
		},
		{
			// The name is a path parameter, so without this the endpoint would
			// delete any ClusterRole in the cluster.
			name:    "a role this platform did not write",
			role:    &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "cluster-admin"}},
			refusal: "not a role this platform manages",
		},
		{
			name:    "no role at all",
			role:    nil,
			refusal: "no role",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := roleRemovalRefusal(tt.role, tt.holders)

			if tt.refusal == "" {
				if err != nil {
					t.Fatalf("refused with %q, want the role to be removable", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("the role was allowed, want a refusal naming %q", tt.refusal)
			}
			if !strings.Contains(err.Error(), tt.refusal) {
				t.Errorf("refused with %q, want a message containing %q", err, tt.refusal)
			}
		})
	}
}

// newHandlerWithResolver builds a handler whose resolver reads the same fake
// cache the handler does, which is how the real one is built over the local
// cluster's cache.
func newHandlerWithResolver(t *testing.T, objs ...client.Object) *handler {
	t.Helper()

	h := newHandlerFor(t, objs...)
	h.Interface = &rbac.DefaultResolver{Cache: h.Cache()}
	return h
}

// deleteRoleCall issues one deletion against the route as AddApisTo registers it,
// so the name comes out of the path the way it does in production.
func deleteRoleCall(h *handler, name, caller string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)

	engine := gin.New()
	engine.Handle(http.MethodDelete, "/roles/:name",
		func(c *gin.Context) { c.Set(constants.UserName, caller) },
		h.requirePlatformAdmin(),
		h.deleteRole,
	)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/roles/"+name, nil))
	return recorder
}

func clusterRoleBinding(name, role string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		RoleRef:    rbacv1.RoleRef{APIGroup: constants.K8sGroupRBAC, Kind: constants.K8sKindClusterRole, Name: role},
		Subjects:   []rbacv1.Subject{{Kind: "User", Name: "alice"}},
	}
}

// roleBinding is how a role granted at a tenant or a project reaches its holder:
// a namespaced RoleBinding pointing at the ClusterRole.
func roleBinding(name, namespace, role string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		RoleRef:    rbacv1.RoleRef{APIGroup: constants.K8sGroupRBAC, Kind: constants.K8sKindClusterRole, Name: role},
		Subjects:   []rbacv1.Subject{{Kind: "User", Name: "alice"}},
	}
}

// The endpoint, end to end over the repository's own fakes: who may call it,
// what it refuses, and that an allowed deletion actually removes the role.
func TestDeletingARole(t *testing.T) {
	root := platformAdmin("root")
	member := tenantMember("alice", "t1")

	tests := []struct {
		name     string
		caller   string
		roleName string
		role     *rbacv1.ClusterRole
		binding  client.Object
		status   int
	}{
		{
			name:     "a custom role nobody holds",
			caller:   "root",
			roleName: "auditor",
			role:     managedRole("auditor"),
			status:   http.StatusOK,
		},
		{
			name:     "a custom role held by a ClusterRoleBinding",
			caller:   "root",
			roleName: "auditor",
			role:     managedRole("auditor"),
			binding:  clusterRoleBinding("alice-auditor", "auditor"),
			status:   http.StatusBadRequest,
		},
		{
			// The case a check over ClusterRoleBindings alone would let through.
			name:     "a custom role held by a namespaced RoleBinding",
			caller:   "root",
			roleName: "auditor",
			role:     managedRole("auditor"),
			binding:  roleBinding("alice-auditor", "kubecube-project-p1", "auditor"),
			status:   http.StatusBadRequest,
		},
		{
			// A RoleBinding to a Role of the same name is a different role.
			name:     "a role a Role of the same name does not hold",
			caller:   "root",
			roleName: "auditor",
			role:     managedRole("auditor"),
			binding:  roleBinding("alice-auditor", "kubecube-project-p1", "some-other-role"),
			status:   http.StatusOK,
		},
		{
			name:     "a built-in role",
			caller:   "root",
			roleName: constants.PlatformAdmin,
			role:     builtinRole(constants.PlatformAdmin),
			status:   http.StatusBadRequest,
		},
		{
			name:     "a role this platform did not write",
			caller:   "root",
			roleName: "cluster-admin",
			role:     &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "cluster-admin"}},
			status:   http.StatusBadRequest,
		},
		{
			name:     "a caller who is not a platform administrator",
			caller:   "alice",
			roleName: "auditor",
			role:     managedRole("auditor"),
			status:   http.StatusForbidden,
		},
		{
			name:     "a role that is already gone",
			caller:   "root",
			roleName: "auditor",
			status:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{root, member}
			if tt.role != nil {
				objs = append(objs, tt.role)
			}
			if tt.binding != nil {
				objs = append(objs, tt.binding)
			}
			h := newHandlerWithResolver(t, objs...)

			recorder := deleteRoleCall(h, tt.roleName, tt.caller)
			if recorder.Code != tt.status {
				t.Fatalf("status = %d (%s), want %d", recorder.Code, recorder.Body.String(), tt.status)
			}

			// The outcome, not the status: an allowed deletion removes the role,
			// and a refused one leaves it exactly where it was.
			remaining := &rbacv1.ClusterRole{}
			err := h.Direct().Get(context.Background(), client.ObjectKey{Name: tt.roleName}, remaining)
			gone := err != nil
			if tt.status == http.StatusOK && tt.role != nil && !gone {
				t.Error("the role is still there after a deletion the endpoint allowed")
			}
			if tt.status != http.StatusOK && tt.role != nil && gone {
				t.Error("the role was removed by a deletion the endpoint refused")
			}
		})
	}
}
