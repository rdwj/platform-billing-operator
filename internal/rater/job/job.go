// Package job implements the rating job: the I/O wrapper around the pure
// rater.Rate() function. It queries unrated events from the ledger, resolves
// the active PriceBook, rates each event, and writes the charge row. Errors
// are routed to the DLQ.
package job

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/rdwj/platform-billing-operator/internal/rater"
)

// PriceBookResolver finds the active PriceBook for rating. The simplest
// implementation reads PriceBook CRs from the K8s API; a cache wrapper
// can reduce API calls.
type PriceBookResolver interface {
	Resolve(ctx context.Context, sku string, eventTime time.Time) (rater.PriceBook, error)
}

type RatingJob struct {
	store    *ledger.Store
	resolver PriceBookResolver
	log      *slog.Logger
	batch    int
}

func New(store *ledger.Store, resolver PriceBookResolver, log *slog.Logger) *RatingJob {
	return &RatingJob{
		store:    store,
		resolver: resolver,
		log:      log,
		batch:    100,
	}
}

// Tick processes one batch of unrated events. Returns the number of events
// rated and any error that prevented the batch from completing. Individual
// event errors are routed to the DLQ and do not stop the batch.
func (j *RatingJob) Tick(ctx context.Context) (int, error) {
	events, err := j.store.QueryUnratedEvents(ctx, j.batch)
	if err != nil {
		return 0, fmt.Errorf("query unrated events: %w", err)
	}
	if len(events) == 0 {
		return 0, nil
	}

	rated := 0
	now := time.Now().UTC()

	for _, ue := range events {
		pb, err := j.resolver.Resolve(ctx, ue.SKU, now)
		if err != nil {
			j.dlq(ctx, ue, fmt.Sprintf("pricebook_resolve: %v", err))
			continue
		}

		ev := usageEventToRaterEvent(ue)
		charge, err := rater.Rate(ev, pb)
		if err != nil {
			j.dlq(ctx, ue, fmt.Sprintf("rate: %v", err))
			continue
		}

		comps, err := json.Marshal(charge.Components)
		if err != nil {
			j.dlq(ctx, ue, fmt.Sprintf("marshal_components: %v", err))
			continue
		}

		warns := json.RawMessage("[]")
		if len(charge.Warnings) > 0 {
			if w, err := json.Marshal(charge.Warnings); err == nil {
				warns = w
			}
		}

		if err := j.store.InsertCharge(ctx, charge.RecordID, charge.TenantID,
			charge.PriceBook, charge.Revision, charge.Total, charge.Currency,
			comps, warns, now); err != nil {
			return rated, fmt.Errorf("insert charge for %s: %w", charge.RecordID, err)
		}
		rated++
	}

	if rated > 0 {
		j.log.Info("rated events", "count", rated, "batch", len(events))
	}
	return rated, nil
}

func (j *RatingJob) dlq(ctx context.Context, ue ledger.UsageEvent, reason string) {
	raw, _ := json.Marshal(ue)
	if err := j.store.InsertDLQ(ctx, raw, reason, "rater"); err != nil {
		j.log.Error("DLQ insert failed", "record_id", ue.RecordID, "reason", reason, "err", err)
	} else {
		j.log.Warn("event sent to DLQ", "record_id", ue.RecordID, "reason", reason)
	}
}

// usageEventToRaterEvent converts the ledger's UsageEvent (with float64
// quantities) to the rater's Event (with json.Number quantities). Token
// counts are integers stored as float64 in JSON — FormatFloat with 'f'
// format and -1 precision produces the exact decimal representation.
func usageEventToRaterEvent(ue ledger.UsageEvent) rater.Event {
	quantities := make(map[string]json.Number, len(ue.Quantities))
	for k, v := range ue.Quantities {
		quantities[k] = json.Number(strconv.FormatFloat(v, 'f', -1, 64))
	}
	return rater.Event{
		SchemaVersion: ue.SchemaVer,
		RecordID:      ue.RecordID,
		TenantID:      ue.TenantID,
		SKU:           ue.SKU,
		Geo:           ue.Geo,
		Quantities:    quantities,
		Status:        ue.Status,
		Partial:       ue.Partial,
	}
}
