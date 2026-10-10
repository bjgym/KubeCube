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
	"encoding/json"
	"fmt"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// RequestOf builds the scope request for one admission request.
//
// It is how the cluster-side backstop reads a write: the same Scope.Decide that
// answers the API layer answers here, from an admission request rather than a
// route. That is the point of the backstop — a session that reaches a cluster
// some other way still meets the same rule.
//
// The subresource is what makes this worth doing carefully. An exec, an attach,
// a port-forward and a log read all arrive as an operation on a pod, and an
// eviction arrives as a create; a mapping that read only the operation would
// call every one of them a read or a write of the pod itself.
func RequestOf(req admission.Request) (Request, error) {
	verb, err := verbOf(req.Operation, req.SubResource)
	if err != nil {
		return Request{}, err
	}

	name := req.Name
	if name == "" {
		// A create carries the object rather than its name.
		name, err = nameOf(req.Object.Raw)
		if err != nil {
			return Request{}, err
		}
	}

	return Request{
		Verb: verb,
		Ref: Ref{
			APIVersion: metav1.GroupVersion{Group: req.Kind.Group, Version: req.Kind.Version}.String(),
			Kind:       req.Kind.Kind,
			Namespace:  req.Namespace,
			Name:       name,
		},
	}, nil
}

func verbOf(operation admissionv1.Operation, subresource string) (Verb, error) {
	switch subresource {
	case "exec":
		return VerbExec, nil
	case "attach":
		return VerbAttach, nil
	case "portforward":
		return VerbPortForward, nil
	case "log", "logs":
		return VerbLogs, nil
	case "scale":
		return VerbScale, nil
	case "eviction":
		return VerbEvict, nil
	}

	switch operation {
	case admissionv1.Create:
		return VerbCreate, nil
	case admissionv1.Update:
		return VerbUpdate, nil
	case admissionv1.Delete:
		return VerbDelete, nil
	case admissionv1.Connect:
		return "", fmt.Errorf("a connect to subresource %q is not one this rule knows", subresource)
	default:
		return "", fmt.Errorf("the operation %q is not one this rule knows", operation)
	}
}

func nameOf(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("the request names no object and carries none")
	}

	object := &metav1.PartialObjectMetadata{}
	if err := json.Unmarshal(raw, object); err != nil {
		return "", fmt.Errorf("reading the object the request carries: %w", err)
	}
	if object.GetName() == "" {
		return "", fmt.Errorf("the object the request carries has no name")
	}

	return object.GetName(), nil
}
