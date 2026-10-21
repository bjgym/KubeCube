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

package quota

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/kubecube-io/kubecube/pkg/clog"
	"github.com/kubecube-io/kubecube/pkg/quota/k8s"
)

type ResourceQuotaValidator struct {
	PivotClient client.Client
	LocalClient client.Client
	decoder     *admission.Decoder
}

func NewValidator(pivotClient, localClient client.Client, decoder *admission.Decoder) *ResourceQuotaValidator {
	return &ResourceQuotaValidator{
		PivotClient: pivotClient,
		LocalClient: localClient,
		decoder:     decoder,
	}
}

func (r *ResourceQuotaValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	oldQuota := &v1.ResourceQuota{}
	currentQuota := &v1.ResourceQuota{}

	defer func() {
		clog.Debug("operation: %v, current quota: %+v, old quota: %+v", req.Operation, currentQuota, oldQuota)
	}()

	switch req.Operation {
	case admissionv1.Create:
		err := r.decoder.Decode(req, currentQuota)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		oldQuota = nil
	case admissionv1.Update:
		err := r.decoder.Decode(req, currentQuota)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		err = r.decoder.DecodeRaw(req.OldObject, oldQuota)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
	case admissionv1.Delete:
		err := r.decoder.DecodeRaw(req.OldObject, oldQuota)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		currentQuota = nil
	}

	// Dropping a finalizer reaches this webhook as an UPDATE, never as a
	// DELETE: the API server only removes a finalizer when a client rewrites
	// metadata.finalizers. This handler compared that rewrite to the parent
	// quota like any other shrink and denied it ("less resource(requests.storage)
	// but parent quota had ..."), so a ResourceQuota that outlives its parent
	// kept the warden finalizer and the namespace holding it could not finish
	// terminating. Nothing else can remove the finalizer, which made the state
	// unrecoverable from the console and from kubectl alike. Allow the repair
	// itself: a DELETE, an update of an object already marked for deletion, and
	// an edit that only rewrites finalizers while leaving the spec untouched.
	// Creates and real spec changes still go through the quota comparison.
	if req.Operation == admissionv1.Delete {
		return admission.Allowed("")
	}
	if currentQuota.DeletionTimestamp != nil {
		return admission.Allowed("")
	}
	if req.Operation == admissionv1.Update && onlyFinalizersChanged(currentQuota, oldQuota) {
		return admission.Allowed("")
	}

	q := k8s.NewQuotaOperator(r.PivotClient, r.LocalClient, currentQuota, oldQuota, context.Background())

	if req.Operation != admissionv1.Delete {
		isOverLoad, reason, err := q.Overload()
		if err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}

		if isOverLoad {
			clog.Warn(reason)
			return admission.Errored(http.StatusNotAcceptable, errors.New(reason))
		}
	}

	//go callback(q, req.Operation == admissionv1.Delete)

	return admission.Allowed("")
}

// onlyFinalizersChanged reports whether an update leaves the quota spec alone
// and only rewrites metadata.finalizers — the shape both this project's
// controllers and a "kubectl patch" use to drop the warden finalizer.
func onlyFinalizersChanged(current, old *v1.ResourceQuota) bool {
	if !reflect.DeepEqual(current.Spec, old.Spec) {
		return false
	}

	return !reflect.DeepEqual(current.Finalizers, old.Finalizers)
}
