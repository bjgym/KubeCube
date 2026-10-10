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

// Package agentapi is the contract between the agent surface and the platform.
//
// It holds the routes the surface calls and the shape of what it sends and
// reads back, so the two halves are written against one description rather than
// two. It is deliberately small and dependency-light: the surface holds no
// cluster credential, and the platform's handlers are the only other reader.
//
// Two rules run through every route here.
//
// Nothing under Prefix names a cluster, a namespace or a group: the session's
// token says which group the call is about, and a route that took one of those
// from its caller would be a route that could be pointed at somebody else's
// group.
//
// Objects are named by kind rather than by resource. The vocabulary of a
// group's membership is kinds — a member label, an admission request and a
// manifest all name a kind — and a request that named a plural resource would
// have to be translated back before it could be compared with a member, which
// is a step that can disagree.
package agentapi

import (
	"fmt"
	"net/url"

	"github.com/kubecube-io/kubecube/pkg/resourcegroup"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// Prefix is where the agent surface's calls live. Everything under it is
// answered as the session whose token carried the call.
const Prefix = constants.ApiPathRoot + "/agent"

// Group is the group the calling session is scoped to.
func Group() string { return Prefix + "/group" }

// GroupDescription is the group's composition: what it holds, how it is doing
// and what it costs.
func GroupDescription() string { return Group() + "/description" }

// RenderMember is where a live member is turned into an applicable manifest.
func RenderMember() string { return Group() + "/render" }

// Resource is one object inside the group's namespace.
func Resource(kind, name string) (string, error) {
	segment, err := objectSegment(kind, name)
	if err != nil {
		return "", err
	}
	return Prefix + "/resources/" + segment, nil
}

// ResourceEvents is the events recorded about one object.
func ResourceEvents(kind, name string) (string, error) {
	path, err := Resource(kind, name)
	if err != nil {
		return "", err
	}
	return path + "/events", nil
}

// ResourceLogs is the log of one object.
func ResourceLogs(kind, name string) (string, error) {
	path, err := Resource(kind, name)
	if err != nil {
		return "", err
	}
	return path + "/logs", nil
}

// ValidateChange is where a change is checked without being applied: the
// server-side dry run, the policy check and the quota estimate.
func ValidateChange() string { return Prefix + "/sandbox/validate" }

// Sandbox is the calling session's sandbox.
func Sandbox() string { return Prefix + "/sandbox" }

// ChangeSets is where a sandbox's applies are recorded.
func ChangeSets() string { return Sandbox() + "/changesets" }

// ChangeSetRollback returns a sandbox to one recorded change set.
func ChangeSetRollback(name string) (string, error) {
	segment, err := escape("change set name", name)
	if err != nil {
		return "", err
	}
	return ChangeSets() + "/" + segment + "/rollback", nil
}

// SmokeTests runs a check inside the sandbox.
func SmokeTests() string { return Sandbox() + "/smoke-tests" }

// ChartVersions lists the versions of one chart the platform offers.
func ChartVersions(chart string) (string, error) {
	segment, err := escape("chart name", chart)
	if err != nil {
		return "", err
	}
	return Prefix + "/charts/" + segment + "/versions", nil
}

// ChangeRequests is where a validated change is proposed for a person to land.
func ChangeRequests() string { return Prefix + "/change-requests" }

// ObjectRef names one object inside the group's namespace.
type ObjectRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// RenderAnswer is what rendering a member answers with.
type RenderAnswer struct {
	// Manifests are applicable, with the platform's own labels and the fields
	// the server owns already dropped.
	Manifests []string `json:"manifests"`

	// Notes say what the rendering changed and why, because a sandbox is an
	// approximation of the member in exactly those places.
	Notes []string `json:"notes"`
}

// ValidateAnswer is the evidence the platform gathered for one change.
//
// It is the same evidence a reviewer reads when a change is proposed: the point
// of validating here is that what a person approves is what was checked. The
// fields are spelled out rather than embedding the rule's own type, so that a
// change to how the rules are written cannot quietly change what this API says.
type ValidateAnswer struct {
	DryRun         resourcegroup.Result `json:"dryRun"`
	Policy         resourcegroup.Result `json:"policy"`
	SmokeTest      resourcegroup.Result `json:"smokeTest"`
	QuotaEstimated bool                 `json:"quotaEstimated"`
	Notes          []string             `json:"notes"`
}

// Evidence is what these fields mean to the rules that read them.
func (a ValidateAnswer) Evidence() resourcegroup.Evidence {
	return resourcegroup.Evidence{
		DryRun:         a.DryRun,
		Policy:         a.Policy,
		SmokeTest:      a.SmokeTest,
		QuotaEstimated: a.QuotaEstimated,
	}
}

// ChangeSetAnswer identifies one recorded apply.
type ChangeSetAnswer struct {
	// Name is the change set inside the sandbox.
	Name string `json:"name"`

	// Digest is the hash of what it holds, so a proposal can name exactly the
	// content that was validated.
	Digest string `json:"digest"`
}

func objectSegment(kind, name string) (string, error) {
	escapedKind, err := escape("kind", kind)
	if err != nil {
		return "", err
	}
	escapedName, err := escape("object name", name)
	if err != nil {
		return "", err
	}
	return escapedKind + "/" + escapedName, nil
}

// escape refuses an empty segment rather than building a path without it: a
// path that silently loses a segment addresses a collection where the caller
// meant an object.
func escape(what, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("no %s was given, and a path built without it would address something else", what)
	}
	return url.PathEscape(value), nil
}
