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

package alertconfig

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

const (
	projectNs = "kubecube-project-p1"
	spaceNs   = "space-a"
	secretNs  = "wechat-token"
	configNs  = "ops"
	ownerMark = "t1-p1-ops"
)

func projectNamespace() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: projectNs, Labels: ownership.ProjectLabels("t1", "p1")},
	}
}

func spaceNamespace() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: spaceNs, Labels: ownership.SpaceLabels("t1", "p1")},
	}
}

// alertmanagerConfig is the shape the console writes: a project's policy in the
// project namespace, labelled so the alertmanager selects it, referring to a
// Secret for its credential.
func alertmanagerConfig() *unstructured.Unstructured {
	config := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": alertmanagerConfigGroup + "/" + alertmanagerConfigVersion,
		"kind":       alertmanagerConfigKind,
		"metadata": map[string]interface{}{
			"name":      configNs,
			"namespace": projectNs,
			"labels":    map[string]interface{}{"kubecube.io/owner": ownerMark},
		},
		"spec": map[string]interface{}{
			"route": map[string]interface{}{"receiver": configNs},
			"receivers": []interface{}{
				map[string]interface{}{
					"name": configNs,
					"wechatConfigs": []interface{}{
						map[string]interface{}{
							"apiSecret": map[string]interface{}{"name": secretNs, "key": "token"},
						},
					},
				},
			},
		},
	}}
	config.SetGroupVersionKind(alertmanagerConfigGVK())
	return config
}

func credentialSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretNs, Namespace: projectNs},
		Data:       map[string][]byte{"token": []byte("s3cret")},
		Type:       corev1.SecretTypeOpaque,
	}
}

func newReconciler(t *testing.T, objects ...client.Object) (*Reconciler, client.Client) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("cannot build the scheme: %v", err)
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &Reconciler{Client: fakeClient, reader: fakeClient, configsServed: true}, fakeClient
}

func mustReconcile(t *testing.T, r *Reconciler, name string) {
	t.Helper()

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}); err != nil {
		t.Fatalf("reconcile of %s failed: %v", name, err)
	}
}

func TestReconcileMaterializesTheConfigAndItsSecretIntoASpace(t *testing.T) {
	r, fakeClient := newReconciler(t, projectNamespace(), spaceNamespace(), alertmanagerConfig(), credentialSecret())
	ctx := context.Background()

	mustReconcile(t, r, spaceNs)

	copied := &unstructured.Unstructured{}
	copied.SetGroupVersionKind(alertmanagerConfigGVK())
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: spaceNs, Name: configNs}, copied); err != nil {
		t.Fatalf("the project's config was not copied into the space: %v", err)
	}

	if got := copied.GetLabels()[constants.MaterializedFromLabel]; got != projectNs {
		t.Errorf("the copy is marked as coming from %q, want %q", got, projectNs)
	}
	// the alertmanager selects the configs it merges by this label, so a copy
	// that dropped it would be ignored and the space would stay unalerted
	if got := copied.GetLabels()["kubecube.io/owner"]; got != ownerMark {
		t.Errorf("the copy carries owner %q, want %q", got, ownerMark)
	}
	if _, found, _ := unstructured.NestedMap(copied.Object, "spec", "route"); !found {
		t.Error("the copy has no spec")
	}

	secret := &corev1.Secret{}
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: spaceNs, Name: secretNs}, secret); err != nil {
		t.Fatalf("the referenced secret was not copied into the space: %v", err)
	}
	if string(secret.Data["token"]) != "s3cret" {
		t.Errorf("the copied secret has data %v, want the project's", secret.Data)
	}
}

// A change in the project namespace has to reach the spaces, which is the path
// the AlertmanagerConfig watch takes.
func TestReconcileOfAProjectNamespaceReachesItsSpaces(t *testing.T) {
	r, fakeClient := newReconciler(t, projectNamespace(), spaceNamespace(), alertmanagerConfig(), credentialSecret())
	ctx := context.Background()

	mustReconcile(t, r, projectNs)

	copied := &unstructured.Unstructured{}
	copied.SetGroupVersionKind(alertmanagerConfigGVK())
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: spaceNs, Name: configNs}, copied); err != nil {
		t.Fatalf("a change in the project namespace did not reach the space: %v", err)
	}
}

// Deleting the config in the project has to stop the alerting in the spaces,
// otherwise the copies outlive the policy they came from.
func TestReconcileRemovesCopiesWhoseSourceIsGone(t *testing.T) {
	stale := &unstructured.Unstructured{}
	stale.SetGroupVersionKind(alertmanagerConfigGVK())
	stale.SetName(configNs)
	stale.SetNamespace(spaceNs)
	stale.SetLabels(map[string]string{constants.MaterializedFromLabel: projectNs})

	staleSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretNs,
			Namespace: spaceNs,
			Labels:    map[string]string{constants.MaterializedFromLabel: projectNs},
		},
	}

	r, fakeClient := newReconciler(t, projectNamespace(), spaceNamespace(), stale, staleSecret)
	ctx := context.Background()

	mustReconcile(t, r, spaceNs)

	gone := &unstructured.Unstructured{}
	gone.SetGroupVersionKind(alertmanagerConfigGVK())
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: spaceNs, Name: configNs}, gone); !errors.IsNotFound(err) {
		t.Errorf("the config copy whose source is gone is still there (err = %v)", err)
	}
	if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: spaceNs, Name: secretNs}, &corev1.Secret{}); !errors.IsNotFound(err) {
		t.Errorf("the secret copy whose source is gone is still there (err = %v)", err)
	}
}

// A namespace the platform does not own is not a space, so nothing is written
// into it.
func TestReconcileLeavesAnUnownedNamespaceAlone(t *testing.T) {
	plain := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}}
	r, fakeClient := newReconciler(t, plain, projectNamespace(), alertmanagerConfig(), credentialSecret())
	ctx := context.Background()

	mustReconcile(t, r, "default")

	secrets := &corev1.SecretList{}
	if err := fakeClient.List(ctx, secrets, client.InNamespace("default")); err != nil {
		t.Fatalf("cannot list secrets: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Errorf("an unowned namespace received %d secrets, want none", len(secrets.Items))
	}
}

// Without the monitoring CRDs there is nothing to materialise, and the watch is
// not registered, so a reconcile must not try to read the absent kind.
func TestReconcileDoesNothingWithoutTheMonitoringCRDs(t *testing.T) {
	r, fakeClient := newReconciler(t, projectNamespace(), spaceNamespace(), alertmanagerConfig(), credentialSecret())
	r.configsServed = false
	ctx := context.Background()

	mustReconcile(t, r, spaceNs)

	secrets := &corev1.SecretList{}
	if err := fakeClient.List(ctx, secrets, client.InNamespace(spaceNs)); err != nil {
		t.Fatalf("cannot list secrets: %v", err)
	}
	if len(secrets.Items) != 0 {
		t.Errorf("a deployment without monitoring received %d secrets, want none", len(secrets.Items))
	}
}
