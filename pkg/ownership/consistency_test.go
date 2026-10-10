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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A namespace's owner is a label, so renaming the namespace cannot change who
// owns it or what the readers decide. Before the migration the name was one of
// the ways ownership was resolved, which is what this pins: the same label set
// has to answer identically whatever the namespace is called.
func TestRenamingANamespaceLeavesItsOwnershipAlone(t *testing.T) {
	for _, labels := range []map[string]string{
		TenantLabels("t1"),
		ProjectLabels("t1", "p1"),
		SpaceLabels("t1", "p1"),
	} {
		before := &metav1.ObjectMeta{Name: "kubecube-tenant-t1", Labels: labels}
		after := &metav1.ObjectMeta{Name: "renamed-to-something-else", Labels: labels}

		kindBefore, nameBefore, okBefore := Of(before)
		kindAfter, nameAfter, okAfter := Of(after)
		if kindBefore != kindAfter || nameBefore != nameAfter || okBefore != okAfter {
			t.Errorf("Of() answered %q/%q/%v before the rename and %q/%q/%v after",
				kindBefore, nameBefore, okBefore, kindAfter, nameAfter, okAfter)
		}

		tenantBefore, _ := TenantOf(before)
		tenantAfter, _ := TenantOf(after)
		if tenantBefore != tenantAfter {
			t.Errorf("TenantOf() answered %q before the rename and %q after", tenantBefore, tenantAfter)
		}

		levelBefore, _ := LevelOf(before)
		levelAfter, _ := LevelOf(after)
		if levelBefore != levelAfter {
			t.Errorf("LevelOf() answered %q before the rename and %q after", levelBefore, levelAfter)
		}

		if reason := Inconsistency(after.Labels); reason != "" {
			t.Errorf("a rename made consistent labels inconsistent: %q", reason)
		}
	}
}

func TestInconsistency(t *testing.T) {
	tests := []struct {
		name    string
		labels  map[string]string
		reports bool
	}{
		{"a tenant namespace", TenantLabels("t1"), false},
		{"a project namespace", ProjectLabels("t1", "p1"), false},
		{"a space", SpaceLabels("t1", "p1"), false},
		{"an unowned namespace", map[string]string{"team": "payments"}, false},
		{"no labels at all", nil, false},

		{
			name: "the derived keys without an owner",
			labels: map[string]string{
				TenantKey: "t1",
				LevelKey:  string(LevelSpace),
			},
			reports: true,
		},
		{
			name: "a tenant owner recording another tenant",
			labels: map[string]string{
				Label:     Tenant("t1"),
				TenantKey: "t2",
				LevelKey:  string(LevelTenant),
			},
			reports: true,
		},
		{
			name: "a tenant owner recording a project level",
			labels: map[string]string{
				Label:     Tenant("t1"),
				TenantKey: "t1",
				LevelKey:  string(LevelProject),
			},
			reports: true,
		},
		{
			name: "a project owner that does not record its tenant",
			labels: map[string]string{
				Label:    Project("p1"),
				LevelKey: string(LevelSpace),
			},
			reports: true,
		},
		{
			name: "a project owner recording the tenant level",
			labels: map[string]string{
				Label:     Project("p1"),
				TenantKey: "t1",
				LevelKey:  string(LevelTenant),
			},
			reports: true,
		},
		{
			// the owner names a project, and only the level key can say whether
			// this is the project's own namespace or a space
			name: "a project owner recording the project level",
			labels: map[string]string{
				Label:     Project("p1"),
				TenantKey: "t1",
				LevelKey:  string(LevelProject),
			},
			reports: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := Inconsistency(tt.labels)
			if tt.reports && reason == "" {
				t.Error("the inconsistency was not reported")
			}
			if !tt.reports && reason != "" {
				t.Errorf("a consistent label set was reported as %q", reason)
			}
		})
	}
}
