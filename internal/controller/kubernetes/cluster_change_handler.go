// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
	"github.com/cloudoperators/permission-manager/pkg/ccrn"
)

// GenericEvent is the event type for the cluster change channel.
type GenericEvent = event.GenericEvent

// NotifyClusterChange sends a GenericEvent into the provided channel to trigger
// ASR re-reconciliation for the given cluster. Non-blocking: if the channel
// buffer is full, the event is dropped (the cluster will be picked up on the
// next periodic reconcile).
func NotifyClusterChange(ch chan<- event.GenericEvent, clusterName string) {
	evt := event.GenericEvent{
		Object: &v1alpha1.ApplicationSpecificRole{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName},
		},
	}
	select {
	case ch <- evt:
	default:
		// Channel full — drop event. The cluster will be reconciled eventually.
	}
}

// enqueueMatchingASRs lists all ASRs and returns reconcile requests for those
// whose CCRN patterns match the given cluster name. This is used as the
// OnClusterChange callback in ClusterReconciler to re-reconcile ASRs when a
// new cluster appears (or becomes valid).
func enqueueMatchingASRs(ctx context.Context, c client.Reader, log logr.Logger, clusterName string) []reconcile.Request {
	var asrList v1alpha1.ApplicationSpecificRoleList
	if err := c.List(ctx, &asrList); err != nil {
		log.Error(err, "failed to list ASRs for cluster change enqueue")
		return nil
	}

	var requests []reconcile.Request

	for i := range asrList.Items {
		asr := &asrList.Items[i]
		if asrMatchesCluster(asr, clusterName) {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(asr),
			})
		}
	}

	if len(requests) > 0 {
		log.Info("enqueuing ASRs for new cluster", "cluster", clusterName, "count", len(requests))
	}

	return requests
}

// asrMatchesCluster returns true if any permission in the ASR has a CCRN
// wildcard (or exact match) that matches the given cluster name.
func asrMatchesCluster(asr *v1alpha1.ApplicationSpecificRole, clusterName string) bool {
	for _, perm := range asr.Spec.Permissions {
		parsed, err := ccrn.Parse(perm.Resource)
		if err != nil {
			continue
		}
		if !parsed.IsKubernetes() {
			continue
		}
		if ccrn.MatchGlob(parsed.ClusterName, clusterName) {
			return true
		}
	}
	return false
}
