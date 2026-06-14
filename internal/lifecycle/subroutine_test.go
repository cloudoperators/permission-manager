// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"
)

func constPhase(r Result, err error) SubRoutine {
	return func(context.Context) (Result, error) { return r, err }
}

func TestExecuteSubRoutine(t *testing.T) {
	ctx := context.Background()
	errBoom := errors.New("boom")

	tests := []struct {
		name        string
		phases      []SubRoutine
		wantResult  ReconcileResult
		wantErr     bool
		wantRequeue time.Duration
	}{
		{
			name:       "all continue -> success",
			phases:     []SubRoutine{constPhase(Continue(), nil), constPhase(Continue(), nil)},
			wantResult: Success,
		},
		{
			name:       "cancel stops with success and short-circuits later phases",
			phases:     []SubRoutine{constPhase(Cancel(), nil), constPhase(Break(), errBoom)},
			wantResult: Success,
		},
		{
			name:       "ignore stops without error and short-circuits",
			phases:     []SubRoutine{constPhase(Ignored(), nil), constPhase(Break(), errBoom)},
			wantResult: Ignore,
		},
		{
			name:       "break stops with failure",
			phases:     []SubRoutine{constPhase(Break(), errBoom)},
			wantResult: Failed,
			wantErr:    true,
		},
		{
			name:       "returned error stops with failure",
			phases:     []SubRoutine{constPhase(Continue(), errBoom)},
			wantResult: Failed,
			wantErr:    true,
		},
		{
			name:        "requeue stops as pending and carries RequeueAfter",
			phases:      []SubRoutine{constPhase(RequeueAfter(2*time.Second), nil)},
			wantResult:  Pending,
			wantRequeue: 2 * time.Second,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, rr, err := ExecuteSubRoutine(ctx, tc.phases)
			if rr != tc.wantResult {
				t.Errorf("ReconcileResult = %q, want %q", rr, tc.wantResult)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if res.RequeueAfter != tc.wantRequeue {
				t.Errorf("RequeueAfter = %v, want %v", res.RequeueAfter, tc.wantRequeue)
			}
		})
	}
}
