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

// Package sandbox owns what may be applied into an agent session's sandbox: the
// policy a rendered manifest set has to pass, the cleanup a live object needs
// before it can be applied again, and the order the objects go in.
//
// It is deliberately apply-only. A sandbox is a disposable namespace, so there
// is nothing to prune, nothing to adopt and no revision to keep: teardown is
// deleting the namespace. That is what makes this cheap enough to build, and it
// is why the same code is not used to write anything into production.
package sandbox

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kubecube-io/kubecube/pkg/utils/kinds"
)

// Policy is what a manifest set must satisfy before it is applied into a
// sandbox. Every rule reads the manifest rather than the requesting subject,
// because the subject is an agent whose reach has to be bounded by the content
// it may install and not only by the namespace it may install into.
type Policy struct {
	// Namespace is the sandbox namespace. Every object must target it. An
	// object naming a different namespace is refused rather than rewritten; one
	// that names none is filled in by the applier, because a manifest exported
	// from a live object is written without one on purpose.
	Namespace string

	// AllowedRegistries, when set, is the only set of image registries a
	// container image may come from. Empty means every registry is allowed.
	AllowedRegistries []string

	// DeniedKinds adds to the built-in denylist. A group may narrow what its
	// sandboxes accept; it cannot widen it.
	DeniedKinds []schema.GroupKind
}

// Violation is one reason a manifest set was refused, named by the object it
// came from so the reason is actionable rather than one opaque error.
type Violation struct {
	Kind      string
	Namespace string
	Name      string
	Reason    string
}

func (v Violation) String() string {
	name := v.Name
	if v.Namespace != "" {
		name = v.Namespace + "/" + v.Name
	}
	return fmt.Sprintf("%s %s: %s", v.Kind, name, v.Reason)
}

// Check returns every violation in the set, so a session learns all of them in
// one pass instead of one per attempt.
func (p Policy) Check(objs []unstructured.Unstructured) []Violation {
	var violations []Violation
	for i := range objs {
		violations = append(violations, p.checkOne(&objs[i])...)
	}
	return violations
}

func (p Policy) checkOne(obj *unstructured.Unstructured) []Violation {
	gvk := obj.GroupVersionKind()

	report := func(reason string) []Violation {
		return []Violation{{
			Kind:      gvk.Kind,
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
			Reason:    reason,
		}}
	}

	if kinds.IsClusterScoped(gvk.GroupKind()) {
		return report("is cluster-scoped, and a sandbox holds namespaced objects only")
	}
	for _, denied := range p.DeniedKinds {
		if denied == gvk.GroupKind() {
			return report("is refused by this group's denylist")
		}
	}

	if ns := obj.GetNamespace(); ns != "" && ns != p.Namespace {
		return report(fmt.Sprintf("targets namespace %q instead of the sandbox namespace %q", ns, p.Namespace))
	}

	if reason := p.checkPodSpec(obj); reason != "" {
		return report(reason)
	}

	return nil
}

// checkPodSpec walks the pod template wherever a workload keeps one, so a
// privileged container is refused the same way in a Deployment, a StatefulSet,
// a DaemonSet, a Job, a CronJob or a bare Pod.
func (p Policy) checkPodSpec(obj *unstructured.Unstructured) string {
	spec := podSpec(obj)
	if spec == nil {
		return ""
	}

	for _, field := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if v, ok, _ := unstructured.NestedBool(spec, field); ok && v {
			return fmt.Sprintf("sets %s, which a sandbox does not allow", field)
		}
	}

	if v, ok, _ := unstructured.NestedBool(spec, "securityContext", "privileged"); ok && v {
		return "runs a privileged pod, which a sandbox does not allow"
	}

	containers, _, _ := unstructured.NestedSlice(spec, "containers")
	initContainers, _, _ := unstructured.NestedSlice(spec, "initContainers")

	for _, group := range [][]interface{}{containers, initContainers} {
		for _, c := range group {
			container, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if v, ok, _ := unstructured.NestedBool(container, "securityContext", "privileged"); ok && v {
				return "runs a privileged container, which a sandbox does not allow"
			}
			if v, ok, _ := unstructured.NestedBool(container, "securityContext", "allowPrivilegeEscalation"); ok && v {
				return "allows privilege escalation, which a sandbox does not allow"
			}
			if reason := p.registryViolation(container); reason != "" {
				return reason
			}
		}
	}

	volumes, _, _ := unstructured.NestedSlice(spec, "volumes")
	for _, v := range volumes {
		volume, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if _, ok, _ := unstructured.NestedString(volume, "hostPath", "path"); ok {
			return "mounts a hostPath volume, which a sandbox does not allow"
		}
	}

	return ""
}

// podSpec returns the pod template of a workload, or nil when the kind carries
// no containers.
func podSpec(obj *unstructured.Unstructured) map[string]interface{} {
	switch obj.GetKind() {
	case "Pod":
		spec, _, _ := unstructured.NestedMap(obj.Object, "spec")
		return spec
	case "CronJob":
		spec, _, _ := unstructured.NestedMap(obj.Object, "spec", "jobTemplate", "spec", "template", "spec")
		return spec
	default:
		spec, _, _ := unstructured.NestedMap(obj.Object, "spec", "template", "spec")
		return spec
	}
}

func (p Policy) registryViolation(container map[string]interface{}) string {
	if len(p.AllowedRegistries) == 0 {
		return ""
	}

	image, _, _ := unstructured.NestedString(container, "image")
	if image == "" {
		return ""
	}

	for _, registry := range p.AllowedRegistries {
		if strings.HasPrefix(image, registry+"/") {
			return ""
		}
	}

	return fmt.Sprintf("uses image %q from a registry this group does not allow", image)
}
