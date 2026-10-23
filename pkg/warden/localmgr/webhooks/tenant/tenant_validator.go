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

package tenant

import (
	"context"
	"fmt"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
	"net/http"

	v1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	tenantv1 "github.com/kubecube-io/kubecube/pkg/apis/tenant/v1"
)

type Validator struct {
	Client   client.Client
	IsMember bool
	decoder  *admission.Decoder
}

func NewValidator(client client.Client, isMember bool, decoder *admission.Decoder) *Validator {
	return &Validator{
		Client:   client,
		IsMember: isMember,
		decoder:  decoder,
	}
}

func (r *Validator) Handle(ctx context.Context, req admission.Request) admission.Response {
	switch req.Operation {
	case v1.Create:
		return admission.Allowed("")
	case v1.Update:
		return admission.Allowed("")
	case v1.Delete:
		tenant := tenantv1.Tenant{}
		err := r.decoder.DecodeRaw(req.OldObject, &tenant)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if r.IsMember && tenant.Annotations[constants.ForceDeleteAnnotation] != "true" {
			// Name the annotation and say what it does, because an operator who has
			// just been refused will otherwise assume the flag forces the delete
			// through. It does not: it only admits this request, and the tenant stays
			// in Terminating until its projects are gone.
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("member cluster does not allow deletion of tenant %s: set %s to \"true\" to admit the request; the annotation permits the delete, it does not skip the cascade - the tenant remains until its projects are gone",
				tenant.Name, constants.ForceDeleteAnnotation))
		}
		return admission.Allowed("")
	}
	return admission.Allowed("")
}
