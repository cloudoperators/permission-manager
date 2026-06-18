// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package api

const (
	// GroupName is the API group for permission-manager resources.
	GroupName = "permission-manager.cloudoperators.dev"

	// LabelDomain is the domain prefix for permission-manager labels.
	LabelDomain = GroupName + "/"

	// LabelSourceASR is the label key for the source ASR name on owned resources.
	LabelSourceASR = LabelDomain + "source-asr"

	// LabelSourceNamespace is the label key for the source ASR namespace on owned resources.
	LabelSourceNamespace = LabelDomain + "source-namespace"

	// LabelASR is the label key for the ASR name (used by kubernetes provider).
	LabelASR = LabelDomain + "asr"

	// LabelManagedByKey is the standard Kubernetes managed-by label key.
	LabelManagedByKey = "app.kubernetes.io/managed-by"

	// ManagedByPMController is the managed-by value for the pm-controller.
	ManagedByPMController = "pm-controller"

	// ManagedByGithub is the managed-by value for the github provider.
	ManagedByGithub = "github"

	// ASRConnectorRefsField is the field indexer path for ASR.spec.connectorRefs.
	ASRConnectorRefsField = ".spec.connectorRefs"
)
