// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"context"
	"errors"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
)

const msgEventDiscarded = "Event is discarded because no event recorder was found in context"

type lifecycleRunKey struct{}

type lifecycleRun struct {
	eventRecorder events.EventRecorder
	objectCopy    RuntimeObject
}

// CreateContextFromRuntimeObject create a new context with the eventRecorder and a copy of the object attached.
//
// This can be used in conjunction with `lifecycle.GetEventRecorderFromContext` or `lifecycle.GetOrigResourceFromContext` to retrieve the values and work with them.
func createContextFromRuntimeObject(ctx context.Context, object RuntimeObject, recorder events.EventRecorder) context.Context {
	if recorder == nil {
		recorder = dummyEventRecorder{ctrl.LoggerFrom(ctx)}
	}
	return context.WithValue(ctx, lifecycleRunKey{}, &lifecycleRun{
		eventRecorder: recorder,
		objectCopy:    object.DeepCopyObject().(RuntimeObject),
	})
}

func getRunFromContext(ctx context.Context) (*lifecycleRun, error) {
	val, ok := ctx.Value(lifecycleRunKey{}).(*lifecycleRun)
	if !ok {
		return nil, errors.New("could  not extract *LifecycleRun from given context")
	}

	return val, nil
}

type dummyEventRecorder struct {
	logger logr.Logger
}

func (d dummyEventRecorder) Eventf(regarding, related runtime.Object, eventtype, reason, action, note string, args ...interface{}) {
	d.logger.Info(msgEventDiscarded, "type", eventtype, "reason", reason, "action", action, "note", note)
}

func GetEventRecorderFromContext(ctx context.Context) events.EventRecorder {
	lifecycleRun, err := getRunFromContext(ctx)
	if err != nil {
		return dummyEventRecorder{ctrl.LoggerFrom(ctx)}
	}

	return lifecycleRun.eventRecorder
}

// GetOrigResourceFromContext returns the unmodified version of the RuntimeObject
func GetOrigResourceFromContext(ctx context.Context) (RuntimeObject, error) {
	lifecycleRun, err := getRunFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// create another copy so that context can not be modified by accident
	return lifecycleRun.objectCopy.DeepCopyObject().(RuntimeObject), nil
}
