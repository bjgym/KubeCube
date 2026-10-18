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

package cluster

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
	"github.com/kubecube-io/kubecube/pkg/utils/env"
)

func makeNs(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

// Onboarding is the one path that hands an existing namespace and its contents
// to a project, so these cases are what keeps a namespace the platform needs, a
// namespace that is already placed, and a namespace whose labels disagree with
// each other out of a tenant.
func TestOnboardable(t *testing.T) {
	terminating := makeNs("hand-built", nil)
	now := metav1.NewTime(time.Now())
	terminating.DeletionTimestamp = &now

	tests := []struct {
		name    string
		ns      *corev1.Namespace
		tenant  string
		project string
		wantErr bool
	}{
		{
			name:    "a plain unmanaged namespace can be onboarded",
			ns:      makeNs("hand-built", nil),
			tenant:  "t1",
			project: "p1",
		},
		{
			name: "a namespace carrying unrelated labels can be onboarded",
			ns: makeNs("hand-built", map[string]string{
				"kubernetes.io/metadata.name": "hand-built",
			}),
			tenant:  "t1",
			project: "p1",
		},
		{
			name:    "a namespace that does not exist is refused",
			ns:      nil,
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "a request naming no project is refused",
			ns:      makeNs("hand-built", nil),
			tenant:  "t1",
			wantErr: true,
		},
		{
			name:    "a request naming no tenant is refused",
			ns:      makeNs("hand-built", nil),
			project: "p1",
			wantErr: true,
		},
		{
			name:    "a tenant's own namespace is already managed",
			ns:      makeNs("kubecube-tenant-t1", ownership.TenantLabels("t1")),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "a project's own namespace is already managed",
			ns:      makeNs("kubecube-project-p1", ownership.ProjectLabels("t1", "p1")),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "a space is already managed",
			ns:      makeNs("space-a", ownership.SpaceLabels("t1", "p1")),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name: "the derived labels without the owner they derive from are refused",
			ns: makeNs("hand-built", map[string]string{
				constants.NamespaceOwnerTenantLabel: "t1",
				constants.NamespaceOwnerLevelLabel:  "space",
			}),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name: "a project-owned namespace recording the wrong tenant is refused",
			ns: makeNs("hand-built", map[string]string{
				constants.NamespaceOwnerLabel:       ownership.Project("p1"),
				constants.NamespaceOwnerTenantLabel: "t2",
				constants.NamespaceOwnerLevelLabel:  "space",
			}),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "default is refused",
			ns:      makeNs("default", nil),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "kube-system is refused",
			ns:      makeNs("kube-system", nil),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "kube-public is refused",
			ns:      makeNs("kube-public", nil),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "kube-node-lease is refused",
			ns:      makeNs("kube-node-lease", nil),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "kubecube-system is refused",
			ns:      makeNs("kubecube-system", nil),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			// the chart installs the control plane into the release namespace,
			// so the platform's own namespace is not always kubecube-system
			name:    "the platform's own namespace is refused",
			ns:      makeNs(env.CubeNamespace(), nil),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "a namespace named like a tenant's is refused",
			ns:      makeNs("kubecube-tenant-x", nil),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "a namespace named like a project's is refused",
			ns:      makeNs("kubecube-project-x", nil),
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
		{
			name:    "a namespace being deleted is refused",
			ns:      terminating,
			tenant:  "t1",
			project: "p1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := onboardable(tt.ns, tt.tenant, tt.project)
			if tt.wantErr && err == nil {
				t.Fatalf("onboardable() = nil, want a refusal")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("onboardable() = %v, want nil", err)
			}
		})
	}
}
