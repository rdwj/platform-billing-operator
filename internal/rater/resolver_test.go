package rater

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// fakeReader implements client.Reader for testing with an in-memory PriceBook list.
type fakeReader struct {
	items []v1alpha1.PriceBook
}

func (f *fakeReader) Get(_ context.Context, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
	return nil
}

func (f *fakeReader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	if pbl, ok := list.(*v1alpha1.PriceBookList); ok {
		pbl.Items = f.items
	}
	return nil
}

func TestK8sResolver_Resolve(t *testing.T) {
	books := []v1alpha1.PriceBook{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pb-v1", Generation: 1},
			Spec: v1alpha1.PriceBookSpec{
				Currency:      "USD",
				EffectiveFrom: metav1.NewTime(time.Now().Add(-24 * time.Hour)),
				SKUs: []v1alpha1.SKU{{
					SKUName: "gpt-4o",
					Unit:    "per-1M-tokens",
					Rates:   map[string]string{"prompt_tokens": "2.50", "completion_tokens": "10.0"},
				}},
			},
			Status: v1alpha1.PriceBookStatus{State: "Active"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pb-v2", Generation: 2},
			Spec: v1alpha1.PriceBookSpec{
				Currency:      "USD",
				EffectiveFrom: metav1.NewTime(time.Now().Add(24 * time.Hour)),
				SKUs: []v1alpha1.SKU{{
					SKUName: "llama-3.1-70b",
					Unit:    "per-1M-tokens",
					Rates:   map[string]string{"prompt_tokens": "0.59"},
				}},
			},
			Status: v1alpha1.PriceBookStatus{State: "Draft"},
		},
	}

	reader := &fakeReader{items: books}
	resolver := NewK8sResolver(reader)
	ctx := context.Background()

	t.Run("resolves active PriceBook for known SKU", func(t *testing.T) {
		pb, err := resolver.Resolve(ctx, "gpt-4o", time.Now())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pb.Name != "pb-v1" {
			t.Errorf("expected PriceBook pb-v1, got %s", pb.Name)
		}
		if pb.Currency != "USD" {
			t.Errorf("expected USD, got %s", pb.Currency)
		}
		if _, ok := pb.SKUs["gpt-4o"]; !ok {
			t.Error("expected SKU gpt-4o in resolved PriceBook")
		}
	})

	t.Run("returns error for unknown SKU", func(t *testing.T) {
		_, err := resolver.Resolve(ctx, "unknown-model", time.Now())
		if err == nil {
			t.Fatal("expected error for unknown SKU")
		}
	})

	t.Run("ignores non-Active PriceBooks", func(t *testing.T) {
		_, err := resolver.Resolve(ctx, "llama-3.1-70b", time.Now())
		if err == nil {
			t.Fatal("expected error — llama SKU is in a Draft PriceBook")
		}
	})

	t.Run("uses cache on second call", func(t *testing.T) {
		// Invalidate the cache to test fresh fetch.
		resolver.mu.Lock()
		resolver.cache = nil
		resolver.mu.Unlock()

		pb1, err := resolver.Resolve(ctx, "gpt-4o", time.Now())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Mutate the reader's items (resolver should use cache, not re-read).
		reader.items = nil
		pb2, err := resolver.Resolve(ctx, "gpt-4o", time.Now())
		if err != nil {
			t.Fatalf("unexpected error on cached call: %v", err)
		}
		if pb1.Name != pb2.Name {
			t.Errorf("expected cached result, got different PriceBooks: %s vs %s", pb1.Name, pb2.Name)
		}

		// Restore for other tests.
		reader.items = books
	})
}
