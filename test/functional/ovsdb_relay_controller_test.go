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
	"os"

	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports

	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	ovnv1 "github.com/openstack-k8s-operators/ovn-operator/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)

func metav1ObjectMeta(namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		GenerateName: "ovsdbrelay-",
		Namespace:    namespace,
	}
}

var _ = Describe("OVSDBRelay controller", func() {
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
})
