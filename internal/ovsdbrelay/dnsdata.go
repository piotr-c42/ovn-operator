/*
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

package ovsdbrelay

import (
	"context"
	"fmt"

	infranetworkv1 "github.com/openstack-k8s-operators/infra-operator/apis/network/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// DNSData - Create DNS entries that openstack dnsmasq will resolve.
func DNSData(
	ctx context.Context,
	helper *helper.Helper,
	serviceName string,
	ipList []string,
	namespace string,
	serviceLabels map[string]string,
) error {
	dnsHostname := serviceName + "." + namespace + ".svc"
	dnsHosts := []infranetworkv1.DNSHost{}
	for _, ip := range ipList {
		record := infranetworkv1.DNSHost{
			IP: ip,
			Hostnames: []string{
				dnsHostname,
			},
		}
		dnsHosts = append(dnsHosts, record)
	}

	// Create DNSData object
	dnsData := &infranetworkv1.DNSData{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels:    serviceLabels,
		},
	}

	_, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), dnsData, func() error {
		dnsData.Spec.Hosts = dnsHosts
		// TODO: use value from DNSMasq instance instead of hardcode
		dnsData.Spec.DNSDataLabelSelectorValue = "dnsdata"
		err := controllerutil.SetControllerReference(helper.GetBeforeObject(), dnsData, helper.GetScheme())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error creating DNSData %s: %w", dnsData.Name, err)
	}
	return nil
}

// GetDBAddress - return the connection string for the given service.
func GetDBAddress(svcSpec *corev1.ServiceSpec, serviceName string, namespace string, scheme string) string {
	if svcSpec == nil || len(svcSpec.Ports) == 0 {
		return ""
	}
	dnsHostname := serviceName + "." + namespace + ".svc"
	return fmt.Sprintf("%s:%s:%d", scheme, dnsHostname, svcSpec.Ports[0].Port)
}
