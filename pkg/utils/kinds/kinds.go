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

// Package kinds answers two questions about a Kubernetes kind that more than
// one reader asks: how it is spelled as a string, and whether it is
// cluster-scoped.
//
// Both matter because a resource group and a sandbox accept namespaced kinds
// only, and because the group API takes its allow and deny lists as strings
// written by a human.
package kinds

import (
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Parse reads a kind the way kubectl spells one: "kind" for the core group and
// "group/kind" otherwise.
//
// The core group is the empty group rather than "v1", which is the mistake this
// function exists to absorb: a list entry written as "v1/ConfigMap" names a
// group that does not exist, and would silently match nothing.
func Parse(s string) schema.GroupKind {
	s = strings.TrimSpace(s)
	if s == "" {
		return schema.GroupKind{}
	}

	if i := strings.Index(s, "/"); i >= 0 {
		return schema.GroupKind{Group: strings.TrimSpace(s[:i]), Kind: strings.TrimSpace(s[i+1:])}
	}

	return schema.GroupKind{Kind: s}
}

// String spells a kind so that Parse reads it back.
func String(gk schema.GroupKind) string {
	if gk.Group == "" {
		return gk.Kind
	}
	return gk.Group + "/" + gk.Kind
}

// clusterScopedKinds are the kinds a group member and a sandbox manifest may
// never name. It is a denylist rather than a discovery lookup so the decision
// does not depend on what a cluster happens to serve: a kind missing from the
// list is still refused when a RESTMapper finds no namespaced resource for it,
// and a kind on the list is refused even where a cluster would accept it.
var clusterScopedKinds = func() map[schema.GroupKind]bool {
	list := []schema.GroupKind{
		{Group: "", Kind: "Namespace"},
		{Group: "", Kind: "Node"},
		{Group: "", Kind: "PersistentVolume"},
		{Group: "", Kind: "ComponentStatus"},
		{Group: "rbac.authorization.k8s.io", Kind: "ClusterRole"},
		{Group: "rbac.authorization.k8s.io", Kind: "ClusterRoleBinding"},
		{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"},
		{Group: "admissionregistration.k8s.io", Kind: "ValidatingWebhookConfiguration"},
		{Group: "admissionregistration.k8s.io", Kind: "MutatingWebhookConfiguration"},
		{Group: "apiregistration.k8s.io", Kind: "APIService"},
		{Group: "storage.k8s.io", Kind: "StorageClass"},
		{Group: "storage.k8s.io", Kind: "CSIDriver"},
		{Group: "storage.k8s.io", Kind: "CSINode"},
		{Group: "storage.k8s.io", Kind: "VolumeAttachment"},
		{Group: "scheduling.k8s.io", Kind: "PriorityClass"},
		{Group: "node.k8s.io", Kind: "RuntimeClass"},
		{Group: "networking.k8s.io", Kind: "IngressClass"},
		{Group: "policy", Kind: "PodSecurityPolicy"},
	}

	set := make(map[schema.GroupKind]bool, len(list))
	for _, gk := range list {
		set[gk] = true
	}
	return set
}()

// IsClusterScoped reports whether a kind is one the platform refuses, whatever
// the requesting subject is allowed to do.
func IsClusterScoped(gk schema.GroupKind) bool {
	return clusterScopedKinds[gk]
}
