package ovsdbrelay

import (
	ovnv1 "github.com/openstack-k8s-operators/ovn-operator/api/v1beta1"
	ovn_common "github.com/openstack-k8s-operators/ovn-operator/internal/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func relayPorts(instance *ovnv1.OVSDBRelay, includeMetrics bool) []corev1.ServicePort {
	ports := []corev1.ServicePort{
		{
			Name:     "ovsdb",
			Port:     GetPort(instance),
			Protocol: corev1.ProtocolTCP,
		},
	}
	if includeMetrics &&
		instance.Spec.ExporterImage != "" &&
		(instance.Spec.MetricsEnabled == nil || *instance.Spec.MetricsEnabled) {
		ports = append(ports, corev1.ServicePort{
			Name:     "metrics",
			Port:     ovn_common.MetricsPort,
			Protocol: corev1.ProtocolTCP,
		})
	}
	return ports
}

// HeadlessService - headless Service giving every relay pod a stable DNS name
// and used as the StatefulSet serviceName.
func HeadlessService(
	instance *ovnv1.OVSDBRelay,
	serviceLabels map[string]string,
	selectorLabels map[string]string,
) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + HeadlessServiceSuffix,
			Namespace: instance.Namespace,
			Labels:    serviceLabels,
		},
		Spec: corev1.ServiceSpec{
			Selector:                 selectorLabels,
			PublishNotReadyAddresses: true,
			Ports:                    relayPorts(instance, true),
			ClusterIP:                corev1.ClusterIPNone,
		},
	}
}

// PodService - Service selecting a single relay pod by its pod-name label.
func PodService(
	serviceName string,
	instance *ovnv1.OVSDBRelay,
	serviceLabels map[string]string,
	selectorLabels map[string]string,
) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: instance.Namespace,
			Labels:    serviceLabels,
		},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabels,
			Ports:    relayPorts(instance, true),
		},
	}
}

// ExternalService - Service exposing the relay group outside the cluster.
func ExternalService(
	instance *ovnv1.OVSDBRelay,
	serviceLabels map[string]string,
	selectorLabels map[string]string,
) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name,
			Namespace: instance.Namespace,
			Labels:    serviceLabels,
		},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabels,
			// Metrics stay on the headless and per-pod Services; exposing the
			// exporter port on the external Service would publish it outside
			// the cluster (and on a LoadBalancer, to the network).
			Ports: relayPorts(instance, false),
		},
	}
}
