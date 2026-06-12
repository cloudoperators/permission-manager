// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package cbindex

import (
	"context"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cloudoperators/permission-manager/api/v1alpha1"
)

// EnqueueASRsForConnectorBinding returns an event handler that:
//  1. Maintains the ConnectorBinding index (side-effect): on each event, it updates
//     or removes the CB from the index.
//  2. Enqueues ASRs that reference the changed ConnectorBinding, triggering their
//     reconciliation.
//
// The handler only enqueues ASRs for create/update events if the CB matches the
// provider. For delete events it always enqueues referencing ASRs (so they can
// clean up stale RBAC).
func EnqueueASRsForConnectorBinding[T any](idx *Index[T], c client.Reader) handler.EventHandler {
	return &cbEventHandler[T]{idx: idx, reader: c}
}

// cbEventHandler implements handler.EventHandler for ConnectorBinding events.
type cbEventHandler[T any] struct {
	idx    *Index[T]
	reader client.Reader
}

func (h *cbEventHandler[T]) Create(ctx context.Context, e event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.handle(ctx, e.Object, false, q)
}

func (h *cbEventHandler[T]) Update(ctx context.Context, e event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.handle(ctx, e.ObjectNew, false, q)
}

func (h *cbEventHandler[T]) Delete(ctx context.Context, e event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.handle(ctx, e.Object, true, q)
}

func (h *cbEventHandler[T]) Generic(ctx context.Context, e event.TypedGenericEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.handle(ctx, e.Object, false, q)
}

func (h *cbEventHandler[T]) handle(ctx context.Context, obj client.Object, isDelete bool, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	cb, ok := obj.(*v1alpha1.ConnectorBinding)
	if !ok {
		return
	}

	// Update or remove from the index
	if isDelete {
		h.idx.Remove(cb.Name)
	} else {
		h.idx.Update(cb)
		// Only enqueue ASRs on create/update if this is our provider's ConnectorBinding
		if cb.Spec.Provider != h.idx.Provider() {
			return
		}
	}

	// Find ASRs that reference this ConnectorBinding
	asrList := &v1alpha1.ApplicationSpecificRoleList{}
	if err := h.reader.List(ctx, asrList); err != nil {
		h.idx.log.Error(err, "failed to list ASRs for ConnectorBinding change")
		return
	}

	for _, asr := range asrList.Items {
		for _, ref := range asr.Spec.ConnectorRefs {
			if ref == cb.Name {
				q.Add(reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(&asr),
				})
				break
			}
		}
	}
}
