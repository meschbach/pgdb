/*
Copyright 2023.

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

type DatabaseState string

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.
const (
	Ready                    DatabaseState = "Ready"
	MissingClusterSecret     DatabaseState = "MissingClusterSecret"
	ClusterConnectionFailure DatabaseState = "ClusterConnectionFailure"
	PermissionsFailure       DatabaseState = "PermissionDenied"
)

// DatabaseSpec defines the desired state of Database
type DatabaseSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ClusterSecret is the name of the a secret containing postgres login info for setting up a datastore
	ClusterSecret string `json:"clusterSecret,omitempty"`
	// ClusterNamespace is the namespace of the cluster secret
	ClusterNamespace string `json:"clusterNamespace,omitempty"`
	//DatabaseSecret is the target secret to write authentication info to.
	DatabaseSecret string `json:"databaseSecret,omitempty"`
	//Controller allows multiple controllers to run in a cluster simultaneously.  If not set then the default controller
	//will be used
	Controller string `json:"controller,omitempty"`
	//AllowPasswordSpecials will allow for special characters in passwords, such as ()[]{}*$! etc
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	AllowPasswordSpecials bool `json:"allowPasswordSpecials"`
}

func (d DatabaseSpec) MatchesController(controllerName string) bool {
	if d.Controller == "" {
		d.Controller = "default"
	}
	return d.Controller == controllerName
}

// DatabaseStatus defines the observed state of Database
type DatabaseStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	DatabaseName string        `json:"database-name,omitempty"`
	State        DatabaseState `json:"state"`
	//ClusterSecretValid is true when the cluster secret has a proper structure for connection, otherwise false.
	//Missing or nil indicates this was not resolved.
	ClusterSecretValid *bool `json:"secret-valid,omitempty"`
	//DatabaseSecretName is the name of the created database secret
	DatabaseSecretName *string `json:"database-secret,omitempty"`
	//Connected indicates the operator was able to connect to the target cluster. Missing or nil means a connection
	// was not attempted
	Connected *bool `json:"connected,omitempty"`
	//Ready indicates teh database has been created and is ready for use
	Ready bool `json:"ready,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.state`

// Database is the Schema for the databases API
type Database struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DatabaseSpec   `json:"spec,omitempty"`
	Status DatabaseStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DatabaseList contains a list of Database
type DatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Database `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Database{}, &DatabaseList{})
}
