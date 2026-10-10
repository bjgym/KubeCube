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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubecube-io/kubecube/pkg/resourcegroup"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// serverPopulatedMetadata is what the API server owns on every object. A
// manifest carrying any of it is refused by the server on create, so exporting
// a live object has to drop it.
var serverPopulatedMetadata = []string{
	"resourceVersion",
	"uid",
	"creationTimestamp",
	"generation",
	"managedFields",
	"selfLink",
}

// platformPopulatedLabels belong to the platform's statement about an object
// rather than to the object itself.
//
// The membership label is the one that matters: a copy that kept it would be
// refused by the cluster when the session applies it, because a person declares
// membership and an agent does not. Dropping it here is what makes the
// render-then-apply path work at all, rather than handing a session a manifest
// the platform itself will reject.
var platformPopulatedLabels = []string{
	resourcegroup.Label,
	constants.MaterializedFromLabel,
}

// serverPopulatedAnnotations belong to whoever wrote the object rather than to
// the object itself: a last-applied blob describes another tool's diff base,
// and the Helm annotations claim the object for a release that does not exist
// in the sandbox.
var serverPopulatedAnnotations = []string{
	"kubectl.kubernetes.io/last-applied-configuration",
	"deployment.kubernetes.io/revision",
	"meta.helm.sh/release-name",
	"meta.helm.sh/release-namespace",
}

// Sanitize turns a live object into a manifest that can be applied into a
// sandbox: the server-owned fields, the ownership links and the allocations
// that only make sense in the cluster they were made in are dropped, and the
// input is never mutated.
//
// It also returns notes for the drops that change what the copy will do — an
// allocated nodePort, a bound volume, a finalizer — because those are the cases
// where a sandbox is an approximation of the member rather than a copy of it,
// and the session has to be told rather than left to discover it.
func Sanitize(obj *unstructured.Unstructured) (*unstructured.Unstructured, []string) {
	out := obj.DeepCopy()
	var notes []string

	unstructured.RemoveNestedField(out.Object, "status")

	for _, field := range serverPopulatedMetadata {
		unstructured.RemoveNestedField(out.Object, "metadata", field)
	}

	if len(out.GetFinalizers()) > 0 {
		notes = append(notes, "finalizers were dropped: a copy that kept them could not be deleted from the sandbox")
		unstructured.RemoveNestedField(out.Object, "metadata", "finalizers")
	}

	if len(out.GetOwnerReferences()) > 0 {
		notes = append(notes, "owner references were dropped: they name objects that do not exist in the sandbox")
		unstructured.RemoveNestedField(out.Object, "metadata", "ownerReferences")
	}

	for _, key := range serverPopulatedAnnotations {
		if _, ok := out.GetAnnotations()[key]; ok {
			unstructured.RemoveNestedField(out.Object, "metadata", "annotations", key)
		}
	}
	if len(out.GetAnnotations()) == 0 {
		unstructured.RemoveNestedField(out.Object, "metadata", "annotations")
	}

	for _, key := range platformPopulatedLabels {
		if _, ok := out.GetLabels()[key]; !ok {
			continue
		}
		unstructured.RemoveNestedField(out.Object, "metadata", "labels", key)
		notes = append(notes, "the platform's own "+key+" label was dropped: a copy carries what it is, not what it belonged to")
	}
	if len(out.GetLabels()) == 0 {
		unstructured.RemoveNestedField(out.Object, "metadata", "labels")
	}

	switch out.GetKind() {
	case "Service":
		notes = append(notes, sanitizeService(out)...)
	case "PersistentVolumeClaim":
		if _, ok, _ := unstructured.NestedString(out.Object, "spec", "volumeName"); ok {
			notes = append(notes, "bound volumeName was dropped: the sandbox binds its own volume")
			unstructured.RemoveNestedField(out.Object, "spec", "volumeName")
		}
	case "Pod":
		if _, ok, _ := unstructured.NestedString(out.Object, "spec", "nodeName"); ok {
			notes = append(notes, "nodeName was dropped: the sandbox schedules the pod itself")
			unstructured.RemoveNestedField(out.Object, "spec", "nodeName")
		}
	}

	return out, notes
}

// sanitizeService drops the allocations a Service gets from the cluster it was
// created in. A copy that kept them would either be refused or claim an address
// that belongs to the original.
func sanitizeService(obj *unstructured.Unstructured) []string {
	var notes []string

	for _, field := range []string{"clusterIP", "clusterIPs", "healthCheckNodePort"} {
		if _, ok, _ := unstructured.NestedFieldNoCopy(obj.Object, "spec", field); ok {
			unstructured.RemoveNestedField(obj.Object, "spec", field)
		}
	}
	notes = append(notes, "cluster-assigned addresses were dropped: the sandbox allocates its own")

	ports, ok, _ := unstructured.NestedSlice(obj.Object, "spec", "ports")
	if !ok {
		return notes
	}

	dropped := false
	for i := range ports {
		port, isMap := ports[i].(map[string]interface{})
		if !isMap {
			continue
		}
		if _, has := port["nodePort"]; has {
			delete(port, "nodePort")
			dropped = true
		}
		ports[i] = port
	}

	if dropped {
		notes = append(notes, "allocated nodePorts were dropped: the sandbox allocates its own")
	}
	_ = unstructured.SetNestedSlice(obj.Object, ports, "spec", "ports")

	return notes
}
