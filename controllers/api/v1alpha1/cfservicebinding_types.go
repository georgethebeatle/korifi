/*
Copyright 2021.

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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// CFServiceBindingSpec defines the desired state of CFServiceBinding
type CFServiceBindingSpec struct {
	// The mutable, user-friendly name of the service binding. Unlike metadata.name, the user can change this field
	DisplayName *string `json:"displayName,omitempty"`

	// The Service this binding uses. When created by the korifi API, this will refer to a CFServiceInstance
	Service v1.ObjectReference `json:"service"`

	// A reference to the CFApp that owns this service binding. The CFApp must be in the same namespace
	AppRef v1.LocalObjectReference `json:"appRef"`
}

// CFServiceBindingStatus defines the observed state of CFServiceBinding
type CFServiceBindingStatus struct {
	// A reference to the Secret containing the credentials.
	// This is required to conform to the Kubernetes Service Bindings spec
	// +optional
	Binding v1.LocalObjectReference `json:"binding"`

	// Conditions capture the current status of the CFServiceBinding
	Conditions []metav1.Condition `json:"conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=`.metadata.creationTimestamp`

// CFServiceBinding is the Schema for the cfservicebindings API
type CFServiceBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec CFServiceBindingSpec `json:"spec,omitempty"`

	Status CFServiceBindingStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CFServiceBindingList contains a list of CFServiceBinding
type CFServiceBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CFServiceBinding `json:"items"`
}

func (b CFServiceBinding) StatusConditions() []metav1.Condition {
	return b.Status.Conditions
}

func init() {
	SchemeBuilder.Register(&CFServiceBinding{}, &CFServiceBindingList{})
}
