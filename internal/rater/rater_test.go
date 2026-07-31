package rater

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// vectorsFile mirrors contracts/rating-vectors.v1.json.
type vectorsFile struct {
	PriceBook PriceBook `json:"pricebook"`
	Vectors   []struct {
		ID             string `json:"id"`
		EventFile      string `json:"event_file"`
		ExpectedCharge struct {
			Currency   string `json:"currency"`
			Components map[string]struct {
				BillableQuantity int64  `json:"billable_quantity"`
				Amount           string `json:"amount"`
			} `json:"components"`
			Total string `json:"total"`
		} `json:"expected_charge"`
	} `json:"vectors"`
}

const contractsDir = "../../contracts"

func loadVectors(t *testing.T) vectorsFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(contractsDir, "rating-vectors.v1.json"))
	if err != nil {
		t.Fatalf("golden vectors: %v", err)
	}
	var vf vectorsFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&vf); err != nil {
		t.Fatalf("golden vectors: %v", err)
	}
	return vf
}

func loadEvent(t *testing.T, rel string) Event {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(contractsDir, rel))
	if err != nil {
		t.Fatalf("event %s: %v", rel, err)
	}
	var ev Event
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("event %s: %v", rel, err)
	}
	return ev
}

// TestGoldenVectors: every vector in contracts/rating-vectors.v1.json must
// match BYTE-EXACTLY — totals, per-component amounts, and billable quantities.
func TestGoldenVectors(t *testing.T) {
	vf := loadVectors(t)
	for _, v := range vf.Vectors {
		t.Run(v.ID, func(t *testing.T) {
			ev := loadEvent(t, v.EventFile)
			got, err := Rate(ev, vf.PriceBook)
			if err != nil {
				t.Fatalf("Rate: %v", err)
			}
			if got.Total != v.ExpectedCharge.Total {
				t.Errorf("total = %q, want %q", got.Total, v.ExpectedCharge.Total)
			}
			if got.Currency != v.ExpectedCharge.Currency {
				t.Errorf("currency = %q, want %q", got.Currency, v.ExpectedCharge.Currency)
			}
			if len(got.Components) != len(v.ExpectedCharge.Components) {
				t.Errorf("component set = %v, want keys %v", got.Components, v.ExpectedCharge.Components)
			}
			for name, want := range v.ExpectedCharge.Components {
				gc, ok := got.Components[name]
				if !ok {
					t.Errorf("missing component %q", name)
					continue
				}
				if gc.Amount != want.Amount {
					t.Errorf("%s amount = %q, want %q", name, gc.Amount, want.Amount)
				}
				if gc.BillableQuantity != want.BillableQuantity {
					t.Errorf("%s billable_quantity = %d, want %d", name, gc.BillableQuantity, want.BillableQuantity)
				}
			}
		})
	}
}

// TestDeterminism: THE property (controllers.md §5) — rating the same events
// against the same PriceBook twice yields byte-identical serialized charges.
func TestDeterminism(t *testing.T) {
	vf := loadVectors(t)
	run := func() []byte {
		var buf bytes.Buffer
		for _, v := range vf.Vectors {
			ev := loadEvent(t, v.EventFile)
			ch, err := Rate(ev, vf.PriceBook)
			if err != nil {
				t.Fatalf("Rate: %v", err)
			}
			b, err := json.Marshal(ch) // map keys are sorted by encoding/json
			if err != nil {
				t.Fatal(err)
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
		return buf.Bytes()
	}
	if !bytes.Equal(run(), run()) {
		t.Fatal("two identical rating runs produced different bytes")
	}
}

// TestTotalEqualsSumOfComponents: the total must equal the sum of ROUNDED
// component amounts (never a re-rounded raw sum).
func TestTotalEqualsSumOfComponents(t *testing.T) {
	vf := loadVectors(t)
	for _, v := range vf.Vectors {
		ev := loadEvent(t, v.EventFile)
		ch, err := Rate(ev, vf.PriceBook)
		if err != nil {
			t.Fatal(err)
		}
		sumCents := int64(0)
		for _, c := range ch.Components {
			var whole, frac int64
			if _, err := fmtSscan(c.Amount, &whole, &frac); err != nil {
				t.Fatalf("bad amount %q", c.Amount)
			}
			sumCents += whole*1_000_000 + frac
		}
		var tw, tf int64
		if _, err := fmtSscan(ch.Total, &tw, &tf); err != nil {
			t.Fatalf("bad total %q", ch.Total)
		}
		if tw*1_000_000+tf != sumCents {
			t.Errorf("%s: total %s != sum of components", v.ID, ch.Total)
		}
	}
}

// TestUnknownQuantityWarns: forward-compat — a new token type must be
// preserved as a warning, not a crash and not a silent drop.
func TestUnknownQuantityWarns(t *testing.T) {
	vf := loadVectors(t)
	ev := loadEvent(t, "examples/maas-streaming-ok.json")
	ev.Quantities["speculation_tokens"] = json.Number("999")
	ch, err := Rate(ev, vf.PriceBook)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range ch.Warnings {
		if w == "unrated_quantity:speculation_tokens" {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want unrated_quantity:speculation_tokens", ch.Warnings)
	}
	if ch.Total != "0.013670" { // unchanged by the unrated key
		t.Errorf("total = %q, want 0.013670", ch.Total)
	}
}

// TestUnknownSKUErrors: contract violation → error (DLQ path), not a $0 charge.
func TestUnknownSKUErrors(t *testing.T) {
	vf := loadVectors(t)
	ev := loadEvent(t, "examples/maas-streaming-ok.json")
	ev.SKU = "not-in-pricebook"
	if _, err := Rate(ev, vf.PriceBook); err == nil {
		t.Fatal("expected unknown_sku error")
	}
}

func TestNegativeQuantityRejected(t *testing.T) {
	vf := loadVectors(t)
	ev := Event{
		SchemaVersion: "usage.v1",
		RecordID:      "test-negative",
		TenantID:      "ten-test",
		SKU:           "llama-3-70b",
		Quantities:    map[string]json.Number{"prompt_tokens": json.Number("-100")},
		Status:        "ok",
	}
	_, err := Rate(ev, vf.PriceBook)
	if err == nil {
		t.Fatal("expected error for negative quantity")
	}
}

func TestEmptyQuantities(t *testing.T) {
	vf := loadVectors(t)
	ev := Event{
		SchemaVersion: "usage.v1",
		RecordID:      "test-empty-qty",
		TenantID:      "ten-test",
		SKU:           "llama-3-70b",
		Quantities:    map[string]json.Number{},
		Status:        "ok",
	}
	ch, err := Rate(ev, vf.PriceBook)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if ch.Total != "0.000000" {
		t.Errorf("total = %q, want 0.000000", ch.Total)
	}
	if len(ch.Components) != 0 {
		t.Errorf("components = %v, want empty", ch.Components)
	}
}

func TestZeroQuantities(t *testing.T) {
	vf := loadVectors(t)
	ev := Event{
		SchemaVersion: "usage.v1",
		RecordID:      "test-zero-qty",
		TenantID:      "ten-test",
		SKU:           "llama-3-70b",
		Quantities: map[string]json.Number{
			"prompt_tokens":     json.Number("0"),
			"completion_tokens": json.Number("0"),
		},
		Status: "ok",
	}
	ch, err := Rate(ev, vf.PriceBook)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if ch.Total != "0.000000" {
		t.Errorf("total = %q, want 0.000000", ch.Total)
	}
	for name, comp := range ch.Components {
		if comp.Amount != "0.000000" {
			t.Errorf("%s amount = %q, want 0.000000", name, comp.Amount)
		}
	}
}

func TestUnsupportedSchemaVersion(t *testing.T) {
	vf := loadVectors(t)
	ev := Event{
		SchemaVersion: "usage.v99",
		RecordID:      "test-bad-schema",
		TenantID:      "ten-test",
		SKU:           "llama-3-70b",
		Quantities:    map[string]json.Number{"prompt_tokens": json.Number("100")},
		Status:        "ok",
	}
	_, err := Rate(ev, vf.PriceBook)
	if err == nil {
		t.Fatal("expected error for unsupported schema version")
	}
}

func TestProviderFaultBillPartial(t *testing.T) {
	vf := loadVectors(t)
	pb := vf.PriceBook
	pb.ProviderFaultPolicy = "bill_partial"
	ev := loadEvent(t, "examples/maas-upstream-error.json")
	ch, err := Rate(ev, pb)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if ch.Total == "0.000000" {
		t.Error("bill_partial on upstream_error should produce non-zero charge")
	}
}

func TestClientDisconnectZeroCharge(t *testing.T) {
	vf := loadVectors(t)
	pb := vf.PriceBook
	pb.ClientDisconnectPolicy = "zero_charge"
	ev := loadEvent(t, "examples/maas-client-disconnect.json")
	ch, err := Rate(ev, pb)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if ch.Total != "0.000000" {
		t.Errorf("total = %q, want 0.000000 for zero_charge disconnect policy", ch.Total)
	}
}

// fmtSscan parses "123.456789" into whole and fractional (6-digit) parts.
func fmtSscan(s string, whole, frac *int64) (int, error) {
	n, err := sscanDec(s, whole, frac)
	return n, err
}

func sscanDec(s string, whole, frac *int64) (int, error) {
	var w, f int64
	var seenDot bool
	digits := 0
	neg := false
	for i, r := range s {
		switch {
		case r == '-' && i == 0:
			neg = true
		case r == '.':
			seenDot = true
		case r >= '0' && r <= '9':
			if seenDot {
				f = f*10 + int64(r-'0')
				digits++
			} else {
				w = w*10 + int64(r-'0')
			}
		default:
			return 0, os.ErrInvalid
		}
	}
	if digits != 6 {
		return 0, os.ErrInvalid // exactly 6 fractional digits, per the Money format
	}
	if neg {
		w, f = -w, -f
	}
	*whole, *frac = w, f
	return 2, nil
}
