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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Graph is what the closure walk needs to know about a cluster. It is an
// interface rather than a client so the rules below can be tested without one,
// and so the same walk serves the pivot, which holds the group, and the member
// cluster, which holds the objects.
type Graph interface {
	// Owners returns the objects a given object owns.
	Owners(ref Ref) []Ref

	// Group returns the group an object declares, if it declares one.
	Group(ref Ref) (string, bool)
}

// OwnerGraph answers the walk from a set of objects, which is what a controller
// holds after a list.
//
// Ownership is read from ownerReferences, which point from a child to its
// owner, so the graph indexes them the other way round. An owner reference
// carries no namespace, and Kubernetes requires an owner to be in the same
// namespace as the object it owns, so the child's namespace is the owner's.
type OwnerGraph struct {
	children map[Ref][]Ref
	declared map[Ref]string
}

// NewOwnerGraph indexes a set of objects by what each one owns.
func NewOwnerGraph(objs []*unstructured.Unstructured) *OwnerGraph {
	g := &OwnerGraph{
		children: make(map[Ref][]Ref, len(objs)),
		declared: make(map[Ref]string, len(objs)),
	}

	for _, obj := range objs {
		if obj == nil {
			continue
		}

		child := RefOf(obj)
		if uid, ok := GroupOf(obj); ok {
			g.declared[child] = uid
		}

		for _, owner := range obj.GetOwnerReferences() {
			parent := Ref{
				APIVersion: owner.APIVersion,
				Kind:       owner.Kind,
				Namespace:  child.Namespace,
				Name:       owner.Name,
			}
			g.children[parent] = append(g.children[parent], child)
		}
	}

	return g
}

func (g *OwnerGraph) Owners(ref Ref) []Ref {
	return g.children[ref]
}

func (g *OwnerGraph) Group(ref Ref) (string, bool) {
	uid, ok := g.declared[ref]
	return uid, ok
}

// Conflict is a walk that stopped because it met another group's member. It is
// reported rather than resolved: two groups claiming one object means one of
// the two declarations is wrong, and only a person knows which.
type Conflict struct {
	From  Ref
	At    Ref
	Group string
}

// Closure returns the derived members of one group: the objects reachable
// through the ownerReference chain of a declared member.
//
// Three rules keep the walk from widening a scope. It never crosses a
// namespace, because a group covers one and an owner reference that leaves it
// would extend the group past what it was declared to cover. It stops at an
// object another group declared, and reports the conflict instead of claiming
// the object. And an object it cannot place is left out rather than guessed at:
// a failed inference narrows a scope, never widens it.
func Closure(declared []Ref, groupUID string, graph Graph) ([]Member, []Conflict) {
	var (
		members   []Member
		conflicts []Conflict
	)

	seen := make(map[Ref]bool, len(declared))
	queue := make([]Ref, 0, len(declared))
	for _, ref := range declared {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		queue = append(queue, ref)
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, owned := range graph.Owners(current) {
			if seen[owned] {
				continue
			}
			if owned.Namespace != current.Namespace {
				continue
			}
			if uid, ok := graph.Group(owned); ok && uid != groupUID {
				conflicts = append(conflicts, Conflict{From: current, At: owned, Group: uid})
				continue
			}

			seen[owned] = true
			members = append(members, Member{Class: ClassDerived, Ref: owned})
			queue = append(queue, owned)
		}
	}

	return members, conflicts
}

// Stripped reports the declared members that no longer carry the label.
//
// Losing the label takes an object out of the group's scope, which is why this
// is reported rather than repaired: re-labelling would fight whatever removed
// it — a CI run re-applying a manifest without the label is the ordinary case —
// and the object's owner is the one who decides whether it still belongs.
func Stripped(previous, current []Ref) []Ref {
	present := make(map[Ref]bool, len(current))
	for _, ref := range current {
		present[ref] = true
	}

	var stripped []Ref
	for _, ref := range previous {
		if !present[ref] {
			stripped = append(stripped, ref)
		}
	}

	return stripped
}
