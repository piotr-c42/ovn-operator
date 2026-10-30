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

package functional_test

import (
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports

	infranetworkv1 "github.com/openstack-k8s-operators/infra-operator/apis/network/v1beta1"
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	ovnv1 "github.com/openstack-k8s-operators/ovn-operator/api/v1beta1"
	ovn_common "github.com/openstack-k8s-operators/ovn-operator/internal/common"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)

func metav1ObjectMeta(namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		GenerateName: "ovsdbrelay-",
		Namespace:    namespace,
	}
}

func relayEnvMap(sts *appsv1.StatefulSet) map[string]string {
	envMap := map[string]string{}
	for _, envVar := range sts.Spec.Template.Spec.Containers[0].Env {
		envMap[envVar.Name] = envVar.Value
	}
	return envMap
}

var _ = Describe("OVSDBRelay controller", func() {
	When("an OVSDBRelay is created with defaults", func() {
		It("defaults the spec and creates the workload, PDB and Services", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			relay := CreateOVSDBRelay(namespace, GetDefaultOVSDBRelaySpec())
			DeferCleanup(DeleteOVSDBRelay, relay)

			instance := GetOVSDBRelay(relay)
			Expect(instance.Spec.DBType).To(Equal(ovnv1.SBDBType))
			Expect(instance.Spec.Replicas).To(Equal(ptr.To(int32(2))))
			Expect(instance.Spec.UseRemoteInDB).To(Equal(ptr.To(true)))
			Expect(instance.Spec.Port).To(Equal(ptr.To(int32(6642))))
			Expect(instance.Spec.LogLevel).To(Equal("info"))
			Expect(instance.Spec.ContainerImage).NotTo(BeEmpty())
			Expect(instance.Spec.ExporterImage).NotTo(BeEmpty())

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			sts := th.GetStatefulSet(stsName)

			Expect(sts.Spec.PodManagementPolicy).To(Equal(appsv1.ParallelPodManagement))
			Expect(sts.Spec.Replicas).To(Equal(ptr.To(int32(2))))
			Expect(sts.Spec.ServiceName).To(Equal(relay.Name + "-headless"))
			Expect(sts.Spec.VolumeClaimTemplates).To(BeEmpty())
			Expect(sts.Spec.Template.Spec.Affinity).NotTo(BeNil())
			Expect(sts.Spec.Template.Spec.NodeSelector).To(BeEmpty())

			container := sts.Spec.Template.Spec.Containers[0]
			Expect(container.Name).To(Equal("ovsdb-relay"))
			Expect(container.Command).To(Equal([]string{
				"/bin/bash", "/usr/local/bin/container-scripts/start-relay.sh",
			}))
			Expect(container.ReadinessProbe.Exec.Command).To(Equal([]string{
				"/usr/local/bin/container-scripts/readiness.sh",
			}))
			Expect(container.LivenessProbe.Exec.Command).To(Equal([]string{
				"/usr/local/bin/container-scripts/liveness.sh",
			}))
			Expect(container.StartupProbe.Exec.Command).To(Equal([]string{
				"/usr/local/bin/container-scripts/liveness.sh",
			}))
			// startup probe must tolerate a full SB sync
			Expect(container.StartupProbe.FailureThreshold).To(BeNumerically(">=", 60))

			// the exporter image is defaulted, so the metrics sidecar runs
			Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(2))
			Expect(sts.Spec.Template.Spec.Containers[1].Name).To(Equal("openstack-network-exporter"))
			Expect(sts.Spec.Template.Spec.Containers[1].Image).To(Equal(instance.Spec.ExporterImage))

			envMap := relayEnvMap(sts)
			Expect(envMap["DB_NAME"]).To(Equal("OVN_Southbound"))
			Expect(envMap["PORT"]).To(Equal("6642"))
			Expect(envMap["USE_REMOTE_IN_DB"]).To(Equal("true"))
			Expect(envMap["TLS_ENABLED"]).To(Equal("false"))
			Expect(envMap["UPSTREAM"]).NotTo(BeEmpty())

			// emptyDir for /run/ovn, no PVC
			foundRunVolume := false
			for _, volume := range sts.Spec.Template.Spec.Volumes {
				if volume.Name == "run" {
					foundRunVolume = true
					Expect(volume.EmptyDir).NotTo(BeNil())
				}
			}
			Expect(foundRunVolume).To(BeTrue())

			// PDB with maxUnavailable=1
			pdb := &policyv1.PodDisruptionBudget{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: relay.Name, Namespace: namespace}, pdb)).Should(Succeed())
			}).Should(Succeed())
			Expect(*pdb.Spec.MaxUnavailable).To(Equal(intstr.FromInt(1)))

			// Headless Service
			headless := th.GetService(types.NamespacedName{
				Name: relay.Name + "-headless", Namespace: namespace})
			Expect(headless.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
			Expect(headless.Spec.PublishNotReadyAddresses).To(BeTrue())

			// Simulate the relay pods becoming ready
			th.SimulateStatefulSetReplicaReadyWithPods(stsName, nil)

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.Conditions.IsTrue(condition.DeploymentReadyCondition)).To(BeTrue())
				g.Expect(inst.Status.Conditions.IsTrue(condition.ExposeServiceReadyCondition)).To(BeTrue())
				g.Expect(inst.Status.Conditions.IsTrue(condition.PDBReadyCondition)).To(BeTrue())
				g.Expect(inst.Status.Conditions.IsTrue(condition.ReadyCondition)).To(BeTrue())
				g.Expect(inst.Status.ReadyCount).To(Equal(int32(2)))
				g.Expect(inst.Status.InternalDBAddress).To(ContainSubstring(
					fmt.Sprintf("tcp:%s-0.%s.svc.", relay.Name, namespace)))
				g.Expect(inst.Status.InternalDBAddress).To(ContainSubstring(
					fmt.Sprintf("tcp:%s-1.%s.svc.", relay.Name, namespace)))
				g.Expect(inst.Status.DBAddress).To(BeEmpty())
			}).Should(Succeed())

			// Per-pod Services
			podSvc := th.GetService(types.NamespacedName{
				Name: relay.Name + "-0", Namespace: namespace})
			Expect(podSvc.Spec.Selector).To(HaveKeyWithValue(
				"statefulset.kubernetes.io/pod-name", relay.Name+"-0"))

			// External exposure was not requested, so no external config
			Expect(k8s_errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{
				Name: relay.Name + "-config", Namespace: namespace}, &corev1.ConfigMap{}))).To(BeTrue())
		})
	})

	When("an OVSDBRelay is exposed on a network attachment", func() {
		It("creates the external Service, DNSData and external config", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			_ = th.CreateNetworkAttachmentDefinition(types.NamespacedName{
				Namespace: namespace, Name: "internalapi"})

			spec := GetDefaultOVSDBRelaySpec()
			spec.NetworkAttachment = "internalapi"
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			th.GetStatefulSet(stsName)
			th.SimulateStatefulSetReplicaReadyWithPods(
				stsName,
				map[string][]string{namespace + "/internalapi": {"10.0.1.10"}},
			)

			expectedAddress := fmt.Sprintf("tcp:%s.%s.svc:6642", relay.Name, namespace)
			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.DBAddress).To(Equal(expectedAddress))
				g.Expect(inst.Status.Conditions.IsTrue(ovnv1.ExternalConfigReadyCondition)).To(BeTrue())
				g.Expect(inst.Status.Conditions.IsTrue(condition.NetworkAttachmentsReadyCondition)).To(BeTrue())
				g.Expect(inst.Status.NetworkAttachments).To(
					HaveKeyWithValue(namespace+"/internalapi", ConsistOf("10.0.1.10", "10.0.1.10")))
			}).Should(Succeed())

			// external Service
			external := th.GetService(types.NamespacedName{Name: relay.Name, Namespace: namespace})
			Expect(external.Spec.ClusterIP).NotTo(Equal(corev1.ClusterIPNone))
			Expect(external.Labels).To(HaveKeyWithValue("type", ovnv1.ServiceExternalType))

			// DNSData points the external name at the relay pod IPs
			dnsData := GetDNSData(types.NamespacedName{Name: relay.Name, Namespace: namespace})
			Expect(dnsData.Spec.Hosts).To(HaveLen(2))
			Expect(dnsData.Spec.Hosts[0].Hostnames).To(ContainElement(
				fmt.Sprintf("%s.%s.svc", relay.Name, namespace)))
			Expect(dnsData.Spec.Hosts[0].IP).To(Equal("10.0.1.10"))

			// external config ConfigMap consumed by dataplane
			cm := th.GetConfigMap(types.NamespacedName{Name: relay.Name + "-config", Namespace: namespace})
			Expect(cm.Data).To(HaveKeyWithValue("ovsdb-config",
				ContainSubstring("ovn-remote: "+expectedAddress)))
		})
	})

	When("TLS is configured", func() {
		It("mounts the certs and enables TLS in the workload", func() {
			DeferCleanup(k8sClient.Delete, ctx, th.CreateCABundleSecret(types.NamespacedName{
				Name: CABundleSecretName, Namespace: namespace}))
			DeferCleanup(k8sClient.Delete, ctx, th.CreateCertSecret(types.NamespacedName{
				Name: OvnDbCertSecretName, Namespace: namespace}))

			dbSpec := GetTLSOVNDBClusterSpec()
			dbSpec.DBType = ovnv1.SBDBType
			dbSpec.Replicas = ptr.To(int32(1))
			db := CreateOVNDBCluster(namespace, dbSpec)
			dbName := types.NamespacedName{Name: db.GetName(), Namespace: namespace}
			DeferCleanup(th.DeleteInstance, GetOVNDBCluster(dbName))
			th.SimulateStatefulSetReplicaReadyWithPods(types.NamespacedName{
				Name: "ovsdbserver-sb", Namespace: namespace}, nil)
			Eventually(func(g Gomega) {
				g.Expect(GetOVNDBCluster(dbName).Status.InternalDBAddress).To(HavePrefix("ssl:"))
			}).Should(Succeed())

			spec := GetTLSOVSDBRelaySpec()
			spec.NodeSelector = &map[string]string{"node-role.kubernetes.io/worker": ""}
			spec.AvailabilityZone = "az1"
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			sts := th.GetStatefulSet(types.NamespacedName{Name: relay.Name, Namespace: namespace})

			envMap := relayEnvMap(sts)
			Expect(envMap["TLS_ENABLED"]).To(Equal("true"))
			Expect(envMap["OVNDB_CERT_PATH"]).To(Equal(ovn_common.OVNDbCertPath))
			Expect(envMap["OVNDB_KEY_PATH"]).To(Equal(ovn_common.OVNDbKeyPath))
			Expect(envMap["OVNDB_CA_CERT_PATH"]).To(Equal(ovn_common.OVNDbCaCertPath))

			// CA bundle and service cert volumes
			th.AssertVolumeExists(CABundleSecretName, sts.Spec.Template.Spec.Volumes)
			th.AssertVolumeExists("relay-tls-certs", sts.Spec.Template.Spec.Volumes)

			container := sts.Spec.Template.Spec.Containers[0]
			th.AssertVolumeMountExists(CABundleSecretName, "tls-ca-bundle.pem", container.VolumeMounts)
			th.AssertVolumeMountExists("relay-tls-certs", "tls.crt", container.VolumeMounts)
			th.AssertVolumeMountExists("relay-tls-certs", "tls.key", container.VolumeMounts)
			th.AssertVolumeMountExists("relay-tls-certs", "ca.crt", container.VolumeMounts)

			// AvailabilityZone and NodeSelector are both applied
			Expect(sts.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(
				"topology.kubernetes.io/zone", "az1"))
			Expect(sts.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(
				"node-role.kubernetes.io/worker", ""))
		})
	})

	When("the SB cluster uses TLS but the relay does not", func() {
		It("reports the missing relay TLS instead of running with an ssl upstream", func() {
			DeferCleanup(k8sClient.Delete, ctx, th.CreateCABundleSecret(types.NamespacedName{
				Name: CABundleSecretName, Namespace: namespace}))
			DeferCleanup(k8sClient.Delete, ctx, th.CreateCertSecret(types.NamespacedName{
				Name: OvnDbCertSecretName, Namespace: namespace}))

			dbSpec := GetTLSOVNDBClusterSpec()
			dbSpec.DBType = ovnv1.SBDBType
			dbSpec.Replicas = ptr.To(int32(1))
			db := CreateOVNDBCluster(namespace, dbSpec)
			dbName := types.NamespacedName{Name: db.GetName(), Namespace: namespace}
			DeferCleanup(th.DeleteInstance, GetOVNDBCluster(dbName))

			// The DB controller only publishes InternalDBAddress once its
			// StatefulSet is ready; envtest has no kubelet to do that.
			th.SimulateStatefulSetReplicaReadyWithPods(types.NamespacedName{
				Name: "ovsdbserver-sb", Namespace: namespace}, nil)

			// The derived address carries the ssl scheme because the SB
			// cluster has TLS configured.
			Eventually(func(g Gomega) {
				g.Expect(GetOVNDBCluster(dbName).Status.InternalDBAddress).To(HavePrefix("ssl:"))
			}, timeout, interval).Should(Succeed())

			relay := CreateOVSDBRelay(namespace, GetDefaultOVSDBRelaySpec())
			DeferCleanup(DeleteOVSDBRelay, relay)

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.Conditions.IsFalse(condition.InputReadyCondition)).To(BeTrue())
				g.Expect(inst.Status.Conditions.Get(condition.InputReadyCondition).Message).To(
					ContainSubstring("spec.tls.secretName is not set"))
			}).Should(Succeed())

			// No workload is created while the upstream cannot be reached.
			Expect(k8s_errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{
				Name: relay.Name, Namespace: namespace}, &appsv1.StatefulSet{}))).To(BeTrue())
		})
	})

	When("the relay uses TLS but the SB cluster does not", func() {
		It("reports the TLS mismatch instead of advertising ssl on a plain upstream", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			DeferCleanup(k8sClient.Delete, ctx, th.CreateCABundleSecret(types.NamespacedName{
				Name: CABundleSecretName, Namespace: namespace}))
			DeferCleanup(k8sClient.Delete, ctx, th.CreateCertSecret(types.NamespacedName{
				Name: OvnDbCertSecretName, Namespace: namespace}))

			relay := CreateOVSDBRelay(namespace, GetTLSOVSDBRelaySpec())
			DeferCleanup(DeleteOVSDBRelay, relay)

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.Conditions.IsFalse(condition.InputReadyCondition)).To(BeTrue())
				g.Expect(inst.Status.Conditions.Get(condition.InputReadyCondition).Message).To(
					ContainSubstring("does not use TLS"))
			}).Should(Succeed())

			Expect(k8s_errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{
				Name: relay.Name, Namespace: namespace}, &appsv1.StatefulSet{}))).To(BeTrue())
		})
	})

	When("the metrics TLS secret is created after the relay", func() {
		It("reconciles the relay without another event", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			metricsSecret := "relay-metrics-cert"
			spec := GetDefaultOVSDBRelaySpec()
			spec.MetricsTLS = tls.SimpleService{
				GenericService: tls.GenericService{SecretName: ptr.To(metricsSecret)},
			}
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.Conditions.IsFalse(condition.TLSInputReadyCondition)).To(BeTrue())
			}).Should(Succeed())

			DeferCleanup(k8sClient.Delete, ctx, th.CreateCertSecret(types.NamespacedName{
				Name: metricsSecret, Namespace: namespace}))

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.Conditions.IsTrue(condition.TLSInputReadyCondition)).To(BeTrue())
			}).Should(Succeed())

			sts := th.GetStatefulSet(types.NamespacedName{Name: relay.Name, Namespace: namespace})
			th.AssertVolumeExists("metrics-certs-tls-certs", sts.Spec.Template.Spec.Volumes)

			var exporter *corev1.Container
			for i := range sts.Spec.Template.Spec.Containers {
				if sts.Spec.Template.Spec.Containers[i].Name == "openstack-network-exporter" {
					exporter = &sts.Spec.Template.Spec.Containers[i]
				}
			}
			Expect(exporter).NotTo(BeNil())
			Expect(exporter.ReadinessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTPS))
			Expect(exporter.LivenessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTPS))
		})
	})

	When("an explicit upstream changes", func() {
		It("rolls the StatefulSet with the new upstream", func() {
			spec := GetDefaultOVSDBRelaySpec()
			spec.Upstream = "tcp:10.0.0.1:6642"
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			sts := th.GetStatefulSet(stsName)
			generation := sts.Generation
			Expect(relayEnvMap(sts)["UPSTREAM"]).To(Equal("tcp:10.0.0.1:6642"))

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				inst.Spec.Upstream = "tcp:10.0.0.2:6642"
				g.Expect(k8sClient.Update(ctx, inst)).Should(Succeed())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				sts := th.GetStatefulSet(stsName)
				g.Expect(sts.Generation).To(BeNumerically(">", generation))
				g.Expect(relayEnvMap(sts)["UPSTREAM"]).To(Equal("tcp:10.0.0.2:6642"))
				g.Expect(GetOVSDBRelay(relay).Status.Upstream).To(Equal("tcp:10.0.0.2:6642"))
			}).Should(Succeed())
		})
	})

	When("the explicit upstream uses a non-default port", func() {
		It("advertises spec.port independently of the upstream port", func() {
			spec := GetDefaultOVSDBRelaySpec()
			spec.Upstream = "tcp:10.0.0.1:6643"
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			sts := th.GetStatefulSet(stsName)
			envMap := relayEnvMap(sts)
			Expect(envMap["PORT"]).To(Equal("6642"))
			Expect(envMap["USE_REMOTE_IN_DB"]).To(Equal("true"))

			headless := th.GetService(types.NamespacedName{
				Name: relay.Name + "-headless", Namespace: namespace})
			Expect(headless.Spec.Ports[0].Port).To(Equal(int32(6642)))
		})
	})

	When("an invalid OVSDBRelay is submitted", func() {
		It("rejects an unsupported DB type", func() {
			spec := GetDefaultOVSDBRelaySpec()
			spec.DBType = ovnv1.NBDBType
			relay := &ovnv1.OVSDBRelay{
				ObjectMeta: metav1ObjectMeta(namespace),
				Spec:       spec,
			}
			Expect(k8sClient.Create(ctx, relay)).NotTo(Succeed())
		})

		It("rejects out of range replicas via the CRD schema", func() {
			// replicas bounds are enforced by the CRD markers; the webhook
			// does not duplicate them.
			spec := GetDefaultOVSDBRelaySpec()
			spec.Replicas = ptr.To(int32(0))
			relay := &ovnv1.OVSDBRelay{
				ObjectMeta: metav1ObjectMeta(namespace),
				Spec:       spec,
			}
			Expect(k8sClient.Create(ctx, relay)).NotTo(Succeed())
		})

		It("rejects an ssl upstream without TLS", func() {
			spec := GetDefaultOVSDBRelaySpec()
			spec.Upstream = "ssl:10.0.0.1:6642"
			relay := &ovnv1.OVSDBRelay{
				ObjectMeta: metav1ObjectMeta(namespace),
				Spec:       spec,
			}
			err := k8sClient.Create(ctx, relay)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ssl"))
		})

		It("rejects a topology in another namespace", func() {
			spec := GetDefaultOVSDBRelaySpec()
			spec.TopologyRef = &topologyv1.TopoRef{Name: "topology", Namespace: "other"}
			relay := &ovnv1.OVSDBRelay{
				ObjectMeta: metav1ObjectMeta(namespace),
				Spec:       spec,
			}
			Expect(k8sClient.Create(ctx, relay)).NotTo(Succeed())
		})
	})

	When("the shipped sample is applied", func() {
		It("is accepted by the API server", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			data, err := os.ReadFile("../../config/samples/ovn_v1beta1_ovsdb_relay.yaml")
			Expect(err).NotTo(HaveOccurred())

			relay := &ovnv1.OVSDBRelay{}
			Expect(yaml.Unmarshal(data, relay)).To(Succeed())
			relay.Namespace = namespace
			Expect(k8sClient.Create(ctx, relay)).To(Succeed())
			DeferCleanup(DeleteOVSDBRelay, types.NamespacedName{
				Name: relay.Name, Namespace: relay.Namespace})

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(types.NamespacedName{
					Name: relay.Name, Namespace: relay.Namespace})
				g.Expect(inst.Spec.Replicas).To(Equal(ptr.To(int32(2))))
				g.Expect(inst.Spec.AvailabilityZone).To(Equal("az1"))
			}).Should(Succeed())
		})
	})

	When("an OVSDBRelay is deleted", func() {
		It("removes the finalizer and leaves owned resources to garbage collection", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			relay := CreateOVSDBRelay(namespace, GetDefaultOVSDBRelaySpec())
			instance := GetOVSDBRelay(relay)

			sts := th.GetStatefulSet(types.NamespacedName{Name: relay.Name, Namespace: namespace})
			Expect(metav1.IsControlledBy(sts, instance)).To(BeTrue())
			headless := th.GetService(types.NamespacedName{
				Name: relay.Name + "-headless", Namespace: namespace})
			Expect(metav1.IsControlledBy(headless, instance)).To(BeTrue())
			pdb := &policyv1.PodDisruptionBudget{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: relay.Name, Namespace: namespace}, pdb)).Should(Succeed())
			}).Should(Succeed())
			Expect(metav1.IsControlledBy(pdb, instance)).To(BeTrue())

			DeleteOVSDBRelay(relay)

			// envtest runs no garbage collector, so the owned resources stay;
			// the owner references asserted above remove them in a real cluster.
			th.GetStatefulSet(types.NamespacedName{Name: relay.Name, Namespace: namespace})
		})
	})
	When("metrics are enabled", func() {
		It("creates the exporter sidecar, metrics port and config", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			spec := GetDefaultOVSDBRelaySpec()
			spec.ExporterImage = "quay.io/test/openstack-network-exporter:latest"
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			sts := th.GetStatefulSet(stsName)
			th.SimulateStatefulSetReplicaReadyWithPods(stsName, nil)
			var exporter *corev1.Container
			for i := range sts.Spec.Template.Spec.Containers {
				if sts.Spec.Template.Spec.Containers[i].Name == "openstack-network-exporter" {
					exporter = &sts.Spec.Template.Spec.Containers[i]
				}
			}
			Expect(exporter).NotTo(BeNil())
			Expect(exporter.Image).To(Equal(spec.ExporterImage))
			Expect(exporter.Ports).To(ContainElement(corev1.ContainerPort{
				Name: "metrics", ContainerPort: 1981, Protocol: corev1.ProtocolTCP,
			}))
			Expect(exporter.ReadinessProbe).NotTo(BeNil())
			Expect(exporter.ReadinessProbe.HTTPGet.Path).To(Equal("/metrics"))
			Expect(exporter.LivenessProbe).NotTo(BeNil())
			Expect(exporter.LivenessProbe.HTTPGet.Path).To(Equal("/metrics"))

			cm := th.GetConfigMap(types.NamespacedName{
				Name: relay.Name + "-metrics-config", Namespace: namespace})
			Expect(cm.Data).To(HaveKey("openstack-network-exporter.yaml"))
			Expect(cm.Data["openstack-network-exporter.yaml"]).To(ContainSubstring("collectors: ['ovnrelay']"))
			Expect(cm.Data["openstack-network-exporter.yaml"]).To(ContainSubstring("ovsdb-relay-rundir: /run/ovn"))

			var svc *corev1.Service
			Eventually(func(g Gomega) {
				svc = th.GetService(types.NamespacedName{
					Name: relay.Name + "-0", Namespace: namespace})
			}).Should(Succeed())
			foundMetrics := false
			for _, port := range svc.Spec.Ports {
				if port.Name == "metrics" && port.Port == 1981 {
					foundMetrics = true
				}
			}
			Expect(foundMetrics).To(BeTrue())
		})
	})

	When("metrics are disabled", func() {
		It("omits the exporter sidecar, metrics port and metrics config", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			spec := GetDefaultOVSDBRelaySpec()
			spec.ExporterImage = "quay.io/test/openstack-network-exporter:latest"
			spec.MetricsEnabled = ptr.To(false)
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			sts := th.GetStatefulSet(stsName)
			Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(1))
			th.SimulateStatefulSetReplicaReadyWithPods(stsName, nil)

			svc := th.GetService(types.NamespacedName{Name: relay.Name + "-0", Namespace: namespace})
			for _, port := range svc.Spec.Ports {
				Expect(port.Name).NotTo(Equal("metrics"))
			}
			th.AssertConfigMapDoesNotExist(types.NamespacedName{
				Name: relay.Name + "-metrics-config", Namespace: namespace})
		})
	})

	When("the relay port is overridden", func() {
		It("advertises the configured port to dataplane clients", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			_ = th.CreateNetworkAttachmentDefinition(types.NamespacedName{
				Namespace: namespace, Name: "internalapi"})

			spec := GetDefaultOVSDBRelaySpec()
			spec.NetworkAttachment = "internalapi"
			spec.Port = ptr.To(int32(16642))
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			sts := th.GetStatefulSet(stsName)
			Expect(relayEnvMap(sts)["PORT"]).To(Equal("16642"))
			th.SimulateStatefulSetReplicaReadyWithPods(
				stsName,
				map[string][]string{namespace + "/internalapi": {"10.0.1.10"}},
			)

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.InternalDBAddress).To(ContainSubstring(":16642"))
				g.Expect(inst.Status.DBAddress).To(Equal(
					fmt.Sprintf("tcp:%s.%s.svc:16642", relay.Name, namespace)))
			}).Should(Succeed())

			headless := th.GetService(types.NamespacedName{
				Name: relay.Name + "-headless", Namespace: namespace})
			Expect(headless.Spec.Ports[0].Port).To(Equal(int32(16642)))
			Expect(headless.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt32(16642)))

			podSvc := th.GetService(types.NamespacedName{
				Name: relay.Name + "-0", Namespace: namespace})
			Expect(podSvc.Spec.Ports[0].Port).To(Equal(int32(16642)))
			Expect(podSvc.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt32(16642)))

			dnsData := GetDNSData(types.NamespacedName{
				Name: relay.Name, Namespace: namespace})
			Expect(dnsData.Spec.Hosts[0].IP).To(Equal("10.0.1.10"))
		})

		It("uses the configured port when the relay does not read SB_Global", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			spec := GetDefaultOVSDBRelaySpec()
			spec.UseRemoteInDB = ptr.To(false)
			spec.Port = ptr.To(int32(16642))
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			sts := th.GetStatefulSet(stsName)
			Expect(relayEnvMap(sts)["USE_REMOTE_IN_DB"]).To(Equal("false"))
			Expect(relayEnvMap(sts)["PORT"]).To(Equal("16642"))
			th.SimulateStatefulSetReplicaReadyWithPods(stsName, nil)

			headless := th.GetService(types.NamespacedName{
				Name: relay.Name + "-headless", Namespace: namespace})
			Expect(headless.Spec.Ports[0].Port).To(Equal(int32(16642)))
			Expect(headless.Spec.Ports[0].TargetPort).To(Equal(intstr.FromInt32(16642)))
		})
	})

	When("a DB cluster name is set", func() {
		It("uses the referenced SB cluster", func() {
			dbSpec := GetDefaultOVNDBClusterSpec()
			dbSpec.DBType = ovnv1.SBDBType
			dbSpec.Replicas = ptr.To(int32(1))
			db := CreateOVNDBCluster(namespace, dbSpec)
			dbName := types.NamespacedName{Name: db.GetName(), Namespace: namespace}
			DeferCleanup(th.DeleteInstance, GetOVNDBCluster(dbName))

			th.SimulateStatefulSetReplicaReadyWithPods(types.NamespacedName{
				Name: "ovsdbserver-sb", Namespace: namespace}, nil)
			Eventually(func(g Gomega) {
				g.Expect(GetOVNDBCluster(dbName).Status.InternalDBAddress).NotTo(BeEmpty())
			}).Should(Succeed())

			spec := GetDefaultOVSDBRelaySpec()
			spec.DBClusterName = db.GetName()
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			Eventually(func(g Gomega) {
				g.Expect(GetOVSDBRelay(relay).Status.Upstream).To(
					Equal(GetOVNDBCluster(dbName).Status.InternalDBAddress))
			}).Should(Succeed())
		})
	})

	When("a DB cluster name points to an NB cluster", func() {
		It("reports the database type mismatch", func() {
			dbSpec := GetDefaultOVNDBClusterSpec()
			dbSpec.DBType = ovnv1.NBDBType
			dbSpec.Replicas = ptr.To(int32(1))
			db := CreateOVNDBCluster(namespace, dbSpec)
			dbName := types.NamespacedName{Name: db.GetName(), Namespace: namespace}
			DeferCleanup(th.DeleteInstance, GetOVNDBCluster(dbName))

			spec := GetDefaultOVSDBRelaySpec()
			spec.DBClusterName = db.GetName()
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			Eventually(func(g Gomega) {
				cond := GetOVSDBRelay(relay).Status.Conditions.Get(condition.InputReadyCondition)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(corev1.ConditionFalse))
				g.Expect(cond.Message).To(ContainSubstring("is of type NB"))
			}).Should(Succeed())
		})
	})

	When("the relay is exposed with a LoadBalancer service", func() {
		It("does not create DNSData and uses the service address", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			_ = th.CreateNetworkAttachmentDefinition(types.NamespacedName{
				Namespace: namespace, Name: "internalapi"})

			spec := GetDefaultOVSDBRelaySpec()
			spec.NetworkAttachment = "internalapi"
			spec.Override.Service = &service.OverrideSpec{
				EmbeddedLabelsAnnotations: &service.EmbeddedLabelsAnnotations{
					Annotations: map[string]string{
						"metallb.universe.tf/address-pool": "osp-internalapi",
					},
				},
				Spec: &service.OverrideServiceSpec{
					Type: corev1.ServiceTypeLoadBalancer,
				},
			}
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			th.GetStatefulSet(stsName)
			th.SimulateStatefulSetReplicaReadyWithPods(
				stsName,
				map[string][]string{namespace + "/internalapi": {"10.0.1.10"}},
			)
			th.SimulateLoadBalancerServiceIP(types.NamespacedName{
				Name: relay.Name, Namespace: namespace})

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.DBAddress).To(Equal(
					fmt.Sprintf("tcp:%s.%s.svc:6642", relay.Name, namespace)))
				g.Expect(inst.Status.Conditions.IsTrue(ovnv1.ExternalConfigReadyCondition)).To(BeTrue())
			}).Should(Succeed())

			external := th.GetService(types.NamespacedName{Name: relay.Name, Namespace: namespace})
			Expect(external.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))
			Expect(external.Labels).To(HaveKeyWithValue("type", ovnv1.ServiceExternalType))
			// The metrics sidecar is scraped through the per-pod Services, not
			// through the externally exposed one.
			Expect(external.Spec.Ports).To(HaveLen(1))
			Expect(external.Spec.Ports[0].Name).To(Equal("ovsdb"))

			dnsData := &infranetworkv1.DNSData{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: relay.Name, Namespace: namespace}, dnsData)
			Expect(k8s_errors.IsNotFound(err)).To(BeTrue())
		})
	})

	When("a topology is referenced", func() {
		It("applies and removes the topology", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			topologyName := types.NamespacedName{
				Namespace: namespace, Name: "relay-topology"}
			topologySpec, topologySpecObj := GetSampleTopologySpec("relay")
			_, topologyRef := infra.CreateTopology(topologyName, topologySpec)

			spec := GetDefaultOVSDBRelaySpec()
			spec.TopologyRef = &topologyRef
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.LastAppliedTopology).To(Equal(&topologyRef))
				g.Expect(th.GetStatefulSet(stsName).Spec.Template.Spec.TopologySpreadConstraints).To(
					Equal(topologySpecObj))
				g.Expect(th.GetStatefulSet(stsName).Spec.Template.Spec.Affinity).To(BeNil())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				tp := infra.GetTopology(topologyName)
				g.Expect(tp.GetFinalizers()).To(ContainElement(
					fmt.Sprintf("openstack.org/ovsdbrelay-%s", relay.Name)))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				inst.Spec.TopologyRef = nil
				g.Expect(k8sClient.Update(ctx, inst)).To(Succeed())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.LastAppliedTopology).To(BeNil())
				g.Expect(th.GetStatefulSet(stsName).Spec.Template.Spec.TopologySpreadConstraints).To(BeEmpty())
				g.Expect(th.GetStatefulSet(stsName).Spec.Template.Spec.Affinity).NotTo(BeNil())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(infra.GetTopology(topologyName).GetFinalizers()).To(BeEmpty())
			}).Should(Succeed())
		})
	})

	When("the relay is scaled down", func() {
		It("removes the stale per-pod Service and address", func() {
			dbs := CreateOVNDBClusters(namespace, nil, 1)
			DeferCleanup(DeleteOVNDBClusters, dbs)

			spec := GetDefaultOVSDBRelaySpec()
			spec.Replicas = ptr.To(int32(3))
			relay := CreateOVSDBRelay(namespace, spec)
			DeferCleanup(DeleteOVSDBRelay, relay)

			stsName := types.NamespacedName{Name: relay.Name, Namespace: namespace}
			th.GetStatefulSet(stsName)
			th.SimulateStatefulSetReplicaReadyWithPods(stsName, nil)

			Eventually(func(g Gomega) {
				g.Expect(GetOVSDBRelay(relay).Status.InternalDBAddress).To(
					ContainSubstring(relay.Name + "-2."))
			}).Should(Succeed())
			th.GetService(types.NamespacedName{Name: relay.Name + "-2", Namespace: namespace})

			Eventually(func(g Gomega) {
				inst := GetOVSDBRelay(relay)
				*inst.Spec.Replicas = 2
				g.Expect(k8sClient.Update(ctx, inst)).To(Succeed())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(*th.GetStatefulSet(stsName).Spec.Replicas).To(Equal(int32(2)))
			}).Should(Succeed())

			// envtest does not run the StatefulSet controller, so remove the
			// pod and mark the scaled StatefulSet ready to trigger a reconcile.
			pod := &corev1.Pod{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: relay.Name + "-2", Namespace: namespace}, pod)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pod)).To(Succeed())
			th.SimulateStatefulSetReplicaReady(stsName)

			Eventually(func(g Gomega) {
				svc := &corev1.Service{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: relay.Name + "-2", Namespace: namespace}, svc)
				g.Expect(k8s_errors.IsNotFound(err)).To(BeTrue())
				inst := GetOVSDBRelay(relay)
				g.Expect(inst.Status.InternalDBAddress).NotTo(ContainSubstring(relay.Name + "-2."))
				g.Expect(inst.Status.ReadyCount).To(Equal(int32(2)))
			}).Should(Succeed())
		})
	})

})
