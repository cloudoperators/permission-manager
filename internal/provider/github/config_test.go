// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

func TestParseAdvancedConfig(t *testing.T) {
	tests := []struct {
		name string
		raw  *runtime.RawExtension
		want string
	}{
		{"nil returns empty", nil, ""},
		{"reads github targetNamespace", rawExt(t, map[string]any{"github": map[string]any{"targetNamespace": "my-ns"}}), "my-ns"},
		{"ignores non-github keys", rawExt(t, map[string]any{"kubernetes": map[string]any{"targetNamespace": "k8s-ns"}}), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAdvancedConfig(tt.raw)
			if err != nil {
				t.Fatalf("ParseAdvancedConfig: %v", err)
			}
			if got.TargetNamespace != tt.want {
				t.Errorf("TargetNamespace = %q, want %q", got.TargetNamespace, tt.want)
			}
		})
	}
}

func TestParseConnectorDefinition(t *testing.T) {
	t.Run("parses definition", func(t *testing.T) {
		got, err := ParseConnectorDefinition(rawExt(t, map[string]any{
			"defaultTargetNamespace": "github-teams",
			"capabilityMapping":      map[string]string{"READ": "pull", "WRITE": "push"},
		}))
		if err != nil {
			t.Fatalf("ParseConnectorDefinition: %v", err)
		}
		if got.DefaultTargetNamespace != "github-teams" || got.CapabilityMapping["WRITE"] != "push" {
			t.Errorf("unexpected definition: %+v", got)
		}
	})

	t.Run("nil definition is an error", func(t *testing.T) {
		if _, err := ParseConnectorDefinition(nil); err == nil {
			t.Error("expected an error for a nil definition")
		}
	})
}

func TestResolveTargetNamespace(t *testing.T) {
	tests := []struct {
		name      string
		advConfig *GithubAdvancedConfig
		cbDef     *GithubConnectorDefinition
		want      string
	}{
		{"advancedConfig wins", &GithubAdvancedConfig{TargetNamespace: "from-advanced"}, &GithubConnectorDefinition{DefaultTargetNamespace: "from-cb"}, "from-advanced"},
		{"connector default next", &GithubAdvancedConfig{}, &GithubConnectorDefinition{DefaultTargetNamespace: "from-cb"}, "from-cb"},
		{"asr namespace fallback", &GithubAdvancedConfig{}, &GithubConnectorDefinition{}, "asr-ns"},
		{"nil inputs fall back to asr namespace", nil, nil, "asr-ns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asr := &v1alpha1.ApplicationSpecificRole{}
			asr.Namespace = "asr-ns"
			if got := ResolveTargetNamespace(tt.advConfig, tt.cbDef, asr); got != tt.want {
				t.Errorf("ResolveTargetNamespace = %q, want %q", got, tt.want)
			}
		})
	}
}

func rawExt(t *testing.T, data any) *runtime.RawExtension {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal test data: %v", err)
	}
	return &runtime.RawExtension{Raw: raw}
}
