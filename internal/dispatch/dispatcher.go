/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/controller"
	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/rdwj/platform-billing-operator/internal/usagesink"
)

const maxRetries = 3

// Config controls the dispatcher's polling and batching behavior.
type Config struct {
	BatchSize     int
	FlushInterval time.Duration
}

func (c Config) withDefaults() Config {
	if c.BatchSize <= 0 {
		c.BatchSize = 100
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 5 * time.Second
	}
	return c
}

// SinkDispatcher is a manager.Runnable that polls the ledger store for new
// usage events and forwards them to all registered usage sinks.
type SinkDispatcher struct {
	store    *ledger.Store
	registry *controller.SinkRegistry
	config   Config
	log      *slog.Logger
}

// New creates a SinkDispatcher.
func New(store *ledger.Store, registry *controller.SinkRegistry, cfg Config, log *slog.Logger) *SinkDispatcher {
	return &SinkDispatcher{
		store:    store,
		registry: registry,
		config:   cfg.withDefaults(),
		log:      log,
	}
}

// Start implements manager.Runnable. It polls for new events and forwards
// them to all registered sinks until ctx is cancelled.
func (d *SinkDispatcher) Start(ctx context.Context) error {
	d.log.Info("sink dispatcher starting",
		"batchSize", d.config.BatchSize,
		"flushInterval", d.config.FlushInterval)

	ticker := time.NewTicker(d.config.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			d.log.Info("sink dispatcher stopping")
			return nil
		case <-ticker.C:
			if err := d.tick(ctx); err != nil {
				d.log.Error("dispatch tick failed", "error", err)
			}
		}
	}
}

// tick runs one dispatch cycle: poll for new events and forward to all sinks.
func (d *SinkDispatcher) tick(ctx context.Context) error {
	if d.registry.Len() == 0 {
		return nil
	}

	cursorTS, cursorRecordID, err := d.store.GetDispatchCursor(ctx)
	if err != nil {
		return fmt.Errorf("read cursor: %w", err)
	}

	pageToken := ""
	if !cursorTS.IsZero() && cursorRecordID != "" {
		pageToken = fmt.Sprintf("%s|%s", cursorTS.Format(time.RFC3339Nano), cursorRecordID)
	}

	for {
		filter := ledger.QueryFilter{
			Limit:     int32(d.config.BatchSize),
			PageToken: pageToken,
		}
		if !cursorTS.IsZero() && pageToken == "" {
			filter.StartTime = cursorTS
		}

		result, err := d.store.QueryEvents(ctx, filter)
		if err != nil {
			return fmt.Errorf("query events: %w", err)
		}

		if len(result.Events) == 0 {
			return nil
		}

		sinkEvents := usagesink.ConvertLedgerEvents(result.Events)

		if err := d.fanOut(ctx, sinkEvents); err != nil {
			return fmt.Errorf("fan out: %w", err)
		}

		last := result.Events[len(result.Events)-1]
		if err := d.store.SetDispatchCursor(ctx, last.EventTS, last.RecordID); err != nil {
			return fmt.Errorf("advance cursor: %w", err)
		}

		d.log.Info("dispatched events",
			"count", len(result.Events),
			"lastRecordID", last.RecordID,
			"lastEventTS", last.EventTS)

		if result.NextPageToken == "" {
			return nil
		}
		pageToken = result.NextPageToken
	}
}

// fanOut sends events to all registered sinks. Returns an error if ANY sink
// failed, which prevents the cursor from advancing. This ensures at-least-once
// delivery: on the next tick the full batch is retried, and sinks that already
// succeeded deduplicate on CloudEvents id.
func (d *SinkDispatcher) fanOut(ctx context.Context, events []usagesink.UsageEvent) error {
	var firstErr error

	d.registry.ForEach(func(name string, b usagesink.UsageSinkBackend) {
		if err := d.sendWithRetry(ctx, name, b, events); err != nil {
			d.log.Error("sink ingest failed after retries",
				"sink", name,
				"error", err,
				"events", len(events))
			if firstErr == nil {
				firstErr = fmt.Errorf("sink %s: %w", name, err)
			}
		}
	})

	return firstErr
}

func (d *SinkDispatcher) sendWithRetry(ctx context.Context, name string, b usagesink.UsageSinkBackend, events []usagesink.UsageEvent) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*attempt) * 500 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			d.log.Warn("retrying sink ingest",
				"sink", name,
				"attempt", attempt+1,
				"error", lastErr)
		}

		_, err := b.IngestEvents(ctx, events)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}
