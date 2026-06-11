// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
)

func TestParseGithubAdvancedConfig(t *testing.T) {
	tests := []struct {
		name           string
		advancedConfig *runtime.RawExtension
		want           *GithubAdvancedConfig
		wantErr        bool
	}{
		{
			name:           "nil advancedConfig returns empty config",
			advancedConfig: nil,
			want:           &GithubAdvancedConfig{},
			wantErr:        false,
		},
		{
			name: "parses targetNamespace from github key",
			advancedConfig: rawExt(t, map[string]interface{}{
				"github": map[string]interface{}{
					"targetNamespace": "my-ns",
				},
			}),
			want:    &GithubAdvancedConfig{TargetNamespace: "my-ns"},
			wantErr: false,
		},
		{
			name: "ignores other keys",
			advancedConfig: rawExt(t, map[string]interface{}{
				"kubernetes": map[string]interface{}{
					"targetNamespace": "k8s-ns",
				},
			}),
			want:    &GithubAdvancedConfig{},
			wantErr: false,
		},
		{
			name: "empty github key returns empty config",
			advancedConfig: rawExt(t, map[string]interface{}{
				"github": map[string]interface{}{},
			}),
			want:    &GithubAdvancedConfig{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAdvancedConfig(tt.advancedConfig)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseAdvancedConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got.TargetNamespace != tt.want.TargetNamespace {
				t.Errorf("ParseAdvancedConfig().TargetNamespace = %q, want %q", got.TargetNamespace, tt.want.TargetNamespace)
			}
		})
	}
}

func TestParseConnectorDefinition(t *testing.T) {
	tests := []struct {
		name    string
		def     *runtime.RawExtension
		want    *GithubConnectorDefinition
		wantErr bool
	}{
		{
			name: "parses capabilityMapping and defaultTargetNamespace",
			def: rawExt(t, map[string]interface{}{
				"defaultTargetNamespace": "github-teams",
				"capabilityMapping": map[string]string{
					"READ":  "pull",
					"WRITE": "push",
					"ADMIN": "admin",
				},
			}),
			want: &GithubConnectorDefinition{
				DefaultTargetNamespace: "github-teams",
				CapabilityMapping: map[string]string{
					"READ":  "pull",
					"WRITE": "push",
					"ADMIN": "admin",
				},
			},
			wantErr: false,
		},
		{
			name:    "nil definition returns error",
			def:     nil,
			want:    nil,
			wantErr: true,
		},
		{
			name: "empty capabilityMapping",
			def: rawExt(t, map[string]interface{}{
				"capabilityMapping": map[string]string{},
			}),
			want: &GithubConnectorDefinition{
				CapabilityMapping: map[string]string{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseConnectorDefinition(tt.def)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseConnectorDefinition() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.DefaultTargetNamespace != tt.want.DefaultTargetNamespace {
				t.Errorf("DefaultTargetNamespace = %q, want %q", got.DefaultTargetNamespace, tt.want.DefaultTargetNamespace)
			}
			if len(got.CapabilityMapping) != len(tt.want.CapabilityMapping) {
				t.Errorf("CapabilityMapping length = %d, want %d", len(got.CapabilityMapping), len(tt.want.CapabilityMapping))
			}
			for k, v := range tt.want.CapabilityMapping {
				if got.CapabilityMapping[k] != v {
					t.Errorf("CapabilityMapping[%q] = %q, want %q", k, got.CapabilityMapping[k], v)
				}
			}
		})
	}
}

func TestResolveTargetNamespace(t *testing.T) {
	tests := []struct {
		name         string
		advConfig    *GithubAdvancedConfig
		cbDef        *GithubConnectorDefinition
		asrNamespace string
		want         string
	}{
		{
			name:         "advancedConfig takes priority",
			advConfig:    &GithubAdvancedConfig{TargetNamespace: "from-advanced"},
			cbDef:        &GithubConnectorDefinition{DefaultTargetNamespace: "from-cb"},
			asrNamespace: "asr-ns",
			want:         "from-advanced",
		},
		{
			name:         "CB default used when advancedConfig empty",
			advConfig:    &GithubAdvancedConfig{},
			cbDef:        &GithubConnectorDefinition{DefaultTargetNamespace: "from-cb"},
			asrNamespace: "asr-ns",
			want:         "from-cb",
		},
		{
			name:         "ASR namespace used as fallback",
			advConfig:    &GithubAdvancedConfig{},
			cbDef:        &GithubConnectorDefinition{},
			asrNamespace: "asr-ns",
			want:         "asr-ns",
		},
		{
			name:         "nil advConfig falls through to CB",
			advConfig:    nil,
			cbDef:        &GithubConnectorDefinition{DefaultTargetNamespace: "from-cb"},
			asrNamespace: "asr-ns",
			want:         "from-cb",
		},
		{
			name:         "nil cbDef falls through to ASR namespace",
			advConfig:    nil,
			cbDef:        nil,
			asrNamespace: "asr-ns",
			want:         "asr-ns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asr := &pmv1.ApplicationSpecificRole{}
			asr.Namespace = tt.asrNamespace

			got := ResolveTargetNamespace(tt.advConfig, tt.cbDef, asr)
			if got != tt.want {
				t.Errorf("ResolveTargetNamespace() = %q, want %q", got, tt.want)
			}
		})
	}
}

// rawExt is a test helper to create runtime.RawExtension from a map.
func rawExt(t *testing.T, data interface{}) *runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("failed to marshal test data: %v", err)
	}
	return &runtime.RawExtension{Raw: raw}
}
