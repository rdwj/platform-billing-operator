package httpapi

import (
	"net/http"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/shopspring/decimal"
)

type usageHandler struct {
	store *ledger.Store
}

type usageSeriesResponse struct {
	Tenant string               `json:"tenant"`
	Bucket string               `json:"bucket"`
	Points []usagePointResponse `json:"points"`
}

type usagePointResponse struct {
	Start      string             `json:"start"`
	SKU        string             `json:"sku"`
	Quantities map[string]float64 `json:"quantities"`
	Charged    string             `json:"charged"`
	Events     int                `json:"events"`
}

func (h *usageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant is required")
		return
	}

	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	if fromStr == "" || toStr == "" {
		writeError(w, http.StatusBadRequest, "from and to are required")
		return
	}

	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "from must be RFC3339")
		return
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "to must be RFC3339")
		return
	}

	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		bucket = "day"
	}
	switch bucket {
	case "hour", "day", "month":
	default:
		writeError(w, http.StatusBadRequest, "bucket must be hour, day, or month")
		return
	}

	sku := r.URL.Query().Get("sku")

	buckets, err := h.store.QueryBucketedUsage(r.Context(), tenant, from, to, bucket, sku)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	points := make([]usagePointResponse, 0, len(buckets))
	for _, b := range buckets {
		charged, _ := decimal.NewFromString(b.Charged)
		points = append(points, usagePointResponse{
			Start:      b.Start.UTC().Format(time.RFC3339),
			SKU:        b.SKU,
			Quantities: b.Quantities,
			Charged:    charged.StringFixed(6),
			Events:     b.Events,
		})
	}

	writeJSON(w, http.StatusOK, usageSeriesResponse{
		Tenant: tenant,
		Bucket: bucket,
		Points: points,
	})
}
