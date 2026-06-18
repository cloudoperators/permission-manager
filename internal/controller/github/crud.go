// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/cloudoperators/permission-manager/pkg/audit"
)

func auditRef(kind string, obj client.Object) audit.ResourceRef {
	return audit.ResourceRef{
		Kind:      kind,
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}
}

// upsertWithAudit creates or updates obj via mutate and emits an audit record for the outcome.
func upsertWithAudit(ctx context.Context, c client.Client, al *audit.Logger, obj client.Object, kind, asrName string, mutate controllerutil.MutateFn) error {
	opResult, err := controllerutil.CreateOrUpdate(ctx, c, obj, mutate)
	if err != nil {
		al.Log(ctx, audit.ActionCreate, audit.OutcomeFailure, asrName, auditRef(kind, obj), err)
		return fmt.Errorf("failed to reconcile %s %q: %w", kind, obj.GetName(), err)
	}
	if opResult == controllerutil.OperationResultNone {
		return nil
	}
	action := audit.ActionUpdate
	if opResult == controllerutil.OperationResultCreated {
		action = audit.ActionCreate
	}
	al.Log(ctx, action, audit.OutcomeSuccess, asrName, auditRef(kind, obj), nil)
	return nil
}

// deleteWithAudit deletes obj, treating not-found as success, and emits an audit record.
func deleteWithAudit(ctx context.Context, c client.Client, al *audit.Logger, obj client.Object, kind, asrName string) error {
	if err := c.Delete(ctx, obj); err != nil {
		if client.IgnoreNotFound(err) != nil {
			al.Log(ctx, audit.ActionDelete, audit.OutcomeFailure, asrName, auditRef(kind, obj), err)
			return fmt.Errorf("failed to delete %s %q: %w", kind, obj.GetName(), err)
		}
		return nil
	}
	al.Log(ctx, audit.ActionDelete, audit.OutcomeSuccess, asrName, auditRef(kind, obj), nil)
	return nil
}
