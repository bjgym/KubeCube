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
	if r.Labels == nil {
		return nil
	}

	return &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: r.Name,
			Labels: ownership.SpaceLabels(
				r.Labels[constants.TenantLabel],
				r.Labels[constants.ProjectLabel],
			),
		},
	}
}
