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

package v1beta1

import (
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	// Cell0Name is the name of Cell0 cell that is mandatory in every deployment
	Cell0Name = "cell0"
)

// NovaCellTemplate defines the input parameters specified by the user to
// create a NovaCell via higher level CRDs.
type NovaCellTemplate struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=openstack
	// CellDatabaseInstance is the name of the MariaDB CR to select the DB
	// Service instance used as the DB of this cell.
	CellDatabaseInstance string `json:"cellDatabaseInstance"`

	// +kubebuilder:validation:Required
	// CellDatabaseUser - username to use when accessing the give cell DB
	CellDatabaseUser string `json:"cellDatabaseUser"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=rabbitmq
	// CellMessageBusInstance is the name of the RabbitMqCluster CR to select
	// the Message Bus Service instance used by the nova services to
	// communicate in this cell. For cell0 it is unused.
	CellMessageBusInstance string `json:"cellMessageBusInstance"`

	// +kubebuilder:validation:Required
	// HasAPIAccess defines if this Cell is configured to have access to the
	// API DB and message bus.
	HasAPIAccess bool `json:"hasAPIAccess"`

	// +kubebuilder:validation:Optional
	// NodeSelector to target subset of worker nodes running cell.
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={containerImage: "quay.io/podified-antelope-centos9/openstack-nova-conductor:current-podified"}
	// ConductorServiceTemplate - defines the cell conductor deployment for the cell.
	ConductorServiceTemplate NovaConductorTemplate `json:"conductorServiceTemplate"`

	// +kubebuilder:validation:Optional
	// MetadataServiceTemplate - defines the metadata service dedicated for the
	// cell. Note that for cell0 metadata service should not be deployed. Also
	// if metadata service needs to be deployed per cell here then it should
	// not be enabled to be deployed on the top level via the Nova CR at the
	// same time. By default Nova CR deploys the metadata service at the top
	// level and disables it on the cell level.
	MetadataServiceTemplate NovaMetadataTemplate `json:"metadataServiceTemplate"`

	// +kubebuilder:validation:Optional
	// NoVNCProxyServiceTemplate - defines the novncproxy service dedicated for
	// the cell. Note that for cell0 novncproxy should not be deployed so
	// the enabled field of this template is defaulted to false in cell0 but
	// defaulted to true in other cells.
	NoVNCProxyServiceTemplate NovaNoVNCProxyTemplate `json:"noVNCProxyServiceTemplate"`

	// NovaComputeTemplates - map of nova computes template with selected drivers in format
	// compute_name: compute_template.Key from map is arbitrary name for the compute with
	// a limit of 20 characters.
	NovaComputeTemplates map[string]NovaComputeTemplate `json:"novaComputeTemplates,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={database: NovaCell0DatabasePassword}
	// PasswordSelectors - Selectors to identify the DB passwords from the
	// Secret
	PasswordSelectors CellPasswordSelector `json:"passwordSelectors"`
}

// NovaCellSpec defines the desired state of NovaCell
type NovaCellSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Required
	// CellName is the name of the Nova Cell. The value "cell0" has a special
	// meaning. The "cell0" Cell cannot have compute nodes associated and the
	// conductor in this cell acts as the super conductor for all the cells in
	// the deployment.
	CellName string `json:"cellName"`

	// +kubebuilder:validation:Required
	// Secret is the name of the Secret instance containing password
	// information for the nova cell. This secret is expected to be
	// generated by the nova-operator based on the information passed to the
	// Nova CR.
	Secret string `json:"secret"`

	// +kubebuilder:validation:Optional
	// NodeSelector to target subset of worker nodes running this services.
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=nova
	// ServiceUser - optional username used for this service to register in
	// keystone
	ServiceUser string `json:"serviceUser"`

	// +kubebuilder:validation:Required
	// KeystoneAuthURL - the URL that the service in the cell can use to talk
	// to keystone
	KeystoneAuthURL string `json:"keystoneAuthURL"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=nova
	// APIDatabaseUser - username to use when accessing the API DB
	APIDatabaseUser string `json:"apiDatabaseUser"`

	// +kubebuilder:validation:Optional
	// APIDatabaseHostname - hostname to use when accessing the API DB. If not
	// provided then up-calls will be disabled. This filed is Required for
	// cell0.
	// TODO(gibi): Add a webhook to validate cell0 constraint
	APIDatabaseHostname string `json:"apiDatabaseHostname"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=nova
	// CellDatabaseUser - username to use when accessing the cell DB
	CellDatabaseUser string `json:"cellDatabaseUser"`

	// +kubebuilder:validation:Required
	// CellDatabaseHostname - hostname to use when accessing the cell DB
	CellDatabaseHostname string `json:"cellDatabaseHostname"`

	// +kubebuilder:validation:Optional
	// Debug - enable debug for different deploy stages. If an init container
	// is used, it runs and the actual action pod gets started with sleep
	// infinity
	Debug Debug `json:"debug,omitempty"`

	// +kubebuilder:validation:Required
	// ConductorServiceTemplate - defines the cell conductor deployment for the cell
	ConductorServiceTemplate NovaConductorTemplate `json:"conductorServiceTemplate"`

	// +kubebuilder:validation:Optional
	// MetadataServiceTemplate - defines the metadata service dedicated for the cell.
	MetadataServiceTemplate NovaMetadataTemplate `json:"metadataServiceTemplate"`

	// +kubebuilder:validation:Required
	// NoVNCProxyServiceTemplate - defines the novvncproxy service dedicated for
	// the cell.
	NoVNCProxyServiceTemplate NovaNoVNCProxyTemplate `json:"noVNCProxyServiceTemplate"`

	// +kubebuilder:validation:Optional
	// NovaComputeTemplates - map of nova computes template with selected drivers in format
	// compute_name: compute_template. Key from map is arbitrary name for the compute.
	// because of that there is a 20 character limit on the compute name.
	NovaComputeTemplates map[string]NovaComputeTemplate `json:"novaComputeTemplates,omitempty"`

	// +kubebuilder:validation:Required
	// ServiceAccount - service account name used internally to provide Nova services the default SA name
	ServiceAccount string `json:"serviceAccount"`
}

// NovaCellStatus defines the observed state of NovaCell
type NovaCellStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// Map of hashes to track e.g. job status
	Hash map[string]string `json:"hash,omitempty"`

	// Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`

	// ConductorServiceReadyCount defines the number of replicas ready from
	// nova-conductor service in the cell
	ConductorServiceReadyCount int32 `json:"conductorServiceReadyCount,omitempty"`

	// MetadataServiceReadyCount defines the number of replicas ready from
	// nova-metadata service in the cell
	MetadataServiceReadyCount int32 `json:"metadataServiceReadyCount,omitempty"`

	// NoVNCPRoxyServiceReadyCount defines the number of replicas ready from
	// nova-novncproxy service in the cell
	NoVNCPRoxyServiceReadyCount int32 `json:"noVNCProxyServiceReadyCount,omitempty"`

	// NovaComputesStatus is a map with format cell_name: NovaComputeCellStatus
	// where NovaComputeCellStatus tell if compute with selected name deployed successfully
	// and indicates if the compute is successfully mapped to the cell in
	// the nova_api database.
	// When a compute is removed from the Spec the operator will delete the
	// related NovaCompute CR and then remove the compute from this Status field.
	NovaComputesStatus map[string]NovaComputeCellStatus `json:"novaComputesStatus,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// NovaCell is the Schema for the novacells API
type NovaCell struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NovaCellSpec   `json:"spec,omitempty"`
	Status NovaCellStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// NovaCellList contains a list of NovaCell
type NovaCellList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NovaCell `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NovaCell{}, &NovaCellList{})
}

// GetConditions returns the list of conditions from the status
func (s NovaCellStatus) GetConditions() condition.Conditions {
	return s.Conditions
}

// IsReady returns true if the Cell reconciled successfully
func (instance NovaCell) IsReady() bool {
	return instance.Status.Conditions.IsTrue(condition.ReadyCondition)
}

// GetSecret returns the value of the NovaCell.Spec.Secret
func (n NovaCell) GetSecret() string {
	return n.Spec.Secret
}
