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

type parameterName string
type nodeName string

// FenceAgentsRemediationTemplateSpec defines the desired state of FenceAgentsRemediationTemplate
type FenceAgentsRemediationTemplateSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of FenceAgentsRemediationTemplate. Edit fenceagentsremediationtemplate_types.go to remove/update
	Foo string `json:"foo,omitempty"`

	// Agent is the type of fence agent that will be used
	Agent string `json:"agent"`

	// SharedParameters are passed to the fencing agent no matter which node is fenced (i.e they are common for all the nodes)
	SharedParameters map[parameterName]string `json:"sharedparameters,omitempty"`

	// NodeParameters are node specific they are passed to the fencing agent according to the node that is fenced
	NodeParameters map[parameterName]NodeValues `json:"nodeparameters,omitempty"`
}

type NodeValues struct {
	NodeNameValueMapping map[nodeName]string `json:"nodenamevaluemapping"`
}

// FenceAgentsRemediationTemplateStatus defines the observed state of FenceAgentsRemediationTemplate
type FenceAgentsRemediationTemplateStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// FenceAgentsRemediationTemplate is the Schema for the fenceagentsremediationtemplates API
type FenceAgentsRemediationTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FenceAgentsRemediationTemplateSpec   `json:"spec,omitempty"`
	Status FenceAgentsRemediationTemplateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FenceAgentsRemediationTemplateList contains a list of FenceAgentsRemediationTemplate
type FenceAgentsRemediationTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FenceAgentsRemediationTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FenceAgentsRemediationTemplate{}, &FenceAgentsRemediationTemplateList{})
}
