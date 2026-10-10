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

// Package v1 contains API Schema definitions for the app v1 API group
// +kubebuilder:object:generate=true
// +groupName=app.kubecube.io
//
// Two constructs in this package are the first of their kind in this
// repository, and both are worth checking the first time `make manifests
// generate` runs against it:
//
//   - []metav1.Condition in the three statuses. controller-gen is pinned to
//     v0.4.1, which predates CEL and x-kubernetes-validations, and no other type
//     here has a Condition field to compare the generated schema with. If it is
//     rejected, use a package-local Condition struct in common_types.go rather
//     than dropping conditions: a person reading a Degraded group needs to see
//     why.
//   - *metav1.Duration for a session's TTL. The generator is expected to write
//     `type: string` for it; if it writes a nested object instead, the field
//     becomes an explicit string with a pattern.
package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "app.kubecube.io", Version: "v1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
