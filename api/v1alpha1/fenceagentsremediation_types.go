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

type ParameterName string
type NodeName string

const (
	// Taints
	Medik8sRemediationTaintKey = "medik8s.io/remediation"
	FARRemediationTaintValue   = "fence-agents-remediation"
)

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
