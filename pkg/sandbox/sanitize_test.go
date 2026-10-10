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

package sandbox

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubecube-io/kubecube/pkg/resourcegroup"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// A member exported into a manifest must not carry the platform's own labels:
// the membership label would be refused by the cluster when the session applies
// the manifest, because a person declares membership and an agent does not.
func TestSanitizeDropsThePlatformsLabels(t *testing.T) {
	member := obj("apps/v1", "Deployment", sandboxNS, "web")
	member.SetLabels(map[string]string{
		resourcegroup.Label:             "8f2c-4d91",
		constants.MaterializedFromLabel: "kubecube-project-demo-space1",
		"app":                           "web",
	})

	clean, notes := Sanitize(member)

	if _, ok := clean.GetLabels()[resourcegroup.Label]; ok {
		t.Error("the membership label survived, and the platform would refuse the manifest it is in")
	}
	if _, ok := clean.GetLabels()[constants.MaterializedFromLabel]; ok {
		t.Error("the materialized-from label survived, and it names a namespace the sandbox does not have")
	}
	if clean.GetLabels()["app"] != "web" {
		t.Errorf("labels = %v, want the object's own labels kept", clean.GetLabels())
	}

	if len(notes) == 0 {
		t.Fatal("no notes, want the drops that matter reported")
	}
	if !strings.Contains(strings.Join(notes, "\n"), resourcegroup.Label) {
		t.Errorf("notes = %v, want the dropped label named", notes)
	}

	if _, ok := member.GetLabels()[resourcegroup.Label]; !ok {
		t.Error("the input was mutated")
	}
}

// An object with nothing to drop keeps no empty metadata behind: an empty
// labels map is a field the server would keep for no reason.
func TestSanitizeRemovesAnEmptyLabelSet(t *testing.T) {
	onlyPlatformLabels := obj("v1", "ConfigMap", sandboxNS, "settings")
	onlyPlatformLabels.SetLabels(map[string]string{resourcegroup.Label: "8f2c-4d91"})

	clean, _ := Sanitize(onlyPlatformLabels)

	if _, ok, _ := unstructured.NestedMap(clean.Object, "metadata", "labels"); ok {
		t.Errorf("labels = %v, want the empty map removed", clean.GetLabels())
	}
}
