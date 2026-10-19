// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

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
	// ResourceTypeClusterResource identifies a cluster-scoped resource CCRN
	// that embeds an apiGroup/resource target.
	ResourceTypeClusterResource ResourceType = "cluster-resource"
	// ResourceTypeNamespaceResource identifies a namespace-scoped resource CCRN
	// that embeds an apiGroup/resource target.
	ResourceTypeNamespaceResource ResourceType = "namespace-resource"
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

// K8sResource holds the resource target embedded in a resource-scoped CCRN
// (cluster-resource / namespace-resource). It is nil for the coarse types.
type K8sResource struct {
	// APIGroup is the Kubernetes API group ("" for the core group).
	APIGroup string
	// Resource is the plural resource name (e.g. "deployments"), or "*".
	Resource string
	// SubResource is the subresource (e.g. "log"), or "" when absent.
	SubResource string
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
	// K8sResource holds the embedded resource target for resource-scoped
	// Kubernetes CCRNs (nil otherwise).
	K8sResource *K8sResource
	// Raw is the original URN string.
	Raw string
}

// DefaultDomain is the default CCRN domain suffix used in URN identifiers.
// Override this at build time or via configuration for deployments using a
// different CCRN registry (e.g., "ccrn.cloud.company" for internal use).
var DefaultDomain = "ccrn.cloudoperators.dev"

const (
	urnPrefix   = "urn:ccrn:"
	ccrnVersion = "v1"
	// coreGroupToken is the apiGroup segment value that denotes the core group ("").
	coreGroupToken = "v1"
)

// Parser parses CCRN URNs against a set of registered k8s domain groups.
// Multiple groups allow deployments that use a custom CCRN registry domain
// alongside the default one. The default domain is always included.
// Use NewParser to construct; the zero value is not usable.
type Parser struct {
	// k8sTypes maps fully-qualified k8s CCRN type name → ResourceType.
	// Built once at construction time; each Parse call does a single map lookup.
	k8sTypes map[string]ResourceType
}

// NewParser returns a Parser that recognises k8s CCRNs whose type names end in
// any of the supplied domain groups. The default domain is always included so
// callers only need to pass additional groups.
func NewParser(k8sGroups []string) *Parser {
	seen := make(map[string]struct{}, len(k8sGroups)+1)
	groups := make([]string, 0, len(k8sGroups)+1)
	groups = append(groups, DefaultDomain)
	seen[DefaultDomain] = struct{}{}
	for _, g := range k8sGroups {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if _, ok := seen[g]; !ok {
			seen[g] = struct{}{}
			groups = append(groups, g)
		}
	}
	k8sTypes := make(map[string]ResourceType, len(groups)*2)
	for _, g := range groups {
		k8sTypes["cluster.k8s."+g] = ResourceTypeCluster
		k8sTypes["namespace.k8s."+g] = ResourceTypeNamespace
		k8sTypes["cluster-resource.k8s."+g] = ResourceTypeClusterResource
		k8sTypes["namespace-resource.k8s."+g] = ResourceTypeNamespaceResource
	}
	return &Parser{k8sTypes: k8sTypes}
}

// Parse parses urn against all registered k8s domain groups. Non-k8s types
// (e.g. GitHub) are parsed using the default domain.
func (p *Parser) Parse(urn string) (*ParsedCCRN, error) {
	ccrnName, parts, err := splitURN(urn)
	if err != nil {
		return nil, err
	}

	switch p.k8sTypes[ccrnName] {
	case ResourceTypeCluster:
		return parseK8sCluster(urn, parts)
	case ResourceTypeNamespace:
		return parseK8sNamespace(urn, parts)
	case ResourceTypeClusterResource:
		return parseK8sClusterResource(urn, parts)
	case ResourceTypeNamespaceResource:
		return parseK8sNamespaceResource(urn, parts)
	}

	return parseGithub(ccrnName, urn, parts)
}

// splitURN validates the URN prefix, splits it into segments, and checks the
// version field. Returns ccrnName (segment 0) and all segments, or an error.
func splitURN(urn string) (ccrnName string, parts []string, err error) {
	if urn == "" {
		return "", nil, errors.New("CCRN URN must not be empty")
	}
	if !strings.HasPrefix(urn, urnPrefix) {
		return "", nil, fmt.Errorf("invalid CCRN URN: must start with %q", urnPrefix)
	}
	body := strings.TrimPrefix(urn, urnPrefix)
	parts = strings.Split(body, "/")
	if len(parts) < 3 {
		return "", nil, fmt.Errorf("invalid CCRN URN: expected at least 3 segments after prefix, got %d", len(parts))
	}
	if parts[1] != ccrnVersion {
		return "", nil, fmt.Errorf("unsupported CCRN version: %q (expected %q)", parts[1], ccrnVersion)
	}
	return parts[0], parts, nil
}

// Parse parses a CCRN URN string using only the default domain.
// Supported formats:
//   - Cluster:          urn:ccrn:cluster.k8s.ccrn.cloudoperators.dev/v1/<cluster-name>
//   - Namespace:        urn:ccrn:namespace.k8s.ccrn.cloudoperators.dev/v1/<cluster-name>/<namespace>
//   - Repository:       urn:ccrn:repository.github.ccrn.cloudoperators.dev/v1/<instance>/<org>/<repo>
//   - Organization:     urn:ccrn:organization.github.ccrn.cloudoperators.dev/v1/<instance>/<org>
//   - Repository type:  urn:ccrn:repository_type.github.ccrn.cloudoperators.dev/v1/<instance>/<org>/<visibility>
func Parse(urn string) (*ParsedCCRN, error) {
	ccrnName, parts, err := splitURN(urn)
	if err != nil {
		return nil, err
	}

	switch ccrnName {
	case "cluster.k8s." + DefaultDomain:
		return parseK8sCluster(urn, parts)
	case "namespace.k8s." + DefaultDomain:
		return parseK8sNamespace(urn, parts)
	case "cluster-resource.k8s." + DefaultDomain:
		return parseK8sClusterResource(urn, parts)
	case "namespace-resource.k8s." + DefaultDomain:
		return parseK8sNamespaceResource(urn, parts)
	default:
		return parseGithub(ccrnName, urn, parts)
	}
}

// parseGithub dispatches github CCRN types using DefaultDomain.
func parseGithub(ccrnName, urn string, parts []string) (*ParsedCCRN, error) {
	switch ccrnName {
	case "organization.github." + DefaultDomain:
		return parseGithubOrganization(urn, parts)
	case "repository.github." + DefaultDomain:
		return parseGithubRepository(urn, parts)
	case "repository_type.github." + DefaultDomain:
		return parseGithubRepositoryType(urn, parts)
	default:
		return nil, fmt.Errorf("unsupported CCRN type: %q", ccrnName)
	}
}

// parseK8sCluster parses a cluster CCRN from already-validated parts.
func parseK8sCluster(urn string, parts []string) (*ParsedCCRN, error) {
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
}

// parseK8sNamespace parses a namespace CCRN from already-validated parts.
func parseK8sNamespace(urn string, parts []string) (*ParsedCCRN, error) {
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
}

// parseK8sClusterResource parses:
// urn:ccrn:cluster-resource.k8s.<domain>/v1/<cluster>/<apigroup>/<resource>[:<subresource>]
func parseK8sClusterResource(urn string, parts []string) (*ParsedCCRN, error) {
	if len(parts) != 5 {
		return nil, fmt.Errorf("cluster-resource CCRN URN must have exactly 5 segments, got %d", len(parts))
	}
	clusterName := parts[2]
	if clusterName == "" {
		return nil, errors.New("cluster name must not be empty")
	}
	res, err := parseResourceSegment(parts[3], parts[4])
	if err != nil {
		return nil, err
	}
	return &ParsedCCRN{
		Type:        ResourceTypeClusterResource,
		Domain:      DomainK8s,
		ClusterName: clusterName,
		K8sResource: res,
		Raw:         urn,
	}, nil
}

// parseK8sNamespaceResource parses:
// urn:ccrn:namespace-resource.k8s.<domain>/v1/<cluster>/<namespace>/<apigroup>/<resource>[:<subresource>]
func parseK8sNamespaceResource(urn string, parts []string) (*ParsedCCRN, error) {
	if len(parts) != 6 {
		return nil, fmt.Errorf("namespace-resource CCRN URN must have exactly 6 segments, got %d", len(parts))
	}
	clusterName := parts[2]
	namespace := parts[3]
	if clusterName == "" {
		return nil, errors.New("cluster name must not be empty")
	}
	if namespace == "" {
		return nil, errors.New("namespace must not be empty")
	}
	res, err := parseResourceSegment(parts[4], parts[5])
	if err != nil {
		return nil, err
	}
	return &ParsedCCRN{
		Type:        ResourceTypeNamespaceResource,
		Domain:      DomainK8s,
		ClusterName: clusterName,
		Namespace:   namespace,
		K8sResource: res,
		Raw:         urn,
	}, nil
}

// parseResourceSegment splits "<apigroup>" + "<resource>[:<subresource>]" into a
// K8sResource. The core-group token "v1" maps to "". A literal "/" in the
// resource cannot reach here; it overflows the segment count upstream. Commas
// are rejected: they are never valid in Kubernetes group/resource names and
// would collide with the comma-joined RBAC rule merge key downstream.
func parseResourceSegment(apiGroup, segment string) (*K8sResource, error) {
	if apiGroup == "" {
		return nil, errors.New("apiGroup must not be empty")
	}
	if strings.Contains(apiGroup, ",") {
		return nil, errors.New("apiGroup must not contain a comma")
	}
	group := apiGroup
	if group == coreGroupToken {
		group = ""
	}
	resource, sub, hasSub := strings.Cut(segment, ":")
	if resource == "" {
		return nil, errors.New("resource must not be empty")
	}
	if strings.Contains(resource, ",") {
		return nil, errors.New("resource must not contain a comma")
	}
	if hasSub {
		if sub == "" {
			return nil, errors.New("subresource must not be empty")
		}
		if strings.Contains(sub, ":") {
			return nil, errors.New("resource must not contain more than one ':' subresource delimiter")
		}
		if strings.Contains(sub, ",") {
			return nil, errors.New("subresource must not contain a comma")
		}
	}
	return &K8sResource{APIGroup: group, Resource: resource, SubResource: sub}, nil
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
