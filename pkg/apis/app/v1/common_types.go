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

package v1

// GroupRef points at one resource group in one cluster. It carries the UID
// because that, and not the name, is the scope key: a name can be deleted and
// recreated, a UID cannot, so recreating a name inherits nothing.
type GroupRef struct {
	// Name of the ResourceGroup.
	Name string `json:"name"`

	// UID of the ResourceGroup.
	UID string `json:"uid"`

	// Cluster the group's members live in.
	Cluster string `json:"cluster"`

	// Namespace the group and its members live in.
	Namespace string `json:"namespace"`
}

// Environment is where a group sits. It is derived from the namespace, then the
// project, then the cluster, and defaults to prod when nothing records one.
// It raises thresholds — approvals, quota caps, the default session mode — and
// takes no part in the decision about what may be written.
// +kubebuilder:validation:Enum=prod;staging;dev
type Environment string

const (
	EnvironmentProd    Environment = "prod"
	EnvironmentStaging Environment = "staging"
	EnvironmentDev     Environment = "dev"
)
