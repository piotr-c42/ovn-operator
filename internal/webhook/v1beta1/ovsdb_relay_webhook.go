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
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ovnv1beta1 "github.com/openstack-k8s-operators/ovn-operator/api/v1beta1"
)

// nolint:unused
// log is for logging in this package.
var ovsdbrelaylog = logf.Log.WithName("ovsdbrelay-resource")

// SetupOVSDBRelayWebhookWithManager registers the webhook for OVSDBRelay in the manager.
func SetupOVSDBRelayWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&ovnv1beta1.OVSDBRelay{}).
		WithValidator(&OVSDBRelayCustomValidator{}).
		WithDefaulter(&OVSDBRelayCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-ovn-openstack-org-v1beta1-ovsdbrelay,mutating=true,failurePolicy=fail,sideEffects=None,groups=ovn.openstack.org,resources=ovsdbrelays,verbs=create;update,versions=v1beta1,name=movsdbrelay-v1beta1.kb.io,admissionReviewVersions=v1

// OVSDBRelayCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind OVSDBRelay when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type OVSDBRelayCustomDefaulter struct {
}

var _ webhook.CustomDefaulter = &OVSDBRelayCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind OVSDBRelay.
func (d *OVSDBRelayCustomDefaulter) Default(_ context.Context, obj runtime.Object) error {
	ovsdbrelay, ok := obj.(*ovnv1beta1.OVSDBRelay)

	if !ok {
		return fmt.Errorf("expected an OVSDBRelay object but got %T: %w", obj, ErrInvalidObjectType)
	}
	ovsdbrelaylog.Info("Defaulting for OVSDBRelay", "name", ovsdbrelay.GetName())

	ovsdbrelay.Default()

	return nil
}

// +kubebuilder:webhook:path=/validate-ovn-openstack-org-v1beta1-ovsdbrelay,mutating=false,failurePolicy=fail,sideEffects=None,groups=ovn.openstack.org,resources=ovsdbrelays,verbs=create;update,versions=v1beta1,name=vovsdbrelay-v1beta1.kb.io,admissionReviewVersions=v1

// OVSDBRelayCustomValidator struct is responsible for validating the OVSDBRelay resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type OVSDBRelayCustomValidator struct {
}

var _ webhook.CustomValidator = &OVSDBRelayCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type OVSDBRelay.
func (v *OVSDBRelayCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	ovsdbrelay, ok := obj.(*ovnv1beta1.OVSDBRelay)
	if !ok {
		return nil, fmt.Errorf("expected a OVSDBRelay object but got %T: %w", obj, ErrInvalidObjectType)
	}
	ovsdbrelaylog.Info("Validation for OVSDBRelay upon creation", "name", ovsdbrelay.GetName())

	return ovsdbrelay.ValidateCreate()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type OVSDBRelay.
func (v *OVSDBRelayCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	ovsdbrelay, ok := newObj.(*ovnv1beta1.OVSDBRelay)
	if !ok {
		return nil, fmt.Errorf("expected a OVSDBRelay object for the newObj but got %T: %w", newObj, ErrInvalidObjectType)
	}
	ovsdbrelaylog.Info("Validation for OVSDBRelay upon update", "name", ovsdbrelay.GetName())

	return ovsdbrelay.ValidateUpdate(oldObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type OVSDBRelay.
func (v *OVSDBRelayCustomValidator) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	ovsdbrelay, ok := obj.(*ovnv1beta1.OVSDBRelay)
	if !ok {
		return nil, fmt.Errorf("expected a OVSDBRelay object but got %T: %w", obj, ErrInvalidObjectType)
	}
	ovsdbrelaylog.Info("Validation for OVSDBRelay upon deletion", "name", ovsdbrelay.GetName())

	return ovsdbrelay.ValidateDelete()
}
