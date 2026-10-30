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
	"testing"

	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
)

func TestUpstreamUsesSSL(t *testing.T) {
	tests := []struct {
		upstream string
		want     bool
	}{
		{upstream: "", want: false},
		{upstream: "tcp:10.0.0.1:6642", want: false},
		{upstream: "ssl:10.0.0.1:6642", want: true},
		{upstream: "tcp:10.0.0.1:6642, ssl:10.0.0.2:6642", want: true},
		{upstream: "tcp:10.0.0.1:6642,tcp:10.0.0.2:6642", want: false},
	}

	for _, tt := range tests {
		if got := UpstreamUsesSSL(tt.upstream); got != tt.want {
			t.Errorf("UpstreamUsesSSL(%q) = %v, want %v", tt.upstream, got, tt.want)
		}
	}
}

func TestOVSDBRelaySpecCoreValidate(t *testing.T) {
	tlsSpec := tls.SimpleService{
		GenericService: tls.GenericService{SecretName: ptr.To("relay-cert")},
	}

	tests := []struct {
		name    string
		spec    OVSDBRelaySpecCore
		wantErr bool
	}{
		{
			name: "valid SB relay with tcp upstream",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "tcp:10.0.0.1:6642",
			},
		},
		{
			name: "valid SB relay with ssl upstream and TLS",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "ssl:10.0.0.1:6642",
				TLS:      tlsSpec,
			},
		},
		{
			name: "valid comma-separated upstream",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "tcp:10.0.0.1:6642, tcp:10.0.0.2:6642",
			},
		},
		{
			name:    "unsupported DB type",
			spec:    OVSDBRelaySpecCore{DBType: NBDBType},
			wantErr: true,
		},
		{
			name: "ssl upstream without TLS",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "ssl:10.0.0.1:6642",
			},
			wantErr: true,
		},
		{
			name: "tcp upstream with TLS and the SB database port",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "tcp:10.0.0.1:6642",
				TLS:      tlsSpec,
			},
			wantErr: true,
		},
		{
			name: "tcp upstream with TLS and a custom port",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "tcp:10.0.0.1:6642",
				TLS:      tlsSpec,
				Port:     ptr.To(int32(16642)),
			},
			wantErr: true,
		},
		{
			name: "tcp upstream with TLS on a non-default upstream port",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "tcp:10.0.0.1:6643",
				TLS:      tlsSpec,
				Port:     ptr.To(int32(6643)),
			},
			wantErr: true,
		},
		{
			name: "tcp upstream with TLS and useRemoteInDB disabled",
			spec: OVSDBRelaySpecCore{
				DBType:        SBDBType,
				Upstream:      "tcp:10.0.0.1:6642",
				TLS:           tlsSpec,
				Port:          ptr.To(int32(16642)),
				UseRemoteInDB: ptr.To(false),
			},
		},
		{
			name: "tcp upstream with TLS and the default port in front of a non-default upstream",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "tcp:10.0.0.1:6643",
				TLS:      tlsSpec,
			},
			wantErr: true,
		},
		{
			name: "upstream without a port",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "tcp:10.0.0.1",
			},
			wantErr: true,
		},
		{
			name: "upstream entries with different ports",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "tcp:10.0.0.1:6643,tcp:10.0.0.2:6644",
			},
		},
		{
			name: "passive pssl scheme is not an upstream",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "pssl:10.0.0.1:6642",
				TLS:      tlsSpec,
			},
			wantErr: true,
		},
		{
			name: "passive ptcp scheme is not an upstream",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "ptcp:10.0.0.1:6642",
			},
			wantErr: true,
		},
		{
			name: "second upstream entry is validated too",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "tcp:10.0.0.1:6642,pssl:10.0.0.2:6642",
			},
			wantErr: true,
		},
		{
			name: "upstream without a host",
			spec: OVSDBRelaySpecCore{
				DBType:   SBDBType,
				Upstream: "tcp:",
			},
			wantErr: true,
		},
		{
			name: "topology in another namespace",
			spec: OVSDBRelaySpecCore{
				DBType:      SBDBType,
				TopologyRef: &topologyv1.TopoRef{Name: "topology", Namespace: "other"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.spec.validate(field.NewPath("spec"), "test-namespace")
			if tt.wantErr && len(errs) == 0 {
				t.Fatalf("expected validation errors, got none")
			}
			if !tt.wantErr && len(errs) != 0 {
				t.Fatalf("expected no validation errors, got %v", errs)
			}
		})
	}
}
