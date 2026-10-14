// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package ccrn

import (
	"testing"
)

func TestParse_ClusterURN(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantType    ResourceType
		wantCluster string
		wantNS      string
		wantErr     bool
	}{
		{
			name:        "valid cluster URN",
			input:       "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/my-cluster",
			wantType:    ResourceTypeCluster,
			wantCluster: "my-cluster",
		},
		{
			name:        "valid cluster URN with complex name",
			input:       "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/prod-eu-west-1",
			wantType:    ResourceTypeCluster,
			wantCluster: "prod-eu-west-1",
		},
		{
			name:    "cluster URN with extra segments",
			input:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/my-cluster/extra",
			wantErr: true,
		},
		{
			name:    "cluster URN with empty cluster name",
			input:   "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", got.Type, tt.wantType)
			}
			if got.ClusterName != tt.wantCluster {
				t.Errorf("ClusterName = %v, want %v", got.ClusterName, tt.wantCluster)
			}
			if got.Namespace != tt.wantNS {
				t.Errorf("Namespace = %v, want %v", got.Namespace, tt.wantNS)
			}
			if got.Raw != tt.input {
				t.Errorf("Raw = %v, want %v", got.Raw, tt.input)
			}
		})
	}
}

func TestParse_NamespaceURN(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantType    ResourceType
		wantCluster string
		wantNS      string
		wantErr     bool
	}{
		{
			name:        "valid namespace URN",
			input:       "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/my-cluster/kube-system",
			wantType:    ResourceTypeNamespace,
			wantCluster: "my-cluster",
			wantNS:      "kube-system",
		},
		{
			name:        "valid namespace URN with complex names",
			input:       "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/prod-eu-west-1/my-app-namespace",
			wantType:    ResourceTypeNamespace,
			wantCluster: "prod-eu-west-1",
			wantNS:      "my-app-namespace",
		},
		{
			name:    "namespace URN missing namespace segment",
			input:   "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/my-cluster",
			wantErr: true,
		},
		{
			name:    "namespace URN with empty namespace",
			input:   "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/my-cluster/",
			wantErr: true,
		},
		{
			name:    "namespace URN with empty cluster name",
			input:   "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1//kube-system",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", got.Type, tt.wantType)
			}
			if got.ClusterName != tt.wantCluster {
				t.Errorf("ClusterName = %v, want %v", got.ClusterName, tt.wantCluster)
			}
			if got.Namespace != tt.wantNS {
				t.Errorf("Namespace = %v, want %v", got.Namespace, tt.wantNS)
			}
		})
	}
}

func TestParse_InvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty string", input: ""},
		{name: "missing prefix", input: "cluster.k8s.ccrn.cloudoperators.dev/v1/foo"},
		{name: "wrong prefix", input: "urn:other:cluster.k8s.ccrn.cloudoperators.dev/v1/foo"},
		{name: "unsupported version", input: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v2/foo"},
		{name: "unsupported type", input: "urn:ccrn:pod.k8s.ccrn.cloudoperators.dev/v1/foo"},
		{name: "too few segments", input: "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.input)
			if err == nil {
				t.Fatalf("expected error for input %q, got nil", tt.input)
			}
		})
	}
}

func TestParsedCCRN_IsKubernetes(t *testing.T) {
	cluster := &ParsedCCRN{Type: ResourceTypeCluster, Domain: DomainK8s}
	if !cluster.IsKubernetes() {
		t.Error("cluster resource should be Kubernetes")
	}

	ns := &ParsedCCRN{Type: ResourceTypeNamespace, Domain: DomainK8s}
	if !ns.IsKubernetes() {
		t.Error("namespace resource should be Kubernetes")
	}

	gh := &ParsedCCRN{Domain: DomainGithub}
	if gh.IsKubernetes() {
		t.Error("github domain resource should not be Kubernetes")
	}
}

func TestParse_GithubRepository(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantInstance string
		wantOrg      string
		wantRepo     string
		wantResType  GithubResourceType
		wantErr      bool
	}{
		{
			name:         "valid repository CCRN",
			input:        "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/my-repo",
			wantInstance: "github.example.com",
			wantOrg:      "my-org",
			wantRepo:     "my-repo",
			wantResType:  GithubResourceTypeRepository,
		},
		{
			name:         "repository CCRN with github.com",
			input:        "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.com/my-org/permission-manager",
			wantInstance: "github.com",
			wantOrg:      "my-org",
			wantRepo:     "permission-manager",
			wantResType:  GithubResourceTypeRepository,
		},
		{
			name:         "repository CCRN with wildcard repo",
			input:        "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/*",
			wantInstance: "github.example.com",
			wantOrg:      "cc",
			wantRepo:     "*",
			wantResType:  GithubResourceTypeRepository,
		},
		{
			name:    "repository CCRN missing repo segment",
			input:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc",
			wantErr: true,
		},
		{
			name:    "repository CCRN with extra segments",
			input:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/repo/extra",
			wantErr: true,
		},
		{
			name:    "repository CCRN empty instance",
			input:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1//cc/repo",
			wantErr: true,
		},
		{
			name:    "repository CCRN empty org",
			input:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com//repo",
			wantErr: true,
		},
		{
			name:    "repository CCRN empty repo",
			input:   "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Domain != DomainGithub {
				t.Errorf("Domain = %q, want %q", got.Domain, DomainGithub)
			}
			if got.Github == nil {
				t.Fatal("Github field is nil, expected GithubResource")
			}
			if got.Github.Instance != tt.wantInstance {
				t.Errorf("Instance = %q, want %q", got.Github.Instance, tt.wantInstance)
			}
			if got.Github.Org != tt.wantOrg {
				t.Errorf("Org = %q, want %q", got.Github.Org, tt.wantOrg)
			}
			if got.Github.Repo != tt.wantRepo {
				t.Errorf("Repo = %q, want %q", got.Github.Repo, tt.wantRepo)
			}
			if got.Github.ResourceType != tt.wantResType {
				t.Errorf("ResourceType = %q, want %q", got.Github.ResourceType, tt.wantResType)
			}
			if got.Raw != tt.input {
				t.Errorf("Raw = %q, want %q", got.Raw, tt.input)
			}
		})
	}
}

func TestParse_GithubOrganization(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantInstance string
		wantOrg      string
		wantResType  GithubResourceType
		wantErr      bool
	}{
		{
			name:         "valid organization CCRN",
			input:        "urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org",
			wantInstance: "github.example.com",
			wantOrg:      "my-org",
			wantResType:  GithubResourceTypeOrganization,
		},
		{
			name:         "organization CCRN with github.com",
			input:        "urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/github.com/my-org",
			wantInstance: "github.com",
			wantOrg:      "my-org",
			wantResType:  GithubResourceTypeOrganization,
		},
		{
			name:    "organization CCRN missing org",
			input:   "urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/github.example.com",
			wantErr: true,
		},
		{
			name:    "organization CCRN with extra segments",
			input:   "urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/extra",
			wantErr: true,
		},
		{
			name:    "organization CCRN empty instance",
			input:   "urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1//cc",
			wantErr: true,
		},
		{
			name:    "organization CCRN empty org",
			input:   "urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/github.example.com/",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Domain != DomainGithub {
				t.Errorf("Domain = %q, want %q", got.Domain, DomainGithub)
			}
			if got.Github == nil {
				t.Fatal("Github field is nil, expected GithubResource")
			}
			if got.Github.Instance != tt.wantInstance {
				t.Errorf("Instance = %q, want %q", got.Github.Instance, tt.wantInstance)
			}
			if got.Github.Org != tt.wantOrg {
				t.Errorf("Org = %q, want %q", got.Github.Org, tt.wantOrg)
			}
			if got.Github.Repo != "" {
				t.Errorf("Repo = %q, want empty for org CCRN", got.Github.Repo)
			}
			if got.Github.Visibility != "" {
				t.Errorf("Visibility = %q, want empty for org CCRN", got.Github.Visibility)
			}
			if got.Github.ResourceType != tt.wantResType {
				t.Errorf("ResourceType = %q, want %q", got.Github.ResourceType, tt.wantResType)
			}
			if got.Raw != tt.input {
				t.Errorf("Raw = %q, want %q", got.Raw, tt.input)
			}
		})
	}
}

func TestParse_GithubRepositoryType(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantInstance   string
		wantOrg        string
		wantVisibility string
		wantResType    GithubResourceType
		wantErr        bool
	}{
		{
			name:           "valid repository_type CCRN with internal visibility",
			input:          "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/internal",
			wantInstance:   "github.example.com",
			wantOrg:        "my-org",
			wantVisibility: "internal",
			wantResType:    GithubResourceTypeRepositoryType,
		},
		{
			name:           "repository_type CCRN with public visibility",
			input:          "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/public",
			wantInstance:   "github.example.com",
			wantOrg:        "cc",
			wantVisibility: "public",
			wantResType:    GithubResourceTypeRepositoryType,
		},
		{
			name:           "repository_type CCRN with private visibility",
			input:          "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/private",
			wantInstance:   "github.example.com",
			wantOrg:        "cc",
			wantVisibility: "private",
			wantResType:    GithubResourceTypeRepositoryType,
		},
		{
			name:           "repository_type CCRN with wildcard visibility",
			input:          "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/*",
			wantInstance:   "github.example.com",
			wantOrg:        "cc",
			wantVisibility: "*",
			wantResType:    GithubResourceTypeRepositoryType,
		},
		{
			name:    "repository_type CCRN missing visibility",
			input:   "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/cc",
			wantErr: true,
		},
		{
			name:    "repository_type CCRN with extra segments",
			input:   "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/public/extra",
			wantErr: true,
		},
		{
			name:    "repository_type CCRN empty instance",
			input:   "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1//cc/public",
			wantErr: true,
		},
		{
			name:    "repository_type CCRN empty org",
			input:   "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com//public",
			wantErr: true,
		},
		{
			name:    "repository_type CCRN empty visibility",
			input:   "urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/github.example.com/cc/",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Domain != DomainGithub {
				t.Errorf("Domain = %q, want %q", got.Domain, DomainGithub)
			}
			if got.Github == nil {
				t.Fatal("Github field is nil, expected GithubResource")
			}
			if got.Github.Instance != tt.wantInstance {
				t.Errorf("Instance = %q, want %q", got.Github.Instance, tt.wantInstance)
			}
			if got.Github.Org != tt.wantOrg {
				t.Errorf("Org = %q, want %q", got.Github.Org, tt.wantOrg)
			}
			if got.Github.Repo != "" {
				t.Errorf("Repo = %q, want empty for repository_type CCRN", got.Github.Repo)
			}
			if got.Github.Visibility != tt.wantVisibility {
				t.Errorf("Visibility = %q, want %q", got.Github.Visibility, tt.wantVisibility)
			}
			if got.Github.ResourceType != tt.wantResType {
				t.Errorf("ResourceType = %q, want %q", got.Github.ResourceType, tt.wantResType)
			}
			if got.Raw != tt.input {
				t.Errorf("Raw = %q, want %q", got.Raw, tt.input)
			}
		})
	}
}

func TestParsedCCRN_IsGithub(t *testing.T) {
	gh := &ParsedCCRN{Domain: DomainGithub}
	if !gh.IsGithub() {
		t.Error("github domain resource should return true for IsGithub()")
	}

	k8s := &ParsedCCRN{Domain: DomainK8s}
	if k8s.IsGithub() {
		t.Error("k8s domain resource should return false for IsGithub()")
	}
}

func TestParse_DomainExtraction(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantDomain string
	}{
		{
			name:       "k8s cluster URN extracts k8s domain",
			input:      "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/my-cluster",
			wantDomain: DomainK8s,
		},
		{
			name:       "k8s namespace URN extracts k8s domain",
			input:      "urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/my-cluster/kube-system",
			wantDomain: DomainK8s,
		},
		{
			name:       "github repository URN extracts github domain",
			input:      "urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.example.com/my-org/my-repo",
			wantDomain: DomainGithub,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Domain != tt.wantDomain {
				t.Errorf("Domain = %q, want %q", got.Domain, tt.wantDomain)
			}
		})
	}
}

func TestDomainConstants(t *testing.T) {
	if DomainK8s != "k8s" {
		t.Errorf("DomainK8s = %q, want %q", DomainK8s, "k8s")
	}
	if DomainGithub != "github" {
		t.Errorf("DomainGithub = %q, want %q", DomainGithub, "github")
	}
}

func TestNewParser_DefaultDomainAlwaysIncluded(t *testing.T) {
	p := NewParser(nil)
	got, err := p.Parse("urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/my-cluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ClusterName != "my-cluster" {
		t.Errorf("ClusterName = %q, want %q", got.ClusterName, "my-cluster")
	}
}

func TestParser_AdditionalK8sGroup(t *testing.T) {
	p := NewParser([]string{"ccrn.mycompany.dev"})

	tests := []struct {
		name        string
		input       string
		wantType    ResourceType
		wantCluster string
		wantNS      string
	}{
		{
			name:        "cluster CCRN with extra group",
			input:       "urn:ccrn:cluster.k8s.ccrn.mycompany.dev/v1/prod-cluster",
			wantType:    ResourceTypeCluster,
			wantCluster: "prod-cluster",
		},
		{
			name:        "namespace CCRN with extra group",
			input:       "urn:ccrn:namespace.k8s.ccrn.mycompany.dev/v1/prod-cluster/kube-system",
			wantType:    ResourceTypeNamespace,
			wantCluster: "prod-cluster",
			wantNS:      "kube-system",
		},
		{
			name:        "default domain still recognised",
			input:       "urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/other-cluster",
			wantType:    ResourceTypeCluster,
			wantCluster: "other-cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.Parse(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.IsKubernetes() {
				t.Error("expected kubernetes domain")
			}
			if got.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", got.Type, tt.wantType)
			}
			if got.ClusterName != tt.wantCluster {
				t.Errorf("ClusterName = %q, want %q", got.ClusterName, tt.wantCluster)
			}
			if got.Namespace != tt.wantNS {
				t.Errorf("Namespace = %q, want %q", got.Namespace, tt.wantNS)
			}
		})
	}
}

func TestParser_GithubDelegatedToPackageLevel(t *testing.T) {
	p := NewParser([]string{"ccrn.mycompany.dev"})
	got, err := p.Parse("urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/github.com/my-org/my-repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.IsGithub() {
		t.Error("expected github domain")
	}
}

func TestParser_DeduplicatesGroups(t *testing.T) {
	p := NewParser([]string{DefaultDomain, "extra.dev", DefaultDomain, "extra.dev"})
	// Both cluster URNs should parse without error — no duplicated matching.
	for _, urn := range []string{
		"urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/c1",
		"urn:ccrn:cluster.k8s.extra.dev/v1/c2",
	} {
		if _, err := p.Parse(urn); err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", urn, err)
		}
	}
}

func TestParse_ResourceScopedCCRNs(t *testing.T) {
	p := NewParser(nil)
	tests := []struct {
		name      string
		input     string
		wantType  ResourceType
		wantNS    string
		wantGroup string
		wantRes   string
		wantSub   string
		wantErr   bool
	}{
		{
			name:      "cluster-resource with named group",
			input:     "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/apps/deployments",
			wantType:  ResourceTypeClusterResource,
			wantGroup: "apps",
			wantRes:   "deployments",
		},
		{
			name:      "cluster-resource core group v1 maps to empty",
			input:     "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/v1/nodes",
			wantType:  ResourceTypeClusterResource,
			wantGroup: "",
			wantRes:   "nodes",
		},
		{
			name:      "namespace-resource with subresource",
			input:     "urn:ccrn:namespace-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/demo/v1/pods:log",
			wantType:  ResourceTypeNamespaceResource,
			wantNS:    "demo",
			wantGroup: "",
			wantRes:   "pods",
			wantSub:   "log",
		},
		{
			name:    "cluster-resource missing resource segment is an error",
			input:   "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/apps",
			wantErr: true,
		},
		{
			name:    "literal slash in resource overflows segment count",
			input:   "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster/apps/deployments/scale",
			wantErr: true,
		},
		{
			name:    "cluster-resource empty apigroup is an error",
			input:   "urn:ccrn:cluster-resource.k8s.ccrn.cloudoperators.dev/v1/remote-cluster//deployments",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.IsKubernetes() {
				t.Errorf("IsKubernetes() = false, want true")
			}
			if got.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", got.Type, tt.wantType)
			}
			if got.Namespace != tt.wantNS {
				t.Errorf("Namespace = %v, want %v", got.Namespace, tt.wantNS)
			}
			if got.K8sResource == nil {
				t.Fatalf("K8sResource = nil, want non-nil")
			}
			if got.K8sResource.APIGroup != tt.wantGroup {
				t.Errorf("APIGroup = %q, want %q", got.K8sResource.APIGroup, tt.wantGroup)
			}
			if got.K8sResource.Resource != tt.wantRes {
				t.Errorf("Resource = %q, want %q", got.K8sResource.Resource, tt.wantRes)
			}
			if got.K8sResource.SubResource != tt.wantSub {
				t.Errorf("SubResource = %q, want %q", got.K8sResource.SubResource, tt.wantSub)
			}
		})
	}
}
