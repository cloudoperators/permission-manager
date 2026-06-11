// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestLog_EmitsOneJSONLine(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "pm-controller-kubernetes", "1.0.0")

	logger.Log(context.Background(), ActionCreate, OutcomeSuccess, "my-asr", ResourceRef{
		Kind:      "ClusterRole",
		Name:      "pm-my-asr",
		Namespace: "",
		Cluster:   "prod-eu",
	}, nil)

	output := buf.String()
	if output == "" {
		t.Fatal("expected output, got empty string")
	}

	// Must end with newline
	if output[len(output)-1] != '\n' {
		t.Fatal("output must end with newline")
	}

	// Must be exactly one line
	lines := bytes.Count(buf.Bytes(), []byte("\n"))
	if lines != 1 {
		t.Fatalf("expected exactly 1 line, got %d", lines)
	}

	// Must be valid JSON
	var record map[string]interface{}
	if err := json.Unmarshal([]byte(output), &record); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

func TestLog_AllActionOutcomeCombinations(t *testing.T) {
	// Fix time for deterministic tests
	origNow := nowFunc
	nowFunc = func() time.Time {
		return time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
	}
	t.Cleanup(func() { nowFunc = origNow })

	tests := []struct {
		name        string
		action      Action
		outcome     Outcome
		asrName     string
		ref         ResourceRef
		err         error
		wantSevNum  float64
		wantSevText string
		wantBody    string
		wantErrMsg  string
	}{
		{
			name:    "create/success cluster-scoped",
			action:  ActionCreate,
			outcome: OutcomeSuccess,
			asrName: "my-asr",
			ref: ResourceRef{
				Kind:    "ClusterRole",
				Name:    "pm-my-asr",
				Cluster: "prod-eu",
			},
			err:         nil,
			wantSevNum:  9,
			wantSevText: "INFO",
			wantBody:    "create ClusterRole pm-my-asr on cluster prod-eu",
			wantErrMsg:  "",
		},
		{
			name:    "create/failure with error",
			action:  ActionCreate,
			outcome: OutcomeFailure,
			asrName: "failing-asr",
			ref: ResourceRef{
				Kind:      "RoleBinding",
				Name:      "pm-failing-asr",
				Namespace: "kube-system",
				Cluster:   "dev-us",
			},
			err:         errors.New("permission denied"),
			wantSevNum:  17,
			wantSevText: "WARN",
			wantBody:    "create RoleBinding pm-failing-asr in kube-system on cluster dev-us",
			wantErrMsg:  "permission denied",
		},
		{
			name:    "update/success namespace-scoped",
			action:  ActionUpdate,
			outcome: OutcomeSuccess,
			asrName: "update-asr",
			ref: ResourceRef{
				Kind:      "Role",
				Name:      "pm-update-asr",
				Namespace: "default",
				Cluster:   "staging",
			},
			err:         nil,
			wantSevNum:  9,
			wantSevText: "INFO",
			wantBody:    "update Role pm-update-asr in default on cluster staging",
			wantErrMsg:  "",
		},
		{
			name:    "update/failure",
			action:  ActionUpdate,
			outcome: OutcomeFailure,
			asrName: "update-fail",
			ref: ResourceRef{
				Kind:    "ClusterRoleBinding",
				Name:    "pm-update-fail",
				Cluster: "prod",
			},
			err:         errors.New("conflict"),
			wantSevNum:  17,
			wantSevText: "WARN",
			wantBody:    "update ClusterRoleBinding pm-update-fail on cluster prod",
			wantErrMsg:  "conflict",
		},
		{
			name:    "delete/success",
			action:  ActionDelete,
			outcome: OutcomeSuccess,
			asrName: "delete-asr",
			ref: ResourceRef{
				Kind:      "RoleBinding",
				Name:      "pm-delete-asr",
				Namespace: "app-ns",
				Cluster:   "prod-eu",
			},
			err:         nil,
			wantSevNum:  9,
			wantSevText: "INFO",
			wantBody:    "delete RoleBinding pm-delete-asr in app-ns on cluster prod-eu",
			wantErrMsg:  "",
		},
		{
			name:    "delete/failure",
			action:  ActionDelete,
			outcome: OutcomeFailure,
			asrName: "delete-fail",
			ref: ResourceRef{
				Kind:    "ClusterRole",
				Name:    "pm-delete-fail",
				Cluster: "dev",
			},
			err:         errors.New("not found"),
			wantSevNum:  17,
			wantSevText: "WARN",
			wantBody:    "delete ClusterRole pm-delete-fail on cluster dev",
			wantErrMsg:  "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(&buf, "pm-controller-kubernetes", "1.0.0")

			logger.Log(context.Background(), tt.action, tt.outcome, tt.asrName, tt.ref, tt.err)

			output := buf.Bytes()
			if len(output) == 0 {
				t.Fatal("expected output, got empty")
			}
			if output[len(output)-1] != '\n' {
				t.Fatal("output must end with newline")
			}

			var rec record
			if err := json.Unmarshal(output, &rec); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}

			// Verify Timestamp
			if rec.Timestamp != "2024-06-15T10:30:00Z" {
				t.Errorf("Timestamp = %q, want %q", rec.Timestamp, "2024-06-15T10:30:00Z")
			}

			// Verify Severity
			if rec.SeverityNumber != int(tt.wantSevNum) {
				t.Errorf("SeverityNumber = %d, want %d", rec.SeverityNumber, int(tt.wantSevNum))
			}
			if rec.SeverityText != tt.wantSevText {
				t.Errorf("SeverityText = %q, want %q", rec.SeverityText, tt.wantSevText)
			}

			// Verify Body
			if rec.Body != tt.wantBody {
				t.Errorf("Body = %q, want %q", rec.Body, tt.wantBody)
			}

			// Verify Attributes
			if rec.Attributes.AuditAction != string(tt.action) {
				t.Errorf("Attributes.audit.action = %q, want %q", rec.Attributes.AuditAction, tt.action)
			}
			if rec.Attributes.AuditOutcome != string(tt.outcome) {
				t.Errorf("Attributes.audit.outcome = %q, want %q", rec.Attributes.AuditOutcome, tt.outcome)
			}
			if rec.Attributes.ASRName != tt.asrName {
				t.Errorf("Attributes.asr.name = %q, want %q", rec.Attributes.ASRName, tt.asrName)
			}
			if rec.Attributes.ResourceKind != tt.ref.Kind {
				t.Errorf("Attributes.resource.kind = %q, want %q", rec.Attributes.ResourceKind, tt.ref.Kind)
			}
			if rec.Attributes.ResourceName != tt.ref.Name {
				t.Errorf("Attributes.resource.name = %q, want %q", rec.Attributes.ResourceName, tt.ref.Name)
			}
			if rec.Attributes.ResourceNamespace != tt.ref.Namespace {
				t.Errorf("Attributes.resource.namespace = %q, want %q", rec.Attributes.ResourceNamespace, tt.ref.Namespace)
			}
			if rec.Attributes.ResourceCluster != tt.ref.Cluster {
				t.Errorf("Attributes.resource.cluster = %q, want %q", rec.Attributes.ResourceCluster, tt.ref.Cluster)
			}
			if rec.Attributes.ErrorMessage != tt.wantErrMsg {
				t.Errorf("Attributes.error.message = %q, want %q", rec.Attributes.ErrorMessage, tt.wantErrMsg)
			}

			// Verify Resource
			if rec.Resource.ServiceName != "pm-controller-kubernetes" {
				t.Errorf("Resource.service.name = %q, want %q", rec.Resource.ServiceName, "pm-controller-kubernetes")
			}
			if rec.Resource.ServiceVersion != "1.0.0" {
				t.Errorf("Resource.service.version = %q, want %q", rec.Resource.ServiceVersion, "1.0.0")
			}
		})
	}
}

func TestLog_OTelFieldsPresent(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "my-service", "2.0.0")

	logger.Log(context.Background(), ActionCreate, OutcomeSuccess, "asr1", ResourceRef{
		Kind:    "ClusterRole",
		Name:    "pm-asr1",
		Cluster: "c1",
	}, nil)

	var raw map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	requiredFields := []string{"Timestamp", "SeverityNumber", "SeverityText", "Body", "Attributes", "Resource"}
	for _, field := range requiredFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("missing required OTel field %q", field)
		}
	}

	attrs, ok := raw["Attributes"].(map[string]interface{})
	if !ok {
		t.Fatal("Attributes is not an object")
	}
	requiredAttrs := []string{"audit.action", "audit.outcome", "asr.name", "resource.kind", "resource.name", "resource.namespace", "resource.cluster", "error.message"}
	for _, attr := range requiredAttrs {
		if _, ok := attrs[attr]; !ok {
			t.Errorf("missing required attribute %q", attr)
		}
	}

	resource, ok := raw["Resource"].(map[string]interface{})
	if !ok {
		t.Fatal("Resource is not an object")
	}
	requiredResource := []string{"service.name", "service.version"}
	for _, r := range requiredResource {
		if _, ok := resource[r]; !ok {
			t.Errorf("missing required resource field %q", r)
		}
	}
}

func TestDisabled_NoOutput(t *testing.T) {
	logger := Disabled()

	// Should not panic and should produce no output.
	logger.Log(context.Background(), ActionCreate, OutcomeSuccess, "test", ResourceRef{
		Kind:    "ClusterRole",
		Name:    "pm-test",
		Cluster: "c1",
	}, nil)

	// Disabled logger has nil writer, so there's nothing to check for output.
	// The main assertion is that it doesn't panic.
}

func TestDisabled_ConcurrentSafety(t *testing.T) {
	logger := Disabled()

	done := make(chan struct{})
	for i := 0; i < 100; i++ {
		go func() {
			logger.Log(context.Background(), ActionUpdate, OutcomeFailure, "concurrent", ResourceRef{
				Kind:    "Role",
				Name:    "pm-concurrent",
				Cluster: "c1",
			}, errors.New("test"))
			done <- struct{}{}
		}()
	}
	for i := 0; i < 100; i++ {
		<-done
	}
}

func TestLog_ConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "svc", "1.0.0")

	done := make(chan struct{})
	n := 50
	for i := 0; i < n; i++ {
		go func() {
			logger.Log(context.Background(), ActionCreate, OutcomeSuccess, "asr", ResourceRef{
				Kind:    "ClusterRole",
				Name:    "pm-asr",
				Cluster: "c1",
			}, nil)
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}

	// Should have exactly n lines
	lines := bytes.Count(buf.Bytes(), []byte("\n"))
	if lines != n {
		t.Fatalf("expected %d lines, got %d", n, lines)
	}

	// Each line should be valid JSON
	for i, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var rec map[string]interface{}
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestLog_BodyFormat(t *testing.T) {
	origNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = origNow })

	tests := []struct {
		name     string
		ref      ResourceRef
		action   Action
		wantBody string
	}{
		{
			name:     "cluster only",
			action:   ActionCreate,
			ref:      ResourceRef{Kind: "ClusterRole", Name: "pm-asr", Cluster: "prod"},
			wantBody: "create ClusterRole pm-asr on cluster prod",
		},
		{
			name:     "namespace and cluster",
			action:   ActionUpdate,
			ref:      ResourceRef{Kind: "RoleBinding", Name: "pm-asr", Namespace: "ns1", Cluster: "prod"},
			wantBody: "update RoleBinding pm-asr in ns1 on cluster prod",
		},
		{
			name:     "namespace only (no cluster)",
			action:   ActionDelete,
			ref:      ResourceRef{Kind: "Role", Name: "pm-asr", Namespace: "ns1"},
			wantBody: "delete Role pm-asr in ns1",
		},
		{
			name:     "no namespace no cluster",
			action:   ActionCreate,
			ref:      ResourceRef{Kind: "ClusterRoleBinding", Name: "pm-asr"},
			wantBody: "create ClusterRoleBinding pm-asr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(&buf, "svc", "1.0.0")
			logger.Log(context.Background(), tt.action, OutcomeSuccess, "asr", tt.ref, nil)

			var rec record
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if rec.Body != tt.wantBody {
				t.Errorf("Body = %q, want %q", rec.Body, tt.wantBody)
			}
		})
	}
}

func TestLog_MultipleCallsProduceMultipleLines(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "svc", "1.0.0")

	logger.Log(context.Background(), ActionCreate, OutcomeSuccess, "a", ResourceRef{Kind: "ClusterRole", Name: "pm-a", Cluster: "c"}, nil)
	logger.Log(context.Background(), ActionDelete, OutcomeFailure, "b", ResourceRef{Kind: "Role", Name: "pm-b", Namespace: "ns", Cluster: "c"}, errors.New("err"))

	lines := bytes.Count(buf.Bytes(), []byte("\n"))
	if lines != 2 {
		t.Fatalf("expected 2 lines, got %d", lines)
	}
}
