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

package transition

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// SpaceRequest is the body of the create-space request: the name of the space
// and the tenant and project labels that say who owns it.
//
// It used to be HNC's SubnamespaceAnchor, carrying the spec, status and list
// types that come with a custom resource. Nothing on either side of the wire
// ever read those, and the backend builds the Namespace itself rather than
// letting HNC materialise one, so what is left is the metadata and the two
// labels. The request field is named after this type, which is why renaming it
// is a change on both sides of the wire.
type SpaceRequest struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

// Namespace builds the space's Namespace from the request.
//
// The parent namespace is not carried over: it used to be recorded as HNC's
// subnamespace annotation, and the ownership labels answer the same question
// without it.
func (r *SpaceRequest) Namespace() *v1.Namespace {
	labels, ok := r.OwnershipLabels()
	if !ok {
		return nil
	}

	return &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   r.Name,
			Labels: labels,
		},
	}
}

// OwnershipLabels are the three ownership labels for the tenant and project the
// request names.
//
// Both the create path and the path that takes an existing namespace under
// management write them from here, so a namespace cannot be labelled one way
// when the platform creates it and another when it adopts one: the label set is
// what every reader of a namespace's tenancy goes through.
//
// It reports false when the request does not name both a tenant and a project.
// SpaceLabels would otherwise write an owner of "project-" with no name, which
// no reader can resolve — a namespace managed by nothing, which is worse than a
// refused request.
func (r *SpaceRequest) OwnershipLabels() (map[string]string, bool) {
	tenant := r.Labels[constants.TenantLabel]
	project := r.Labels[constants.ProjectLabel]

	if tenant == "" || project == "" {
		return nil, false
	}

	return ownership.SpaceLabels(tenant, project), true
}
