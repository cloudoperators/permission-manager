// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"maps"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ProviderConfig holds the provider feature-gate state loaded from pm-provider-cm.
type ProviderConfig struct {
	data map[string]string
}

// Enabled returns true if the provider key is "true" or absent (fail-open).
func (c *ProviderConfig) Enabled(name string) bool {
	v, ok := c.data[name]
	if !ok {
		return true
	}
	return v == "true"
}

// NewProviderConfig loads the provider config ConfigMap from the given namespace.
// If the ConfigMap is not found, it returns a fail-open config and logs a warning.
func NewProviderConfig(ctx context.Context, c client.Reader, name, namespace string) (*ProviderConfig, error) {
	log := ctrl.LoggerFrom(ctx)
	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cm); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.Info("provider config not found, defaulting all providers enabled", "configmap", name)
			return &ProviderConfig{data: map[string]string{}}, nil
		}
		return nil, err
	}
	data := make(map[string]string, len(cm.Data))
	maps.Copy(data, cm.Data)
	return &ProviderConfig{data: data}, nil
}

// ReloadProviderConfig reloads the provider config ConfigMap and returns a fresh ProviderConfig.
func ReloadProviderConfig(ctx context.Context, c client.Reader, name, namespace string) (*ProviderConfig, error) {
	return NewProviderConfig(ctx, c, name, namespace)
}
