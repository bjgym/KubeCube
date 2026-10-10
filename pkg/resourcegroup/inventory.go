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

// Inventory is what one group holds, as the platform observed it.
type Inventory struct {
	// Declared are the objects a human brought into the group.
	Declared []Ref

	// Derived are the objects reached through a declared member's owner chain.
	Derived []Ref

	// Conflicts are the walks that stopped because they met another group's
	// member. They are reported rather than resolved: two groups claiming one
	// object means one of the two declarations is wrong, and only a person
	// knows which.
	Conflicts []Conflict
}

// Members is everything the group holds, declared and derived together. It is
// what a scope check is built from, so it is one list rather than two: a rule
// that reads only the declared half would miss every pod.
func (i Inventory) Members() []Ref {
	out := make([]Ref, 0, len(i.Declared)+len(i.Derived))
	out = append(out, i.Declared...)
	out = append(out, i.Derived...)
	return out
}

// TakeInventory works out the membership of one group from a set of objects.
//
// It is the whole computation in one call — what is declared, what follows from
// it, and where the walk was refused — because the three answers come from one
// pass over the same set, and a controller that asked for them separately would
// be reading three different moments of a changing cluster.
func TakeInventory(objs []*unstructured.Unstructured, groupUID string) Inventory {
	var inventory Inventory

	for _, obj := range objs {
		if obj == nil {
			continue
		}
		if uid, ok := GroupOf(obj); ok && uid == groupUID {
			inventory.Declared = append(inventory.Declared, RefOf(obj))
		}
	}

	derived, conflicts := Closure(inventory.Declared, groupUID, NewOwnerGraph(objs))
	inventory.Derived = make([]Ref, 0, len(derived))
	for _, member := range derived {
		inventory.Derived = append(inventory.Derived, member.Ref)
	}
	inventory.Conflicts = conflicts

	return inventory
}
