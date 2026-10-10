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
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FieldManager names the platform as the owner of the fields it applies, so a
// later apply can tell its own fields from everyone else's.
const FieldManager = "kubecube-sandbox"

// Applier applies a manifest set into a sandbox namespace.
//
// It is apply-only on purpose. A sandbox is a disposable namespace, so there is
// nothing to prune, nothing to adopt and no revision to keep: teardown is
// deleting the namespace. That is why the half of a release manager that
// deletes things is absent here rather than missing.
type Applier struct {
	Client client.Client

	// Policy is checked before anything reaches the cluster: a set that names a
	// cluster-scoped kind, a privileged container or a foreign namespace is
	// refused without a request being made.
	Policy Policy
}

// RefusedError carries every violation, so one attempt reports all the reasons
// instead of one per round trip.
type RefusedError struct {
	Violations []Violation
}

func (e *RefusedError) Error() string {
	reasons := make([]string, 0, len(e.Violations))
	for _, v := range e.Violations {
		reasons = append(reasons, v.String())
	}
	return "the manifest set was refused: " + strings.Join(reasons, "; ")
}

// DryRun asks the API server to validate the set without keeping it, so a
// session learns about an invalid manifest before it changes the sandbox.
func (a Applier) DryRun(ctx context.Context, objs []unstructured.Unstructured) error {
	return a.apply(ctx, objs, client.DryRunAll)
}

// Apply applies the set into the sandbox namespace, in kind order.
func (a Applier) Apply(ctx context.Context, objs []unstructured.Unstructured) error {
	return a.apply(ctx, objs)
}

func (a Applier) apply(ctx context.Context, objs []unstructured.Unstructured, extra ...client.PatchOption) error {
	if violations := a.Policy.Check(objs); len(violations) > 0 {
		return &RefusedError{Violations: violations}
	}

	options := append([]client.PatchOption{client.FieldOwner(FieldManager), client.ForceOwnership}, extra...)

	for _, obj := range Order(objs) {
		target := obj.DeepCopy()
		if target.GetNamespace() == "" {
			target.SetNamespace(a.Policy.Namespace)
		}

		if err := a.Client.Patch(ctx, target, client.Apply, options...); err != nil {
			return fmt.Errorf("apply %s %s: %w", target.GetKind(), objectName(target), err)
		}
	}

	return nil
}

func objectName(obj *unstructured.Unstructured) string {
	if obj.GetNamespace() == "" {
		return obj.GetName()
	}
	return obj.GetNamespace() + "/" + obj.GetName()
}
