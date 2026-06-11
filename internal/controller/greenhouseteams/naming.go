// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package greenhouseteams

import (
	"fmt"

	"github.com/cloudoperators/permission-manager/pkg/naming"
)

const (
	// TeamNamePrefix is the prefix applied to Team CR names.
	TeamNamePrefix = "pm-"
)

// TeamCRName computes the Team CR name: pm-<teamName>.
// teamName is either advancedConfig.teamName or asr.Spec.Name.
func TeamCRName(teamName string) string {
	name := TeamNamePrefix + teamName
	return naming.TruncateWithHash(name, naming.MaxK8sNameLength)
}

// TRBName computes the TeamRoleBinding CR name: pm-<asrName>-<teamRole>-<clusterName>.
// Truncates at 253 chars with a hash suffix if needed.
func TRBName(asrName, teamRole, clusterName string) string {
	name := fmt.Sprintf("%s%s-%s-%s", TeamNamePrefix, asrName, teamRole, clusterName)
	return naming.TruncateWithHash(name, naming.MaxK8sNameLength)
}
