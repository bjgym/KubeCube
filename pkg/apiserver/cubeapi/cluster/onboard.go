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
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/kubecube-io/kubecube/pkg/ownership"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
	"github.com/kubecube-io/kubecube/pkg/utils/env"
)

// platformNamespaces are the namespaces the platform needs for itself and never
// hands to a tenant.
//
// They are listed by name rather than selected by a prefix, because "kube-*" is
// not the platform's: a member cluster's own add-ons use it too, and a user's
// namespace may start with it. The list is what the console cannot offer as
// unmanaged, so a name that is missing here is offered and can be adopted —
// which is why it is a refusal rather than a filter.
var platformNamespaces = sets.New[string](
	"default",
	"kube-system",
	"kube-public",
	"kube-node-lease",
	"kubecube-system",
)

// onboardable reports why an existing namespace cannot be taken under management
// as a space of the named project, and returns nil when it can.
//
// It is pure and takes the namespace rather than reading it, so the rules can be
// tested without a cluster: every one of them is a statement about what the
// platform already knows, and the two facts it cannot know — that the namespace
// exists and that the tenant and project do — are checked by the caller, which
// has a client.
//
// The order is the order the reasons are worth reporting in: an owned namespace
// is refused for being owned rather than for whatever else it may also be, and
// the platform's own namespaces are named as such instead of being described as
// unowned.
func onboardable(ns *v1.Namespace, tenant, project string) error {
	if ns == nil {
		return fmt.Errorf("it does not exist")
	}

	if tenant == "" || project == "" {
		return fmt.Errorf("the request names no tenant or no project")
	}

	if kind, name, ok := ownership.Of(ns); ok {
		return fmt.Errorf("it is already managed by %v %v", kind, name)
	}

	// An unowned namespace can still carry the derived keys, written by something
	// that did not write the owner. Adopting it would leave those keys describing
	// a tenancy the owner label contradicts, and every reader resolves the owner
	// first, so the namespace would read as managed by one scope while carrying
	// another.
	if reason := ownership.Inconsistency(ns.Labels); reason != "" {
		return fmt.Errorf("its ownership labels are inconsistent: %v", reason)
	}

	if isPlatformNamespace(ns.Name) {
		return fmt.Errorf("%v is a namespace the platform itself needs", ns.Name)
	}

	// A tenant's or a project's own namespace is created by the platform, from
	// the name prefix it owns. One that carries no owner label is a leftover of
	// an interrupted create rather than a namespace somebody built by hand, so
	// adopting it as a space would leave two namespaces claiming the same name
	// convention at different levels.
	if strings.HasPrefix(ns.Name, constants.TenantNsPrefix) || strings.HasPrefix(ns.Name, constants.ProjectNsPrefix) {
		return fmt.Errorf("%v carries the platform's tenant or project namespace prefix", ns.Name)
	}

	// A namespace on its way out is not one to hand to a project: the labels
	// would outlive nothing, and the quota created with them would be refused by
	// the API server.
	if !ns.DeletionTimestamp.IsZero() {
		return fmt.Errorf("it is being deleted")
	}

	return nil
}

// isPlatformNamespace reports whether the namespace is one the platform needs
// for itself.
//
// The install namespace is asked for rather than assumed: the chart installs the
// control plane into the release namespace, so kubecube-system is only its
// default name.
func isPlatformNamespace(name string) bool {
	return platformNamespaces.Has(name) || name == env.CubeNamespace()
}
