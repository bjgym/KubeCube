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

package resourcegroup

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func testMapper() meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Group: "apps", Version: "v1"},
		{Version: "v1"},
	})

	mapper.AddSpecific(
		schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
		schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
		schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployment"},
		meta.RESTScopeNamespace,
	)
	mapper.AddSpecific(
		schema.GroupVersionKind{Version: "v1", Kind: "Pod"},
		schema.GroupVersionResource{Version: "v1", Resource: "pods"},
		schema.GroupVersionResource{Version: "v1", Resource: "pod"},
		meta.RESTScopeNamespace,
	)

	return mapper
}

func TestRefOfResource(t *testing.T) {
	mapper := testMapper()

	tests := []struct {
		name     string
		group    string
		version  string
		resource string
		want     Ref
	}{
		{
			name:  "a grouped resource with its version",
			group: "apps", version: "v1", resource: "deployments",
			want: Ref{APIVersion: "apps/v1", Kind: "Deployment", Namespace: groupNS, Name: "web"},
		},
		{
			name:  "a grouped resource whose route carries no version",
			group: "apps", resource: "deployments",
			want: Ref{APIVersion: "apps/v1", Kind: "Deployment", Namespace: groupNS, Name: "web"},
		},
		{
			name:     "a core resource",
			resource: "pods",
			want:     Ref{APIVersion: "v1", Kind: "Pod", Namespace: groupNS, Name: "web-6d4f-x2k"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := tt.want.Name
			got, err := RefOfResource(mapper, tt.group, tt.version, tt.resource, groupNS, name)
			if err != nil {
				t.Fatalf("RefOfResource: %v", err)
			}
			if got != tt.want {
				t.Errorf("ref = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRefOfResourceRefusesWhatTheClusterDoesNotServe(t *testing.T) {
	if got, err := RefOfResource(testMapper(), "apps", "v1", "widgets", groupNS, "w"); err == nil {
		t.Fatalf("RefOfResource = %+v, want a refusal", got)
	}
	if got, err := RefOfResource(nil, "apps", "v1", "deployments", groupNS, "w"); err == nil {
		t.Fatalf("RefOfResource without a mapper = %+v, want a refusal", got)
	}
}

// The two halves of a scope check have to name an object the same way: the
// inventory is built from objects, and a request arrives as a route. If these
// disagreed, a session would be refused access to a member it can see.
func TestResourceAndObjectNameTheSameRef(t *testing.T) {
	mapper := testMapper()

	fromRoute, err := RefOfResource(mapper, "apps", "v1", "deployments", groupNS, "web")
	if err != nil {
		t.Fatalf("RefOfResource: %v", err)
	}

	fromObject := RefOf(obj("apps/v1", "Deployment", groupNS, "web", map[string]string{Label: groupUID}))

	if fromRoute != fromObject {
		t.Fatalf("route ref %+v != object ref %+v", fromRoute, fromObject)
	}
}
