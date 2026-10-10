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

// Package ownership owns where a namespace sits in the tenancy tree: which
// tenant it belongs to, which project owns it, and which of the three levels it
// is.
//
// Those facts used to be spread across the HNC protocol: an ownership label per
// kind, a depth label naming every ancestor, a subnamespace annotation, and the
// name convention. Readers had to consult more than one of them and could
// disagree with each other. Here they are three labels written from one input,
// so they cannot drift, and each one is selectable, which is what the readers
// actually need.
package ownership

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"

	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// Kind is the kind of object that owns a namespace.
type Kind string

const (
	KindTenant  Kind = "tenant"
	KindProject Kind = "project"
)

// Level is where a namespace sits in the tree.
type Level string

const (
	LevelTenant  Level = "tenant"
	LevelProject Level = "project"
	LevelSpace   Level = "space"
)

// The three keys. Label is the authority; the other two are derived from it and
// written with it, so they cannot disagree about where a namespace sits.
const (
	Label     = constants.OwnerLabel
	TenantKey = constants.OwnerTenantLabel
	LevelKey  = constants.OwnerLevelLabel
)

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

// TenantLabels are the labels of a tenant's own namespace.
func TenantLabels(tenant string) map[string]string {
	return map[string]string{
		Label:     Tenant(tenant),
		TenantKey: tenant,
		LevelKey:  string(LevelTenant),
	}
}

// ProjectLabels are the labels of a project's own namespace.
func ProjectLabels(tenant, project string) map[string]string {
	return map[string]string{
		Label:     Project(project),
		TenantKey: tenant,
		LevelKey:  string(LevelProject),
	}
}

// SpaceLabels are the labels of a space: a namespace a project owns, one level
// below the project's own namespace.
//
// The level is recorded rather than derived from the name, because a reader has
// to select a project's spaces without matching the project's own namespace, and
// a space's name is something a user chooses.
func SpaceLabels(tenant, project string) map[string]string {
	return map[string]string{
		Label:     Project(project),
		TenantKey: tenant,
		LevelKey:  string(LevelSpace),
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
// controller's map function.
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

// LevelOf reports where the object sits in the tree.
//
// Migrating from: a namespace without the level label is placed from what it
// does carry, and a project-owned namespace is a space unless its name is the
// project's own.
func LevelOf(obj metav1.Object) (Level, bool) {
	if v := obj.GetLabels()[LevelKey]; v != "" {
		return Level(v), true
	}

	kind, _, ok := Of(obj)
	if !ok {
		return "", false
	}
	if kind == KindTenant {
		return LevelTenant, true
	}
	if strings.HasPrefix(obj.GetName(), constants.ProjectNsPrefix) {
		return LevelProject, true
	}
	return LevelSpace, true
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

// Selector matches the namespaces a tenant or project owns.
func Selector(kind Kind, name string) labels.Selector {
	value := Tenant(name)
	if kind == KindProject {
		value = Project(name)
	}
	return labels.Set{Label: value}.AsSelector()
}

// TenantSelector matches every namespace under a tenant, spaces included. It is
// what replaces selecting on the HNC tenant label.
func TenantSelector(tenant string) labels.Selector {
	return labels.Set{TenantKey: tenant}.AsSelector()
}

// SpaceSelector matches a project's spaces without matching the project's own
// namespace. It is what replaces selecting on the project's depth label.
func SpaceSelector(project string) labels.Selector {
	return labels.Set{Label: Project(project), LevelKey: string(LevelSpace)}.AsSelector()
}

// ManagedSelector matches every namespace the platform owns.
func ManagedSelector() labels.Selector {
	req, err := labels.NewRequirement(Label, selection.Exists, nil)
	if err != nil {
		// a requirement on a constant key with no values cannot fail to build
		return labels.Nothing()
	}
	return labels.NewSelector().Add(*req)
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
