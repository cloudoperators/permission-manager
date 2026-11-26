// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pmapis "github.com/cloudoperators/permission-manager/api"
	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// SetupIndexes registers every field index the controllers rely on. It is called once from main,
// before the controllers start, because an index must be registered on the shared cache exactly
// once regardless of how many controllers query it.
func SetupIndexes(mgr ctrl.Manager) error {
	ctx := context.Background()
	idx := mgr.GetFieldIndexer()

	if err := idx.IndexField(ctx, &v1alpha1.ApplicationSpecificRole{}, pmapis.ASRConnectorRefsField,
		func(obj client.Object) []string {
			asr, ok := obj.(*v1alpha1.ApplicationSpecificRole)
			if !ok {
				return nil
			}
			return asr.Spec.ConnectorRefs
		}); err != nil {
		return err
	}

	if err := idx.IndexField(ctx, &v1alpha1.ServiceIdentity{}, pmapis.ServiceIdentityRolesField,
		func(obj client.Object) []string {
			si, ok := obj.(*v1alpha1.ServiceIdentity)
			if !ok {
				return nil
			}
			return si.Spec.Roles
		}); err != nil {
		return err
	}

	if err := idx.IndexField(ctx, &v1alpha1.ServiceIdentity{}, pmapis.ServiceIdentityConnectorRefsField,
		func(obj client.Object) []string {
			si, ok := obj.(*v1alpha1.ServiceIdentity)
			if !ok {
				return nil
			}
			return si.Spec.ConnectorRefs
		}); err != nil {
		return err
	}

	return nil
}
