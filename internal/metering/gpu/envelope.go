package gpu

import (
	"encoding/json"
	"fmt"
	"time"
)

// Internal types for JSON marshaling -- must match usage.v1 schema exactly.

type usageEnvelope struct {
	SchemaVersion string             `json:"schema_version"`
	RecordID      string             `json:"record_id"`
	OccurredAt    string             `json:"occurred_at"`
	TenantID      string             `json:"tenant_id"`
	Source        envelopeSource     `json:"source"`
	SKU           string             `json:"sku"`
	Geo           string             `json:"geo,omitempty"`
	Quantities    map[string]float64 `json:"quantities"`
	Status        string             `json:"status"`
	Interval      *envelopeInterval  `json:"interval"`
	Attributes    map[string]string  `json:"attributes,omitempty"`
}

type envelopeSource struct {
	Profile          string `json:"profile"`
	Collector        string `json:"collector"`
	CollectorVersion string `json:"collector_version,omitempty"`
}

type envelopeInterval struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Seq   int    `json:"seq"`
}

// BuildEnvelope constructs a usage.v1 JSON envelope from an IntervalEvent.
// This is a pure function: no I/O, no side effects.
func BuildEnvelope(ev IntervalEvent, collectorVersion string) (json.RawMessage, error) {
	envelope := usageEnvelope{
		SchemaVersion: "usage.v1",
		RecordID:      ev.RecordID,
		OccurredAt:    ev.OccurredAt.UTC().Format(time.RFC3339),
		TenantID:      ev.TenantID,
		Source: envelopeSource{
			Profile:          ProfileGPUaaS,
			Collector:        CollectorName,
			CollectorVersion: collectorVersion,
		},
		SKU: ev.SKU,
		Geo: ev.Geo,
		Quantities: map[string]float64{
			"gpu_seconds":    ev.GPUSeconds,
			"slice_fraction": ev.SliceFraction,
		},
		Status: ev.Status,
		Interval: &envelopeInterval{
			Start: ev.IntervalStart.UTC().Format(time.RFC3339),
			End:   ev.IntervalEnd.UTC().Format(time.RFC3339),
			Seq:   ev.IntervalSeq,
		},
	}

	if ev.Queue != "" || ev.WorkloadName != "" {
		envelope.Attributes = make(map[string]string)
		if ev.Queue != "" {
			envelope.Attributes["queue"] = ev.Queue
		}
		if ev.WorkloadName != "" {
			envelope.Attributes["workload"] = ev.WorkloadName
		}
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal usage.v1 envelope: %w", err)
	}
	return json.RawMessage(data), nil
}

// FormatRecordID builds the record_id for a GPUaaS interval event.
func FormatRecordID(workloadUID string, seq int) string {
	return fmt.Sprintf("%s-interval-%04d", workloadUID, seq)
}
