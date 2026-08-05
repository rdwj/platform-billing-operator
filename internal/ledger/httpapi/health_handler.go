package httpapi

import (
	"net/http"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"golang.org/x/sync/errgroup"
)

type healthHandler struct {
	store *ledger.Store
}

type healthResponse struct {
	DLQDepth      int64     `json:"dlqDepth"`
	Watermark     time.Time `json:"watermark"`
	DriftExceeded int64     `json:"driftExceeded"`
	IngestRate    int64     `json:"ingestRate"`
}

func (h *healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	var resp healthResponse
	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		depth, err := h.store.QueryDLQDepth(gCtx)
		if err != nil {
			return err
		}
		resp.DLQDepth = depth
		return nil
	})
	g.Go(func() error {
		ts, err := h.store.QueryWatermark(gCtx)
		if err != nil {
			return err
		}
		resp.Watermark = ts
		return nil
	})
	g.Go(func() error {
		count, err := h.store.QueryDriftExceededCount(gCtx)
		if err != nil {
			return err
		}
		resp.DriftExceeded = count
		return nil
	})
	g.Go(func() error {
		rate, err := h.store.QueryIngestEventRate(gCtx)
		if err != nil {
			return err
		}
		resp.IngestRate = rate
		return nil
	})

	if err := g.Wait(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
