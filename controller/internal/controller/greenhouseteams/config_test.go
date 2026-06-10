package greenhouseteams

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestParseConnectorDefinition(t *testing.T) {
	tests := []struct {
		name    string
		raw     *runtime.RawExtension
		wantDef *GreenhouseTeamsConnectorDefinition
		wantErr bool
	}{
		{
			name: "valid definition with all fields",
			raw: &runtime.RawExtension{
				Raw: mustMarshal(t, map[string]interface{}{
					"defaultTargetNamespace": "org-ccloud",
					"capabilityMapping": map[string]string{
						"READ":  "viewer",
						"WRITE": "editor",
						"ADMIN": "admin",
					},
				}),
			},
			wantDef: &GreenhouseTeamsConnectorDefinition{
				DefaultTargetNamespace: "org-ccloud",
				CapabilityMapping: map[string]string{
					"READ":  "viewer",
					"WRITE": "editor",
					"ADMIN": "admin",
				},
			},
			wantErr: false,
		},
		{
			name: "valid definition without defaultTargetNamespace",
			raw: &runtime.RawExtension{
				Raw: mustMarshal(t, map[string]interface{}{
					"capabilityMapping": map[string]string{
						"READ": "viewer",
					},
				}),
			},
			wantDef: &GreenhouseTeamsConnectorDefinition{
				DefaultTargetNamespace: "",
				CapabilityMapping: map[string]string{
					"READ": "viewer",
				},
			},
			wantErr: false,
		},
		{
			name: "empty capabilityMapping gets initialized",
			raw: &runtime.RawExtension{
				Raw: mustMarshal(t, map[string]interface{}{
					"defaultTargetNamespace": "org-ns",
				}),
			},
			wantDef: &GreenhouseTeamsConnectorDefinition{
				DefaultTargetNamespace: "org-ns",
				CapabilityMapping:      map[string]string{},
			},
			wantErr: false,
		},
		{
			name:    "nil raw extension",
			raw:     nil,
			wantDef: nil,
			wantErr: true,
		},
		{
			name:    "nil raw bytes",
			raw:     &runtime.RawExtension{Raw: nil},
			wantDef: nil,
			wantErr: true,
		},
		{
			name: "invalid JSON",
			raw: &runtime.RawExtension{
				Raw: []byte("not valid json"),
			},
			wantDef: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseConnectorDefinition(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseConnectorDefinition() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if got.DefaultTargetNamespace != tt.wantDef.DefaultTargetNamespace {
				t.Errorf("DefaultTargetNamespace = %q, want %q", got.DefaultTargetNamespace, tt.wantDef.DefaultTargetNamespace)
			}
			if len(got.CapabilityMapping) != len(tt.wantDef.CapabilityMapping) {
				t.Errorf("CapabilityMapping length = %d, want %d", len(got.CapabilityMapping), len(tt.wantDef.CapabilityMapping))
			}
			for k, v := range tt.wantDef.CapabilityMapping {
				if got.CapabilityMapping[k] != v {
					t.Errorf("CapabilityMapping[%q] = %q, want %q", k, got.CapabilityMapping[k], v)
				}
			}
		})
	}
}

func TestParseAdvancedConfig(t *testing.T) {
	tests := []struct {
		name    string
		raw     *runtime.RawExtension
		wantCfg *GreenhouseTeamsAdvancedConfig
		wantErr bool
	}{
		{
			name: "valid config with all fields",
			raw: &runtime.RawExtension{
				Raw: mustMarshal(t, map[string]interface{}{
					"greenhouse-teams": map[string]interface{}{
						"teamName":         "my-team",
						"targetNamespaces": []string{"org-ccloud", "org-other"},
					},
				}),
			},
			wantCfg: &GreenhouseTeamsAdvancedConfig{
				TeamName:         "my-team",
				TargetNamespaces: []string{"org-ccloud", "org-other"},
			},
			wantErr: false,
		},
		{
			name: "valid config with only targetNamespaces",
			raw: &runtime.RawExtension{
				Raw: mustMarshal(t, map[string]interface{}{
					"greenhouse-teams": map[string]interface{}{
						"targetNamespaces": []string{"org-ccloud"},
					},
				}),
			},
			wantCfg: &GreenhouseTeamsAdvancedConfig{
				TeamName:         "",
				TargetNamespaces: []string{"org-ccloud"},
			},
			wantErr: false,
		},
		{
			name: "valid config with only teamName",
			raw: &runtime.RawExtension{
				Raw: mustMarshal(t, map[string]interface{}{
					"greenhouse-teams": map[string]interface{}{
						"teamName": "my-team",
					},
				}),
			},
			wantCfg: &GreenhouseTeamsAdvancedConfig{
				TeamName:         "my-team",
				TargetNamespaces: nil,
			},
			wantErr: false,
		},
		{
			name:    "nil raw extension returns empty config",
			raw:     nil,
			wantCfg: &GreenhouseTeamsAdvancedConfig{},
			wantErr: false,
		},
		{
			name:    "nil raw bytes returns empty config",
			raw:     &runtime.RawExtension{Raw: nil},
			wantCfg: &GreenhouseTeamsAdvancedConfig{},
			wantErr: false,
		},
		{
			name: "no greenhouse-teams key returns empty config",
			raw: &runtime.RawExtension{
				Raw: mustMarshal(t, map[string]interface{}{
					"kubernetes": map[string]interface{}{
						"something": "else",
					},
				}),
			},
			wantCfg: &GreenhouseTeamsAdvancedConfig{},
			wantErr: false,
		},
		{
			name: "invalid JSON at top level",
			raw: &runtime.RawExtension{
				Raw: []byte("not valid json"),
			},
			wantCfg: nil,
			wantErr: true,
		},
		{
			name: "invalid JSON in greenhouse-teams value",
			raw: &runtime.RawExtension{
				Raw: []byte(`{"greenhouse-teams": "not-an-object"}`),
			},
			wantCfg: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAdvancedConfig(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseAdvancedConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if got.TeamName != tt.wantCfg.TeamName {
				t.Errorf("TeamName = %q, want %q", got.TeamName, tt.wantCfg.TeamName)
			}
			if len(got.TargetNamespaces) != len(tt.wantCfg.TargetNamespaces) {
				t.Errorf("TargetNamespaces length = %d, want %d", len(got.TargetNamespaces), len(tt.wantCfg.TargetNamespaces))
				return
			}
			for i, ns := range tt.wantCfg.TargetNamespaces {
				if got.TargetNamespaces[i] != ns {
					t.Errorf("TargetNamespaces[%d] = %q, want %q", i, got.TargetNamespaces[i], ns)
				}
			}
		})
	}
}

func TestResolveTargetNamespaces(t *testing.T) {
	tests := []struct {
		name    string
		advCfg  *GreenhouseTeamsAdvancedConfig
		cbDef   *GreenhouseTeamsConnectorDefinition
		wantNs  []string
		wantErr bool
	}{
		{
			name: "priority 1: advancedConfig.targetNamespaces",
			advCfg: &GreenhouseTeamsAdvancedConfig{
				TargetNamespaces: []string{"org-ccloud", "org-other"},
			},
			cbDef: &GreenhouseTeamsConnectorDefinition{
				DefaultTargetNamespace: "org-default",
			},
			wantNs:  []string{"org-ccloud", "org-other"},
			wantErr: false,
		},
		{
			name:   "priority 2: CB defaultTargetNamespace",
			advCfg: &GreenhouseTeamsAdvancedConfig{},
			cbDef: &GreenhouseTeamsConnectorDefinition{
				DefaultTargetNamespace: "org-default",
			},
			wantNs:  []string{"org-default"},
			wantErr: false,
		},
		{
			name:   "priority 2: nil advCfg falls to CB default",
			advCfg: nil,
			cbDef: &GreenhouseTeamsConnectorDefinition{
				DefaultTargetNamespace: "org-default",
			},
			wantNs:  []string{"org-default"},
			wantErr: false,
		},
		{
			name:    "error: no target namespaces available",
			advCfg:  &GreenhouseTeamsAdvancedConfig{},
			cbDef:   &GreenhouseTeamsConnectorDefinition{},
			wantNs:  nil,
			wantErr: true,
		},
		{
			name:    "error: nil cbDef and no advCfg namespaces",
			advCfg:  &GreenhouseTeamsAdvancedConfig{},
			cbDef:   nil,
			wantNs:  nil,
			wantErr: true,
		},
		{
			name:    "error: both nil",
			advCfg:  nil,
			cbDef:   nil,
			wantNs:  nil,
			wantErr: true,
		},
		{
			name: "empty targetNamespaces falls through to CB default",
			advCfg: &GreenhouseTeamsAdvancedConfig{
				TargetNamespaces: []string{},
			},
			cbDef: &GreenhouseTeamsConnectorDefinition{
				DefaultTargetNamespace: "org-fallback",
			},
			wantNs:  []string{"org-fallback"},
			wantErr: false,
		},
		{
			name: "empty targetNamespaces with no CB default is error",
			advCfg: &GreenhouseTeamsAdvancedConfig{
				TargetNamespaces: []string{},
			},
			cbDef:   &GreenhouseTeamsConnectorDefinition{},
			wantNs:  nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTargetNamespaces(tt.advCfg, tt.cbDef)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveTargetNamespaces() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.wantNs) {
				t.Errorf("got %d namespaces, want %d: %v", len(got), len(tt.wantNs), got)
				return
			}
			for i, ns := range tt.wantNs {
				if got[i] != ns {
					t.Errorf("namespace[%d] = %q, want %q", i, got[i], ns)
				}
			}
		})
	}
}

// mustMarshal is a test helper that marshals v to JSON or fails the test.
func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal test data: %v", err)
	}
	return data
}
