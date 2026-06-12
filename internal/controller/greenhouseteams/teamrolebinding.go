// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package greenhouseteams

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	greenhousev1alpha2 "github.com/cloudoperators/greenhouse/api/v1alpha2"

	"github.com/cloudoperators/permission-manager/pkg/audit"
)

// ReconcileTRB creates or updates a TeamRoleBinding CR in the given target namespace.
func ReconcileTRB(
	ctx context.Context,
	c client.Client,
	state *DesiredState,
	trb *DesiredTRB,
	targetNamespace string,
	asrName string,
	asrNamespace string,
	al *audit.Logger,
) error {
	trbObj := &greenhousev1alpha2.TeamRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      trb.Name,
			Namespace: targetNamespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, c, trbObj, func() error {
		// Set labels
		if trbObj.Labels == nil {
			trbObj.Labels = make(map[string]string)
		}
		for k, v := range managedLabels(asrName, asrNamespace) {
			trbObj.Labels[k] = v
		}

		// Set spec
		trbObj.Spec.TeamRoleRef = trb.TeamRole
		trbObj.Spec.TeamRefs = []string{state.TeamName}
		trbObj.Spec.ClusterSelector = greenhousev1alpha2.ClusterSelector{
			Name: trb.ClusterName,
		}
		if len(trb.Namespaces) > 0 {
			trbObj.Spec.Namespaces = trb.Namespaces
		} else {
			trbObj.Spec.Namespaces = nil
		}

		return nil
	})

	ref := audit.ResourceRef{
		Kind:      "TeamRoleBinding",
		Name:      trb.Name,
		Namespace: targetNamespace,
		Cluster:   "",
	}

	if err != nil {
		al.Log(ctx, opToAction(op), audit.OutcomeFailure, asrName, ref, err)
		return fmt.Errorf("failed to reconcile TRB %s/%s: %w", targetNamespace, trb.Name, err)
	}

	if op != controllerutil.OperationResultNone {
		al.Log(ctx, opToAction(op), audit.OutcomeSuccess, asrName, ref, nil)
	}

	return nil
}

// ReconcileAllTRBs reconciles all desired TRBs and removes stale ones.
func ReconcileAllTRBs(
	ctx context.Context,
	c client.Client,
	state *DesiredState,
	targetNamespace string,
	asrName string,
	asrNamespace string,
	al *audit.Logger,
) error {
	// Create/update desired TRBs
	for i := range state.TRBs {
		if err := ReconcileTRB(ctx, c, state, &state.TRBs[i], targetNamespace, asrName, asrNamespace, al); err != nil {
			return err
		}
	}

	// Remove stale TRBs
	return DeleteStaleTRBs(ctx, c, state, asrName, asrNamespace, targetNamespace, al)
}

// DeleteStaleTRBs lists existing managed TRBs and deletes any not in the desired state.
func DeleteStaleTRBs(
	ctx context.Context,
	c client.Client,
	state *DesiredState,
	asrName string,
	asrNamespace string,
	targetNamespace string,
	al *audit.Logger,
) error {
	existing, err := ListManagedTRBs(ctx, c, asrName, asrNamespace, targetNamespace)
	if err != nil {
		return err
	}

	// Build set of desired names
	desiredNames := make(map[string]struct{}, len(state.TRBs))
	for _, trb := range state.TRBs {
		desiredNames[trb.Name] = struct{}{}
	}

	// Delete stale
	for i := range existing {
		if _, wanted := desiredNames[existing[i].Name]; !wanted {
			if err := c.Delete(ctx, &existing[i]); err != nil {
				if client.IgnoreNotFound(err) != nil {
					return fmt.Errorf("failed to delete stale TRB %s/%s: %w", targetNamespace, existing[i].Name, err)
				}
			} else {
				al.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, audit.ResourceRef{
					Kind:      "TeamRoleBinding",
					Name:      existing[i].Name,
					Namespace: targetNamespace,
					Cluster:   "",
				}, nil)
			}
		}
	}
	return nil
}

// ListManagedTRBs lists all TeamRoleBinding CRs managed by this reconciler for the given ASR.
func ListManagedTRBs(ctx context.Context, c client.Client, asrName, asrNamespace, targetNamespace string) ([]greenhousev1alpha2.TeamRoleBinding, error) {
	var trbList greenhousev1alpha2.TeamRoleBindingList
	if err := c.List(ctx, &trbList,
		client.InNamespace(targetNamespace),
		client.MatchingLabels{
			LabelManagedBy:       LabelManagedByValue,
			LabelSourceASR:       asrName,
			LabelSourceNamespace: asrNamespace,
		},
	); err != nil {
		return nil, fmt.Errorf("failed to list managed TRBs in %s: %w", targetNamespace, err)
	}
	return trbList.Items, nil
}

// DeleteAllManagedTRBs deletes all managed TRBs for the given ASR in a target namespace.
func DeleteAllManagedTRBs(ctx context.Context, c client.Client, asrName, asrNamespace, targetNamespace string, al *audit.Logger) error {
	existing, err := ListManagedTRBs(ctx, c, asrName, asrNamespace, targetNamespace)
	if err != nil {
		return err
	}
	for i := range existing {
		if err := c.Delete(ctx, &existing[i]); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("failed to delete TRB %s/%s: %w", targetNamespace, existing[i].Name, err)
			}
		} else {
			al.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, audit.ResourceRef{
				Kind:      "TeamRoleBinding",
				Name:      existing[i].Name,
				Namespace: targetNamespace,
				Cluster:   "",
			}, nil)
		}
	}
	return nil
}
