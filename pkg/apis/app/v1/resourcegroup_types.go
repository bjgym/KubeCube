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

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GroupPhase is the observed phase of a group.
// +kubebuilder:validation:Enum=Pending;Ready;Degraded
type GroupPhase string

const (
	GroupPhasePending  GroupPhase = "Pending"
	GroupPhaseReady    GroupPhase = "Ready"
	GroupPhaseDegraded GroupPhase = "Degraded"
)

// Scope narrows which kinds may be declared as members of this group.
//
// A kind is written as "kind" for the core group and "group/kind" otherwise,
// the way kubectl spells one, because the group of apiVersion: v1 is the empty
// group rather than "v1".
type Scope struct {
	// AllowedKinds, when set, is the only set of kinds a member may be. Empty
	// means every namespaced kind outside DeniedKinds.
	// +optional
	AllowedKinds []string `json:"allowedKinds,omitempty"`

	// DeniedKinds adds to the built-in denylist, which already holds PVC, PV,
	// Namespace, CRDs and every cluster-scoped kind. It can only add.
	// +optional
	DeniedKinds []string `json:"deniedKinds,omitempty"`
}

// AgentPolicy is what a group allows an agent session to be.
type AgentPolicy struct {
	// DefaultMode is the mode a new session gets when its owner does not choose
	// one. A prod location defaults to readonly.
	// +kubebuilder:validation:Enum=readonly;sandbox
	// +optional
	DefaultMode SessionMode `json:"defaultMode,omitempty"`

	// MaxSessionTTL caps the lifetime a session may ask for.
	// +optional
	MaxSessionTTL *metav1.Duration `json:"maxSessionTTL,omitempty"`

	// SandboxQuotaCap caps what one sandbox may consume, on top of the quota its
	// namespace already carries.
	// +optional
	SandboxQuotaCap corev1.ResourceList `json:"sandboxQuotaCap,omitempty"`
}

// MemberSummary counts what the group currently holds. Declared members are the
// ones a human labelled; derived members are reached through the ownerReference
// chain of a declared member.
type MemberSummary struct {
	// +optional
	Declared int `json:"declared,omitempty"`

	// +optional
	Derived int `json:"derived,omitempty"`

	// UnlabelledWarnings counts members that lost the group label. Losing it
	// takes an object out of scope, so the platform reports it and never
	// re-labels it on its own.
	// +optional
	UnlabelledWarnings int `json:"unlabelledWarnings,omitempty"`
}

// ResourceGroupSpec defines the desired state of ResourceGroup
type ResourceGroupSpec struct {
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:MinLength=1
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// +kubebuilder:validation:MaxLength=200
	// +optional
	Description string `json:"description,omitempty"`

	// Cluster the members live in. One group covers one cluster and one
	// namespace, which is what keeps its scope expressible as a label selector.
	Cluster string `json:"cluster"`

	// +optional
	Scope Scope `json:"scope,omitempty"`

	// +optional
	Agent AgentPolicy `json:"agent,omitempty"`
}

// ResourceGroupStatus defines the observed state of ResourceGroup. Only cube
// writes it: the group's members are created and changed by Helm, CI, operators
// and the console, so the platform observes them rather than owning them.
type ResourceGroupStatus struct {
	// +optional
	Phase GroupPhase `json:"phase,omitempty"`

	// GroupUID is the value every member carries under
	// kubecube.io/resource-group. It is the scope key.
	// +optional
	GroupUID string `json:"groupUID,omitempty"`

	// Environment is derived from the namespace, then the project, then the
	// cluster, and defaults to prod.
	// +optional
	Environment Environment `json:"environment,omitempty"`

	// +optional
	Members MemberSummary `json:"members,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ResourceGroup is the Schema for the resourcegroups API
// +kubebuilder:resource:categories="kubecube",scope="Namespaced"
// +kubebuilder:printcolumn:name="DisplayName",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.cluster`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type ResourceGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ResourceGroupSpec   `json:"spec,omitempty"`
	Status ResourceGroupStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ResourceGroupList contains a list of ResourceGroup
type ResourceGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourceGroup `json:"items"`
}
