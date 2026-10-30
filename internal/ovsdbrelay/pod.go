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

	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	nad "github.com/openstack-k8s-operators/lib-common/modules/common/networkattachment"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s_labels "k8s.io/apimachinery/pkg/labels"
)

// PodsWithLabels - Query the pods in a namespace matching the given labels.
func PodsWithLabels(
	ctx context.Context,
	helper *helper.Helper,
	namespace string,
	serviceLabels map[string]string,
) (*corev1.PodList, error) {
	podSelectorString := k8s_labels.Set(serviceLabels).String()
	return helper.GetKClient().CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: podSelectorString})
}

// PodIPInNetwork - Return the IP of a pod on the given network attachment.
func PodIPInNetwork(
	pod corev1.Pod,
	namespace string,
	networkAttachment string,
	conditions *condition.Conditions,
) (string, error) {
	netStat, err := nad.GetNetworkStatusFromAnnotation(pod.Annotations)
	if err != nil {
		err = fmt.Errorf("error while getting the Network Status for pod %s: %w", pod.Name, err)
		return "", err
	}
	for _, v := range netStat {
		if v.Name == namespace+"/"+networkAttachment {
			for _, ip := range v.IPs {
				return ip, nil
			}
		}
	}
	err = fmt.Errorf("%w IP address from pod %s in network %s, IP is empty", util.ErrInvalidStatus, pod.Name, networkAttachment)
	conditions.Set(condition.FalseCondition(
		condition.NetworkAttachmentsReadyCondition,
		condition.ErrorReason,
		condition.SeverityWarning,
		condition.NetworkAttachmentsReadyErrorMessage,
		err.Error()))
	return "", err
}
