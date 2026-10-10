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

	"k8s.io/apimachinery/pkg/labels"
)

// A sandbox namespace is owned by the project it was created for, so that the
// readers which already work off the owner label keep working: quota admission
// selects on it, and the tenant key still answers the tenant-scoped questions.
func TestSandboxLabels(t *testing.T) {
	got := SandboxLabels("t1", "p1")

	want := map[string]string{
		Label:     "project:p1",
		TenantKey: "t1",
		LevelKey:  "sandbox",
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("label %s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("labels = %v, want exactly %v", got, want)
	}
}

func TestSandboxOwnership(t *testing.T) {
	obj := ns("kubecube-agent-sess-7f3a", SandboxLabels("t1", "p1"))

	kind, name, ok := Of(obj)
	if !ok || kind != KindProject || name != "p1" {
		t.Errorf("Of = (%q, %q, %v), want (project, p1, true)", kind, name, ok)
	}

	if tenant, ok := TenantOf(obj); !ok || tenant != "t1" {
		t.Errorf("TenantOf = (%q, %v), want (t1, true)", tenant, ok)
	}

	if !Managed(obj) {
		t.Error("Managed = false, want true: a sandbox is a namespace the platform owns")
	}

	if level, ok := LevelOf(obj); !ok || level != LevelSandbox {
		t.Errorf("LevelOf = (%q, %v), want (sandbox, true)", level, ok)
	}
}

// The space enumerations are the reason the level exists: a reader that lists a
// project's spaces must not be handed a sandbox, because a sandbox is not a
// place a user works and must not appear in the console's space list.
func TestSandboxIsNotASpace(t *testing.T) {
	sandbox := labels.Set(SandboxLabels("t1", "p1"))
	space := labels.Set(SpaceLabels("t1", "p1"))

	if SpaceSelector("p1").Matches(sandbox) {
		t.Error("SpaceSelector matches a sandbox, want no match")
	}
	if !SpaceSelector("p1").Matches(space) {
		t.Error("SpaceSelector does not match a space, want a match")
	}

	// Selecting everything a project owns still finds it, which is what quota
	// admission and the project's own views need.
	if !Selector(KindProject, "p1").Matches(sandbox) {
		t.Error("Selector(project) does not match a sandbox, want a match")
	}
}

// The ResourceQuota webhook selects on the owner label alone, so a sandbox has
// to match that selector or it would be admitted outside the project's quota.
func TestSandboxIsAdmittedByQuota(t *testing.T) {
	if !ManagedSelector().Matches(labels.Set(SandboxLabels("t1", "p1"))) {
		t.Error("ManagedSelector does not match a sandbox, want a match")
	}
	if UnownedSelector().Matches(labels.Set(SandboxLabels("t1", "p1"))) {
		t.Error("UnownedSelector matches a sandbox, want no match")
	}
}

func TestSandboxConsistency(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   bool // true when an inconsistency is expected
	}{
		{
			name:   "a sandbox written by this package is consistent",
			labels: SandboxLabels("t1", "p1"),
		},
		{
			name:   "a sandbox without its tenant is reported",
			labels: map[string]string{Label: Project("p1"), LevelKey: string(LevelSandbox)},
			want:   true,
		},
		{
			name:   "a sandbox without an owner is reported",
			labels: map[string]string{TenantKey: "t1", LevelKey: string(LevelSandbox)},
			want:   true,
		},
		{
			name:   "an unknown level is reported",
			labels: map[string]string{Label: Project("p1"), TenantKey: "t1", LevelKey: "job"},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Inconsistency(tt.labels) != ""
			if got != tt.want {
				t.Errorf("Inconsistency(%v) reported %v, want %v", tt.labels, got, tt.want)
			}
		})
	}
}
