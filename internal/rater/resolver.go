package rater

import (
	"context"
	"fmt"
	"sync"
	"time"

	v1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const cacheTTL = 30 * time.Second

type cachedEntry struct {
	books     []v1alpha1.PriceBook
	fetchedAt time.Time
}

// K8sResolver implements job.PriceBookResolver by listing PriceBook CRs from
// the K8s API. It caches the list with a 30s TTL to avoid per-event API calls.
type K8sResolver struct {
	client client.Reader
	mu     sync.RWMutex
	cache  *cachedEntry
}

func NewK8sResolver(c client.Reader) *K8sResolver {
	return &K8sResolver{client: c}
}

// Resolve finds the active PriceBook whose SKUs include the requested SKU.
// The eventTime parameter is reserved for future time-travel pricing; currently
// only state=Active PriceBooks are considered.
func (r *K8sResolver) Resolve(ctx context.Context, sku string, _ time.Time) (PriceBook, error) {
	books, err := r.listPriceBooks(ctx)
	if err != nil {
		return PriceBook{}, fmt.Errorf("list pricebooks: %w", err)
	}

	for _, pb := range books {
		if pb.Status.State != "Active" {
			continue
		}
		for _, s := range pb.Spec.SKUs {
			if s.SKUName == sku {
				return PriceBookSpecToRaterPriceBook(pb.Name, int(pb.Generation), pb.Spec)
			}
		}
	}

	return PriceBook{}, fmt.Errorf("no active PriceBook found for SKU %q", sku)
}

func (r *K8sResolver) listPriceBooks(ctx context.Context) ([]v1alpha1.PriceBook, error) {
	r.mu.RLock()
	if r.cache != nil && time.Since(r.cache.fetchedAt) < cacheTTL {
		books := r.cache.books
		r.mu.RUnlock()
		return books, nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock.
	if r.cache != nil && time.Since(r.cache.fetchedAt) < cacheTTL {
		return r.cache.books, nil
	}

	var list v1alpha1.PriceBookList
	if err := r.client.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("list PriceBook CRs: %w", err)
	}

	r.cache = &cachedEntry{
		books:     list.Items,
		fetchedAt: time.Now(),
	}
	return list.Items, nil
}
