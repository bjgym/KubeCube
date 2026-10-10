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
	appv1 "github.com/kubecube-io/kubecube/pkg/apis/app/v1"
	"github.com/kubecube-io/kubecube/pkg/utils/constants"
)

// DeriveEnvironment reads where a group sits from the labels of its namespace,
// then its project, then its cluster, in that order, and answers prod when none
// of them records one.
//
// The order is the point: the narrowest place that says something wins, so a
// single namespace can be marked without moving anything else. The default is
// the other point: an unlabelled location is treated as production, which makes
// every threshold — approvals, quota caps, the default session mode — apply
// until someone says otherwise. The reverse default would turn forgetting a
// label into a quiet widening.
//
// The label is a threshold and never a boundary: it can loosen an approval, and
// it cannot widen who may write, because what may be written is decided by the
// sandbox and not by this value.
func DeriveEnvironment(labels ...map[string]string) appv1.Environment {
	for _, set := range labels {
		switch value := appv1.Environment(set[constants.EnvironmentLabel]); value {
		case appv1.EnvironmentProd, appv1.EnvironmentStaging, appv1.EnvironmentDev:
			return value
		}
	}

	return appv1.EnvironmentProd
}
