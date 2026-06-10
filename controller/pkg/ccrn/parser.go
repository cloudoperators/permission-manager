// Package ccrn provides a parser for Common Cloud Resource Name (CCRN) URNs
// used in the permission-manager system.
package ccrn

import (
	"errors"
	"fmt"
	"strings"
)

// ResourceType represents the type of resource identified by a CCRN.
type ResourceType string

const (
	// ResourceTypeCluster identifies a Kubernetes cluster resource.
	ResourceTypeCluster ResourceType = "cluster"
	// ResourceTypeNamespace identifies a Kubernetes namespace resource.
	ResourceTypeNamespace ResourceType = "namespace"
)

// Domain constants identify the provider domain of a CCRN.
const (
	// DomainK8s identifies Kubernetes resources.
	DomainK8s = "k8s"
	// DomainGithub identifies GitHub resources.
	DomainGithub = "github"
)

// GithubResourceType identifies the sub-type of a GitHub CCRN.
type GithubResourceType string

const (
	// GithubResourceTypeRepository identifies a specific repository.
	GithubResourceTypeRepository GithubResourceType = "repository"
	// GithubResourceTypeOrganization identifies an organization.
	GithubResourceTypeOrganization GithubResourceType = "organization"
	// GithubResourceTypeRepositoryType identifies a class of repositories by visibility.
	GithubResourceTypeRepositoryType GithubResourceType = "repository_type"
)

// GithubResource holds the parsed components of a GitHub CCRN.
type GithubResource struct {
	// Instance is the GitHub Enterprise hostname (e.g., "github.example.com", "github.com").
	Instance string
	// Org is the GitHub organization name.
	Org string
	// Repo is the repository name (only set for repository CCRNs).
	Repo string
	// Visibility is the repository visibility (only set for repository_type CCRNs).
	Visibility string
	// ResourceType identifies what kind of GitHub resource this CCRN refers to.
	ResourceType GithubResourceType
}

// ParsedCCRN holds the parsed components of a CCRN URN.
type ParsedCCRN struct {
	// Type is the resource type (cluster, namespace, etc.).
	Type ResourceType
	// Domain is the provider domain (e.g., "k8s", "github").
	Domain string
	// ClusterName is the name of the Kubernetes cluster.
	ClusterName string
	// Namespace is the namespace name (only set for namespace-type CCRNs).
	Namespace string
	// Github holds GitHub-specific parsed data (only set for github domain CCRNs).
	Github *GithubResource
	// Raw is the original URN string.
	Raw string
}

// DefaultDomain is the default CCRN domain suffix used in URN identifiers.
// Override this at build time or via configuration for deployments using a
// different CCRN registry (e.g., "ccrn.cloud.sap" for SAP internal use).
var DefaultDomain = "ccrn.cloudoperators.dev"

const (
	urnPrefix   = "urn:ccrn:"
	ccrnVersion = "v1"
)

// ccrnSuffix returns the current domain suffix (e.g., ".ccrn.cloudoperators.dev").
func ccrnSuffix() string { return "." + DefaultDomain }

// CCRN type names derived from the configurable domain.
func clusterCCRNName() string   { return "cluster.k8s." + DefaultDomain }
func namespaceCCRNName() string { return "namespace.k8s." + DefaultDomain }

func githubOrganizationCCRNName() string   { return "organization.github." + DefaultDomain }
func githubRepositoryCCRNName() string     { return "repository.github." + DefaultDomain }
func githubRepositoryTypeCCRNName() string { return "repository_type.github." + DefaultDomain }

// Parse parses a CCRN URN string and returns its components.
// Supported formats:
//   - Cluster:          urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/<cluster-name>
//   - Namespace:        urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/<cluster-name>/<namespace>
//   - Repository:       urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/<instance>/<org>/<repo>
//   - Organization:     urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/<instance>/<org>
//   - Repository type:  urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/<instance>/<org>/<visibility>
func Parse(urn string) (*ParsedCCRN, error) {
	if urn == "" {
		return nil, errors.New("CCRN URN must not be empty")
	}
	if !strings.HasPrefix(urn, urnPrefix) {
		return nil, fmt.Errorf("invalid CCRN URN: must start with %q", urnPrefix)
	}

	body := strings.TrimPrefix(urn, urnPrefix)
	parts := strings.Split(body, "/")

	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid CCRN URN: expected at least 3 segments after prefix, got %d", len(parts))
	}

	ccrnName := parts[0]
	version := parts[1]

	if version != ccrnVersion {
		return nil, fmt.Errorf("unsupported CCRN version: %q (expected %q)", version, ccrnVersion)
	}

	switch ccrnName {
	case clusterCCRNName():
		if len(parts) != 3 {
			return nil, fmt.Errorf("cluster CCRN URN must have exactly 3 segments, got %d", len(parts))
		}
		clusterName := parts[2]
		if clusterName == "" {
			return nil, errors.New("cluster name must not be empty")
		}
		return &ParsedCCRN{
			Type:        ResourceTypeCluster,
			Domain:      DomainK8s,
			ClusterName: clusterName,
			Raw:         urn,
		}, nil

	case namespaceCCRNName():
		if len(parts) != 4 {
			return nil, fmt.Errorf("namespace CCRN URN must have exactly 4 segments, got %d", len(parts))
		}
		clusterName := parts[2]
		namespace := parts[3]
		if clusterName == "" {
			return nil, errors.New("cluster name must not be empty")
		}
		if namespace == "" {
			return nil, errors.New("namespace must not be empty")
		}
		return &ParsedCCRN{
			Type:        ResourceTypeNamespace,
			Domain:      DomainK8s,
			ClusterName: clusterName,
			Namespace:   namespace,
			Raw:         urn,
		}, nil

	case githubOrganizationCCRNName():
		return parseGithubOrganization(urn, parts)

	case githubRepositoryCCRNName():
		return parseGithubRepository(urn, parts)

	case githubRepositoryTypeCCRNName():
		return parseGithubRepositoryType(urn, parts)

	default:
		return nil, fmt.Errorf("unsupported CCRN type: %q", ccrnName)
	}
}

// parseGithubOrganization parses: urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/<instance>/<org>
func parseGithubOrganization(urn string, parts []string) (*ParsedCCRN, error) {
	// Expected: [ccrnName, version, instance, org] = 4 parts
	if len(parts) != 4 {
		return nil, fmt.Errorf("organization CCRN must have exactly 4 segments, got %d", len(parts))
	}
	instance := parts[2]
	org := parts[3]
	if instance == "" {
		return nil, errors.New("instance must not be empty in organization CCRN")
	}
	if org == "" {
		return nil, errors.New("org must not be empty in organization CCRN")
	}
	return &ParsedCCRN{
		Domain: DomainGithub,
		Github: &GithubResource{
			Instance:     instance,
			Org:          org,
			ResourceType: GithubResourceTypeOrganization,
		},
		Raw: urn,
	}, nil
}

// parseGithubRepository parses: urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/<instance>/<org>/<repo>
func parseGithubRepository(urn string, parts []string) (*ParsedCCRN, error) {
	// Expected: [ccrnName, version, instance, org, repo] = 5 parts
	if len(parts) != 5 {
		return nil, fmt.Errorf("repository CCRN must have exactly 5 segments, got %d", len(parts))
	}
	instance := parts[2]
	org := parts[3]
	repo := parts[4]
	if instance == "" {
		return nil, errors.New("instance must not be empty in repository CCRN")
	}
	if org == "" {
		return nil, errors.New("org must not be empty in repository CCRN")
	}
	if repo == "" {
		return nil, errors.New("repo must not be empty in repository CCRN")
	}
	return &ParsedCCRN{
		Domain: DomainGithub,
		Github: &GithubResource{
			Instance:     instance,
			Org:          org,
			Repo:         repo,
			ResourceType: GithubResourceTypeRepository,
		},
		Raw: urn,
	}, nil
}

// parseGithubRepositoryType parses: urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/<instance>/<org>/<visibility>
func parseGithubRepositoryType(urn string, parts []string) (*ParsedCCRN, error) {
	// Expected: [ccrnName, version, instance, org, visibility] = 5 parts
	if len(parts) != 5 {
		return nil, fmt.Errorf("repository_type CCRN must have exactly 5 segments, got %d", len(parts))
	}
	instance := parts[2]
	org := parts[3]
	visibility := parts[4]
	if instance == "" {
		return nil, errors.New("instance must not be empty in repository_type CCRN")
	}
	if org == "" {
		return nil, errors.New("org must not be empty in repository_type CCRN")
	}
	if visibility == "" {
		return nil, errors.New("visibility must not be empty in repository_type CCRN")
	}
	return &ParsedCCRN{
		Domain: DomainGithub,
		Github: &GithubResource{
			Instance:     instance,
			Org:          org,
			Visibility:   visibility,
			ResourceType: GithubResourceTypeRepositoryType,
		},
		Raw: urn,
	}, nil
}

// IsKubernetes returns true if this CCRN refers to a Kubernetes resource.
func (p *ParsedCCRN) IsKubernetes() bool {
	return p.Domain == DomainK8s
}

// IsGithub returns true if this CCRN refers to a GitHub resource.
func (p *ParsedCCRN) IsGithub() bool {
	return p.Domain == DomainGithub
}
