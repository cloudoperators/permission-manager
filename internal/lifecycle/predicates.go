// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	pmconfig "github.com/cloudoperators/permission-manager/internal/config"
)

// ProviderConfigPredicate returns a predicate that passes only update events
// for pm-provider-cm in the given namespace.
func ProviderConfigPredicate(namespace string) predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectNew.GetName() == pmconfig.ProviderConfigName &&
				e.ObjectNew.GetNamespace() == namespace
		},
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}
