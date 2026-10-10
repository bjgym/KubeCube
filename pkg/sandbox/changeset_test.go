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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRecordNumbersChangeSets(t *testing.T) {
	applier, c, _ := newApplier(t, Policy{Namespace: sandboxNS})
	ctx := context.Background()
	objs := []unstructured.Unstructured{configMap(sandboxNS, "settings")}

	first, err := applier.Record(ctx, objs)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if first.Name != "change-0001" {
		t.Errorf("first change set = %s, want change-0001", first.Name)
	}

	second, err := applier.Record(ctx, objs)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if second.Name != "change-0002" {
		t.Errorf("second change set = %s, want change-0002", second.Name)
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: sandboxNS, Name: first.Name}, cm); err != nil {
		t.Fatalf("the change set was not stored: %v", err)
	}
	if cm.Labels[ChangeSetLabel] != "true" {
		t.Errorf("labels = %v, want the change set marked", cm.Labels)
	}
}

// A ConfigMap that merely carries the label must not decide the next number:
// the sandbox's history is the sets this package wrote, not everything in it.
func TestRecordIgnoresForeignNames(t *testing.T) {
	applier, c, _ := newApplier(t, Policy{Namespace: sandboxNS})
	ctx := context.Background()

	foreign := &corev1.ConfigMap{}
	foreign.Name = "notes"
	foreign.Namespace = sandboxNS
	foreign.Labels = map[string]string{ChangeSetLabel: "true"}
	if err := c.Create(ctx, foreign); err != nil {
		t.Fatalf("creating the foreign ConfigMap: %v", err)
	}

	set, err := applier.Record(ctx, []unstructured.Unstructured{configMap(sandboxNS, "settings")})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if set.Name != "change-0001" {
		t.Errorf("change set = %s, want change-0001", set.Name)
	}
}

func TestRecordAndLoadRoundTrip(t *testing.T) {
	applier, _, _ := newApplier(t, Policy{Namespace: sandboxNS})
	ctx := context.Background()

	objs := []unstructured.Unstructured{
		configMap(sandboxNS, "settings"),
		deployment(plainContainer()),
	}

	set, err := applier.Record(ctx, objs)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if set.Digest == "" {
		t.Error("digest is empty, want the hash of what was recorded")
	}

	loaded, err := applier.Load(ctx, set.Name)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != len(objs) {
		t.Fatalf("loaded %d objects, want %d", len(loaded), len(objs))
	}
	for i := range objs {
		if loaded[i].GetKind() != objs[i].GetKind() || loaded[i].GetName() != objs[i].GetName() {
			t.Errorf("object %d = %s/%s, want %s/%s", i,
				loaded[i].GetKind(), loaded[i].GetName(), objs[i].GetKind(), objs[i].GetName())
		}
		if loaded[i].GetNamespace() != objs[i].GetNamespace() {
			t.Errorf("object %d namespace = %q, want %q", i, loaded[i].GetNamespace(), objs[i].GetNamespace())
		}
	}
}

func TestDigestIdentifiesTheContent(t *testing.T) {
	applier, _, _ := newApplier(t, Policy{Namespace: sandboxNS})
	ctx := context.Background()

	same := []unstructured.Unstructured{configMap(sandboxNS, "settings")}
	first, err := applier.Record(ctx, same)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	again, err := applier.Record(ctx, same)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if first.Digest != again.Digest {
		t.Errorf("digests differ for the same manifests: %s and %s", first.Digest, again.Digest)
	}

	changed := configMap(sandboxNS, "settings")
	_ = unstructured.SetNestedField(changed.Object, "changed", "data", "key")
	other, err := applier.Record(ctx, []unstructured.Unstructured{changed})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if other.Digest == first.Digest {
		t.Error("the digest did not change with the content, so it cannot identify what was validated")
	}
}

func TestRecordRefusesASetThePolicyWouldRefuse(t *testing.T) {
	applier, c, _ := newApplier(t, Policy{Namespace: sandboxNS})
	ctx := context.Background()

	clusterRole := *obj("rbac.authorization.k8s.io/v1", "ClusterRole", "", "reader")
	_, err := applier.Record(ctx, []unstructured.Unstructured{clusterRole})

	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Record = %v, want a RefusedError", err)
	}

	sets := &corev1.ConfigMapList{}
	if err := c.List(ctx, sets, client.InNamespace(sandboxNS)); err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(sets.Items) != 0 {
		t.Errorf("recorded %d change sets, want none for a refused set", len(sets.Items))
	}
}

func TestRollbackReappliesARecordedSet(t *testing.T) {
	applier, c, _ := newApplier(t, Policy{Namespace: sandboxNS})
	ctx := context.Background()

	set, err := applier.Record(ctx, []unstructured.Unstructured{configMap(sandboxNS, "settings")})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	if err := applier.Apply(ctx, []unstructured.Unstructured{configMap(sandboxNS, "settings")}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	settings := configMap(sandboxNS, "settings")

	applied := settings.DeepCopy()
	if err := c.Delete(ctx, applied); err != nil {
		t.Fatalf("deleting the applied object: %v", err)
	}

	if err := applier.Rollback(ctx, set.Name); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(settings.GroupVersionKind())
	if err := c.Get(ctx, types.NamespacedName{Namespace: sandboxNS, Name: "settings"}, got); err != nil {
		t.Errorf("the recorded object was not applied again: %v", err)
	}
}
