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
	"net"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var ovsdbRelayDefaults OVSDBRelayDefaults

// log is for logging in this package.
var ovsdbrelaylog = logf.Log.WithName("ovsdbrelay-resource")

// SetupOVSDBRelayDefaults - initialize OVSDBRelay spec defaults for use with either internal or external webhooks
func SetupOVSDBRelayDefaults(defaults OVSDBRelayDefaults) {
	ovsdbRelayDefaults = defaults
	ovsdbrelaylog.Info("OVSDBRelay defaults initialized", "defaults", defaults)
}

// Default sets default values for the OVSDBRelay resource
func (r *OVSDBRelay) Default() {
	ovsdbrelaylog.Info("default", "name", r.Name)

	r.Spec.Default()
}

// Default - set defaults for this OVSDBRelay spec
func (spec *OVSDBRelaySpec) Default() {
	if spec.ContainerImage == "" {
		spec.ContainerImage = ovsdbRelayDefaults.ContainerImageURL
	}
	if spec.ExporterImage == "" {
		spec.ExporterImage = ovsdbRelayDefaults.ExporterImageURL
	}
	spec.OVSDBRelaySpecCore.Default()
}

// Default - set defaults for this OVSDBRelay core spec (this version is called by OpenStackControlplane webhooks)
func (spec *OVSDBRelaySpecCore) Default() {
	if spec.DBType == "" {
		spec.DBType = SBDBType
	}
	if spec.Replicas == nil {
		spec.Replicas = ptr.To(OVSDBRelayReplicasDefault)
	}
	if spec.UseRemoteInDB == nil {
		spec.UseRemoteInDB = ptr.To(true)
	}
	if spec.Port == nil {
		spec.Port = ptr.To(OVSDBRelayPortDefault)
	}
	if spec.LogLevel == "" {
		spec.LogLevel = OVSDBRelayLogLevelDefault
	}
	if spec.MetricsEnabled == nil {
		spec.MetricsEnabled = ptr.To(true)
	}
}

// ValidateCreate validates the OVSDBRelay resource on creation
func (r *OVSDBRelay) ValidateCreate() (admission.Warnings, error) {
	ovsdbrelaylog.Info("validate create", "name", r.Name)

	errors := r.Spec.validate(field.NewPath("spec"), r.Namespace)
	if len(errors) != 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "ovn.openstack.org", Kind: "OVSDBRelay"},
			r.Name, errors)
	}
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *OVSDBRelay) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	ovsdbrelaylog.Info("validate update", "name", r.Name)

	errors := r.Spec.validate(field.NewPath("spec"), r.Namespace)
	if len(errors) != 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: "ovn.openstack.org", Kind: "OVSDBRelay"},
			r.Name, errors)
	}
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *OVSDBRelay) ValidateDelete() (admission.Warnings, error) {
	ovsdbrelaylog.Info("validate delete", "name", r.Name)

	return nil, nil
}

func (spec *OVSDBRelaySpec) validate(basePath *field.Path, namespace string) field.ErrorList {
	errors := spec.OVSDBRelaySpecCore.validate(basePath, namespace)

	if spec.ContainerImage == "" {
		errors = append(errors, field.Required(
			basePath.Child("containerImage"), "containerImage must not be empty"))
	}

	return errors
}

func (spec *OVSDBRelaySpecCore) validate(basePath *field.Path, namespace string) field.ErrorList {
	errors := field.ErrorList{}

	if spec.DBType != SBDBType {
		errors = append(errors, field.NotSupported(
			basePath.Child("dbType"), spec.DBType, []string{SBDBType}))
	}

	errors = append(errors, spec.validateUpstream(basePath.Child("upstream"))...)
	errors = append(errors, spec.ValidateTopology(basePath, namespace)...)

	return errors
}

// upstreamEntries returns the trimmed entries of a comma-separated OVSDB
// connection string.
func upstreamEntries(upstream string) []string {
	entries := []string{}
	for _, conn := range strings.Split(upstream, ",") {
		entries = append(entries, strings.TrimSpace(conn))
	}
	return entries
}

// upstreamScheme returns the scheme and host of one OVSDB connection string
// entry. ok is false when the entry has no scheme or no host.
func upstreamScheme(entry string) (scheme, host string, ok bool) {
	scheme, host, found := strings.Cut(entry, ":")
	if !found || scheme == "" || host == "" {
		return "", "", false
	}
	return scheme, host, true
}

// UpstreamUsesSSL returns true if any entry of the (comma-separated) upstream
// connection string uses the ssl scheme.
func UpstreamUsesSSL(upstream string) bool {
	for _, conn := range upstreamEntries(upstream) {
		scheme, _, ok := upstreamScheme(conn)
		if ok && scheme == "ssl" {
			return true
		}
	}
	return false
}

// upstreamEntryPort returns the explicit port of one upstream entry.
func upstreamEntryPort(entry string) (int32, bool) {
	_, hostPort, ok := strings.Cut(entry, ":")
	if !ok {
		return 0, false
	}
	_, portString, err := net.SplitHostPort(hostPort)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(portString, 10, 32)
	if err != nil || value < 1 || value > 65535 {
		return 0, false
	}
	return int32(value), true
}

// validateUpstream ensures every explicitly configured upstream entry is an
// active OVSDB connection string and that TLS is configured when ssl is used.
// Passive schemes (pssl, ptcp) are rejected: a relay needs an active remote.
func (spec *OVSDBRelaySpecCore) validateUpstream(basePath *field.Path) field.ErrorList {
	errors := field.ErrorList{}

	if spec.Upstream == "" {
		return errors
	}

	for _, upstream := range upstreamEntries(spec.Upstream) {
		scheme, _, ok := upstreamScheme(upstream)
		if !ok {
			errors = append(errors, field.Invalid(
				basePath, upstream,
				"must be an OVSDB connection string including a scheme, host and port, e.g. ssl:host:6642"))
			continue
		}

		switch scheme {
		case "ssl", "tcp":
			// Active schemes; ssl requires TLS, checked once below. Require an
			// explicit port instead of relying on OVSDB's 6640 default, which
			// differs from the SB database port operators expect here.
			if _, ok := upstreamEntryPort(upstream); !ok {
				errors = append(errors, field.Invalid(
					basePath, upstream,
					"must include an explicit port, e.g. ssl:host:6642"))
			}
		default:
			errors = append(errors, field.NotSupported(
				basePath, scheme, []string{"ssl", "tcp"}))
		}
	}

	if UpstreamUsesSSL(spec.Upstream) && !spec.TLS.Enabled() {
		errors = append(errors, field.Invalid(
			basePath, spec.Upstream,
			"upstream uses ssl but spec.tls.secretName is not set"))
	}

	useRemoteInDB := spec.UseRemoteInDB == nil || *spec.UseRemoteInDB
	if useRemoteInDB && !UpstreamUsesSSL(spec.Upstream) && spec.TLS.Enabled() {
		errors = append(errors, field.Invalid(
			basePath, spec.Upstream,
			"upstream does not use TLS but spec.tls.secretName is set; with useRemoteInDB the relay also serves the plaintext connection from SB_Global.connections"))
	}

	return errors
}
