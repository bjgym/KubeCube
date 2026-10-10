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

package kinds

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want schema.GroupKind
	}{
		{in: "ConfigMap", want: schema.GroupKind{Kind: "ConfigMap"}},
		{in: "apps/Deployment", want: schema.GroupKind{Group: "apps", Kind: "Deployment"}},
		{in: "  apps/Deployment  ", want: schema.GroupKind{Group: "apps", Kind: "Deployment"}},
		{in: "", want: schema.GroupKind{}},
		{in: "v1/ConfigMap", want: schema.GroupKind{Group: "v1", Kind: "ConfigMap"}},
	}

	for _, tt := range tests {
		if got := Parse(tt.in); got != tt.want {
			t.Errorf("Parse(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestStringRoundTripsThroughParse(t *testing.T) {
	for _, gk := range []schema.GroupKind{
		{Kind: "ConfigMap"},
		{Group: "apps", Kind: "Deployment"},
	} {
		if got := Parse(String(gk)); got != gk {
			t.Errorf("Parse(String(%v)) = %v, want it to round-trip", gk, got)
		}
	}
}

func TestIsClusterScoped(t *testing.T) {
	scoped := []schema.GroupKind{
		{Kind: "Namespace"},
		{Kind: "PersistentVolume"},
		{Group: "rbac.authorization.k8s.io", Kind: "ClusterRole"},
		{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"},
		{Group: "admissionregistration.k8s.io", Kind: "ValidatingWebhookConfiguration"},
	}
	for _, gk := range scoped {
		if !IsClusterScoped(gk) {
			t.Errorf("IsClusterScoped(%v) = false, want true", gk)
		}
	}

	namespaced := []schema.GroupKind{
		{Kind: "ConfigMap"},
		{Kind: "PersistentVolumeClaim"},
		{Group: "apps", Kind: "Deployment"},
		{Group: "rbac.authorization.k8s.io", Kind: "Role"},
	}
	for _, gk := range namespaced {
		if IsClusterScoped(gk) {
			t.Errorf("IsClusterScoped(%v) = true, want false", gk)
		}
	}
}
