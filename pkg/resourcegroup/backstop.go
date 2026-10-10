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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Sandbox is what the cluster-side rule reads off the namespace a write lands
// in.
//
// Everything the rule is scoped by is on the namespace itself, so the cluster
// needs nothing from the platform to answer: a webhook that had to ask the
// control plane would be a webhook that fails open when the control plane is
// down.
type Sandbox struct {
	// Name is the namespace.
	Name string

	// UID is the namespace's UID, so a sandbox deleted and recreated under the
	// same name is not the one a session was given.
	UID string

	// SessionUID is the session the sandbox belongs to, from the namespace's
	// own label.
	SessionUID string
}

// SandboxOf reads the sandbox facts off a namespace object.
//
// A namespace without the marker is not a sandbox, and the caller is told so
// rather than handed an empty one: a rule that read "no label" as "nothing to
// restrict" would let a write through exactly where it could not be checked.
func SandboxOf(ns *corev1.Namespace) (Sandbox, error) {
	if ns == nil {
		return Sandbox{}, fmt.Errorf("no namespace was given")
	}

	sessionUID := ns.Labels[SandboxLabel]
	if sessionUID == "" {
		return Sandbox{}, fmt.Errorf("namespace %s is not a sandbox: it carries no %s label", ns.Name, SandboxLabel)
	}

	return Sandbox{Name: ns.Name, UID: string(ns.UID), SessionUID: sessionUID}, nil
}

// Backstop is the cluster-side rule: what a write inside a sandbox may touch.
//
// It is the last line rather than the first. The API layer has already refused
// everything outside the session's sandbox, and the sandbox policy has already
// refused what a sandbox may not hold; what is left for the cluster itself to
// refuse is the one thing only the object in hand can show — a write that would
// declare membership. That is the promise the whole design rests on, and this
// is where it holds even if every layer above was bypassed.
type Backstop struct {
	// Sandbox is the namespace the writes this rule sees land in.
	Sandbox Sandbox
}

// Decide answers whether one write may proceed.
//
// old is the object as the cluster holds it, which is nil for a create; new is
// the object the request carries, which is nil for a delete. A patch arrives
// with new set to the object as it would be afterwards, so the same comparison
// answers it.
func (b Backstop) Decide(req Request, old, new *unstructured.Unstructured) error {
	if req.Ref.Namespace == "" {
		return fmt.Errorf("%s is cluster-scoped, and an agent session writes namespaced objects only", req.Ref)
	}
	if b.Sandbox.Name == "" {
		return fmt.Errorf("this rule was built without a sandbox namespace, so it can allow nothing")
	}
	if req.Ref.Namespace != b.Sandbox.Name {
		return fmt.Errorf("%s is outside the sandbox %s this rule is scoped to", req.Ref, b.Sandbox.Name)
	}
	if b.Sandbox.SessionUID == "" {
		return fmt.Errorf("sandbox %s names no session, so nothing written in it can be attributed", b.Sandbox.Name)
	}

	// A delete carries no object, so there is nothing it could declare. It is
	// also the one write that cannot put anything into a group: what a delete
	// reaches here is a sandbox object, and a member of a group lives in the
	// group's own namespace, which this rule refuses above.
	if new == nil {
		return nil
	}

	before := groupOf(old)
	after := groupOf(new)

	if before != after {
		return fmt.Errorf("%s would %s, and a person declares membership rather than an agent",
			req.Ref, describeMembershipChange(before, after))
	}

	return nil
}

// groupOf reads the group label of an object, tolerating the absent object a
// create or a delete arrives with.
func groupOf(obj *unstructured.Unstructured) string {
	if obj == nil {
		return ""
	}
	uid, _ := GroupOf(obj)
	return uid
}

func describeMembershipChange(before, after string) string {
	switch {
	case before == "" && after != "":
		return fmt.Sprintf("put the object in group %s", after)
	case before != "" && after == "":
		return fmt.Sprintf("take the object out of group %s", before)
	case before != after:
		return fmt.Sprintf("move the object from group %s to group %s", before, after)
	default:
		return "change the object's membership"
	}
}
