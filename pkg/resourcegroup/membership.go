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

// Package resourcegroup owns who is in a resource group: what a declaration has
// to satisfy, which objects a declared member brings with it, and what happens
// when a member loses its label.
//
// A group does not own its members' lifecycle. It records that they belong
// together and it is the scope key an agent session is bounded by, so the rules
// here are about membership and not about applying anything: the platform never
// creates, updates or deletes a member, and nothing here writes to a cluster.
package resourcegroup

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecube-io/kubecube/pkg/utils/constants"
	"github.com/kubecube-io/kubecube/pkg/utils/kinds"
)

// Label is what a declared member carries. Its value is the group UID rather
// than the group name: a name can be deleted and recreated and a UID cannot, so
// recreating a name inherits neither members nor scope.
const Label = "kubecube.io/resource-group"

// Class is what an object is to a group.
type Class string

const (
	// ClassDeclared is an object a human brought into the group.
	ClassDeclared Class = "declared"

	// ClassDerived is an object reached through the ownerReference chain of a
	// declared member. It carries no label of its own — a Pod or a ReplicaSet
	// cannot be labelled by whoever created its owner — so it is inferred, and
	// an inference that fails leaves the object out of the group.
	ClassDerived Class = "derived"
)

// Ref identifies one object.
type Ref struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
}

func (r Ref) String() string {
	name := r.Name
	if r.Namespace != "" {
		name = r.Namespace + "/" + r.Name
	}
	return fmt.Sprintf("%s %s", r.Kind, name)
}

// RefOf reads the identity of an object.
func RefOf(obj *unstructured.Unstructured) Ref {
	return Ref{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
	}
}

// GroupOf returns the group an object declares, if it declares one.
func GroupOf(obj metav1.Object) (string, bool) {
	uid := obj.GetLabels()[Label]
	return uid, uid != ""
}

// Member is one object in a group.
type Member struct {
	Class Class
	Ref   Ref
}

// Group is the identity a declaration is checked against.
type Group struct {
	// UID is what members carry under Label.
	UID string

	// Namespace is the one namespace the group covers. One group covers one
	// cluster and one namespace, which is what keeps its scope expressible as a
	// label selector.
	Namespace string

	// AllowedKinds, when set, is the only set of kinds a member may be.
	AllowedKinds []schema.GroupKind

	// DeniedKinds narrows further. It can only narrow: the rules below hold
	// whatever a group asks for.
	DeniedKinds []schema.GroupKind
}

// denylistedKinds are namespaced kinds the platform refuses as members. A
// PersistentVolumeClaim is namespaced, so no cluster-scoped rule catches it,
// and it is refused because a member is a thing a group may show, copy into a
// sandbox and authorize against, and a claim is none of those safely.
var denylistedKinds = []schema.GroupKind{
	{Kind: "PersistentVolumeClaim"},
}

// Declaration is a candidate member.
type Declaration struct {
	Group Group

	// Object is the object a human wants to bring into the group.
	Object *unstructured.Unstructured

	// DeclaredBy maps an object to the group UID it already belongs to, so a
	// declaration that would put one object in two groups is refused rather
	// than resolved. The label on the object answers the same question for an
	// object the platform has seen; the map covers one whose label was stripped
	// while the platform still records it.
	DeclaredBy map[Ref]string
}

// Check returns why a declaration is refused, or "" when it is allowed. The
// reason is written for the person who asked, because a refusal that says only
// "forbidden" leaves them guessing which rule they met.
func (d Declaration) Check() string {
	if d.Object == nil {
		return "no object was given"
	}

	gvk := d.Object.GroupVersionKind()

	if kinds.IsClusterScoped(gvk.GroupKind()) {
		return "is cluster-scoped, and a group holds namespaced objects only"
	}

	if ns := d.Object.GetNamespace(); ns != d.Group.Namespace {
		return fmt.Sprintf("lives in namespace %q, and this group covers %q", ns, d.Group.Namespace)
	}

	if from := d.Object.GetLabels()[constants.MaterializedFromLabel]; from != "" {
		return fmt.Sprintf("is a copy the platform materialized from %s, and a copy is never a member", from)
	}

	if uid, ok := GroupOf(d.Object); ok && uid != d.Group.UID {
		return alreadyDeclared(uid)
	}
	if uid, ok := d.DeclaredBy[RefOf(d.Object)]; ok && uid != d.Group.UID {
		return alreadyDeclared(uid)
	}

	if reason := d.refusesKind(gvk.GroupKind()); reason != "" {
		return reason
	}

	return ""
}

func alreadyDeclared(uid string) string {
	return fmt.Sprintf("already belongs to group %s, and an object belongs to at most one group", uid)
}

func (d Declaration) refusesKind(gk schema.GroupKind) string {
	for _, denied := range denylistedKinds {
		if denied == gk {
			return fmt.Sprintf("is a %s, which a group refuses as a member", gk.Kind)
		}
	}

	for _, denied := range d.Group.DeniedKinds {
		if denied == gk {
			return "is refused by this group's denylist"
		}
	}

	if len(d.Group.AllowedKinds) == 0 {
		return ""
	}

	for _, allowed := range d.Group.AllowedKinds {
		if allowed == gk {
			return ""
		}
	}

	allowed := make([]string, 0, len(d.Group.AllowedKinds))
	for _, gk := range d.Group.AllowedKinds {
		allowed = append(allowed, kinds.String(gk))
	}

	return fmt.Sprintf("is not in this group's allowed kinds (%s)", strings.Join(allowed, ", "))
}
