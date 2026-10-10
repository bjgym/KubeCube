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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

func TestStripRetired(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: "space-a",
		Labels: map[string]string{
			ownership.Label:                             ownership.Project("p1"),
			ownership.TenantKey:                         "t1",
			ownership.LevelKey:                          string(ownership.LevelSpace),
			constants.HncTenantLabel:                    "t1",
			constants.HncProjectLabel:                   "p1",
			constants.HncIncludedNsLabel:                "true",
			"space-a" + constants.HncSuffix:             "0",
			"kubecube-project-p1" + constants.HncSuffix: "1",
			"kubernetes.io/metadata.name":               "space-a",
			"team":                                      "payments",
		},
		Annotations: map[string]string{
			constants.HncAnnotation: "kubecube-project-p1",
			"hnc.x-k8s.io/ns":       "true",
			"kubecube.io/note":      "keep me",
		},
	}}

	if removed := stripRetired(ns); removed != 7 {
		t.Errorf("removed %v fields, want 7", removed)
	}

	// the ownership labels stay: they are what every reader consults now
	for _, key := range []string{ownership.Label, ownership.TenantKey, ownership.LevelKey} {
		if _, ok := ns.Labels[key]; !ok {
			t.Errorf("the ownership label %s was removed", key)
		}
	}

	// a label the platform never wrote is not the platform's to remove
	if ns.Labels["team"] != "payments" {
		t.Error("a label written by someone else was removed")
	}
	if ns.Labels["kubernetes.io/metadata.name"] != "space-a" {
		t.Error("a label the api server maintains was removed")
	}
	if ns.Annotations["kubecube.io/note"] != "keep me" {
		t.Error("an annotation written by someone else was removed")
	}

	for _, key := range []string{
		constants.HncTenantLabel,
		constants.HncProjectLabel,
		constants.HncIncludedNsLabel,
		"space-a" + constants.HncSuffix,
		"kubecube-project-p1" + constants.HncSuffix,
	} {
		if _, ok := ns.Labels[key]; ok {
			t.Errorf("the retired label %s survived", key)
		}
	}
	for _, key := range []string{constants.HncAnnotation, "hnc.x-k8s.io/ns"} {
		if _, ok := ns.Annotations[key]; ok {
			t.Errorf("the retired annotation %s survived", key)
		}
	}
}

// A namespace with nothing to strip must report nothing, so that a reconcile
// writes only when it has something to change.
func TestStripRetiredReportsNothingOnACleanNamespace(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Labels:      ownership.SpaceLabels("t1", "p1"),
		Annotations: map[string]string{"kubecube.io/note": "keep me"},
	}}

	if removed := stripRetired(ns); removed != 0 {
		t.Errorf("removed %v fields from a clean namespace, want 0", removed)
	}
}
