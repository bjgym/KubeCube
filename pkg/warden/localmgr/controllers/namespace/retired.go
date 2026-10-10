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

package namespace

import (
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// retiredKeys are the protocol fields the platform used to write on a namespace
// and now records for itself: the HNC ownership pair, the depth of every
// ancestor including the namespace itself, the marker that made HNC include the
// namespace in its tree, and the annotations HNC and the old creation path left
// behind.
//
// Only the keys the platform wrote are listed, rather than everything under
// `hnc.x-k8s.io`, because a label the platform never wrote is someone else's and
// removing it would be a deletion the platform has no business making.
func retiredLabels(labels map[string]string) []string {
	var retired []string

	for key := range labels {
		switch {
		case key == constants.HncTenantLabel,
			key == constants.HncProjectLabel,
			key == constants.HncIncludedNsLabel,
			key == constants.HncInherited,
			strings.HasSuffix(key, constants.HncSuffix):
			retired = append(retired, key)
		}
	}

	sort.Strings(retired)
	return retired
}

// retiredAnnotations names the annotations to remove. The subnamespace
// annotation is only removed once the namespace has an owner label, which is
// what the caller guarantees by adopting first.
func retiredAnnotations(annotations map[string]string) []string {
	var retired []string

	for key := range annotations {
		switch key {
		case constants.HncAnnotation, hncNamespaceAnnotation:
			retired = append(retired, key)
		}
	}

	sort.Strings(retired)
	return retired
}

// hncNamespaceAnnotation is the annotation the old tenant-namespace creation
// path wrote to have HNC adopt the namespace. It has no constant of its own
// because nothing else ever named it.
const hncNamespaceAnnotation = "hnc.x-k8s.io/ns"

// stripRetired removes the retired protocol fields from the namespace in place
// and reports how many it removed, so that a reconcile with nothing to strip
// writes nothing.
func stripRetired(ns *corev1.Namespace) int {
	removed := 0

	for _, key := range retiredLabels(ns.Labels) {
		delete(ns.Labels, key)
		removed++
	}
	for _, key := range retiredAnnotations(ns.Annotations) {
		delete(ns.Annotations, key)
		removed++
	}

	return removed
}
