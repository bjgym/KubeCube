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

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// RefOfResource maps what an API route knows — a group, a version and a
// resource — to the identity a member has.
//
// Both vocabularies are needed and neither can be dropped: a route, a Role and
// a URL name a resource in the plural, while a manifest, a member label and an
// admission request name a kind. The conversion is discovery-driven rather than
// a table of our own, so a kind the cluster serves is reachable without a
// release of the platform, and a kind it does not serve is refused rather than
// guessed at.
//
// The version may be left empty when the route does not carry one; the mapper
// then answers with the preferred version.
func RefOfResource(mapper meta.RESTMapper, group, version, resource, namespace, name string) (Ref, error) {
	if mapper == nil {
		return Ref{}, fmt.Errorf("no REST mapper was given, so %s/%s cannot be resolved to a kind", group, resource)
	}

	asked := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}

	gvk, err := mapper.KindFor(asked)
	if err != nil {
		return Ref{}, fmt.Errorf("resolving %s to a kind: %w", asked.String(), err)
	}

	return Ref{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Namespace:  namespace,
		Name:       name,
	}, nil
}
