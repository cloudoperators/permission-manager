// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package lifecycle implements the reconcile "phases" pattern.
package lifecycle

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type Result struct {
	reconcile.Result
	Pending bool
	Cancel  bool
	Ignore  bool
	Break   bool
}

type SubRoutine func(context.Context) (Result, error)

// ReconcileResult is the outcome of a subroutine chain run by ExecuteSubRoutine.
type ReconcileResult string

const (
	// Success should be returned in case the operator reached its target state.
	Success ReconcileResult = "Success"
	// Ignore should be returned to skip status updates.
	Ignore ReconcileResult = "Ignore"
	// Failed should be returned in case the operator wasn't able to reach its target state and without external changes it's unlikely that this will succeed in the next try.
	Failed ReconcileResult = "Failed"
	// Pending should be returned in case the operator is still trying to reach the target state.
	Pending ReconcileResult = "Pending"
)

func ExecuteSubRoutine(ctx context.Context, subRoutines []SubRoutine) (ctrl.Result, ReconcileResult, error) {
	var result Result
	var err error

	for _, operation := range subRoutines {
		result, err = operation(ctx)
		if result.Ignore {
			return ctrl.Result{}, Ignore, nil
		}
		if result.Cancel {
			return ctrl.Result{}, Success, nil
		}
		if result.Break {
			return ctrl.Result{}, Failed, err
		}
		if result.Pending {
			return ctrl.Result{}, Pending, err
		}
		if result.RequeueAfter > 0 {
			return result.Result, Pending, err
		}
		if err != nil {
			return result.Result, Failed, err
		}
	}

	return result.Result, Success, nil
}

func Requeue() Result {
	return Result{Result: ctrl.Result{RequeueAfter: 5 * time.Second}}
}

func RequeueAfter(duration time.Duration) Result {
	return Result{Result: ctrl.Result{RequeueAfter: duration}}
}

func Cancel() Result {
	return Result{Cancel: true}
}

func Ignored() Result {
	return Result{Ignore: true}
}

func Continue() Result {
	return Result{}
}

func Break() Result {
	return Result{Break: true}
}
