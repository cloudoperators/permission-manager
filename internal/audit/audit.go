// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

// Package audit provides a typed audit logger emitting OTel-conformant JSON
// lines to an io.Writer. It is designed to be thread-safe and does not depend
// on any external packages beyond the Go standard library.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Action represents the type of operation being audited.
type Action string

const (
	// ActionCreate represents a create operation.
	ActionCreate Action = "create"
	// ActionUpdate represents an update operation.
	ActionUpdate Action = "update"
	// ActionDelete represents a delete operation.
	ActionDelete Action = "delete"
)

// Outcome represents the result of an audited operation.
type Outcome string

const (
	// OutcomeSuccess indicates the operation succeeded.
	OutcomeSuccess Outcome = "success"
	// OutcomeFailure indicates the operation failed.
	OutcomeFailure Outcome = "failure"
)

// ResourceRef identifies the Kubernetes resource being acted upon.
type ResourceRef struct {
	// Kind is the Kubernetes resource kind (e.g., ClusterRole, RoleBinding).
	Kind string
	// Name is the resource name.
	Name string
	// Namespace is the resource namespace (empty for cluster-scoped resources).
	Namespace string
	// Cluster is the target cluster name.
	Cluster string
}

// Logger emits structured audit records as JSON lines to a writer.
// It is safe for concurrent use.
type Logger struct {
	mu             sync.Mutex
	w              io.Writer
	serviceName    string
	serviceVersion string
	enabled        bool
}

// New creates a Logger that writes audit records to w.
func New(w io.Writer, serviceName, serviceVersion string) *Logger {
	return &Logger{
		w:              w,
		serviceName:    serviceName,
		serviceVersion: serviceVersion,
		enabled:        true,
	}
}

// Disabled returns a no-op Logger that discards all audit records.
// It is safe for concurrent use and produces zero allocations on the hot path.
func Disabled() *Logger {
	return &Logger{enabled: false}
}

// Log emits a single audit record as a newline-terminated JSON line.
// If the logger is disabled, this is a no-op.
func (l *Logger) Log(_ context.Context, action Action, outcome Outcome, asrName string, ref ResourceRef, err error) {
	if !l.enabled {
		return
	}

	record := newRecord(l.serviceName, l.serviceVersion, action, outcome, asrName, ref, err)

	buf, marshalErr := json.Marshal(record)
	if marshalErr != nil {
		// Best-effort: should never happen with controlled input.
		return
	}
	buf = append(buf, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(buf) //nolint:errcheck // write failures in a logger are unrecoverable
}

// buildBody constructs the human-readable body string.
func buildBody(action Action, ref ResourceRef) string {
	body := fmt.Sprintf("%s %s %s", action, ref.Kind, ref.Name)
	if ref.Namespace != "" {
		body += " in " + ref.Namespace
	}
	if ref.Cluster != "" {
		body += " on cluster " + ref.Cluster
	}
	return body
}

// severityForOutcome maps outcome to OTel severity.
func severityForOutcome(outcome Outcome) (sevNum int, sevText string) {
	if outcome == OutcomeFailure {
		return 17, "WARN"
	}
	return 9, "INFO"
}

// nowFunc is overridable for testing.
var nowFunc = func() time.Time { return time.Now().UTC() }
