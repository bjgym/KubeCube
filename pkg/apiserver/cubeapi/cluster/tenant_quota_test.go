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

package cluster

import (
	"context"
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	quotav1 "github.com/kubecube-io/kubecube/pkg/apis/quota/v1"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// The member's resourcequota webhook refuses a space whose tenant has no per-cluster
// quota on the pivot, and nothing created that parent. These cases are what keeps the
// repair idempotent and keeps it from touching a quota somebody did set.
func TestEnsureTenantQuota(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := quotav1.SchemeBuilder.AddToScheme(scheme); err != nil {
		t.Fatalf("build scheme: %v", err)
	}
	ctx := context.Background()

	t.Run("creates the parent the member's webhook looks for", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		if err := ensureTenantQuota(ctx, c, "member-1", "t1"); err != nil {
			t.Fatalf("ensureTenantQuota: %v", err)
		}

		got := &quotav1.CubeResourceQuota{}
		if err := c.Get(ctx, types.NamespacedName{Name: "member-1.t1"}, got); err != nil {
			t.Fatalf("the parent quota was not created: %v", err)
		}
		if got.Spec.Target.Kind != quotav1.TenantObj || got.Spec.Target.Name != "t1" {
			t.Errorf("target is %v/%v, want Tenant/t1", got.Spec.Target.Kind, got.Spec.Target.Name)
		}
		if got.Labels[constants.CubeQuotaLabel] != "member-1.t1" {
			t.Errorf("quota label is %q, want member-1.t1", got.Labels[constants.CubeQuotaLabel])
		}
		if got.Labels[constants.ClusterLabel] != "member-1" || got.Labels[constants.TenantLabel] != "t1" {
			t.Errorf("cluster/tenant labels are %q/%q", got.Labels[constants.ClusterLabel], got.Labels[constants.TenantLabel])
		}
	})

	t.Run("leaves an existing quota exactly as its owner set it", func(t *testing.T) {
		existing := &quotav1.CubeResourceQuota{
			ObjectMeta: metav1.ObjectMeta{Name: "member-1.t1"},
			Spec: quotav1.CubeResourceQuotaSpec{
				Target: quotav1.TargetObj{Name: "t1", Kind: quotav1.TenantObj},
				Hard:   v1.ResourceList{v1.ResourceRequestsCPU: resource.MustParse("2")},
			},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

		if err := ensureTenantQuota(ctx, c, "member-1", "t1"); err != nil {
			t.Fatalf("ensureTenantQuota: %v", err)
		}

		got := &quotav1.CubeResourceQuota{}
		if err := c.Get(ctx, types.NamespacedName{Name: "member-1.t1"}, got); err != nil {
			t.Fatalf("get: %v", err)
		}
		if len(got.Spec.Hard) != 1 {
			t.Errorf("hard limits are %v, want the one that was already there", got.Spec.Hard)
		}
	})

	t.Run("a space naming no tenant is not given a parent", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		if err := ensureTenantQuota(ctx, c, "member-1", ""); err != nil {
			t.Fatalf("ensureTenantQuota: %v", err)
		}

		list := &quotav1.CubeResourceQuotaList{}
		if err := c.List(ctx, list); err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(list.Items) != 0 {
			t.Errorf("created %d quotas for a space with no tenant", len(list.Items))
		}
	})
}
