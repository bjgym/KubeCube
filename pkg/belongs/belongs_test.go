/*
Copyright 2022 KubeCube Authors

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

package belongs

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	userv1 "github.com/kubecube-io/kubecube/pkg/apis/user/v1"
	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

func nsWith(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func makeUser(tenants, projects []string, platformAdmin bool) *userv1.User {
	u := &userv1.User{}
	u.Status.BelongTenants = tenants
	u.Status.PlatformAdmin = platformAdmin
	for _, p := range projects {
		u.Status.BelongProjectInfos = append(u.Status.BelongProjectInfos, userv1.ProjectInfo{Project: p, Tenant: "t1"})
	}
	return u
}

// A namespace is reachable through either fact it carries: the tenant it belongs
// to, or the project that owns it. Losing either half silently revokes access
// for a whole role, which is what these cases pin down.
func TestNamespaceJudgement(t *testing.T) {
	tenantMember := makeUser([]string{"t1"}, nil, false)
	projectMember := makeUser(nil, []string{"p1"}, false)
	otherTenantMember := makeUser([]string{"t2"}, nil, false)
	platformAdmin := makeUser(nil, nil, true)

	space := ownership.ProjectLabels("t1", "p1")
	tenantNs := ownership.TenantLabels("t1")

	tests := []struct {
		name string
		user *userv1.User
		obj  *corev1.Namespace
		want bool
	}{
		{"a platform admin reaches anything", platformAdmin, nsWith("default", nil), true},
		{"a tenant member reaches the tenant namespace", tenantMember, nsWith("kubecube-tenant-t1", tenantNs), true},
		{"a tenant member reaches a space through the tenant", tenantMember, nsWith("space-a", space), true},
		{"a project member reaches a space through the project", projectMember, nsWith("space-a", space), true},
		{"a project member reaches the project namespace", projectMember, nsWith("kubecube-project-p1", space), true},
		{"a project member does not reach the tenant namespace", projectMember, nsWith("kubecube-tenant-t1", tenantNs), false},
		{"another tenant's member reaches nothing", otherTenantMember, nsWith("space-a", space), false},
		{"a hand-built tenant namespace is reachable by its name", tenantMember, nsWith("kubecube-tenant-t1", nil), true},
		{"a hand-built project namespace is not reachable by its name", projectMember, nsWith("kubecube-project-p1", nil), false},
		{"an unowned namespace belongs to nobody", tenantMember, nsWith("default", nil), false},
		{
			name: "the hnc labels still decide while they are being migrated from",
			user: tenantMember,
			obj: nsWith("space-a", map[string]string{
				constants.HncTenantLabel:  "t1",
				constants.HncProjectLabel: "p1",
			}),
			want: true,
		},
		{
			name: "the hnc project label alone reaches a project member",
			user: projectMember,
			obj:  nsWith("space-a", map[string]string{constants.HncProjectLabel: "p1"}),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := namespaceJudgement(tt.user, tt.obj)
			if err != nil {
				t.Fatalf("namespaceJudgement() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("namespaceJudgement() = %v, want %v", got, tt.want)
			}
		})
	}
}
