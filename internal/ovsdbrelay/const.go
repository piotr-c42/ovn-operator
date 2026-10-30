// Package ovsdbrelay provides functionality for managing OVSDB relay groups
package ovsdbrelay

import (
	ovnv1 "github.com/openstack-k8s-operators/ovn-operator/api/v1beta1"
)

const (
	// DBNameSB is the OVSDB database name of the OVN Southbound database
	DBNameSB = "OVN_Southbound"

	// RelayRunDir is the runtime directory of the relay process
	RelayRunDir = "/run/ovn"

	// ScriptsPath is where the scripts ConfigMap is mounted
	ScriptsPath = "/usr/local/bin/container-scripts"
	// StartScript is the relay entrypoint script
	StartScript = "start-relay.sh"
	// ReadinessScript is the relay readiness probe script
	ReadinessScript = "readiness.sh"
	// LivenessScript is the relay liveness probe script
	LivenessScript = "liveness.sh"

	// HeadlessServiceSuffix is appended to the CR name for the StatefulSet's
	// headless Service.
	HeadlessServiceSuffix = "-headless"

	// TLSVolumeID is the identifier used for TLS volume and mount names. It is
	// kept short to stay within the 63 character volume name limit.
	TLSVolumeID = "relay"

	// ContainerName is the name of the relay container
	ContainerName = "ovsdb-relay"
)

// GetPort returns the configured relay port.
func GetPort(instance *ovnv1.OVSDBRelay) int32 {
	if instance.Spec.Port != nil {
		return *instance.Spec.Port
	}
	return ovnv1.OVSDBRelayPortDefault
}

// GetReplicas returns the configured number of relay replicas.
func GetReplicas(instance *ovnv1.OVSDBRelay) int32 {
	if instance.Spec.Replicas != nil {
		return *instance.Spec.Replicas
	}
	return ovnv1.OVSDBRelayReplicasDefault
}

// UseRemoteInDB returns the effective value of spec.useRemoteInDB.
func UseRemoteInDB(instance *ovnv1.OVSDBRelay) bool {
	return instance.Spec.UseRemoteInDB == nil || *instance.Spec.UseRemoteInDB
}
