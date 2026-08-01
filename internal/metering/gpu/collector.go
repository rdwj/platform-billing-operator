package gpu

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// RBAC markers for the GPU collector.
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=workloads,verbs=get;list;watch
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=workloads/status,verbs=get
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=resourceflavors,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// Collector is a controller-runtime Reconciler that watches Kueue Workload CRs
// and emits GPU usage events for billing. It tracks workload lifecycle transitions
// (admitted, evicted, finished) and emits interval-based usage records.
type Collector struct {
	client.Client
	Log    logr.Logger
	Config CollectorConfig
	Sender EventSender

	mu        sync.Mutex
	workloads map[string]*WorkloadState // keyed by Workload UID
}

// NewCollector creates a GPU metering collector.
func NewCollector(c client.Client, log logr.Logger, config CollectorConfig, sender EventSender) *Collector {
	return &Collector{
		Client:    c,
		Log:       log,
		Config:    config,
		Sender:    sender,
		workloads: make(map[string]*WorkloadState),
	}
}

// SetupWithManager registers the collector as a controller watching Kueue Workloads.
func (c *Collector) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueuev1beta1.Workload{}).
		Named("gpu-collector").
		Complete(c)
}

// Reconcile handles Workload lifecycle transitions: admission starts tracking,
// finish/eviction emits a final interval, and deletion cleans up state.
func (c *Collector) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := c.Log.WithValues("workload", req.NamespacedName)

	var wl kueuev1beta1.Workload
	if err := c.Get(ctx, req.NamespacedName, &wl); err != nil {
		// Workload deleted: emit final interval for any tracked state.
		c.mu.Lock()
		defer c.mu.Unlock()
		for uid, ws := range c.workloads {
			if ws.WorkloadName == req.Name && ws.Namespace == req.Namespace {
				c.emitFinalInterval(ctx, log, ws, time.Now())
				delete(c.workloads, uid)
				return ctrl.Result{}, nil
			}
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	uid := string(wl.UID)

	// Finished takes priority: emit final interval and stop tracking.
	finishedCond := meta.FindStatusCondition(wl.Status.Conditions, "Finished")
	if finishedCond != nil && finishedCond.Status == metav1.ConditionTrue {
		c.mu.Lock()
		defer c.mu.Unlock()
		if ws, tracking := c.workloads[uid]; tracking {
			c.emitFinalInterval(ctx, log, ws, finishedCond.LastTransitionTime.Time)
			delete(c.workloads, uid)
		}
		return ctrl.Result{}, nil
	}

	// Evicted: emit final interval and stop tracking.
	evictedCond := meta.FindStatusCondition(wl.Status.Conditions, "Evicted")
	if evictedCond != nil && evictedCond.Status == metav1.ConditionTrue {
		c.mu.Lock()
		defer c.mu.Unlock()
		if ws, tracking := c.workloads[uid]; tracking {
			c.emitFinalInterval(ctx, log, ws, evictedCond.LastTransitionTime.Time)
			delete(c.workloads, uid)
		}
		return ctrl.Result{}, nil
	}

	// Not yet admitted: nothing to do.
	if wl.Status.Admission == nil {
		return ctrl.Result{}, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, tracking := c.workloads[uid]; tracking {
		return ctrl.Result{}, nil // already tracking
	}

	// New admission: resolve tenant, SKU, and start tracking.
	ws, err := c.initWorkloadState(ctx, &wl)
	if err != nil {
		log.Error(err, "failed to initialize workload state")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	c.workloads[uid] = ws
	log.Info("tracking new GPU workload",
		"tenant", ws.TenantID, "sku", ws.SKU, "gpus", ws.GPUCount)

	return ctrl.Result{}, nil
}

// initWorkloadState resolves tenant, SKU, and builds a WorkloadState for a newly admitted Workload.
func (c *Collector) initWorkloadState(ctx context.Context, wl *kueuev1beta1.Workload) (*WorkloadState, error) {
	// Resolve tenant from namespace annotation.
	var ns corev1.Namespace
	if err := c.Get(ctx, types.NamespacedName{Name: wl.Namespace}, &ns); err != nil {
		return nil, fmt.Errorf("get namespace %s: %w", wl.Namespace, err)
	}
	tenantID := ns.Annotations["billing.opendatahub.io/tenant-id"]
	if tenantID == "" {
		return nil, fmt.Errorf("namespace %s missing billing.opendatahub.io/tenant-id annotation", wl.Namespace)
	}

	// Resolve SKU from the ResourceFlavor's nodeLabels.
	sku, sliceFraction, gpuCount, err := c.resolveSKU(ctx, wl)
	if err != nil {
		return nil, fmt.Errorf("resolve SKU: %w", err)
	}

	// Use the Admitted condition's timestamp as the admission time.
	admittedAt := time.Now()
	if admittedCond := meta.FindStatusCondition(wl.Status.Conditions, "Admitted"); admittedCond != nil {
		admittedAt = admittedCond.LastTransitionTime.Time
	}

	return &WorkloadState{
		WorkloadUID:     string(wl.UID),
		WorkloadName:    wl.Name,
		Namespace:       wl.Namespace,
		TenantID:        tenantID,
		SKU:             sku,
		SliceFraction:   sliceFraction,
		Geo:             c.Config.DefaultGeo,
		Queue:           wl.Spec.QueueName,
		GPUCount:        gpuCount,
		AdmittedAt:      admittedAt,
		LastIntervalEnd: admittedAt,
		IntervalSeq:     0,
	}, nil
}

// resolveSKU gets the SKU from the ResourceFlavor assigned to nvidia.com/gpu.
func (c *Collector) resolveSKU(ctx context.Context, wl *kueuev1beta1.Workload) (string, float64, int64, error) {
	if wl.Status.Admission == nil {
		return "", 0, 0, fmt.Errorf("workload not admitted")
	}

	// Find the ResourceFlavor assigned for nvidia.com/gpu.
	var flavorName string
	for _, psa := range wl.Status.Admission.PodSetAssignments {
		for resName, flavor := range psa.Flavors {
			if resName == "nvidia.com/gpu" {
				flavorName = string(flavor)
				break
			}
		}
		if flavorName != "" {
			break
		}
	}

	if flavorName == "" {
		// No GPU flavor assigned. Fall back to the first flavor for dev/test.
		if len(wl.Status.Admission.PodSetAssignments) > 0 {
			for _, flavor := range wl.Status.Admission.PodSetAssignments[0].Flavors {
				flavorName = string(flavor)
				break
			}
		}
		if flavorName == "" {
			return "", 0, 0, fmt.Errorf("no ResourceFlavor assigned to workload")
		}
	}

	// Look up the ResourceFlavor CR for its nodeLabels.
	var rf kueuev1beta1.ResourceFlavor
	if err := c.Get(ctx, types.NamespacedName{Name: flavorName}, &rf); err != nil {
		return "", 0, 0, fmt.Errorf("get ResourceFlavor %s: %w", flavorName, err)
	}

	sku, sliceFraction, err := DeriveWorkloadSKU(rf.Spec.NodeLabels, c.Config.SKUMapping)
	if err != nil {
		return "", 0, 0, err
	}

	gpuCount, err := ParseGPUCount(rf.Spec.NodeLabels)
	if err != nil {
		return "", 0, 0, err
	}

	return sku, sliceFraction, gpuCount, nil
}

// emitFinalInterval emits the last usage event covering time since the previous interval boundary.
func (c *Collector) emitFinalInterval(ctx context.Context, log logr.Logger, ws *WorkloadState, endTime time.Time) {
	if !endTime.After(ws.LastIntervalEnd) {
		return
	}

	duration := endTime.Sub(ws.LastIntervalEnd)
	gpuSeconds := duration.Seconds() * float64(ws.GPUCount)

	ev := IntervalEvent{
		RecordID:      FormatRecordID(ws.WorkloadUID, ws.IntervalSeq),
		TenantID:      ws.TenantID,
		SKU:           ws.SKU,
		Geo:           ws.Geo,
		GPUSeconds:    gpuSeconds,
		SliceFraction: ws.SliceFraction,
		IntervalStart: ws.LastIntervalEnd,
		IntervalEnd:   endTime,
		IntervalSeq:   ws.IntervalSeq,
		Queue:         ws.Queue,
		WorkloadName:  ws.WorkloadName,
		Status:        "ok",
		OccurredAt:    endTime,
	}

	envelope, err := BuildEnvelope(ev, c.Config.CollectorVersion)
	if err != nil {
		log.Error(err, "build envelope failed", "record_id", ev.RecordID)
		return
	}

	if err := c.Sender.Send(ctx, envelope); err != nil {
		log.Error(err, "send event failed", "record_id", ev.RecordID)
		return
	}

	log.Info("emitted GPU usage event", "record_id", ev.RecordID,
		"gpu_seconds", gpuSeconds, "interval_seq", ws.IntervalSeq)
}

// StartIntervalTicker runs as a manager.RunnableFunc goroutine. It fires every
// IntervalDuration and emits pending interval events for long-running workloads.
func (c *Collector) StartIntervalTicker(ctx context.Context) error {
	interval := c.Config.IntervalDuration
	if interval == 0 {
		interval = DefaultInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			c.emitPendingIntervals(ctx, now)
		}
	}
}

// emitPendingIntervals walks all tracked workloads and emits interval events
// for any that have been running longer than one interval since their last emission.
func (c *Collector) emitPendingIntervals(ctx context.Context, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	log := c.Log.WithName("interval-ticker")

	for _, ws := range c.workloads {
		interval := c.Config.IntervalDuration
		if interval == 0 {
			interval = DefaultInterval
		}

		for now.Sub(ws.LastIntervalEnd) >= interval {
			intervalEnd := ws.LastIntervalEnd.Add(interval)
			gpuSeconds := interval.Seconds() * float64(ws.GPUCount)

			ev := IntervalEvent{
				RecordID:      FormatRecordID(ws.WorkloadUID, ws.IntervalSeq),
				TenantID:      ws.TenantID,
				SKU:           ws.SKU,
				Geo:           ws.Geo,
				GPUSeconds:    gpuSeconds,
				SliceFraction: ws.SliceFraction,
				IntervalStart: ws.LastIntervalEnd,
				IntervalEnd:   intervalEnd,
				IntervalSeq:   ws.IntervalSeq,
				Queue:         ws.Queue,
				WorkloadName:  ws.WorkloadName,
				Status:        "ok",
				OccurredAt:    intervalEnd,
			}

			envelope, err := BuildEnvelope(ev, c.Config.CollectorVersion)
			if err != nil {
				log.Error(err, "build envelope failed", "record_id", ev.RecordID)
				break
			}

			if err := c.Sender.Send(ctx, envelope); err != nil {
				log.Error(err, "send event failed", "record_id", ev.RecordID)
				break
			}

			log.Info("emitted interval event", "record_id", ev.RecordID,
				"gpu_seconds", gpuSeconds, "interval_seq", ws.IntervalSeq)

			ws.LastIntervalEnd = intervalEnd
			ws.IntervalSeq++
		}
	}
}

// ActiveWorkloadCount returns the number of workloads currently being tracked.
func (c *Collector) ActiveWorkloadCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.workloads)
}
