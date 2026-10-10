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

package namespace

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

func newReconciler(t *testing.T, objects ...client.Object) (*Reconciler, client.Client) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot build the scheme: %v", err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot build the scheme: %v", err)
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &Reconciler{Client: fakeClient, reader: fakeClient}, fakeClient
}

// The marker is what syncmgr uses to decide an object is not its business, so
// removing it has to leave the object itself in place.
func TestStripPropagatedRemovesTheMarkerAndKeepsTheObject(t *testing.T) {
	marked := map[string]string{constants.HncInherited: "kubecube-project-p1"}

	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "gen-a", Namespace: "space-a", Labels: map[string]string{constants.HncInherited: "kubecube-project-p1"}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      "s1",
		Namespace: "space-a",
		Labels:    map[string]string{constants.HncInherited: "kubecube-project-p1", "keep": "me"},
	}}
	unmarked := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "gen-b", Namespace: "space-a"}}
	elsewhere := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "gen-c", Namespace: "space-b", Labels: marked}}

	r, fakeClient := newReconciler(t, binding, secret, unmarked, elsewhere)
	ctx := context.Background()

	if err := r.stripPropagated(ctx, "space-a"); err != nil {
		t.Fatalf("stripPropagated failed: %v", err)
	}

	got := &rbacv1.RoleBinding{}
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: "space-a", Name: "gen-a"}, got); err != nil {
		t.Fatalf("the object was deleted rather than cleaned: %v", err)
	}
	if _, ok := got.Labels[constants.HncInherited]; ok {
		t.Error("the marker survived on the rolebinding")
	}

	copied := &corev1.Secret{}
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: "space-a", Name: "s1"}, copied); err != nil {
		t.Fatalf("the secret was deleted rather than cleaned: %v", err)
	}
	if _, ok := copied.Labels[constants.HncInherited]; ok {
		t.Error("the marker survived on the secret")
	}
	if copied.Labels["keep"] != "me" {
		t.Error("another label was removed along with the marker")
	}

	// an object in a namespace that was not asked about is left alone
	untouched := &rbacv1.RoleBinding{}
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: "space-b", Name: "gen-c"}, untouched); err != nil {
		t.Fatalf("cannot read the other namespace's object: %v", err)
	}
	if _, ok := untouched.Labels[constants.HncInherited]; !ok {
		t.Error("an object in another namespace was cleaned")
	}
}

// A namespace with nothing marked must be a no-op, so a reconcile writes only
// when it has something to change.
func TestStripPropagatedDoesNothingOnACleanNamespace(t *testing.T) {
	r, fakeClient := newReconciler(t, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "gen-a", Namespace: "space-a"}})
	ctx := context.Background()

	if err := r.stripPropagated(ctx, "space-a"); err != nil {
		t.Fatalf("stripPropagated failed: %v", err)
	}

	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: "space-a", Name: "gen-a"}, &rbacv1.RoleBinding{}); err != nil {
		t.Fatalf("a clean namespace was disturbed: %v", err)
	}
}
