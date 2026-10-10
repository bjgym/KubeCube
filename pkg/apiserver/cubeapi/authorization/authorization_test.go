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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tenantv1 "github.com/kubecube-io/kubecube/pkg/apis/tenant/v1"
	userv1 "github.com/kubecube-io/kubecube/pkg/apis/user/v1"
	clientfake "github.com/kubecube-io/kubecube/pkg/multicluster/client/fake"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		rbacv1.AddToScheme,
		userv1.AddToScheme,
		tenantv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("building the scheme: %v", err)
		}
	}
	return scheme
}

// newHandlerFor builds a handler over the repository's own fakes: no API server,
// no kubeconfig, nothing that would make this lane depend on an environment.
func newHandlerFor(t *testing.T, objs ...client.Object) *handler {
	t.Helper()

	return &handler{Client: clientfake.NewFakeClients(&clientfake.Options{
		Scheme: testScheme(t),
		Objs:   objs,
	})}
}

func tenantMember(name string, scope string) *userv1.User {
	return &userv1.User{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: userv1.UserSpec{
			ScopeBindings: []userv1.ScopeBinding{
				{ScopeType: userv1.TenantScope, ScopeName: scope, Role: constants.TenantAdmin},
			},
		},
	}
}

func platformAdmin(name string) *userv1.User {
	return &userv1.User{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     userv1.UserStatus{PlatformAdmin: true},
	}
}

// call issues one request against a route whose caller the authentication
// middleware would have set, which is how the guards read it in production.
func call(h *handler, method, path, caller, body string, guards ...gin.HandlerFunc) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)

	engine := gin.New()
	handlers := []gin.HandlerFunc{func(c *gin.Context) {
		c.Set(constants.UserName, caller)
	}}
	handlers = append(handlers, guards...)
	// The route is registered without its query string, which is what gin
	// matches on; the request carries it.
	engine.Handle(method, strings.SplitN(path, "?", 2)[0], handlers...)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	return recorder
}

func bindingBody(label, value, role, user string) string {
	return fmt.Sprintf(`{
	  "metadata": {"labels": {%q: %q}},
	  "subjects": [{"kind": "User", "name": %q}],
	  "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": %q}
	}`, label, value, user, role)
}

// Writing permissions is not like reading them: a read tells you what you may
// do, and a write decides what everybody may do.
func TestTheRoleEndpointIsForPlatformAdministrators(t *testing.T) {
	member := tenantMember("alice", "t1")
	admin := platformAdmin("root")
	h := newHandlerFor(t, member, admin)

	reached := func(c *gin.Context) { c.Status(http.StatusOK) }
	guard := h.requirePlatformAdmin()

	tests := []struct {
		name   string
		caller string
		status int
	}{
		{name: "a platform administrator", caller: "root", status: http.StatusOK},
		{name: "a tenant member", caller: "alice", status: http.StatusForbidden},
		{name: "a caller the platform does not know", caller: "nobody", status: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := call(h, http.MethodPut, "/roles/r", tt.caller, `{"scope":"platform","items":{}}`, guard, reached)
			if recorder.Code != tt.status {
				t.Errorf("status = %d (%s), want %d", recorder.Code, recorder.Body.String(), tt.status)
			}
		})
	}
}

// A binding grants a role inside a scope, so the caller has to administer that
// scope — and the one nesting the platform has is a tenant over its projects.
func TestABindingNeedsAuthorityOverItsScope(t *testing.T) {
	alice := tenantMember("alice", "t1")
	bob := tenantMember("bob", "t2")
	root := platformAdmin("root")
	target := &userv1.User{ObjectMeta: metav1.ObjectMeta{Name: "carol"}}

	projectOfT1 := &tenantv1.Project{ObjectMeta: metav1.ObjectMeta{
		Name:   "p1",
		Labels: map[string]string{constants.TenantLabel: "t1"},
	}}
	projectOfT2 := &tenantv1.Project{ObjectMeta: metav1.ObjectMeta{
		Name:   "p2",
		Labels: map[string]string{constants.TenantLabel: "t2"},
	}}

	tests := []struct {
		name   string
		caller string
		label  string
		value  string
		status int
	}{
		{
			name:   "a tenant administrator binding into their own tenant",
			caller: "alice", label: constants.TenantLabel, value: "t1",
			status: http.StatusOK,
		},
		{
			name:   "a tenant administrator binding into another tenant",
			caller: "alice", label: constants.TenantLabel, value: "t2",
			status: http.StatusForbidden,
		},
		{
			name:   "a tenant administrator binding into a project of their own tenant",
			caller: "alice", label: constants.ProjectLabel, value: "p1",
			status: http.StatusOK,
		},
		{
			name:   "a tenant administrator binding into a project of another tenant",
			caller: "alice", label: constants.ProjectLabel, value: "p2",
			status: http.StatusForbidden,
		},
		{
			name:   "a platform administrator binding anywhere",
			caller: "root", label: constants.TenantLabel, value: "t2",
			status: http.StatusOK,
		},
		{
			name:   "a tenant member with no authority binding at all",
			caller: "carol", label: constants.TenantLabel, value: "t1",
			status: http.StatusForbidden,
		},
		{
			name:   "a binding that names no scope",
			caller: "root", label: "kubecube.io/nothing", value: "x",
			status: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A handler per case: a binding that succeeds writes to the target
			// user, and a case that ran before this one would otherwise have
			// granted the authority this one is asking about.
			h := newHandlerFor(t, alice, bob, root, target, projectOfT1, projectOfT2)

			body := bindingBody(tt.label, tt.value, constants.TenantAdmin, "carol")
			recorder := call(h, http.MethodPost, "/bindings", tt.caller, body, h.createBinds)

			if recorder.Code != tt.status {
				t.Errorf("status = %d (%s), want %d", recorder.Code, recorder.Body.String(), tt.status)
			}
		})
	}
}

// Removing a binding is the same authority as making one.
func TestDeletingABindingNeedsTheSameAuthority(t *testing.T) {
	alice := tenantMember("alice", "t1")

	h := newHandlerFor(t, alice,
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "carol-t2",
				Namespace: "kubecube",
				Labels:    map[string]string{constants.TenantLabel: "t2"},
			},
			Subjects: []rbacv1.Subject{{Kind: "User", Name: "carol"}},
			RoleRef:  rbacv1.RoleRef{Kind: "ClusterRole", Name: constants.TenantAdmin},
		},
	)

	recorder := call(h, http.MethodDelete, "/bindings?name=carol-t2&namespace=kubecube", "alice", "", h.deleteBinds)
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d (%s), want 403", recorder.Code, recorder.Body.String())
	}
}
