package greenhouseteams

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
)

// GreenhouseTeamsConnectorDefinition is the provider-specific definition
// parsed from the ConnectorBinding's spec.definition field.
type GreenhouseTeamsConnectorDefinition struct {
	DefaultTargetNamespace string            `json:"defaultTargetNamespace,omitempty"`
	CapabilityMapping     map[string]string  `json:"capabilityMapping"`
}

// GreenhouseTeamsAdvancedConfig is the per-ASR advanced configuration
// parsed from the ASR's spec.advancedConfig field under the "greenhouse-teams" key.
type GreenhouseTeamsAdvancedConfig struct {
	TeamName         string   `json:"teamName,omitempty"`
	TargetNamespaces []string `json:"targetNamespaces,omitempty"`
}

// ParseConnectorDefinition parses the ConnectorBinding definition into
// the greenhouse-teams connector definition struct.
func ParseConnectorDefinition(raw *runtime.RawExtension) (*GreenhouseTeamsConnectorDefinition, error) {
	if raw == nil || raw.Raw == nil {
		return nil, fmt.Errorf("ConnectorBinding definition is nil")
	}

	var def GreenhouseTeamsConnectorDefinition
	if err := json.Unmarshal(raw.Raw, &def); err != nil {
		return nil, fmt.Errorf("failed to parse ConnectorBinding definition: %w", err)
	}

	if def.CapabilityMapping == nil {
		def.CapabilityMapping = make(map[string]string)
	}

	return &def, nil
}

// ParseAdvancedConfig parses the ASR's advancedConfig for the greenhouse-teams provider.
// The advancedConfig is expected to be a JSON object with a "greenhouse-teams" key.
func ParseAdvancedConfig(raw *runtime.RawExtension) (*GreenhouseTeamsAdvancedConfig, error) {
	if raw == nil || raw.Raw == nil {
		// No advanced config — return empty config (target namespaces will
		// need to come from the CB default).
		return &GreenhouseTeamsAdvancedConfig{}, nil
	}

	// The advancedConfig may be a flat structure keyed by provider name
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw.Raw, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to parse advancedConfig: %w", err)
	}

	providerConfig, ok := wrapper[ProviderGreenhouseTeams]
	if !ok {
		// No greenhouse-teams key — return empty config
		return &GreenhouseTeamsAdvancedConfig{}, nil
	}

	var cfg GreenhouseTeamsAdvancedConfig
	if err := json.Unmarshal(providerConfig, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse advancedConfig.greenhouse-teams: %w", err)
	}

	return &cfg, nil
}

// ResolveTargetNamespaces resolves the target namespaces for team CR creation.
// Priority: advancedConfig.targetNamespaces > [CB.defaultTargetNamespace] > error
// Unlike the kubernetes provider, there is NO fallback to the ASR namespace —
// greenhouse CRs MUST be in an org namespace.
func ResolveTargetNamespaces(advCfg *GreenhouseTeamsAdvancedConfig, cbDef *GreenhouseTeamsConnectorDefinition) ([]string, error) {
	// Priority 1: advancedConfig.targetNamespaces
	if advCfg != nil && len(advCfg.TargetNamespaces) > 0 {
		return advCfg.TargetNamespaces, nil
	}

	// Priority 2: CB defaultTargetNamespace
	if cbDef != nil && cbDef.DefaultTargetNamespace != "" {
		return []string{cbDef.DefaultTargetNamespace}, nil
	}

	// No fallback — error
	return nil, fmt.Errorf("targetNamespaces is required: neither advancedConfig.greenhouse-teams.targetNamespaces nor ConnectorBinding defaultTargetNamespace is set")
}
