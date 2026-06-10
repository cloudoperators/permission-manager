package greenhouseteams

import (
	"fmt"
	"slices"

	greenhouse "github.com/cloudoperators/permission-manager/controller/pkg/greenhouse"

	pmv1 "github.com/cloudoperators/permission-manager/api/v1"
	"github.com/cloudoperators/permission-manager/controller/pkg/ccrn"
)

// DesiredTRB represents a single desired TeamRoleBinding to be reconciled.
type DesiredTRB struct {
	// Name is the computed TRB resource name.
	Name string
	// TeamRole is the resolved teamRoleRef from the capability mapping.
	TeamRole string
	// ClusterName is the target cluster extracted from the CCRN.
	ClusterName string
	// Namespaces is populated when the CCRN is namespace-scoped.
	Namespaces []string
}

// DesiredState represents the full desired state for a single ASR reconciliation
// within a single target namespace.
type DesiredState struct {
	// TeamName is the computed Team CR name (pm-<teamName>).
	TeamName string
	// MappedIDPGroup is the value for Team.spec.mappedIdPGroup (always = asr.Spec.Name).
	MappedIDPGroup string
	// TRBs is the list of desired TeamRoleBindings.
	TRBs []DesiredTRB
}

// ComputeDesiredState computes the desired Team and TeamRoleBinding state from an ASR's permissions.
// It iterates permissions, parses CCRNs, resolves capabilities to team roles, and produces
// the set of TRBs needed. Non-k8s CCRNs are silently skipped. Unmapped capabilities are skipped
// with a warning (they don't fail the reconciliation).
//
// Parameters:
//   - asr: the ApplicationSpecificRole being reconciled
//   - teamName: the resolved team name (advancedConfig.teamName or asr.Spec.Name)
//   - capMapping: the capability-to-teamRole mapping from the ConnectorBinding definition
func ComputeDesiredState(
	asr *pmv1.ApplicationSpecificRole,
	teamName string,
	capMapping greenhouse.CapabilityMapping,
) (*DesiredState, []string) {
	state := &DesiredState{
		TeamName:       TeamCRName(teamName),
		MappedIDPGroup: asr.Spec.Name,
	}

	var warnings []string

	// Track unique TRBs by (teamRole, clusterName) to merge namespaces
	type trbKey struct {
		teamRole    string
		clusterName string
	}
	trbMap := make(map[trbKey]*DesiredTRB)

	for _, perm := range asr.Spec.Permissions {
		// Parse the CCRN
		parsed, err := ccrn.Parse(perm.Resource)
		if err != nil {
			// Not a valid CCRN — skip silently (could be a different resource format)
			warnings = append(warnings, fmt.Sprintf("skipping unparseable resource %q: %v", perm.Resource, err))
			continue
		}

		// Only handle k8s cluster/namespace CCRNs
		if !parsed.IsKubernetes() {
			continue
		}

		// Resolve capability to team role
		teamRole, err := capMapping.Resolve(perm.Capability)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipping unmapped capability %q for resource %q: %v", perm.Capability, perm.Resource, err))
			continue
		}

		key := trbKey{teamRole: teamRole, clusterName: parsed.ClusterName}

		existing, ok := trbMap[key]
		if !ok {
			trb := &DesiredTRB{
				Name:        TRBName(asr.Spec.Name, teamRole, parsed.ClusterName),
				TeamRole:    teamRole,
				ClusterName: parsed.ClusterName,
			}
			if parsed.Type == ccrn.ResourceTypeNamespace && parsed.Namespace != "" {
				trb.Namespaces = []string{parsed.Namespace}
			}
			trbMap[key] = trb
		} else {
			// Merge namespace if this is a namespace-scoped CCRN
			if parsed.Type == ccrn.ResourceTypeNamespace && parsed.Namespace != "" {
				if !slices.Contains(existing.Namespaces, parsed.Namespace) {
					existing.Namespaces = append(existing.Namespaces, parsed.Namespace)
				}
			}
		}
	}

	// Collect TRBs from map
	for _, trb := range trbMap {
		state.TRBs = append(state.TRBs, *trb)
	}

	return state, warnings
}
