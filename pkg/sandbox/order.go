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
	"sort"

	"helm.sh/helm/v3/pkg/releaseutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Order sorts a manifest set the way Helm installs one: by kind, in Helm's own
// install order. The list is reused rather than restated so that a kind added
// to Helm's order is ordered here too, and so that a sandbox applies a member
// in the same sequence the cluster that owns it would.
//
// The sort is stable, so objects of one kind keep the order the member gave
// them, and a kind Helm does not know goes last rather than first: an unknown
// kind is more likely to depend on the ones it does know than the other way
// round.
func Order(objs []unstructured.Unstructured) []unstructured.Unstructured {
	rank := installRank()

	out := make([]unstructured.Unstructured, len(objs))
	copy(out, objs)

	sort.SliceStable(out, func(i, j int) bool {
		return rankOf(rank, out[i].GetKind()) < rankOf(rank, out[j].GetKind())
	})

	return out
}

func installRank() map[string]int {
	rank := make(map[string]int, len(releaseutil.InstallOrder))
	for i, kind := range releaseutil.InstallOrder {
		rank[kind] = i
	}
	return rank
}

func rankOf(rank map[string]int, kind string) int {
	if r, ok := rank[kind]; ok {
		return r
	}
	return len(rank)
}
