package greenhouseteams

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/cloudoperators/permission-manager/controller/pkg/audit"
	greenhouse "github.com/cloudoperators/permission-manager/controller/pkg/greenhouse"
	"github.com/cloudoperators/permission-manager/controller/pkg/naming"
)

const (
	// LabelManagedBy is the standard managed-by label key (app.kubernetes.io/managed-by).
	LabelManagedBy = naming.LabelManagedByKey
	// LabelManagedByValue is the managed-by value for the greenhouse-teams provider.
	LabelManagedByValue = naming.ManagedByGreenhouseTeams
	// LabelSourceASR is the fully-qualified label key for the source ASR name.
	LabelSourceASR = naming.LabelSourceASR
	// LabelSourceNamespace is the fully-qualified label key for the source ASR namespace.
	LabelSourceNamespace = naming.LabelSourceNamespace
)

// managedLabels returns the labels applied to all managed resources.
func managedLabels(asrName, asrNamespace string) map[string]string {
	return map[string]string{
		LabelManagedBy:       LabelManagedByValue,
		LabelSourceASR:       asrName,
		LabelSourceNamespace: asrNamespace,
	}
}

// opToAction maps a controllerutil.OperationResult to an audit.Action.
func opToAction(op controllerutil.OperationResult) audit.Action {
	switch op {
	case controllerutil.OperationResultCreated:
		return audit.ActionCreate
	default:
		return audit.ActionUpdate
	}
}

// ReconcileTeam creates or updates a Team CR in the given target namespace.
// It uses CreateOrUpdate to be idempotent.
func ReconcileTeam(
	ctx context.Context,
	c client.Client,
	state *DesiredState,
	targetNamespace string,
	asrName string,
	asrNamespace string,
	al *audit.Logger,
) error {
	team := &greenhouse.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name:      state.TeamName,
			Namespace: targetNamespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, c, team, func() error {
		// Set labels
		if team.Labels == nil {
			team.Labels = make(map[string]string)
		}
		for k, v := range managedLabels(asrName, asrNamespace) {
			team.Labels[k] = v
		}

		// Set spec
		team.Spec.MappedIDPGroup = state.MappedIDPGroup

		return nil
	})

	ref := audit.ResourceRef{
		Kind:      "Team",
		Name:      state.TeamName,
		Namespace: targetNamespace,
		Cluster:   "",
	}

	if err != nil {
		al.Log(ctx, opToAction(op), audit.OutcomeFailure, asrName, ref, err)
		return fmt.Errorf("failed to reconcile Team %s/%s: %w", targetNamespace, state.TeamName, err)
	}

	if op != controllerutil.OperationResultNone {
		al.Log(ctx, opToAction(op), audit.OutcomeSuccess, asrName, ref, nil)
	}

	return nil
}

// ListManagedTeams lists all Team CRs managed by this reconciler for the given ASR.
func ListManagedTeams(ctx context.Context, c client.Client, asrName, asrNamespace, targetNamespace string) ([]greenhouse.Team, error) {
	var teamList greenhouse.TeamList
	if err := c.List(ctx, &teamList,
		client.InNamespace(targetNamespace),
		client.MatchingLabels{
			LabelManagedBy:       LabelManagedByValue,
			LabelSourceASR:       asrName,
			LabelSourceNamespace: asrNamespace,
		},
	); err != nil {
		return nil, fmt.Errorf("failed to list managed Teams in %s: %w", targetNamespace, err)
	}
	return teamList.Items, nil
}
