/*
Copyright 2026.

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
	"fmt"

	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	// OVSDBRelayPortDefault is the default port the relay listens on
	OVSDBRelayPortDefault int32 = 6642

	// OVSDBRelayReplicasDefault is the default number of relay replicas
	OVSDBRelayReplicasDefault int32 = 2

	// OVSDBRelayLogLevelDefault is the default relay log level
	OVSDBRelayLogLevelDefault = "info"

	// OVSDBRelayContainerImage is the fall-back container image for OVSDBRelay.
	// The relay is stateless ovsdb-server, so the SB DB image is reused.
	OVSDBRelayContainerImage = OVNSBContainerImage

	// ServiceExternalType - Constant to identify externally exposed services
	ServiceExternalType = "external"
)

// OVSDBRelayDefaults -
type OVSDBRelayDefaults struct {
	ContainerImageURL string
	ExporterImageURL  string
}

// OVSDBRelaySpec defines the desired state of OVSDBRelay
type OVSDBRelaySpec struct {
	// +kubebuilder:validation:Required
	// ContainerImage - Container Image URL (will be set to environmental default if empty)
	ContainerImage string `json:"containerImage"`

	// +kubebuilder:validation:Optional
	// ExporterImage - Container Image URL for the openstack-network-exporter metrics sidecar (will be set to environmental default if empty)
	ExporterImage string `json:"exporterImage,omitempty"`

	OVSDBRelaySpecCore `json:",inline"`
}

// OVSDBRelaySpecCore -
type OVSDBRelaySpecCore struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="SB"
	// +kubebuilder:validation:Enum=SB
	// DBType - database type served by the relay. Only SB is supported for now.
	DBType string `json:"dbType"`

	// +kubebuilder:validation:Optional
	// DBClusterName - name of the OVNDBCluster this relay fronts. When empty
	// the first SB cluster in the namespace is used.
	DBClusterName string `json:"dbClusterName,omitempty"`

	// +kubebuilder:validation:Optional
	// Upstream - explicit upstream OVSDB connection string (e.g.
	// ssl:ovsdbserver-sb-0.example.svc:6642). When empty it is derived from
	// the referenced OVNDBCluster.
	Upstream string `json:"upstream,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=32
	// Replicas of the OVSDB relay to run
	Replicas *int32 `json:"replicas"`

	// +kubebuilder:validation:Optional
	// AvailabilityZone - when set, the relay pods are pinned to nodes in this
	// zone by adding topology.kubernetes.io/zone to the node selector.
	AvailabilityZone string `json:"availabilityZone,omitempty"`

	// +kubebuilder:validation:Optional
	// NodeSelector to target subset of worker nodes running this service
	NodeSelector *map[string]string `json:"nodeSelector,omitempty"`

	// +kubebuilder:validation:Optional
	// NetworkAttachment is a NetworkAttachment resource name to expose the
	// relay service to the given network.
	NetworkAttachment string `json:"networkAttachment"`

	// +kubebuilder:validation:Optional
	// Override, provides the ability to override the generated manifest of several child resources.
	Override OVSDBRelayOverrideSpec `json:"override,omitempty"`

	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	// TLS - Parameters related to TLS
	TLS tls.SimpleService `json:"tls,omitempty"`

	// +kubebuilder:validation:Optional
	// Resources - Compute Resources required by this service (Limits/Requests).
	// https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=off;emer;err;warn;info;dbg
	// +kubebuilder:default=info
	// LogLevel - Set log level off, emer, err, warn, info or dbg. Default is info.
	LogLevel string `json:"logLevel,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	// MetricsEnabled enables the metrics sidecar container for collecting relay metrics.
	// The sidecar only runs when ExporterImage is set; it defaults to the environmental
	// openstack-network-exporter image.
	MetricsEnabled *bool `json:"metricsEnabled,omitempty"`

	// +kubebuilder:validation:Optional
	// MetricsTLS - Parameters related to TLS for metrics sidecar
	MetricsTLS tls.SimpleService `json:"metricsTLS,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	// UseRemoteInDB - when true the relay also serves the connection stored in
	// SB_Global.connections (db: remote), which binds the SB database's own
	// port and scheme (6642 for OVN SB). The relay always serves spec.port:
	// through the db: listener when the ports match, otherwise through an
	// additional pssl/ptcp listener. The upstream scheme must match spec.tls:
	// with TLS enabled a plain upstream is rejected. When false it listens
	// only on pssl/ptcp:<spec.port>.
	UseRemoteInDB *bool `json:"useRemoteInDB,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=6642
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// Port - port the relay listens on and exposes through its Services.
	Port *int32 `json:"port,omitempty"`

	// +kubebuilder:validation:Optional
	// TopologyRef to apply the Topology defined by the associated CR referenced
	// by name
	TopologyRef *topologyv1.TopoRef `json:"topologyRef,omitempty"`
}

// OVSDBRelayOverrideSpec to override the generated manifest of several child resources.
type OVSDBRelayOverrideSpec struct {
	// Override configuration for the external Service created to serve traffic
	// to the relay group.
	Service *service.OverrideSpec `json:"service,omitempty"`
}

// OVSDBRelayStatus defines the observed state of OVSDBRelay
type OVSDBRelayStatus struct {
	// ReadyCount of OVSDB relay instances
	ReadyCount int32 `json:"readyCount,omitempty"`

	// Map of hashes to track e.g. job status
	Hash map[string]string `json:"hash,omitempty"`

	// Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`

	// NetworkAttachments status of the deployment pods
	NetworkAttachments map[string][]string `json:"networkAttachments,omitempty"`

	// InternalDBAddress - relay addresses used by other Pods in the cluster
	InternalDBAddress string `json:"internalDbAddress,omitempty"`

	// DBAddress - relay address used by external nodes
	DBAddress string `json:"dbAddress,omitempty"`

	// Upstream - OVSDB connection string the relay forwards writes to
	Upstream string `json:"upstream,omitempty"`

	//ObservedGeneration - the most recent generation observed for this service. If the observed generation is less than the spec generation, then the controller has not processed the latest changes.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastAppliedTopology - the last applied Topology
	LastAppliedTopology *topologyv1.TopoRef `json:"lastAppliedTopology,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="NetworkAttachments",type="string",JSONPath=".status.networkAttachments",description="NetworkAttachments"
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[0].status",description="Status"
//+kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[0].message",description="Message"

// OVSDBRelay is the Schema for the ovsdbrelays API
type OVSDBRelay struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OVSDBRelaySpec   `json:"spec,omitempty"`
	Status OVSDBRelayStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// OVSDBRelayList contains a list of OVSDBRelay
type OVSDBRelayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OVSDBRelay `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OVSDBRelay{}, &OVSDBRelayList{})
}

// IsReady - returns true if service is ready to serve requests
func (instance OVSDBRelay) IsReady() bool {
	return instance.Status.Conditions.IsTrue(condition.ReadyCondition)
}

// RbacConditionsSet - set the conditions for the rbac object
func (instance OVSDBRelay) RbacConditionsSet(c *condition.Condition) {
	instance.Status.Conditions.Set(c)
}

// RbacNamespace - return the namespace
func (instance OVSDBRelay) RbacNamespace() string {
	return instance.Namespace
}

// RbacResourceName - return the name to be used for rbac objects (serviceaccount, role, rolebinding)
func (instance OVSDBRelay) RbacResourceName() string {
	return "ovsdb-relay-" + instance.Name
}

// GetInternalEndpoint - return the DNS name that openshift coreDNS can resolve
func (instance OVSDBRelay) GetInternalEndpoint() (string, error) {
	if instance.Status.InternalDBAddress == "" {
		return "", fmt.Errorf("internal DBEndpoint not ready yet for %s", instance.Spec.DBType)
	}
	return instance.Status.InternalDBAddress, nil
}

// ExternalRequested - returns true when the relay must be exposed outside the
// cluster.
func (instance OVSDBRelay) ExternalRequested() bool {
	return instance.Spec.NetworkAttachment != "" || instance.Spec.Override.Service != nil
}

// GetExternalEndpoint - return the DNS that openstack dnsmasq can resolve
func (instance OVSDBRelay) GetExternalEndpoint() (string, error) {
	if instance.ExternalRequested() && instance.Status.DBAddress == "" {
		return "", fmt.Errorf("external DBEndpoint not ready yet for %s", instance.Spec.DBType)
	}
	return instance.Status.DBAddress, nil
}

// GetSpecTopologyRef - Returns the TopologyRef set in the Spec
func (instance *OVSDBRelay) GetSpecTopologyRef() *topologyv1.TopoRef {
	return instance.Spec.TopologyRef
}

// GetLastAppliedTopology - Returns the LastAppliedTopology Set in the Status
func (instance *OVSDBRelay) GetLastAppliedTopology() *topologyv1.TopoRef {
	return instance.Status.LastAppliedTopology
}

// SetLastAppliedTopology - Sets the LastAppliedTopology value in the Status
func (instance *OVSDBRelay) SetLastAppliedTopology(topologyRef *topologyv1.TopoRef) {
	instance.Status.LastAppliedTopology = topologyRef
}

// ValidateTopology -
func (instance *OVSDBRelaySpecCore) ValidateTopology(
	basePath *field.Path,
	namespace string,
) field.ErrorList {
	var allErrs field.ErrorList
	allErrs = append(allErrs, topologyv1.ValidateTopologyRef(
		instance.TopologyRef,
		*basePath.Child("topologyRef"), namespace)...)
	return allErrs
}
