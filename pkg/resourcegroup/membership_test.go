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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

const groupNS = "kubecube-project-demo-space1"

const groupUID = "8f2c-4d91"

func obj(apiVersion, kind, namespace, name string, labels map[string]string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]interface{}{
			"name": name,
		},
	}}
	if namespace != "" {
		_ = unstructured.SetNestedField(o.Object, namespace, "metadata", "namespace")
	}
	if len(labels) > 0 {
		l := make(map[string]interface{}, len(labels))
		for k, v := range labels {
			l[k] = v
		}
		_ = unstructured.SetNestedMap(o.Object, l, "metadata", "labels")
	}
	return o
}

func group() Group {
	return Group{UID: groupUID, Namespace: groupNS}
}

func TestDeclarationAllowsAnOrdinaryObject(t *testing.T) {
	d := Declaration{
		Group:  group(),
		Object: obj("v1", "ConfigMap", groupNS, "settings", nil),
	}

	if reason := d.Check(); reason != "" {
		t.Fatalf("Check = %q, want the declaration allowed", reason)
	}
}

func TestDeclarationRefusesClusterScopedObjects(t *testing.T) {
	for _, o := range []*unstructured.Unstructured{
		obj("rbac.authorization.k8s.io/v1", "ClusterRole", "", "reader", nil),
		obj("v1", "Namespace", "", "someone-elses", nil),
	} {
		d := Declaration{Group: group(), Object: o}
		if reason := d.Check(); !strings.Contains(reason, "cluster-scoped") {
			t.Errorf("%s: Check = %q, want a cluster-scoped refusal", o.GetKind(), reason)
		}
	}
}

func TestDeclarationRefusesAnotherNamespace(t *testing.T) {
	d := Declaration{
		Group:  group(),
		Object: obj("v1", "ConfigMap", "somewhere-else", "settings", nil),
	}

	reason := d.Check()
	if !strings.Contains(reason, "somewhere-else") || !strings.Contains(reason, groupNS) {
		t.Fatalf("Check = %q, want it to name both namespaces", reason)
	}
}

// A copy the platform materialized into a namespace is a derivation, not a
// member: declaring it would put the platform's own fan-out inside a group's
// scope and let an agent reach objects nobody put there.
func TestDeclarationRefusesMaterializedCopies(t *testing.T) {
	d := Declaration{
		Group: group(),
		Object: obj("v1", "ConfigMap", groupNS, "alert-policy", map[string]string{
			constants.MaterializedFromLabel: "kubecube-project-demo",
		}),
	}

	reason := d.Check()
	if !strings.Contains(reason, "materialized") {
		t.Fatalf("Check = %q, want a materialized-copy refusal", reason)
	}
}

func TestDeclarationRefusesASecondGroup(t *testing.T) {
	tests := []struct {
		name       string
		object     *unstructured.Unstructured
		declaredBy map[Ref]string
	}{
		{
			name:   "the label names another group",
			object: obj("v1", "ConfigMap", groupNS, "settings", map[string]string{Label: "another-group-uid"}),
		},
		{
			name:       "the platform records another group",
			object:     obj("v1", "ConfigMap", groupNS, "settings", nil),
			declaredBy: map[Ref]string{RefOf(obj("v1", "ConfigMap", groupNS, "settings", nil)): "another-group-uid"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Declaration{Group: group(), Object: tt.object, DeclaredBy: tt.declaredBy}
			if reason := d.Check(); !strings.Contains(reason, "at most one group") {
				t.Fatalf("Check = %q, want a conflict refusal", reason)
			}
		})
	}
}

func TestDeclarationIsIdempotentForItsOwnGroup(t *testing.T) {
	o := obj("v1", "ConfigMap", groupNS, "settings", map[string]string{Label: groupUID})
	d := Declaration{
		Group:      group(),
		Object:     o,
		DeclaredBy: map[Ref]string{RefOf(o): groupUID},
	}

	if reason := d.Check(); reason != "" {
		t.Fatalf("Check = %q, want re-declaring into the same group allowed", reason)
	}
}

func TestDeclarationRefusesDenylistedKinds(t *testing.T) {
	pvc := Declaration{
		Group:  group(),
		Object: obj("v1", "PersistentVolumeClaim", groupNS, "data", nil),
	}
	if reason := pvc.Check(); !strings.Contains(reason, "PersistentVolumeClaim") {
		t.Errorf("Check = %q, want the claim refused", reason)
	}

	narrowed := Declaration{
		Group: Group{
			UID:         groupUID,
			Namespace:   groupNS,
			DeniedKinds: []schema.GroupKind{{Kind: "Secret"}},
		},
		Object: obj("v1", "Secret", groupNS, "credentials", nil),
	}
	if reason := narrowed.Check(); !strings.Contains(reason, "denylist") {
		t.Errorf("Check = %q, want the group's own denylist to refuse it", reason)
	}
}

func TestDeclarationHonoursAllowedKinds(t *testing.T) {
	g := Group{
		UID:          groupUID,
		Namespace:    groupNS,
		AllowedKinds: []schema.GroupKind{{Group: "apps", Kind: "Deployment"}},
	}

	allowed := Declaration{Group: g, Object: obj("apps/v1", "Deployment", groupNS, "web", nil)}
	if reason := allowed.Check(); reason != "" {
		t.Fatalf("Check = %q, want the allowed kind accepted", reason)
	}

	refused := Declaration{Group: g, Object: obj("v1", "ConfigMap", groupNS, "settings", nil)}
	reason := refused.Check()
	if !strings.Contains(reason, "allowed kinds") || !strings.Contains(reason, "apps/Deployment") {
		t.Fatalf("Check = %q, want it to name the allowed kinds", reason)
	}
}

type fakeGraph struct {
	owners map[Ref][]Ref
	groups map[Ref]string
}

func (g fakeGraph) Owners(ref Ref) []Ref { return g.owners[ref] }

func (g fakeGraph) Group(ref Ref) (string, bool) {
	uid, ok := g.groups[ref]
	return uid, ok
}

func ref(kind, name string) Ref {
	return Ref{Kind: kind, Namespace: groupNS, Name: name}
}

func TestClosureWalksTheOwnerChain(t *testing.T) {
	deployment := ref("Deployment", "web")
	replicaset := ref("ReplicaSet", "web-6d4f")
	pod := ref("Pod", "web-6d4f-x2k")

	graph := fakeGraph{owners: map[Ref][]Ref{
		deployment: {replicaset},
		replicaset: {pod},
	}}

	members, conflicts := Closure([]Ref{deployment}, groupUID, graph)

	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %v, want none", conflicts)
	}
	if len(members) != 2 {
		t.Fatalf("members = %v, want the replicaset and the pod", members)
	}
	for _, m := range members {
		if m.Class != ClassDerived {
			t.Errorf("%s: class = %q, want derived", m.Ref, m.Class)
		}
	}
	if members[0].Ref != replicaset || members[1].Ref != pod {
		t.Errorf("members = %v, want the walk to reach the pod through the replicaset", members)
	}
}

func TestClosureNeverCrossesANamespace(t *testing.T) {
	deployment := ref("Deployment", "web")
	elsewhere := Ref{Kind: "ReplicaSet", Namespace: "another-space", Name: "web-6d4f"}

	graph := fakeGraph{owners: map[Ref][]Ref{deployment: {elsewhere}}}

	members, _ := Closure([]Ref{deployment}, groupUID, graph)
	if len(members) != 0 {
		t.Fatalf("members = %v, want an object in another namespace left out", members)
	}
}

func TestClosureStopsAtAnotherGroupsMember(t *testing.T) {
	deployment := ref("Deployment", "web")
	claimed := ref("ReplicaSet", "web-6d4f")

	graph := fakeGraph{
		owners: map[Ref][]Ref{deployment: {claimed}},
		groups: map[Ref]string{claimed: "another-group-uid"},
	}

	members, conflicts := Closure([]Ref{deployment}, groupUID, graph)

	if len(members) != 0 {
		t.Fatalf("members = %v, want another group's member left out", members)
	}
	if len(conflicts) != 1 || conflicts[0].At != claimed || conflicts[0].Group != "another-group-uid" {
		t.Fatalf("conflicts = %v, want the conflict reported", conflicts)
	}
}

func TestClosureKeepsItsOwnDeclaredMembersOut(t *testing.T) {
	// A StatefulSet's claim is declared and its pods are derived; the walk must
	// not list the declared claim a second time as a derived member.
	statefulset := ref("StatefulSet", "db")
	claim := ref("PersistentVolumeClaim", "data-db-0")
	pod := ref("Pod", "db-0")

	graph := fakeGraph{owners: map[Ref][]Ref{
		statefulset: {claim, pod},
	}}

	members, _ := Closure([]Ref{statefulset, claim}, groupUID, graph)

	if len(members) != 1 || members[0].Ref != pod {
		t.Fatalf("members = %v, want only the pod", members)
	}
}

func TestClosureTerminatesOnACycle(t *testing.T) {
	a := ref("ConfigMap", "a")
	b := ref("ConfigMap", "b")

	graph := fakeGraph{owners: map[Ref][]Ref{a: {b}, b: {a}}}

	members, _ := Closure([]Ref{a}, groupUID, graph)
	if len(members) != 1 || members[0].Ref != b {
		t.Fatalf("members = %v, want the walk to terminate after b", members)
	}
}

func TestStrippedReportsMembersThatLostTheLabel(t *testing.T) {
	kept := ref("Deployment", "web")
	lost := ref("ConfigMap", "settings")

	stripped := Stripped([]Ref{kept, lost}, []Ref{kept})

	if len(stripped) != 1 || stripped[0] != lost {
		t.Fatalf("Stripped = %v, want the object that lost the label", stripped)
	}
	if got := Stripped([]Ref{kept}, []Ref{kept}); len(got) != 0 {
		t.Fatalf("Stripped = %v, want nothing reported when every member is still labelled", got)
	}
}

func TestOwnerGraphIndexesOwnerReferencesTheOtherWayRound(t *testing.T) {
	deployment := obj("apps/v1", "Deployment", groupNS, "web", map[string]string{Label: groupUID})
	replicaset := obj("apps/v1", "ReplicaSet", groupNS, "web-6d4f", nil)
	pod := obj("v1", "Pod", groupNS, "web-6d4f-x2k", nil)

	// Owner references point from a child to its owner and carry no namespace.
	setOwner(replicaset, "apps/v1", "Deployment", "web")
	setOwner(pod, "apps/v1", "ReplicaSet", "web-6d4f")

	graph := NewOwnerGraph([]*unstructured.Unstructured{deployment, replicaset, pod})

	members, conflicts := Closure([]Ref{RefOf(deployment)}, groupUID, graph)

	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %v, want none", conflicts)
	}
	if len(members) != 2 {
		t.Fatalf("members = %v, want the replicaset and the pod", members)
	}
	if members[1].Ref != RefOf(pod) {
		t.Errorf("members = %v, want the walk to reach the pod", members)
	}
}

func setOwner(child *unstructured.Unstructured, apiVersion, kind, name string) {
	_ = unstructured.SetNestedSlice(child.Object, []interface{}{
		map[string]interface{}{
			"apiVersion": apiVersion,
			"kind":       kind,
			"name":       name,
			"uid":        name + "-uid",
		},
	}, "metadata", "ownerReferences")
}
