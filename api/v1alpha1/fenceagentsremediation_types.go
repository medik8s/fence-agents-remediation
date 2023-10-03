/*
Copyright 2022.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	// FARFinalizer is a finalizer for a FenceAgentsRemediation CR deletion
	FARFinalizer string = "fence-agents-remediation.medik8s.io/far-finalizer"
	// Taints
	FARNoExecuteTaintKey = "medik8s.io/fence-agents-remediation"
	// FenceAgentActionSucceededType is the condition type used to signal whether the Fence Agent action was succeeded successfully or not
	FenceAgentActionSucceededType = "FenceAgentActionSucceeded"
	// condition messages
	RemediationFinishedNodeNotFoundConditionMessage = "FAR CR name doesn't match a node name"
	RemediationInterruptedByNHCConditionMessage     = "Node Healthcheck timeout annotation has been set"
	RemediationStartedConditionMessage              = "FAR CR was found, its name matches one of the cluster nodes, and a finalizer was set to the CR"
	FenceAgentSucceededConditionMessage             = "FAR taint was added and the fence agent command has been created and executed successfully"
	RemediationFinishedSuccessfullyConditionMessage = "The unhealthy node was fully remediated (it was tainted, fenced using FA and all the node resources have been deleted)"
)

// ConditionsChangeReason represents the reason of updating the some or all the conditions
type ConditionsChangeReason string

const (
	// RemediationFinishedNodeNotFound - CR was found but its name doesn't matche a node
	RemediationFinishedNodeNotFound ConditionsChangeReason = "RemediationFinishedNodeNotFound"
	// RemediationInterruptedByNHC - Remediation was interrupted by NHC timeout annotation
	RemediationInterruptedByNHC ConditionsChangeReason = "RemediationInterruptedByNHC"
	// RemediationStarted - CR was found, its name matches a node, and a finalizer was set
	RemediationStarted ConditionsChangeReason = "RemediationStarted"
	// FenceAgentSucceeded - FAR taint was added, fence agent command has been created and executed successfully
	FenceAgentSucceeded ConditionsChangeReason = "FenceAgentSucceeded"
	// RemediationFinishedSuccessfully - The unhealthy node was fully remediated/fenced (it was tainted, fenced by FA and all of its resources have been deleted)
	RemediationFinishedSuccessfully ConditionsChangeReason = "RemediationFinishedSuccessfully"
)

type ParameterName string
type NodeName string

// FenceAgentsRemediationSpec defines the desired state of FenceAgentsRemediation
type FenceAgentsRemediationSpec struct {
	// Agent is the name of fence agent that will be used
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	Agent string `json:"agent"`

	// SharedParameters are passed to the fencing agent regardless of which node is about to be fenced (i.e., they are common for all the nodes)
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	SharedParameters map[ParameterName]string `json:"sharedparameters,omitempty"`

	// NodeParameters are passed to the fencing agent according to the node that is fenced, since they are node specific
	//+operator-sdk:csv:customresourcedefinitions:type=spec
	NodeParameters map[ParameterName]map[NodeName]string `json:"nodeparameters,omitempty"`
}

// FenceAgentsRemediationStatus defines the observed state of FenceAgentsRemediation
type FenceAgentsRemediationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Represents the observations of a FenceAgentsRemediation's current state.
	// Known .status.conditions.type are: "Processing", "FenceAgentActionSucceeded", and "Succeeded".
	// +listType=map
	// +listMapKey=type
	//+optional
	// +operator-sdk:csv:customresourcedefinitions:type=status,displayName="conditions",xDescriptors="urn:alm:descriptor:io.kubernetes.conditions"
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastUpdateTime is the last time the status was updated.
	//
	//+optional
	//+kubebuilder:validation:Type=string
	//+kubebuilder:validation:Format=date-time
	//+operator-sdk:csv:customresourcedefinitions:type=status
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=far

// +operator-sdk:csv:customresourcedefinitions:resources={{"FenceAgentsRemediation","v1alpha1","fenceagentsremediations"}}
// FenceAgentsRemediation is the Schema for the fenceagentsremediations API
type FenceAgentsRemediation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FenceAgentsRemediationSpec   `json:"spec,omitempty"`
	Status FenceAgentsRemediationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// FenceAgentsRemediationList contains a list of FenceAgentsRemediation
type FenceAgentsRemediationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FenceAgentsRemediation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FenceAgentsRemediation{}, &FenceAgentsRemediationList{})
}
