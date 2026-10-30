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

package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	infranetworkv1 "github.com/openstack-k8s-operators/infra-operator/apis/network/v1beta1"
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/clusterdns"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/configmap"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	nad "github.com/openstack-k8s-operators/lib-common/modules/common/networkattachment"
	"github.com/openstack-k8s-operators/lib-common/modules/common/pdb"
	common_rbac "github.com/openstack-k8s-operators/lib-common/modules/common/rbac"
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	"github.com/openstack-k8s-operators/lib-common/modules/common/statefulset"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	ovnv1 "github.com/openstack-k8s-operators/ovn-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/ovn-operator/internal/ovsdbrelay"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// fields to index to reconcile OVSDBRelay when referenced objects change
var ovsdbRelayWatchFields = []string{
	caBundleSecretNameField,
	tlsField,
	metricsTLSField,
	topologyField,
}

// OVSDBRelayReconciler reconciles a OVSDBRelay object
type OVSDBRelayReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Scheme  *runtime.Scheme
}

// GetClient -
func (r *OVSDBRelayReconciler) GetClient() client.Client {
	return r.Client
}

// GetKClient -
func (r *OVSDBRelayReconciler) GetKClient() kubernetes.Interface {
	return r.Kclient
}

// GetScheme -
func (r *OVSDBRelayReconciler) GetScheme() *runtime.Scheme {
	return r.Scheme
}

// GetLogger returns a logger object with a prefix of "controller.name" and additional controller context fields
func (r *OVSDBRelayReconciler) GetLogger(ctx context.Context) logr.Logger {
	return log.FromContext(ctx).WithName("Controllers").WithName("OVSDBRelay")
}

//+kubebuilder:rbac:groups=ovn.openstack.org,resources=ovsdbrelays,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ovn.openstack.org,resources=ovsdbrelays/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ovn.openstack.org,resources=ovsdbrelays/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=ovn.openstack.org,resources=ovndbclusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete;
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete;
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;patch;update;delete;
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;patch;update;delete;
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;
//+kubebuilder:rbac:groups=k8s.cni.cncf.io,resources=network-attachment-definitions,verbs=get;list;watch
//+kubebuilder:rbac:groups=network.openstack.org,resources=dnsdata,verbs=get;list;watch;create;update;patch;delete

// service account, role, rolebinding
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=rolebindings,verbs=get;list;watch;create;update;patch
// service account permissions that are needed to grant permission to the above
// +kubebuilder:rbac:groups="security.openshift.io",resourceNames=restricted-v2,resources=securitycontextconstraints,verbs=use
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=topology.openstack.org,resources=topologies,verbs=get;list;watch;update

// Reconcile - OVSDB relay
func (r *OVSDBRelayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	Log := r.GetLogger(ctx)

	// Fetch the OVSDBRelay instance
	instance := &ovnv1.OVSDBRelay{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	helper, err := helper.NewHelper(
		instance,
		r.Client,
		r.Kclient,
		r.Scheme,
		Log,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	//
	// initialize status
	//
	if instance.Status.Conditions == nil {
		instance.Status.Conditions = condition.Conditions{}
	}

	// Save a copy of the conditions so that we can restore the LastTransitionTime
	// when a condition's state doesn't change.
	savedConditions := instance.Status.Conditions.DeepCopy()

	// initialize conditions used later as Status=Unknown
	cl := condition.CreateList(
		condition.UnknownCondition(condition.InputReadyCondition, condition.InitReason, condition.InputReadyInitMessage),
		condition.UnknownCondition(condition.ExposeServiceReadyCondition, condition.InitReason, condition.ExposeServiceReadyInitMessage),
		condition.UnknownCondition(condition.ServiceConfigReadyCondition, condition.InitReason, condition.ServiceConfigReadyInitMessage),
		condition.UnknownCondition(condition.NetworkAttachmentsReadyCondition, condition.InitReason, condition.NetworkAttachmentsReadyInitMessage),
		condition.UnknownCondition(condition.DeploymentReadyCondition, condition.InitReason, condition.DeploymentReadyInitMessage),
		condition.UnknownCondition(condition.ServiceAccountReadyCondition, condition.InitReason, condition.ServiceAccountReadyInitMessage),
		condition.UnknownCondition(condition.RoleReadyCondition, condition.InitReason, condition.RoleReadyInitMessage),
		condition.UnknownCondition(condition.RoleBindingReadyCondition, condition.InitReason, condition.RoleBindingReadyInitMessage),
		condition.UnknownCondition(condition.TLSInputReadyCondition, condition.InitReason, condition.InputReadyInitMessage),
		condition.UnknownCondition(condition.PDBReadyCondition, condition.InitReason, condition.PDBReadyInitMessage),
		condition.UnknownCondition(ovnv1.ExternalConfigReadyCondition, condition.InitReason, ovnv1.ExternalConfigInitMessage),
	)

	// Init Topology condition if there's a reference
	if instance.Spec.TopologyRef != nil {
		c := condition.UnknownCondition(condition.TopologyReadyCondition, condition.InitReason, condition.TopologyReadyInitMessage)
		cl.Set(c)
	}
	instance.Status.Conditions.Init(&cl)
	instance.Status.ObservedGeneration = instance.Generation

	if instance.Status.Hash == nil {
		instance.Status.Hash = map[string]string{}
	}
	if instance.Status.NetworkAttachments == nil {
		instance.Status.NetworkAttachments = map[string][]string{}
	}

	// Always patch the instance status when exiting this function so we can persist any changes.
	defer func() {
		// Don't update the status, if reconciler Panics
		if r := recover(); r != nil {
			Log.Info(fmt.Sprintf("panic during reconcile %v\n", r))
			panic(r)
		}
		// update the Ready condition based on the sub conditions
		if instance.Status.Conditions.AllSubConditionIsTrue() {
			instance.Status.Conditions.MarkTrue(
				condition.ReadyCondition, condition.ReadyMessage)
		} else {
			// something is not ready so reset the Ready condition
			instance.Status.Conditions.MarkUnknown(
				condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage)
			// and recalculate it based on the state of the rest of the conditions
			instance.Status.Conditions.Set(
				instance.Status.Conditions.Mirror(condition.ReadyCondition))
		}
		condition.RestoreLastTransitionTimes(&instance.Status.Conditions, savedConditions)
		err := helper.PatchInstance(ctx, instance)
		if err != nil {
			_err = err
			return
		}
	}()

	// If we're not deleting this and the service object doesn't have our finalizer, add it.
	if instance.DeletionTimestamp.IsZero() && controllerutil.AddFinalizer(instance, helper.GetFinalizer()) {
		return ctrl.Result{}, nil
	}

	// Handle service delete
	if !instance.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, instance, helper)
	}

	// Handle non-deleted relays
	return r.reconcileNormal(ctx, instance, helper)
}

// SetupWithManager sets up the controller with the Manager.
func (r *OVSDBRelayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	crs := &ovnv1.OVSDBRelayList{}

	// index caBundleSecretNameField
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &ovnv1.OVSDBRelay{}, caBundleSecretNameField, func(rawObj client.Object) []string {
		cr := rawObj.(*ovnv1.OVSDBRelay)
		if cr.Spec.TLS.CaBundleSecretName == "" {
			return nil
		}
		return []string{cr.Spec.TLS.CaBundleSecretName}
	}); err != nil {
		return err
	}

	// index tlsField
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &ovnv1.OVSDBRelay{}, tlsField, func(rawObj client.Object) []string {
		cr := rawObj.(*ovnv1.OVSDBRelay)
		if cr.Spec.TLS.SecretName == nil {
			return nil
		}
		return []string{*cr.Spec.TLS.SecretName}
	}); err != nil {
		return err
	}

	// index metricsTLSField
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &ovnv1.OVSDBRelay{}, metricsTLSField, func(rawObj client.Object) []string {
		// Extract the metrics TLS secret name from the spec, if one is provided
		cr := rawObj.(*ovnv1.OVSDBRelay)
		if cr.Spec.MetricsTLS.SecretName == nil {
			return nil
		}
		return []string{*cr.Spec.MetricsTLS.SecretName}
	}); err != nil {
		return err
	}

	// index topologyField
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &ovnv1.OVSDBRelay{}, topologyField, func(rawObj client.Object) []string {
		cr := rawObj.(*ovnv1.OVSDBRelay)
		if cr.Spec.TopologyRef == nil {
			return nil
		}
		return []string{cr.Spec.TopologyRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&ovnv1.OVSDBRelay{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&infranetworkv1.DNSData{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Watches(&ovnv1.OVNDBCluster{}, handler.EnqueueRequestsFromMapFunc(ovnv1.OVNCRNamespaceMapFunc(crs, mgr.GetClient()))).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findObjectsForSrc),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Watches(&topologyv1.Topology{},
			handler.EnqueueRequestsFromMapFunc(r.findObjectsForSrc),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

func (r *OVSDBRelayReconciler) findObjectsForSrc(ctx context.Context, src client.Object) []reconcile.Request {
	requests := []reconcile.Request{}

	Log := r.GetLogger(ctx)

	for _, field := range ovsdbRelayWatchFields {
		crList := &ovnv1.OVSDBRelayList{}
		listOps := &client.ListOptions{
			FieldSelector: fields.OneTermEqualSelector(field, src.GetName()),
			Namespace:     src.GetNamespace(),
		}
		err := r.List(ctx, crList, listOps)
		if err != nil {
			Log.Error(err, fmt.Sprintf("listing %s for field: %s - %s", crList.GroupVersionKind().Kind, field, src.GetNamespace()))
			return requests
		}

		for _, item := range crList.Items {
			Log.Info(fmt.Sprintf("input source %s changed, reconcile: %s - %s", src.GetName(), item.GetName(), item.GetNamespace()))

			requests = append(requests,
				reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      item.GetName(),
						Namespace: item.GetNamespace(),
					},
				},
			)
		}
	}

	return requests
}

func (r *OVSDBRelayReconciler) reconcileDelete(ctx context.Context, instance *ovnv1.OVSDBRelay, helper *helper.Helper) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info("Reconciling OVSDBRelay delete")

	// Remove finalizer on the Topology CR
	if ctrlResult, err := topologyv1.EnsureDeletedTopologyRef(
		ctx,
		helper,
		instance.Status.LastAppliedTopology,
		instance.Name,
	); err != nil {
		return ctrlResult, err
	}

	// Service is deleted so remove the finalizer.
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
	Log.Info("Reconciled OVSDBRelay delete successfully")

	return ctrl.Result{}, nil
}

func (r *OVSDBRelayReconciler) reconcileNormal(ctx context.Context, instance *ovnv1.OVSDBRelay, helper *helper.Helper) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info("Reconciling OVSDBRelay")

	// Service account, role, binding
	rbacRules := []rbacv1.PolicyRule{
		{
			APIGroups:     []string{"security.openshift.io"},
			ResourceNames: []string{"restricted-v2"},
			Resources:     []string{"securitycontextconstraints"},
			Verbs:         []string{"use"},
		},
	}
	rbacResult, err := common_rbac.ReconcileRbac(ctx, helper, instance, rbacRules)
	if err != nil {
		return rbacResult, err
	} else if (rbacResult != ctrl.Result{}) {
		return rbacResult, nil
	}

	serviceLabels := map[string]string{
		common.AppSelector: instance.Name,
	}

	//
	// Resolve the upstream connection
	//
	upstream, err := r.resolveUpstream(ctx, helper, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			"Waiting for the relay upstream: %s",
			err.Error()))
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	instance.Status.Upstream = upstream
	if ovnv1.UpstreamUsesSSL(upstream) && !instance.Spec.TLS.Enabled() {
		// The upstream can be derived from the OVNDBCluster and be ssl: even
		// when spec.upstream is not set, so this cannot be left to the webhook.
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			"Upstream %s uses TLS but spec.tls.secretName is not set", upstream))
		return ctrl.Result{}, nil
	}
	if ovsdbrelay.UseRemoteInDB(instance) &&
		!ovnv1.UpstreamUsesSSL(upstream) && instance.Spec.TLS.Enabled() {
		// The db: remote serves the connection stored in
		// SB_Global.connections, so its scheme must match the upstream's;
		// otherwise it exposes a plaintext listener alongside the TLS one.
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			"Upstream %s does not use TLS but spec.tls.secretName is set", upstream))
		return ctrl.Result{}, nil
	}
	instance.Status.Conditions.MarkTrue(condition.InputReadyCondition, condition.InputReadyMessage)

	//
	// Network attachments
	//
	networkAttachments := []string{}
	nadList := []netattdefv1.NetworkAttachmentDefinition{}
	if instance.Spec.NetworkAttachment != "" {
		networkAttachments = append(networkAttachments, instance.Spec.NetworkAttachment)

		netAttachDef, err := nad.GetNADWithName(ctx, helper, instance.Spec.NetworkAttachment, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				Log.Info(fmt.Sprintf("network-attachment-definition %s not found", instance.Spec.NetworkAttachment))
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.NetworkAttachmentsReadyCondition,
					condition.ErrorReason,
					condition.SeverityWarning,
					condition.NetworkAttachmentsReadyWaitingMessage,
					instance.Spec.NetworkAttachment))
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.NetworkAttachmentsReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.NetworkAttachmentsReadyErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}

		if netAttachDef != nil {
			nadList = append(nadList, *netAttachDef)
		}
	}

	serviceAnnotations, err := nad.EnsureNetworksAnnotation(nadList)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed create network annotation from %s: %w",
			instance.Spec.NetworkAttachment, err)
	}

	//
	// TLS input validation
	//
	configMapVars := make(map[string]env.Setter)

	if instance.Spec.TLS.CaBundleSecretName != "" {
		hash, err := tls.ValidateCACertSecret(
			ctx,
			helper.GetClient(),
			types.NamespacedName{
				Name:      instance.Spec.TLS.CaBundleSecretName,
				Namespace: instance.Namespace,
			},
		)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.TLSInputReadyCondition,
					condition.ErrorReason,
					condition.SeverityWarning,
					condition.TLSInputReadyWaitingMessage, instance.Spec.TLS.CaBundleSecretName))
				return ctrl.Result{}, nil
			}
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.TLSInputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.TLSInputErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}

		if hash != "" {
			configMapVars[tls.CABundleKey] = env.SetValue(hash)
		}
	}

	if instance.Spec.TLS.Enabled() {
		hash, err := instance.Spec.TLS.ValidateCertSecret(ctx, helper, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.TLSInputReadyCondition,
					condition.RequestedReason,
					condition.SeverityInfo,
					condition.TLSInputReadyWaitingMessage, err.Error()))
				return ctrl.Result{}, nil
			}
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.TLSInputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.TLSInputErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}
		configMapVars[tls.TLSHashName] = env.SetValue(hash)
	}
	if instance.Spec.ExporterImage != "" &&
		(instance.Spec.MetricsEnabled == nil || *instance.Spec.MetricsEnabled) &&
		instance.Spec.MetricsTLS.Enabled() {
		hash, err := instance.Spec.MetricsTLS.ValidateCertSecret(ctx, helper, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.TLSInputReadyCondition,
					condition.RequestedReason,
					condition.SeverityInfo,
					condition.TLSInputReadyWaitingMessage, err.Error()))
				return ctrl.Result{}, nil
			}
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.TLSInputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.TLSInputErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}
		configMapVars[tls.TLSHashName+"_metrics"] = env.SetValue(hash)
	}
	instance.Status.Conditions.MarkTrue(condition.TLSInputReadyCondition, condition.InputReadyMessage)

	//
	// Create the scripts ConfigMap and calculate the input hash
	//
	err = r.generateServiceConfigMaps(ctx, helper, instance, &configMapVars)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}

	inputHash, err := r.createHashOfInputHashes(ctx, instance, configMapVars)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	//
	// Handle Topology
	//
	topology, err := ensureTopology(
		ctx,
		helper,
		instance,      // topologyHandler
		instance.Name, // finalizer
		&instance.Status.Conditions,
		labels.GetLabelSelector(serviceLabels),
	)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.TopologyReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.TopologyReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, fmt.Errorf("waiting for Topology requirements: %w", err)
	}

	//
	// StatefulSet
	//
	stsSpec, err := ovsdbrelay.StatefulSet(instance, inputHash,
		serviceLabels, serviceAnnotations, topology)
	if err != nil {
		return ctrl.Result{}, err
	}
	sfset := statefulset.NewStatefulSet(stsSpec, time.Duration(5)*time.Second)
	ctrlResult, err := sfset.CreateOrPatch(ctx, helper)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.DeploymentReadyErrorMessage,
			err.Error()))
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DeploymentReadyRunningMessage))
		return ctrlResult, nil
	}

	//
	// PodDisruptionBudget
	//
	if ovsdbrelay.GetReplicas(instance) > 1 {
		pdbSpec := pdb.MaxUnavailablePodDisruptionBudget(
			instance.Name,
			instance.Namespace,
			intstr.FromInt(1),
			serviceLabels,
		)
		pdbInstance := pdb.NewPDB(pdbSpec, 5*time.Second)

		ctrlResult, err := pdbInstance.CreateOrPatch(ctx, helper)
		if err != nil {
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.PDBReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.PDBReadyErrorMessage, err.Error()))
			return ctrl.Result{}, err
		} else if (ctrlResult != ctrl.Result{}) {
			return ctrlResult, nil
		}
	} else {
		err := pdb.DeletePDBWithName(ctx, helper, instance.Name, instance.Namespace)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete PDB: %w", err)
		}
	}
	instance.Status.Conditions.MarkTrue(condition.PDBReadyCondition, condition.PDBReadyMessage)

	//
	// Services
	//
	ctrlResult, err = r.reconcileServices(ctx, instance, helper, serviceLabels)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ExposeServiceReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ExposeServiceReadyErrorMessage,
			err.Error()))
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ExposeServiceReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.ExposeServiceReadyRunningMessage))
		return ctrlResult, nil
	}

	externalRequested := instance.ExternalRequested()
	if externalRequested {
		if instance.Status.DBAddress == "" {
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.ExposeServiceReadyCondition,
				condition.RequestedReason,
				condition.SeverityInfo,
				condition.ExposeServiceReadyRunningMessage))
		} else {
			instance.Status.Conditions.MarkTrue(condition.ExposeServiceReadyCondition, condition.ExposeServiceReadyMessage)
		}
	} else {
		instance.Status.Conditions.MarkTrue(condition.ExposeServiceReadyCondition, condition.ExposeServiceReadyMessage)
	}

	//
	// External config for dataplane nodes
	//
	if externalRequested && instance.Status.DBAddress != "" {
		err = r.generateExternalConfigMaps(ctx, helper, instance)
		if err != nil {
			instance.Status.Conditions.Set(condition.FalseCondition(
				ovnv1.ExternalConfigReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				ovnv1.ExternalConfigErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}
		instance.Status.Conditions.MarkTrue(ovnv1.ExternalConfigReadyCondition, condition.ServiceConfigReadyMessage)
	} else if !externalRequested {
		err = r.deleteExternalConfigMap(ctx, helper, instance.Namespace, instance.Name)
		if err != nil {
			return ctrl.Result{}, err
		}
		instance.Status.Conditions.MarkTrue(ovnv1.ExternalConfigReadyCondition, "External config not required")
	}

	stateful := sfset.GetStatefulSet()
	if stateful.Generation == stateful.Status.ObservedGeneration {
		instance.Status.ReadyCount = stateful.Status.ReadyReplicas
	}

	// verify if network attachment matches expectations
	networkReady, networkAttachmentStatus, err := nad.VerifyNetworkStatusFromAnnotation(
		ctx, helper, networkAttachments, serviceLabels, instance.Status.ReadyCount)
	if err != nil {
		return ctrl.Result{}, err
	}

	instance.Status.NetworkAttachments = networkAttachmentStatus
	if networkReady {
		instance.Status.Conditions.MarkTrue(condition.NetworkAttachmentsReadyCondition, condition.NetworkAttachmentsReadyMessage)
	} else {
		err := fmt.Errorf("%w with ips as configured in NetworkAttachments: %s", util.ErrPodsInterfaces, instance.Spec.NetworkAttachment)
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.NetworkAttachmentsReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.NetworkAttachmentsReadyErrorMessage,
			err.Error()))

		return ctrl.Result{}, nil
	}

	if !statefulset.IsReady(stateful) {
		Log.Info("Deployment is not ready")
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DeploymentReadyRunningMessage))
		// It is OK to return success as we are watching for StatefulSet changes
		return ctrl.Result{}, nil
	}
	instance.Status.Conditions.MarkTrue(condition.DeploymentReadyCondition, condition.DeploymentReadyMessage)

	Log.Info("Reconciled OVSDBRelay successfully")
	return ctrl.Result{}, nil
}

// resolveUpstream returns the upstream connection string the relay forwards to.
func (r *OVSDBRelayReconciler) resolveUpstream(
	ctx context.Context,
	helper *helper.Helper,
	instance *ovnv1.OVSDBRelay,
) (string, error) {
	if instance.Spec.Upstream != "" {
		return instance.Spec.Upstream, nil
	}

	var dbCluster *ovnv1.OVNDBCluster
	if instance.Spec.DBClusterName != "" {
		dbCluster = &ovnv1.OVNDBCluster{}
		err := helper.GetClient().Get(ctx, types.NamespacedName{
			Name:      instance.Spec.DBClusterName,
			Namespace: instance.Namespace,
		}, dbCluster)
		if err != nil {
			return "", fmt.Errorf("unable to get OVNDBCluster %s: %w", instance.Spec.DBClusterName, err)
		}
	} else {
		var err error
		dbCluster, err = ovnv1.GetDBClusterByType(ctx, helper, instance.Namespace, map[string]string{}, ovnv1.SBDBType)
		if err != nil {
			return "", fmt.Errorf("unable to find an SB OVNDBCluster: %w", err)
		}
	}

	if dbCluster.Spec.DBType != instance.Spec.DBType {
		return "", fmt.Errorf(
			"OVNDBCluster %s is of type %s, but OVSDBRelay %s serves %s",
			dbCluster.Name, dbCluster.Spec.DBType, instance.Name, instance.Spec.DBType)
	}

	// Prefer the per-pod internal addresses so the relay spreads its
	// upstream connections across all database members. Some deployments only
	// populate DBAddress (the service DNS name), so fall back to it.
	if dbCluster.Status.InternalDBAddress != "" {
		return dbCluster.Status.InternalDBAddress, nil
	}
	if dbCluster.Status.DBAddress != "" {
		return dbCluster.Status.DBAddress, nil
	}

	return "", fmt.Errorf("OVNDBCluster %s has no DB address yet", dbCluster.Name)
}

// reconcileServices creates the headless, per-pod and optional external Services
// and updates the status connection strings.
func (r *OVSDBRelayReconciler) reconcileServices(
	ctx context.Context,
	instance *ovnv1.OVSDBRelay,
	helper *helper.Helper,
	serviceLabels map[string]string,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info("Reconciling OVSDBRelay Services")

	// Headless Service used by the StatefulSet for stable pod DNS names
	headlessLabels := util.MergeMaps(serviceLabels, map[string]string{"type": ovnv1.ServiceHeadlessType})
	headlessSvc, err := service.NewService(
		ovsdbrelay.HeadlessService(instance, headlessLabels, serviceLabels),
		time.Duration(5)*time.Second,
		nil,
	)
	if err != nil {
		return ctrl.Result{}, err
	}
	ctrlResult, err := headlessSvc.CreateOrPatch(ctx, helper)
	if err != nil {
		return ctrl.Result{}, err
	} else if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}

	// Per-pod Services
	podList, err := ovsdbrelay.PodsWithLabels(ctx, helper, instance.Namespace, serviceLabels)
	if err != nil {
		return ctrl.Result{}, err
	}

	podNames := map[string]bool{}
	for _, relayPod := range podList.Items {
		podNames[relayPod.Name] = true
		selectorLabels := map[string]string{
			common.AppSelector:                   instance.Name,
			"statefulset.kubernetes.io/pod-name": relayPod.Name,
		}
		podServiceLabels := util.MergeMaps(selectorLabels, map[string]string{"type": ovnv1.ServiceClusterType})

		svc, err := service.NewService(
			ovsdbrelay.PodService(relayPod.Name, instance, podServiceLabels, selectorLabels),
			time.Duration(5)*time.Second,
			nil,
		)
		if err != nil {
			return ctrl.Result{}, err
		}
		ctrlResult, err := svc.CreateOrPatch(ctx, helper)
		if err != nil {
			return ctrl.Result{}, err
		} else if (ctrlResult != ctrl.Result{}) {
			return ctrlResult, nil
		}
	}

	// Delete per-pod Services left behind by a scale down
	clusterServiceLabels := util.MergeMaps(serviceLabels, map[string]string{"type": ovnv1.ServiceClusterType})
	svcList, err := service.GetServicesListWithLabel(ctx, helper, instance.Namespace, clusterServiceLabels)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, svc := range svcList.Items {
		if !podNames[svc.Name] {
			if !metav1.IsControlledBy(&svc, instance) {
				continue
			}
			if err := helper.GetClient().Delete(ctx, &svc); err != nil && !k8s_errors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("error deleting stale service %s: %w", svc.Name, err)
			}
		}
	}

	// External Service
	externalRequested := instance.ExternalRequested()
	var externalSvc *service.Service
	if externalRequested {
		svcOverride := instance.Spec.Override.Service
		if svcOverride != nil {
			if svcOverride.EmbeddedLabelsAnnotations == nil {
				svcOverride.EmbeddedLabelsAnnotations = &service.EmbeddedLabelsAnnotations{}
			}
			if svcOverride.Spec == nil {
				svcOverride.Spec = &service.OverrideServiceSpec{}
			}
		}

		externalLabels := util.MergeMaps(serviceLabels, map[string]string{"type": ovnv1.ServiceExternalType})
		externalSvc, err = service.NewService(
			ovsdbrelay.ExternalService(instance, externalLabels, serviceLabels),
			time.Duration(5)*time.Second,
			svcOverride,
		)
		if err != nil {
			return ctrl.Result{}, err
		}

		// add annotation to register service name in dnsmasq
		if externalSvc.GetServiceType() == corev1.ServiceTypeLoadBalancer {
			externalSvc.AddAnnotation(map[string]string{
				service.AnnotationHostnameKey: externalSvc.GetServiceHostname(),
			})
		}

		ctrlResult, err = externalSvc.CreateOrPatch(ctx, helper)
		if err != nil {
			return ctrl.Result{}, err
		} else if (ctrlResult != ctrl.Result{}) {
			return ctrlResult, nil
		}
	} else {
		// Remove a Service left behind when the external exposure is disabled
		existing, err := service.GetServiceWithName(ctx, helper, instance.Name, instance.Namespace)
		if err != nil && !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if existing != nil && existing.Labels["type"] == ovnv1.ServiceExternalType && metav1.IsControlledBy(existing, instance) {
			if err := helper.GetClient().Delete(ctx, existing); err != nil && !k8s_errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
	}

	// DNSData for the external service. The relay pod IPs are registered under
	// the external Service name so dnsmasq on the dataplane can resolve it.
	if instance.Spec.NetworkAttachment != "" &&
		(externalSvc == nil || externalSvc.GetServiceType() != corev1.ServiceTypeLoadBalancer) {
		dnsIPsList := []string{}
		replicas := int(ovsdbrelay.GetReplicas(instance))
		for i, relayPod := range podList.Items {
			if i >= replicas {
				break
			}
			dnsIP, err := ovsdbrelay.PodIPInNetwork(
				relayPod,
				instance.Namespace,
				instance.Spec.NetworkAttachment,
				&instance.Status.Conditions,
			)
			if err != nil {
				return ctrl.Result{}, err
			}
			dnsIPsList = append(dnsIPsList, dnsIP)
		}

		err = ovsdbrelay.DNSData(
			ctx,
			helper,
			instance.Name,
			dnsIPsList,
			instance.Namespace,
			serviceLabels,
		)
		if err != nil {
			return ctrl.Result{}, err
		}

		if len(podList.Items) < replicas {
			Log.Info(fmt.Sprintf("not all pods are yet created, number of expected pods: %v, current pods: %v", replicas, len(podList.Items)))
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}
	} else {
		dnsdata := &infranetworkv1.DNSData{
			ObjectMeta: metav1.ObjectMeta{
				Name:      instance.Name,
				Namespace: instance.Namespace,
			},
		}
		err = helper.GetClient().Delete(ctx, dnsdata)
		if err != nil && !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("error deleting dnsdata %s: %w", instance.Name, err)
		}
	}

	scheme := "tcp"
	if instance.Spec.TLS.Enabled() {
		scheme = "ssl"
	}

	// InternalDBAddress from the ready per-pod Services
	internalDBAddress := []string{}
	svcList, err = service.GetServicesListWithLabel(ctx, helper, instance.Namespace, clusterServiceLabels)
	if err != nil {
		return ctrl.Result{}, err
	}
	sort.Slice(svcList.Items, func(i, j int) bool {
		return svcList.Items[i].Name < svcList.Items[j].Name
	})
	clusterDomain := clusterdns.GetDNSClusterDomain()
	for _, svc := range svcList.Items {
		if len(svc.Spec.Ports) == 0 {
			continue
		}
		internalDBAddress = append(internalDBAddress,
			fmt.Sprintf("%s:%s.%s.svc.%s:%d", scheme, svc.Name, svc.Namespace, clusterDomain, svc.Spec.Ports[0].Port))
	}
	instance.Status.InternalDBAddress = strings.Join(internalDBAddress, ",")

	if externalSvc != nil {
		instance.Status.DBAddress = ovsdbrelay.GetDBAddress(
			externalSvc.GetSpec(), instance.Name, instance.Namespace, scheme)
	} else {
		instance.Status.DBAddress = ""
	}

	Log.Info("Reconciled OVSDBRelay Services successfully")
	return ctrl.Result{}, nil
}

// generateServiceConfigMaps - create the scripts ConfigMap
func (r *OVSDBRelayReconciler) generateServiceConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *ovnv1.OVSDBRelay,
	envVars *map[string]env.Setter,
) error {
	cmLabels := labels.GetLabels(instance, labels.GetGroupLabel(instance.Name), map[string]string{})

	templateParameters := make(map[string]any)

	cms := []util.Template{
		{
			Name:             fmt.Sprintf("%s-scripts", instance.Name),
			Namespace:        instance.Namespace,
			Type:             util.TemplateTypeScripts,
			MultiTemplateDir: "ovsdb_relay",
			InstanceType:     "OVSDBRelay",
			Labels:           cmLabels,
			ConfigOptions:    templateParameters,
		},
	}
	if instance.Spec.ExporterImage != "" &&
		(instance.Spec.MetricsEnabled == nil || *instance.Spec.MetricsEnabled) {
		cms = append(cms, util.Template{
			Name:      fmt.Sprintf("%s-metrics-config", instance.Name),
			Namespace: instance.Namespace,
			Type:      util.TemplateTypeNone,
			AdditionalTemplate: map[string]string{
				"openstack-network-exporter.yaml": "/ovsdb_relay/config/openstack-network-exporter.yaml",
				"web-config.yaml":                 "/ovsdb_relay/config/web-config.yaml",
			},
			InstanceType: "OVSDBRelay",
			Labels:       cmLabels,
			ConfigOptions: map[string]any{
				"TLS": instance.Spec.MetricsTLS.Enabled(),
			},
		})
	}
	return configmap.EnsureConfigMaps(ctx, h, instance, cms, envVars)
}

// generateExternalConfigMaps - create the <name>-config ConfigMap consumed by
// dataplane nodes to point ovn-controller at the relay.
func (r *OVSDBRelayReconciler) generateExternalConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *ovnv1.OVSDBRelay,
) error {
	cmLabels := labels.GetLabels(instance, labels.GetGroupLabel(instance.Name), map[string]string{})

	externalEndpoint, err := instance.GetExternalEndpoint()
	if err != nil {
		return err
	}

	templateParameters := map[string]any{
		"OVNRemote": externalEndpoint,
	}

	cms := []util.Template{
		{
			Name:      fmt.Sprintf("%s-config", instance.Name),
			Namespace: instance.Namespace,
			Type:      util.TemplateTypeNone,
			AdditionalTemplate: map[string]string{
				"ovsdb-config": "/ovsdb_relay/config/ovsdb-config",
			},
			InstanceType:  "OVSDBRelay",
			Labels:        cmLabels,
			ConfigOptions: templateParameters,
		},
	}
	return configmap.EnsureConfigMaps(ctx, h, instance, cms, nil)
}

func (r *OVSDBRelayReconciler) deleteExternalConfigMap(
	ctx context.Context,
	h *helper.Helper,
	namespace string,
	name string,
) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-config", name),
			Namespace: namespace,
		},
	}

	err := h.GetClient().Delete(ctx, cm)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("error deleting external config map %s: %w", cm.Name, err)
	}
	return nil
}

// createHashOfInputHashes - creates a hash of hashes which gets added to the resources which requires a restart
// if any of the input resources change, like configs, passwords, ...
func (r *OVSDBRelayReconciler) createHashOfInputHashes(
	ctx context.Context,
	instance *ovnv1.OVSDBRelay,
	envVars map[string]env.Setter,
) (string, error) {
	Log := r.GetLogger(ctx)

	mergedMapVars := env.MergeEnvs([]corev1.EnvVar{}, envVars)
	hash, err := util.ObjectHash(mergedMapVars)
	if err != nil {
		return hash, err
	}
	if hashMap, changed := util.SetHash(instance.Status.Hash, common.InputHashName, hash); changed {
		instance.Status.Hash = hashMap
		Log.Info(fmt.Sprintf("Input maps hash %s - %s", common.InputHashName, hash))
	}
	return hash, nil
}
