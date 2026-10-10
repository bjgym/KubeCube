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

	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
)

// Verb is one operation a request asks for.
//
// The list is longer than the verbs RBAC names, because the routes that reach an
// object without naming it are the ones a scope check forgets: logs, exec,
// attach, portforward, scale and evict all act on a pod or a workload, and a
// rule that only knows get and update lets every one of them through.
type Verb string

const (
	VerbGet              Verb = "get"
	VerbList             Verb = "list"
	VerbWatch            Verb = "watch"
	VerbCreate           Verb = "create"
	VerbUpdate           Verb = "update"
	VerbPatch            Verb = "patch"
	VerbDelete           Verb = "delete"
	VerbDeleteCollection Verb = "deletecollection"
	VerbLogs             Verb = "logs"
	VerbExec             Verb = "exec"
	VerbAttach           Verb = "attach"
	VerbPortForward      Verb = "portforward"
	VerbScale            Verb = "scale"
	VerbEvict            Verb = "evict"
)

// known reports whether a verb is one this rule understands. An unknown verb is
// refused rather than treated as a read, because the verbs that are not listed
// here are the ones nobody thought about.
func (v Verb) known() bool {
	switch v {
	case VerbGet, VerbList, VerbWatch, VerbCreate, VerbUpdate, VerbPatch,
		VerbDelete, VerbDeleteCollection, VerbLogs, VerbExec, VerbAttach,
		VerbPortForward, VerbScale, VerbEvict:
		return true
	default:
		return false
	}
}

// writes reports whether a verb changes something. Exec, attach and portforward
// are writes: they reach into a running container rather than reading a record
// of it.
func (v Verb) writes() bool {
	switch v {
	case VerbCreate, VerbUpdate, VerbPatch, VerbDelete, VerbDeleteCollection,
		VerbExec, VerbAttach, VerbPortForward, VerbScale, VerbEvict:
		return true
	default:
		return false
	}
}

// Request is one thing a subject wants to do to one object.
type Request struct {
	Verb Verb
	Ref  Ref
}

// Access is the answer. Decided reports whether the agent rules had an opinion
// at all: a subject that is not an agent session is none of this package's
// business, and saying so is not the same as allowing it.
type Access struct {
	Decided bool
	Allowed bool
	Reason  string
}

// Scope is what one agent session was granted, together with the membership the
// platform computed for its group.
//
// Everything here narrows. The caller resolves the session from a token and the
// group from the request, and the rules below can only refuse what that
// resolution would have allowed.
type Scope struct {
	// Session is the agent session's UID. Empty means the subject is not an
	// agent, and this package abstains.
	Session string

	// GroupUID is the group the session is bound to.
	GroupUID string

	// GroupNamespace is the one namespace the group covers.
	GroupNamespace string

	// SandboxNamespace is the one namespace the session may write in.
	SandboxNamespace string

	// Mode is what the session may do. An unknown mode allows nothing.
	Mode appv1.SessionMode

	// Members is the group's membership as the platform computed it: the
	// declared members and the objects reached through their owner chain.
	Members []Ref
}

// Decide answers whether a subject may act on one object.
//
// The rules are about where a write lands and what a read may see, and nothing
// here is a substitute for the platform's own authorization: a session reaches
// an object only if its owner could, and this decides whether it reaches this
// one at all.
func (s Scope) Decide(req Request) Access {
	if s.Session == "" {
		return Access{Decided: false}
	}

	refuse := func(reason string) Access {
		return Access{Decided: true, Reason: fmt.Sprintf("%s %s", req.Ref, reason)}
	}
	allow := func(reason string) Access {
		return Access{Decided: true, Allowed: true, Reason: fmt.Sprintf("%s %s", req.Ref, reason)}
	}

	if !req.Verb.known() {
		return refuse(fmt.Sprintf("is reached with the verb %q, which this rule does not know", req.Verb))
	}

	if req.Ref.Namespace == "" {
		return refuse("is cluster-scoped, and an agent session reaches namespaced objects only")
	}

	// A Secret's contents are not readable by an agent session anywhere,
	// including inside its own sandbox: the session may have applied the
	// manifest, but the values a chart filled in are not its to read.
	if !req.Verb.writes() && req.Ref.Kind == "Secret" {
		return refuse("is a Secret, whose contents an agent session does not read")
	}

	if req.Verb.writes() {
		if s.Mode != appv1.SessionModeSandbox {
			return refuse(fmt.Sprintf("is written by a %s session, which writes nowhere", s.Mode))
		}
		if req.Ref.Namespace != s.SandboxNamespace {
			return refuse(fmt.Sprintf("is outside the session's sandbox %q", s.SandboxNamespace))
		}
		return allow("is inside the session's sandbox")
	}

	if req.Ref.Namespace == s.SandboxNamespace {
		return allow("is inside the session's sandbox")
	}
	if s.isMember(req.Ref) {
		return allow("is a member of the session's group")
	}

	return refuse(fmt.Sprintf("is not a member of group %s", s.GroupUID))
}

func (s Scope) isMember(ref Ref) bool {
	for _, member := range s.Members {
		if member == ref {
			return true
		}
	}
	return false
}
