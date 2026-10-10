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

// ChangeRequestPhase is the observed phase of a change request.
// +kubebuilder:validation:Enum=Pending;Accepted;Rejected;Applied
type ChangeRequestPhase string

const (
	ChangeRequestPhasePending  ChangeRequestPhase = "Pending"
	ChangeRequestPhaseAccepted ChangeRequestPhase = "Accepted"
	ChangeRequestPhaseRejected ChangeRequestPhase = "Rejected"
	ChangeRequestPhaseApplied  ChangeRequestPhase = "Applied"
)

// EvidenceResult is the outcome of one check the platform ran while a change
// was being prepared.
//
// It is a closed set rather than a free string because the rules that read it
// have to tell "failed" from "never run": a field nobody filled in would
// otherwise read as success, and a reviewer would approve something nothing
// checked.
// +kubebuilder:validation:Enum=passed;failed;not-run
type EvidenceResult string

const (
	// EvidencePassed is a check that ran and passed.
	EvidencePassed EvidenceResult = "passed"

	// EvidenceFailed is a check that ran and failed.
	EvidenceFailed EvidenceResult = "failed"

	// EvidenceNotRun is a check that was never run.
	EvidenceNotRun EvidenceResult = "not-run"
)

// ChangeSet is the validated set of manifests a session proposes to land.
type ChangeSet struct {
	// ConfigMapRef names the ConfigMap in the group's namespace that holds the
	// normalized manifests.
	ConfigMapRef string `json:"configMapRef"`

	// Digest is the hash of that content, so a reviewer approves exactly what
	// was validated rather than whatever the ConfigMap holds later.
	Digest string `json:"digest"`
}

// EvidenceQuota is the estimated cost of a change against what the space has
// left, so an over-budget change is refused before it is applied.
type EvidenceQuota struct {
	// +optional
	Delta corev1.ResourceList `json:"delta,omitempty"`

	// +optional
	Remaining corev1.ResourceList `json:"remaining,omitempty"`
}

// Evidence is what the session proved before asking a human to land a change.
type Evidence struct {
	// +optional
	DryRun EvidenceResult `json:"dryRun,omitempty"`

	// +optional
	Policy EvidenceResult `json:"policy,omitempty"`

	// +optional
	Quota EvidenceQuota `json:"quota,omitempty"`

	// +optional
	SmokeTest EvidenceResult `json:"smokeTest,omitempty"`
}

// ChangeRequestSpec defines the desired state of ChangeRequest
type ChangeRequestSpec struct {
	GroupRef GroupRef `json:"groupRef"`

	// SessionRef names the AgentSession that produced the change.
	SessionRef string `json:"sessionRef"`

	ChangeSet ChangeSet `json:"changeSet"`

	// +optional
	Evidence Evidence `json:"evidence,omitempty"`
}

// ChangeRequestStatus defines the observed state of ChangeRequest. The platform
// never lands a change itself, so Applied means a human landed it through the
// channel that already owns those resources.
type ChangeRequestStatus struct {
	// +optional
	Phase ChangeRequestPhase `json:"phase,omitempty"`

	// LandedBy records the human who landed the change, once it is Applied.
	// +optional
	LandedBy string `json:"landedBy,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ChangeRequest is the Schema for the changerequests API
// +kubebuilder:resource:categories="kubecube",scope="Namespaced"
// +kubebuilder:printcolumn:name="Group",type=string,JSONPath=`.spec.groupRef.name`
// +kubebuilder:printcolumn:name="Session",type=string,JSONPath=`.spec.sessionRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type ChangeRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ChangeRequestSpec   `json:"spec,omitempty"`
	Status ChangeRequestStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ChangeRequestList contains a list of ChangeRequest
type ChangeRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ChangeRequest `json:"items"`
}
