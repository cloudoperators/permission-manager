// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

package audit

// record represents an OTel Logs Data Model conformant audit record.
type record struct {
	Timestamp      string            `json:"Timestamp"`
	SeverityNumber int               `json:"SeverityNumber"`
	SeverityText   string            `json:"SeverityText"`
	Body           string            `json:"Body"`
	Attributes     recordAttributes  `json:"Attributes"`
	Resource       recordResource    `json:"Resource"`
}

// recordAttributes holds the structured audit attributes.
type recordAttributes struct {
	AuditAction       string `json:"audit.action"`
	AuditOutcome      string `json:"audit.outcome"`
	ASRName           string `json:"asr.name"`
	ResourceKind      string `json:"resource.kind"`
	ResourceName      string `json:"resource.name"`
	ResourceNamespace string `json:"resource.namespace"`
	ResourceCluster   string `json:"resource.cluster"`
	ErrorMessage      string `json:"error.message"`
}

// recordResource holds OTel resource attributes.
type recordResource struct {
	ServiceName    string `json:"service.name"`
	ServiceVersion string `json:"service.version"`
}

// newRecord constructs an audit record from the given parameters.
func newRecord(serviceName, serviceVersion string, action Action, outcome Outcome, asrName string, ref ResourceRef, err error) record {
	sevNum, sevText := severityForOutcome(outcome)

	var errMsg string
	if err != nil {
		errMsg = err.Error()
	}

	return record{
		Timestamp:      nowFunc().Format("2006-01-02T15:04:05.999999999Z07:00"),
		SeverityNumber: sevNum,
		SeverityText:   sevText,
		Body:           buildBody(action, ref),
		Attributes: recordAttributes{
			AuditAction:       string(action),
			AuditOutcome:      string(outcome),
			ASRName:           asrName,
			ResourceKind:      ref.Kind,
			ResourceName:      ref.Name,
			ResourceNamespace: ref.Namespace,
			ResourceCluster:   ref.Cluster,
			ErrorMessage:      errMsg,
		},
		Resource: recordResource{
			ServiceName:    serviceName,
			ServiceVersion: serviceVersion,
		},
	}
}
