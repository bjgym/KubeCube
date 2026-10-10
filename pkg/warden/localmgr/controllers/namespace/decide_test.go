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
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

func makeNs(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

// Adoption is the one place the name convention decides ownership, so these
// cases are what keeps a hand-built tree ownable — and what keeps the platform
// from inventing visibility for a namespace it cannot place.
func TestAdoption(t *testing.T) {
	handBuiltTenant := makeNs("kubecube-tenant-t1", nil)
	ownedTenant := makeNs("kubecube-tenant-t1", ownership.TenantLabels("t1"))
	ownedProject := makeNs("kubecube-project-p1", ownership.ProjectLabels("t1", "p1"))
	handBuiltProject := makeNs("kubecube-project-p1", nil)
	hncLabelledProject := makeNs("kubecube-project-p1", map[string]string{
		constants.HncProjectLabel: "p1",
		constants.HncTenantLabel:  "t1",
	})

	tests := []struct {
		name   string
		ns     *corev1.Namespace
		parent *corev1.Namespace
		want   map[string]string
		adopt  bool
	}{
		{
			name: "an owned namespace is left alone",
			ns:   ownedTenant,
		},
		{
			name:  "a namespace carrying the hnc labels has them carried across",
			ns:    hncLabelledProject,
			want:  ownership.ProjectLabels("t1", "p1"),
			adopt: true,
		},
		{
			name:  "a hand-built tenant namespace is adopted from its name",
			ns:    handBuiltTenant,
			want:  ownership.TenantLabels("t1"),
			adopt: true,
		},
		{
			name:   "a project namespace takes its tenant from the namespace it sits in",
			ns:     handBuiltProject,
			parent: handBuiltTenant,
			want:   ownership.ProjectLabels("t1", "p1"),
			adopt:  true,
		},
		{
			name:   "a project namespace under an already owned parent works too",
			ns:     handBuiltProject,
			parent: ownedTenant,
			want:   ownership.ProjectLabels("t1", "p1"),
			adopt:  true,
		},
		{
			name: "a project namespace with no parent cannot be placed",
			ns:   handBuiltProject,
		},
		{
			name:   "a namespace under an owned project inherits that project",
			ns:     makeNs("team-a", nil),
			parent: ownedProject,
			want:   ownership.ProjectLabels("t1", "p1"),
			adopt:  true,
		},
		{
			name:   "a namespace under a tenant namespace is not thereby a tenant namespace",
			ns:     makeNs("team-a", nil),
			parent: ownedTenant,
		},
		{
			name: "a plain namespace is left unmanaged",
			ns:   makeNs("default", nil),
		},
		{
			name:   "a namespace whose parent cannot be placed is left unmanaged",
			ns:     makeNs("team-a", nil),
			parent: handBuiltProject,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, adopt := Adoption(tt.ns, tt.parent)
			if adopt != tt.adopt {
				t.Fatalf("Adoption() adopt = %v, want %v", adopt, tt.adopt)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Adoption() labels = %v, want %v", got, tt.want)
			}
		})
	}
}
