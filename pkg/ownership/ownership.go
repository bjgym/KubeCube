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
	labels := obj.GetLabels()

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
	if name := obj.GetName(); strings.HasPrefix(name, constants.TenantNsPrefix) {
		return KindTenant, strings.TrimPrefix(name, constants.TenantNsPrefix), true
	}

	return "", "", false
}

// TenantOf reports the tenant owning the object, if a tenant owns it.
func TenantOf(obj metav1.Object) (string, bool) {
	kind, name, ok := Of(obj)
	if !ok || kind != KindTenant {
		return "", false
	}
	return name, true
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
