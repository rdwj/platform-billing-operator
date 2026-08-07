package gpu

import "time"

const (
	// ProfileGPUaaS is the metering profile identifier for GPU-as-a-Service events.
	ProfileGPUaaS = "gpuaas"
	// CollectorName identifies this collector in usage.v1 source.collector.
	CollectorName = "kueue-collector"
	// DefaultInterval is the default metering interval for GPU workloads.
	DefaultInterval = time.Hour
)

// CollectorConfig holds runtime configuration for the GPU metering collector.
type CollectorConfig struct {
	LedgerIngestURL  string
	IntervalDuration time.Duration
	CollectorVersion string
	DefaultGeo       string
	SKUMapping       map[string]SKUInfo
}

// SKUInfo maps a derived SKU key to its PriceBook name and MIG slice fraction.
type SKUInfo struct {
	SKU           string
	SliceFraction float64
}

// WorkloadState tracks the metering state for a single active Kueue Workload.
type WorkloadState struct {
	WorkloadUID     string
	WorkloadName    string
	Namespace       string
	TenantID        string
	SKU             string
	SliceFraction   float64
	Geo             string
	Queue           string
	GPUCount        int64
	AdmittedAt      time.Time
	LastIntervalEnd time.Time
	IntervalSeq     int
}

// IntervalEvent holds the data needed to build a usage.v1 envelope for one interval.
type IntervalEvent struct {
	RecordID      string
	TenantID      string
	SKU           string
	Geo           string
	GPUSeconds    float64
	SliceFraction float64
	IntervalStart time.Time
	IntervalEnd   time.Time
	IntervalSeq   int
	Queue         string
	WorkloadName  string
	Status        string
	Reason        string
	OccurredAt    time.Time
}
