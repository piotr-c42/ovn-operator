package ovsdbrelay

import (
	"fmt"
	"strconv"
	"strings"

	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/affinity"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	ovnv1 "github.com/openstack-k8s-operators/ovn-operator/api/v1beta1"
	ovn_common "github.com/openstack-k8s-operators/ovn-operator/internal/common"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// StatefulSet builds the relay StatefulSet.
func StatefulSet(
	instance *ovnv1.OVSDBRelay,
	configHash string,
	labels map[string]string,
	annotations map[string]string,
	topology *topologyv1.Topology,
) (*appsv1.StatefulSet, error) {
	startupProbe := &corev1.Probe{
		// A relay performs a full SB database sync on first start, which can
		// take several minutes on large deployments.
		TimeoutSeconds:      5,
		PeriodSeconds:       10,
		FailureThreshold:    120,
		InitialDelaySeconds: 5,
	}
	startupProbe.Exec = &corev1.ExecAction{
		Command: []string{ScriptsPath + "/" + LivenessScript},
	}
	readinessProbe := &corev1.Probe{
		TimeoutSeconds:      5,
		PeriodSeconds:       10,
		FailureThreshold:    3,
		InitialDelaySeconds: 5,
	}
	readinessProbe.Exec = &corev1.ExecAction{
		Command: []string{ScriptsPath + "/" + ReadinessScript},
	}
	livenessProbe := &corev1.Probe{
		TimeoutSeconds:      5,
		PeriodSeconds:       10,
		FailureThreshold:    3,
		InitialDelaySeconds: 10,
	}
	livenessProbe.Exec = &corev1.ExecAction{
		Command: []string{ScriptsPath + "/" + LivenessScript},
	}

	useRemoteInDB := UseRemoteInDB(instance)

	envVars := map[string]env.Setter{}
	envVars["CONFIG_HASH"] = env.SetValue(configHash)
	envVars["DB_TYPE"] = env.SetValue(strings.ToLower(instance.Spec.DBType))
	envVars["DB_NAME"] = env.SetValue(DBNameSB)
	envVars["PORT"] = env.SetValue(fmt.Sprintf("%d", GetPort(instance)))
	envVars["USE_REMOTE_IN_DB"] = env.SetValue(strconv.FormatBool(useRemoteInDB))
	envVars["UPSTREAM"] = env.SetValue(instance.Status.Upstream)
	envVars["LOG_LEVEL"] = env.SetValue(instance.Spec.LogLevel)
	envVars["OVN_RUNDIR"] = env.SetValue(RelayRunDir)
	envVars["TLS_ENABLED"] = env.SetValue(strconv.FormatBool(instance.Spec.TLS.Enabled()))
	if instance.Spec.TLS.Enabled() {
		envVars["OVNDB_CERT_PATH"] = env.SetValue(ovn_common.OVNDbCertPath)
		envVars["OVNDB_KEY_PATH"] = env.SetValue(ovn_common.OVNDbKeyPath)
		envVars["OVNDB_CA_CERT_PATH"] = env.SetValue(ovn_common.OVNDbCaCertPath)
	}

	var scriptsVolumeDefaultMode int32 = 0755
	volumes := []corev1.Volume{
		{
			Name: "scripts",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					DefaultMode: &scriptsVolumeDefaultMode,
					LocalObjectReference: corev1.LocalObjectReference{
						Name: instance.Name + "-scripts",
					},
				},
			},
		},
		{
			Name: "run",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "scripts",
			MountPath: ScriptsPath,
			ReadOnly:  true,
		},
		{
			Name:      "run",
			MountPath: RelayRunDir,
		},
	}

	// add CA bundle if defined
	if instance.Spec.TLS.CaBundleSecretName != "" {
		volumes = append(volumes, instance.Spec.TLS.CreateVolume())
		volumeMounts = append(volumeMounts, instance.Spec.TLS.CreateVolumeMounts(nil)...)
	}

	// add relay cert and CA
	if instance.Spec.TLS.Enabled() {
		svc := tls.Service{
			SecretName: *instance.Spec.TLS.SecretName,
			CertMount:  ptr.To(ovn_common.OVNDbCertPath),
			KeyMount:   ptr.To(ovn_common.OVNDbKeyPath),
			CaMount:    ptr.To(ovn_common.OVNDbCaCertPath),
		}
		volumes = append(volumes, svc.CreateVolume(TLSVolumeID))
		volumeMounts = append(volumeMounts, svc.CreateVolumeMounts(TLSVolumeID)...)
	}

	metricsEnabled := instance.Spec.ExporterImage != "" &&
		(instance.Spec.MetricsEnabled == nil || *instance.Spec.MetricsEnabled)
	metricsTLS := metricsEnabled && instance.Spec.MetricsTLS.Enabled()
	// The exporter serves HTTPS when web-config carries tls_server_config, so
	// the kubelet probes must use the same scheme.
	metricsProbeScheme := corev1.URISchemeHTTP
	if metricsTLS {
		metricsProbeScheme = corev1.URISchemeHTTPS
	}
	metricsVolumeMounts := []corev1.VolumeMount{
		{
			Name:      "run",
			MountPath: RelayRunDir,
			ReadOnly:  true,
		},
		{
			Name:      "metrics-config",
			MountPath: "/etc/config",
			ReadOnly:  true,
		},
	}
	if metricsEnabled {
		volumes = append(volumes, corev1.Volume{
			Name: "metrics-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: instance.Name + "-metrics-config",
					},
				},
			},
		})
	}
	if metricsTLS {
		metricsSvc := tls.Service{
			SecretName: *instance.Spec.MetricsTLS.SecretName,
			CertMount:  ptr.To(ovn_common.OVNMetricsCertPath),
			KeyMount:   ptr.To(ovn_common.OVNMetricsKeyPath),
			CaMount:    ptr.To(ovn_common.OVNDbCaCertPath),
		}
		volumes = append(volumes, metricsSvc.CreateVolume("metrics-certs"))
		metricsVolumeMounts = append(metricsVolumeMounts, metricsSvc.CreateVolumeMounts("metrics-certs")...)
	}

	containers := []corev1.Container{
		{
			Name:                     ContainerName,
			Command:                  []string{"/bin/bash", ScriptsPath + "/" + StartScript},
			Image:                    instance.Spec.ContainerImage,
			Env:                      env.MergeEnvs([]corev1.EnvVar{}, envVars),
			VolumeMounts:             volumeMounts,
			Resources:                instance.Spec.Resources,
			ReadinessProbe:           readinessProbe,
			LivenessProbe:            livenessProbe,
			StartupProbe:             startupProbe,
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		},
	}
	if metricsEnabled {
		containers = append(containers, corev1.Container{
			Name:    "openstack-network-exporter",
			Image:   instance.Spec.ExporterImage,
			Command: []string{"/app/openstack-network-exporter"},
			Env: []corev1.EnvVar{
				{
					Name:  "OPENSTACK_NETWORK_EXPORTER_YAML",
					Value: "/etc/config/openstack-network-exporter.yaml",
				},
			},
			Ports: []corev1.ContainerPort{
				{
					Name:          "metrics",
					ContainerPort: ovn_common.MetricsPort,
					Protocol:      corev1.ProtocolTCP,
				},
			},
			VolumeMounts: metricsVolumeMounts,
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/metrics",
						Port:   intstr.FromInt32(ovn_common.MetricsPort),
						Scheme: metricsProbeScheme,
					},
				},
				InitialDelaySeconds: 5,
				PeriodSeconds:       10,
				FailureThreshold:    3,
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/metrics",
						Port:   intstr.FromInt32(ovn_common.MetricsPort),
						Scheme: metricsProbeScheme,
					},
				},
				InitialDelaySeconds: 10,
				PeriodSeconds:       10,
				FailureThreshold:    3,
			},
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		})
	}

	statefulset := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name,
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			ServiceName:         instance.Name + HeadlessServiceSuffix,
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Replicas:            ptr.To(GetReplicas(instance)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
					Labels:      labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: instance.RbacResourceName(),
					Containers:         containers,
					Volumes:            volumes,
				},
			},
		},
	}

	nodeSelector := map[string]string{}
	if instance.Spec.NodeSelector != nil {
		for k, v := range *instance.Spec.NodeSelector {
			nodeSelector[k] = v
		}
	}
	if instance.Spec.AvailabilityZone != "" {
		nodeSelector["topology.kubernetes.io/zone"] = instance.Spec.AvailabilityZone
	}
	if len(nodeSelector) > 0 {
		statefulset.Spec.Template.Spec.NodeSelector = nodeSelector
	}

	if topology != nil {
		topology.ApplyTo(&statefulset.Spec.Template)
	} else {
		// If possible two pods of the same relay group should not run on the
		// same worker node.
		statefulset.Spec.Template.Spec.Affinity = affinity.DistributePods(
			common.AppSelector,
			[]string{instance.Name},
			corev1.LabelHostname,
		)
	}

	return statefulset, nil
}
