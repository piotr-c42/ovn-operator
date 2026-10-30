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
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	ovnv1 "github.com/openstack-k8s-operators/ovn-operator/api/v1beta1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

func GetDefaultOVSDBRelaySpec() ovnv1.OVSDBRelaySpec {
	return ovnv1.OVSDBRelaySpec{
		OVSDBRelaySpecCore: ovnv1.OVSDBRelaySpecCore{
			DBType: ovnv1.SBDBType,
		},
	}
}

func GetTLSOVSDBRelaySpec() ovnv1.OVSDBRelaySpec {
	spec := GetDefaultOVSDBRelaySpec()
	spec.TLS = tls.SimpleService{
		Ca: tls.Ca{
			CaBundleSecretName: CABundleSecretName,
		},
		GenericService: tls.GenericService{
			SecretName: ptr.To(OvnDbCertSecretName),
		},
	}
	return spec
}

func CreateOVSDBRelay(namespace string, spec ovnv1.OVSDBRelaySpec) types.NamespacedName {
	return ovn.CreateOVSDBRelay(nil, namespace, spec)
}

func GetOVSDBRelay(name types.NamespacedName) *ovnv1.OVSDBRelay {
	return ovn.GetOVSDBRelay(name)
}

func OVSDBRelayConditionGetter(name types.NamespacedName) condition.Conditions {
	instance := ovn.GetOVSDBRelay(name)
	return instance.Status.Conditions
}

func DeleteOVSDBRelay(name types.NamespacedName) {
	ovn.DeleteOVSDBRelay(name)
}
