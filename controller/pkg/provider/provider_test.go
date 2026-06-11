// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"testing"
)

func TestProviderConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		want     string
	}{
		{
			name:     "ProviderKubernetes",
			constant: ProviderKubernetes,
			want:     "kubernetes",
		},
		{
			name:     "ProviderGithub",
			constant: ProviderGithub,
			want:     "github",
		},
		{
			name:     "ProviderGreenhouseTeams",
			constant: ProviderGreenhouseTeams,
			want:     "greenhouse-teams",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.want {
				t.Errorf("got %q, want %q", tt.constant, tt.want)
			}
		})
	}
}

func TestFinalizerName(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		want     string
	}{
		{
			name:     "kubernetes provider",
			provider: ProviderKubernetes,
			want:     "permission-manager.cloudoperators.dev/kubernetes",
		},
		{
			name:     "github provider",
			provider: ProviderGithub,
			want:     "permission-manager.cloudoperators.dev/github",
		},
		{
			name:     "greenhouse-teams provider",
			provider: ProviderGreenhouseTeams,
			want:     "permission-manager.cloudoperators.dev/greenhouse-teams",
		},
		{
			name:     "arbitrary provider string",
			provider: "custom-provider",
			want:     "permission-manager.cloudoperators.dev/custom-provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FinalizerName(tt.provider)
			if got != tt.want {
				t.Errorf("FinalizerName(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}
