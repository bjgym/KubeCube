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

package resourcegroup

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

func labelled(apiVersion, kind, namespace, name, uid string) *unstructured.Unstructured {
	return obj(apiVersion, kind, namespace, name, map[string]string{Label: uid})
}

func TestTakeInventory(t *testing.T) {
	deployment := labelled("apps/v1", "Deployment", groupNS, "web", groupUID)
	replicaset := obj("apps/v1", "ReplicaSet", groupNS, "web-6d4f", nil)
	pod := obj("v1", "Pod", groupNS, "web-6d4f-x2k", nil)
	setOwner(replicaset, "apps/v1", "Deployment", "web")
	setOwner(pod, "apps/v1", "ReplicaSet", "web-6d4f")

	unrelated := obj("v1", "ConfigMap", groupNS, "someone-elses", nil)
	otherGroup := labelled("v1", "ConfigMap", groupNS, "theirs", "another-group-uid")

	inventory := TakeInventory(
		[]*unstructured.Unstructured{deployment, replicaset, pod, unrelated, otherGroup},
		groupUID,
	)

	if len(inventory.Declared) != 1 || inventory.Declared[0] != RefOf(deployment) {
		t.Errorf("declared = %v, want only the labelled Deployment", inventory.Declared)
	}
	if len(inventory.Derived) != 2 {
		t.Fatalf("derived = %v, want the ReplicaSet and the Pod", inventory.Derived)
	}
	if inventory.Derived[1] != RefOf(pod) {
		t.Errorf("derived = %v, want the walk to reach the Pod", inventory.Derived)
	}
	if len(inventory.Conflicts) != 0 {
		t.Errorf("conflicts = %v, want none", inventory.Conflicts)
	}

	members := inventory.Members()
	if len(members) != 3 {
		t.Errorf("members = %v, want declared and derived together", members)
	}
}

func TestTakeInventoryReportsAConflict(t *testing.T) {
	deployment := labelled("apps/v1", "Deployment", groupNS, "web", groupUID)
	replicaset := labelled("apps/v1", "ReplicaSet", groupNS, "web-6d4f", "another-group-uid")
	setOwner(replicaset, "apps/v1", "Deployment", "web")

	inventory := TakeInventory([]*unstructured.Unstructured{deployment, replicaset}, groupUID)

	if len(inventory.Derived) != 0 {
		t.Errorf("derived = %v, want another group's member left out", inventory.Derived)
	}
	if len(inventory.Conflicts) != 1 || inventory.Conflicts[0].Group != "another-group-uid" {
		t.Fatalf("conflicts = %v, want the conflict reported", inventory.Conflicts)
	}
}

func TestTakeInventoryNeverCrossesANamespace(t *testing.T) {
	deployment := labelled("apps/v1", "Deployment", groupNS, "web", groupUID)
	elsewhere := obj("v1", "Pod", "another-space", "web-6d4f-x2k", nil)
	setOwner(elsewhere, "apps/v1", "Deployment", "web")

	inventory := TakeInventory([]*unstructured.Unstructured{deployment, elsewhere}, groupUID)

	if len(inventory.Derived) != 0 {
		t.Errorf("derived = %v, want an object in another namespace left out", inventory.Derived)
	}
}

func TestTakeInventoryOfNothing(t *testing.T) {
	inventory := TakeInventory(nil, groupUID)

	if len(inventory.Members()) != 0 || len(inventory.Conflicts) != 0 {
		t.Errorf("inventory = %+v, want an empty one", inventory)
	}
}

func TestDeriveEnvironment(t *testing.T) {
	dev := map[string]string{constants.EnvironmentLabel: "dev"}
	staging := map[string]string{constants.EnvironmentLabel: "staging"}
	prod := map[string]string{constants.EnvironmentLabel: "prod"}
	unlabelled := map[string]string{"kubecube.io/namespace-owner": "project:demo"}
	unknown := map[string]string{constants.EnvironmentLabel: "production"}

	tests := []struct {
		name   string
		labels []map[string]string
		want   appv1.Environment
	}{
		{
			name:   "the namespace decides first",
			labels: []map[string]string{dev, prod, prod},
			want:   appv1.EnvironmentDev,
		},
		{
			name:   "the project decides when the namespace says nothing",
			labels: []map[string]string{unlabelled, staging, dev},
			want:   appv1.EnvironmentStaging,
		},
		{
			name:   "the cluster decides when nothing narrower does",
			labels: []map[string]string{unlabelled, unlabelled, dev},
			want:   appv1.EnvironmentDev,
		},
		{
			name:   "nothing labelled is production",
			labels: []map[string]string{unlabelled, unlabelled, unlabelled},
			want:   appv1.EnvironmentProd,
		},
		{
			name:   "no labels at all is production",
			labels: nil,
			want:   appv1.EnvironmentProd,
		},
		{
			name:   "a value this platform does not know is not trusted",
			labels: []map[string]string{unknown, unknown, unknown},
			want:   appv1.EnvironmentProd,
		},
		{
			name:   "a namespace marked production stays production",
			labels: []map[string]string{prod, dev, dev},
			want:   appv1.EnvironmentProd,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveEnvironment(tt.labels...); got != tt.want {
				t.Errorf("DeriveEnvironment = %q, want %q", got, tt.want)
			}
		})
	}
}
