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

// Package ownership owns the single fact that says which tenant or project a
// namespace belongs to.
//
// The fact used to be spread across the HNC protocol: an ownership label per
// kind, a depth label naming every ancestor, a subnamespace annotation, and the
// name convention. Readers had to consult more than one of them and could
// disagree with each other. Here it is one label with one value.
package ownership

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// Kind is the kind of object that owns a namespace.
type Kind string

const (
	KindTenant  Kind = "tenant"
	KindProject Kind = "project"
)

// Label is the only key that records namespace ownership.
const Label = constants.OwnerLabel

// TenantKey records which tenant an owned namespace belongs to. It is derived
// from the owner and written in the same operation, so the two cannot disagree
// about who owns the namespace. It exists because readers have to select every
// namespace under a tenant, and the owner of a project namespace names the
// project rather than the tenant it sits in.
const TenantKey = constants.OwnerTenantLabel

const (
	tenantPrefix  = "tenant:"
	projectPrefix = "project:"
)

// Tenant returns the label value for a namespace owned by a tenant. It is the
// tenant's own namespace.
func Tenant(name string) string {
	return tenantPrefix + name
}

// Project returns the label value for a namespace owned by a project. That
// covers the project's own namespace and every space beneath it, because a
// space inherits the project it was created in.
func Project(name string) string {
	return projectPrefix + name
}

// TenantLabels are the ownership labels of a tenant's own namespace.
func TenantLabels(tenant string) map[string]string {
	return map[string]string{
		Label:     Tenant(tenant),
		TenantKey: tenant,
	}
}

// ProjectLabels are the ownership labels of a project namespace and of every
// space beneath it. The tenant is carried as well as the project because a
// reader has to be able to select every namespace under a tenant, and the
// project's name does not say which tenant it belongs to.
func ProjectLabels(tenant, project string) map[string]string {
	return map[string]string{
		Label:     Project(project),
		TenantKey: tenant,
	}
}

// Of resolves which tenant or project owns the object, and reports false when
// nothing owns it.
//
// The label is authoritative. The remaining branches exist only while
// namespaces written before the label was introduced are still in the cluster:
// they are a migration path, not a second source of truth, and they are what
// gets deleted once every namespace carries the label. Falling back rather than
// failing means a namespace never silently loses its owner part-way through the
// migration.
func Of(obj metav1.Object) (kind Kind, name string, ok bool) {
	return OfLabels(obj.GetLabels(), obj.GetName())
}

// OfLabels resolves ownership from a label set plus the object's name, for the
// callers that hold labels rather than an object: a namespace predicate, a
// controller's map function, a selector builder.
func OfLabels(labels map[string]string, objName string) (kind Kind, name string, ok bool) {
	if v := labels[Label]; v != "" {
		if kind, name, parsed := parse(v); parsed {
			return kind, name, true
		}
	}

	// migrating from: the HNC protocol labels
	if v := labels[constants.HncTenantLabel]; v != "" {
		return KindTenant, v, true
	}
	if v := labels[constants.HncProjectLabel]; v != "" {
		return KindProject, v, true
	}

	// migrating from: the name convention, which only ever covered tenants
	if strings.HasPrefix(objName, constants.TenantNsPrefix) {
		return KindTenant, strings.TrimPrefix(objName, constants.TenantNsPrefix), true
	}

	return "", "", false
}

// ManagedLabels reports whether a label set belongs to a namespace the platform
// owns. The name is not consulted: a claim of ownership has to come from a
// label, which is the whole point of having one.
func ManagedLabels(labels map[string]string) bool {
	_, _, ok := OfLabels(labels, "")
	return ok
}

// TenantOf reports which tenant the object belongs to, whether the tenant owns
// it directly or owns it through a project. A project-owned namespace answers
// with its tenant, which is what lets a reader select everything under a tenant
// and what lets an authorization decision honour tenant membership on a space.
func TenantOf(obj metav1.Object) (string, bool) {
	return TenantOfLabels(obj.GetLabels(), obj.GetName())
}

// TenantOfLabels is TenantOf for a bare label set.
func TenantOfLabels(labels map[string]string, objName string) (string, bool) {
	if v := labels[TenantKey]; v != "" {
		return v, true
	}

	// migrating from: the HNC tenant label, then the name convention
	if v := labels[constants.HncTenantLabel]; v != "" {
		return v, true
	}
	if strings.HasPrefix(objName, constants.TenantNsPrefix) {
		return strings.TrimPrefix(objName, constants.TenantNsPrefix), true
	}

	// an owner that is itself a tenant, when the derived key is absent
	if kind, name, ok := OfLabels(labels, objName); ok && kind == KindTenant {
		return name, true
	}

	return "", false
}

// ProjectOf reports the project owning the object, if a project owns it.
func ProjectOf(obj metav1.Object) (string, bool) {
	kind, name, ok := Of(obj)
	if !ok || kind != KindProject {
		return "", false
	}
	return name, true
}

// Managed reports whether the platform owns the object at all.
func Managed(obj metav1.Object) bool {
	_, _, ok := Of(obj)
	return ok
}

// Is reports whether the object is owned by the named tenant or project.
func Is(obj metav1.Object, kind Kind, name string) bool {
	k, n, ok := Of(obj)
	return ok && k == kind && n == name
}

func parse(value string) (Kind, string, bool) {
	if strings.HasPrefix(value, tenantPrefix) {
		if name := strings.TrimPrefix(value, tenantPrefix); name != "" {
			return KindTenant, name, true
		}
		return "", "", false
	}
	if strings.HasPrefix(value, projectPrefix) {
		if name := strings.TrimPrefix(value, projectPrefix); name != "" {
			return KindProject, name, true
		}
		return "", "", false
	}
	return "", "", false
}
