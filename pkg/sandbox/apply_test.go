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
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// applied is one server-side apply the applier issued.
type applied struct {
	kind      string
	namespace string
	name      string
	dryRun    bool
}

func configMap(namespace, name string) unstructured.Unstructured {
	return *obj("v1", "ConfigMap", namespace, name)
}

// newApplier builds an applier over a fake client that behaves like a server
// for the two things that matter here: a server-side apply creates an object
// that does not exist yet, and a dry-run apply keeps nothing. The
// controller-runtime fake client does neither on its own, so the interceptor
// does, and it records what was issued.
func newApplier(t *testing.T, policy Policy) (Applier, client.Client, *[]applied) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("building the scheme: %v", err)
	}

	var calls []applied
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				options := (&client.PatchOptions{}).ApplyOptions(opts)
				calls = append(calls, applied{
					kind:      obj.GetObjectKind().GroupVersionKind().Kind,
					namespace: obj.GetNamespace(),
					name:      obj.GetName(),
					dryRun:    len(options.DryRun) > 0,
				})

				if len(options.DryRun) > 0 {
					return nil
				}

				// A real server creates on the first apply and merges after it.
				err := c.Create(ctx, obj)
				if apierrors.IsAlreadyExists(err) {
					return nil
				}
				return err
			},
		}).
		Build()

	return Applier{Client: c, Policy: policy}, c, &calls
}

func TestApplierAppliesIntoTheSandboxNamespaceInKindOrder(t *testing.T) {
	applier, c, calls := newApplier(t, Policy{Namespace: sandboxNS})
	ctx := context.Background()

	// The Deployment comes first in the input and last in the order; the
	// ConfigMap with no namespace is what an export of a live object looks like.
	objs := []unstructured.Unstructured{
		deployment(plainContainer()),
		configMap(sandboxNS, "settings"),
		configMap("", "exported"),
	}

	if err := applier.Apply(ctx, objs); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, call := range *calls {
		if call.namespace != sandboxNS {
			t.Errorf("%s %s was applied into %q, want the sandbox namespace", call.kind, call.name, call.namespace)
		}
	}

	if got := (*calls)[len(*calls)-1].kind; got != "Deployment" {
		t.Errorf("last applied kind = %s, want the Deployment last (order = %v)", got, *calls)
	}

	for _, want := range []struct {
		gvk  schema.GroupVersionKind
		name string
	}{
		{schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, "settings"},
		{schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, "exported"},
		{schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, "web"},
	} {
		got := &unstructured.Unstructured{}
		got.SetGroupVersionKind(want.gvk)
		if err := c.Get(ctx, types.NamespacedName{Namespace: sandboxNS, Name: want.name}, got); err != nil {
			t.Errorf("%s %s was not applied: %v", want.gvk.Kind, want.name, err)
		}
	}
}

func TestApplierIsIdempotent(t *testing.T) {
	applier, _, _ := newApplier(t, Policy{Namespace: sandboxNS})
	ctx := context.Background()
	objs := []unstructured.Unstructured{configMap(sandboxNS, "settings")}

	if err := applier.Apply(ctx, objs); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := applier.Apply(ctx, objs); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
}

// A refused set must not reach the cluster at all: the policy is checked first,
// so a session cannot learn what a cluster would have accepted by trying.
func TestApplierRefusesBeforeItWrites(t *testing.T) {
	applier, c, calls := newApplier(t, Policy{
		Namespace:         sandboxNS,
		AllowedRegistries: []string{"registry.internal"},
	})

	objs := []unstructured.Unstructured{
		configMap(sandboxNS, "settings"),
		deployment(map[string]interface{}{"name": "app", "image": "docker.io/library/nginx:1.25"}),
	}

	err := applier.Apply(context.Background(), objs)

	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Apply = %v, want a RefusedError", err)
	}
	if len(refused.Violations) != 1 {
		t.Errorf("violations = %v, want the one bad object reported", refused.Violations)
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %v, want the whole set refused before any write", *calls)
	}

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: sandboxNS, Name: "settings"}, got); err == nil {
		t.Error("the allowed object was applied anyway")
	}
}

func TestApplierDryRunKeepsNothing(t *testing.T) {
	applier, c, calls := newApplier(t, Policy{Namespace: sandboxNS})
	ctx := context.Background()

	if err := applier.DryRun(ctx, []unstructured.Unstructured{configMap(sandboxNS, "settings")}); err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	if len(*calls) != 1 || !(*calls)[0].dryRun {
		t.Fatalf("calls = %v, want one dry-run apply", *calls)
	}

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
	if err := c.Get(ctx, types.NamespacedName{Namespace: sandboxNS, Name: "settings"}, got); err == nil {
		t.Error("DryRun kept the object, want a validation only")
	}
}

func TestApplierRefusesClusterScopedKinds(t *testing.T) {
	applier, _, calls := newApplier(t, Policy{Namespace: sandboxNS})

	clusterRole := *obj("rbac.authorization.k8s.io/v1", "ClusterRole", "", "reader")
	err := applier.Apply(context.Background(), []unstructured.Unstructured{clusterRole})

	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Apply = %v, want a RefusedError", err)
	}
	if len(*calls) != 0 {
		t.Errorf("calls = %v, want nothing issued", *calls)
	}
}
