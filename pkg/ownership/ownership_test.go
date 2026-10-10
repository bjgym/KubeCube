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

package ownership

import (
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

func ns(name string, labels map[string]string) *v1.Namespace {
	return &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func TestOf(t *testing.T) {
	tests := []struct {
		name     string
		obj      *v1.Namespace
		wantKind Kind
		wantName string
		wantOk   bool
	}{
		{
			name:     "owner label names a tenant",
			obj:      ns("kubecube-tenant-t1", map[string]string{Label: "tenant:t1"}),
			wantKind: KindTenant, wantName: "t1", wantOk: true,
		},
		{
			name:     "owner label names a project, which is also what a space carries",
			obj:      ns("space-a", map[string]string{Label: Project("p1")}),
			wantKind: KindProject, wantName: "p1", wantOk: true,
		},
		{
			name: "the hnc labels decide nothing once the owner label is gone",
			obj: ns("space-a", map[string]string{
				constants.HncTenantLabel:  "t1",
				constants.HncProjectLabel: "p1",
			}),
		},
		{
			name: "the tenant name prefix decides nothing on its own",
			obj:  ns("kubecube-tenant-t1", nil),
		},
		{
			name: "the project name prefix never owned anything on its own",
			obj:  ns("kubecube-project-p1", nil),
		},
		{
			name: "a plain namespace is unmanaged",
			obj:  ns("default", nil),
		},
		{
			name: "no labels at all",
			obj:  ns("", nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, name, ok := Of(tt.obj)
			if ok != tt.wantOk {
				t.Fatalf("Of() ok = %v, want %v", ok, tt.wantOk)
			}
			if kind != tt.wantKind {
				t.Errorf("Of() kind = %q, want %q", kind, tt.wantKind)
			}
			if name != tt.wantName {
				t.Errorf("Of() name = %q, want %q", name, tt.wantName)
			}
		})
	}
}

func TestSingleKindReaders(t *testing.T) {
	tenantNs := ns("kubecube-tenant-t1", map[string]string{Label: Tenant("t1")})
	projectNs := ns("space-a", map[string]string{Label: Project("p1")})
	plain := ns("default", nil)

	if name, ok := TenantOf(tenantNs); !ok || name != "t1" {
		t.Errorf("TenantOf(tenant ns) = %q, %v; want %q, true", name, ok, "t1")
	}
	if _, ok := TenantOf(projectNs); ok {
		t.Error("TenantOf(project ns) reported a tenant")
	}
	if name, ok := ProjectOf(projectNs); !ok || name != "p1" {
		t.Errorf("ProjectOf(project ns) = %q, %v; want %q, true", name, ok, "p1")
	}
	if _, ok := TenantOf(projectNs); ok {
		t.Error("TenantOf() invented a tenant for a namespace that records none")
	}

	// a project-owned namespace answers with its tenant as well, because a
	// tenant member reaches a space through their tenant membership
	owned := ns("space-a", map[string]string{Label: Project("p1"), TenantKey: "t1"})
	if name, ok := TenantOf(owned); !ok || name != "t1" {
		t.Errorf("TenantOf(project-owned ns) = %q, %v; want %q, true", name, ok, "t1")
	}
	if name, ok := ProjectOf(owned); !ok || name != "p1" {
		t.Errorf("ProjectOf(project-owned ns) = %q, %v; want %q, true", name, ok, "p1")
	}
	if _, ok := ProjectOf(tenantNs); ok {
		t.Error("ProjectOf(tenant ns) reported a project")
	}
	if Managed(plain) {
		t.Error("Managed(plain ns) = true, want false")
	}
	if !Managed(tenantNs) || !Managed(projectNs) {
		t.Error("Managed() = false for an owned namespace")
	}
	if !Is(projectNs, KindProject, "p1") {
		t.Error("Is(project ns, project, p1) = false")
	}
	if Is(projectNs, KindTenant, "p1") || Is(projectNs, KindProject, "p2") {
		t.Error("Is() matched the wrong kind or name")
	}
}

func TestLabelConstructors(t *testing.T) {
	tests := []struct {
		name       string
		labels     map[string]string
		wantKind   Kind
		wantOwner  string
		wantTenant string
		wantLevel  Level
	}{
		{"a tenant namespace", TenantLabels("t1"), KindTenant, "t1", "t1", LevelTenant},
		{"a project namespace", ProjectLabels("t1", "p1"), KindProject, "p1", "t1", LevelProject},
		{"a space", SpaceLabels("t1", "p1"), KindProject, "p1", "t1", LevelSpace},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := ns("x", tt.labels)

			kind, owner, ok := Of(obj)
			if !ok || kind != tt.wantKind || owner != tt.wantOwner {
				t.Errorf("Of() = %q, %q, %v; want %q, %q, true", kind, owner, ok, tt.wantKind, tt.wantOwner)
			}
			if tenant, ok := TenantOf(obj); !ok || tenant != tt.wantTenant {
				t.Errorf("TenantOf() = %q, %v; want %q, true", tenant, ok, tt.wantTenant)
			}
			if level, ok := LevelOf(obj); !ok || level != tt.wantLevel {
				t.Errorf("LevelOf() = %q, %v; want %q, true", level, ok, tt.wantLevel)
			}
		})
	}
}

// The selectors are what the readers migrated onto, so what they match is the
// behaviour that has to be preserved: a tenant reaches everything under it, a
// project reaches itself and its spaces, and only a space selector excludes the
// project's own namespace.
func TestSelectors(t *testing.T) {
	all := []*v1.Namespace{
		ns("kubecube-tenant-t1", TenantLabels("t1")),
		ns("kubecube-project-p1", ProjectLabels("t1", "p1")),
		ns("space-a", SpaceLabels("t1", "p1")),
		ns("kubecube-tenant-t2", TenantLabels("t2")),
		ns("default", nil),
		ns("kube-public", map[string]string{"kubernetes.io/metadata.name": "kube-public"}),
	}

	matched := func(sel labels.Selector) []string {
		names := []string{}
		for _, obj := range all {
			if sel.Matches(labels.Set(obj.GetLabels())) {
				names = append(names, obj.GetName())
			}
		}
		return names
	}

	tests := []struct {
		name string
		sel  labels.Selector
		want []string
	}{
		{
			name: "the tenant selector reaches the tenant, its project and its spaces",
			sel:  TenantSelector("t1"),
			want: []string{"kubecube-tenant-t1", "kubecube-project-p1", "space-a"},
		},
		{
			name: "the project selector reaches the project and its spaces",
			sel:  Selector(KindProject, "p1"),
			want: []string{"kubecube-project-p1", "space-a"},
		},
		{
			name: "the space selector reaches spaces without the project's own namespace",
			sel:  SpaceSelector("p1"),
			want: []string{"space-a"},
		},
		{
			name: "the managed selector reaches everything the platform owns",
			sel:  ManagedSelector(),
			want: []string{"kubecube-tenant-t1", "kubecube-project-p1", "space-a", "kubecube-tenant-t2"},
		},
		{
			// the complement, which is what a platform administrator is shown:
			// a namespace created by hand is not a failure, it is simply not the
			// platform's
			name: "the unowned selector reaches exactly the namespaces nobody owns",
			sel:  UnownedSelector(),
			want: []string{"default", "kube-public"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matched(tt.sel); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("selector matched %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValueBuildersRoundTrip(t *testing.T) {
	tests := []struct {
		value    string
		wantKind Kind
		wantName string
	}{
		{Tenant("t1"), KindTenant, "t1"},
		{Project("p1"), KindProject, "p1"},
	}
	for _, tt := range tests {
		kind, name, ok := Of(ns("x", map[string]string{Label: tt.value}))
		if !ok {
			t.Fatalf("Of(%q) reported no owner", tt.value)
		}
		if kind != tt.wantKind || name != tt.wantName {
			t.Errorf("Of(%q) = %q, %q; want %q, %q", tt.value, kind, name, tt.wantKind, tt.wantName)
		}
	}
}
