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

// SessionMode is what a session may do. There is no mode that writes to a
// production group: an agent writes only inside its sandbox, and a human lands
// the change.
// +kubebuilder:validation:Enum=readonly;sandbox
type SessionMode string

const (
	SessionModeReadOnly SessionMode = "readonly"
	SessionModeSandbox  SessionMode = "sandbox"
)

// SessionPhase is the observed phase of a session.
// +kubebuilder:validation:Enum=Pending;Ready;Expired;Revoked
type SessionPhase string

const (
	SessionPhasePending SessionPhase = "Pending"
	SessionPhaseReady   SessionPhase = "Ready"
	SessionPhaseExpired SessionPhase = "Expired"
	SessionPhaseRevoked SessionPhase = "Revoked"
)

// ToolName is one MCP tool a session may be granted.
// +kubebuilder:validation:Enum=list_group_resources;get_resource;get_events;get_logs;describe_group;render_member;validate_change;list_chart_versions;sandbox_apply;sandbox_rollback;run_smoke_test;destroy_sandbox;propose_change
type ToolName string

const (
	ToolListGroupResources ToolName = "list_group_resources"
	ToolGetResource        ToolName = "get_resource"
	ToolGetEvents          ToolName = "get_events"
	ToolGetLogs            ToolName = "get_logs"
	ToolDescribeGroup      ToolName = "describe_group"
	ToolRenderMember       ToolName = "render_member"
	ToolValidateChange     ToolName = "validate_change"
	ToolListChartVersions  ToolName = "list_chart_versions"
	ToolSandboxApply       ToolName = "sandbox_apply"
	ToolSandboxRollback    ToolName = "sandbox_rollback"
	ToolRunSmokeTest       ToolName = "run_smoke_test"
	ToolDestroySandbox     ToolName = "destroy_sandbox"
	ToolProposeChange      ToolName = "propose_change"
)

// SessionOwner is who authorized the session and which client holds it. Every
// action a session takes traces back to the user recorded here.
type SessionOwner struct {
	// User is the platform user who authorized the session.
	User string `json:"user"`

	// Client is the MCP client the session was issued to.
	// +optional
	Client string `json:"client,omitempty"`
}

// SandboxSpec is the disposable environment a session writes in.
type SandboxSpec struct {
	// TTL is how long the sandbox lives.
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// QuotaCap caps the sandbox on top of the quota its namespace carries.
	// +optional
	QuotaCap corev1.ResourceList `json:"quotaCap,omitempty"`
}

// AgentSessionSpec defines the desired state of AgentSession
type AgentSessionSpec struct {
	Owner SessionOwner `json:"owner"`

	// GroupRef is the group the session is scoped to.
	GroupRef GroupRef `json:"groupRef"`

	// Mode is readonly or sandbox.
	// +optional
	Mode SessionMode `json:"mode,omitempty"`

	// Tools is the allowlist of MCP tools this session may call. A tool absent
	// from the list is refused before it reaches the platform.
	// +optional
	Tools []ToolName `json:"tools,omitempty"`

	// +optional
	Sandbox SandboxSpec `json:"sandbox,omitempty"`
}

// AgentSessionStatus defines the observed state of AgentSession
type AgentSessionStatus struct {
	// +optional
	Phase SessionPhase `json:"phase,omitempty"`

	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// SandboxNamespace is the namespace the session may write in. It is not a
	// space: it carries kubecube.io/namespace-level: sandbox, so the readers
	// that enumerate a project's spaces never see it.
	// +optional
	SandboxNamespace string `json:"sandboxNamespace,omitempty"`

	// SandboxNamespaceUID binds the session token to one namespace object, so
	// deleting the namespace and recreating the same name invalidates the token
	// rather than handing the session a fresh sandbox.
	// +optional
	SandboxNamespaceUID string `json:"sandboxNamespaceUID,omitempty"`

	// TokenJTI is the id of the token currently issued for this session. A
	// token whose jti is not this value is refused.
	// +optional
	TokenJTI string `json:"tokenJTI,omitempty"`

	// +optional
	LastUsedAt *metav1.Time `json:"lastUsedAt,omitempty"`

	// +optional
	Actions int64 `json:"actions,omitempty"`

	// +optional
	UsedQuota corev1.ResourceList `json:"usedQuota,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// AgentSession is the Schema for the agentsessions API
// +kubebuilder:resource:categories="kubecube",scope="Namespaced"
// +kubebuilder:printcolumn:name="Group",type=string,JSONPath=`.spec.groupRef.name`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="ExpiresAt",type="date",JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type AgentSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentSessionSpec   `json:"spec,omitempty"`
	Status AgentSessionStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// AgentSessionList contains a list of AgentSession
type AgentSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentSession `json:"items"`
}
