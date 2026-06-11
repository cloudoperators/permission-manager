// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/provider"
)

// ProviderGithub is the provider name for GitHub ConnectorBindings.
const ProviderGithub = provider.ProviderGithub

// FinalizerGithub is the finalizer added to ASRs managed by the GitHub provider.
const FinalizerGithub = "permission-manager.cloudoperators.dev/github"

// GithubConnectorDefinition represents the definition field of a GitHub ConnectorBinding.
type GithubConnectorDefinition struct {
	DefaultTargetNamespace string            `json:"defaultTargetNamespace,omitempty"`
	CapabilityMapping     map[string]string  `json:"capabilityMapping"`
}

// GithubAdvancedConfig represents the github-specific advanced config from an ASR.
type GithubAdvancedConfig struct {
	TargetNamespace string `json:"targetNamespace,omitempty"`
}

// ParseAdvancedConfig extracts the github section from an ASR's advancedConfig.
// Returns an empty config if advancedConfig is nil or doesn't contain a github key.
func ParseAdvancedConfig(raw *runtime.RawExtension) (*GithubAdvancedConfig, error) {
	if raw == nil || raw.Raw == nil {
		return &GithubAdvancedConfig{}, nil
	}

	// First unmarshal the top-level map to find the "github" key
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(raw.Raw, &topLevel); err != nil {
		return nil, fmt.Errorf("failed to unmarshal advancedConfig: %w", err)
	}

	githubRaw, ok := topLevel["github"]
	if !ok {
		return &GithubAdvancedConfig{}, nil
	}

	var cfg GithubAdvancedConfig
	if err := json.Unmarshal(githubRaw, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal advancedConfig.github: %w", err)
	}
	return &cfg, nil
}

// ParseConnectorDefinition extracts the GithubConnectorDefinition from a ConnectorBinding's definition.
func ParseConnectorDefinition(raw *runtime.RawExtension) (*GithubConnectorDefinition, error) {
	if raw == nil || raw.Raw == nil {
		return nil, fmt.Errorf("ConnectorBinding definition is nil")
	}

	var def GithubConnectorDefinition
	if err := json.Unmarshal(raw.Raw, &def); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ConnectorBinding definition: %w", err)
	}
	return &def, nil
}

// ResolveTargetNamespace determines the target namespace using the priority:
// 1. advancedConfig.github.targetNamespace (from the ASR)
// 2. ConnectorBinding defaultTargetNamespace
// 3. ASR namespace (fallback)
func ResolveTargetNamespace(advConfig *GithubAdvancedConfig, cbDef *GithubConnectorDefinition, asr *pmv1.ApplicationSpecificRole) string {
	if advConfig != nil && advConfig.TargetNamespace != "" {
		return advConfig.TargetNamespace
	}
	if cbDef != nil && cbDef.DefaultTargetNamespace != "" {
		return cbDef.DefaultTargetNamespace
	}
	return asr.Namespace
}
