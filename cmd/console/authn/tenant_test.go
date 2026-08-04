package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockTenantLister struct {
	tenants []TenantInfo
	err     error
}

func (m *mockTenantLister) ListTenantAccounts(_ context.Context) ([]TenantInfo, error) {
	return m.tenants, m.err
}

func TestResolve_SingleMatch(t *testing.T) {
	lister := &mockTenantLister{
		tenants: []TenantInfo{
			{Name: "acme-corp", Groups: []string{"acme-users", "acme-admins"}},
			{Name: "globex", Groups: []string{"globex-team"}},
		},
	}
	resolver := NewTenantResolver(lister)

	tenant, err := resolver.Resolve(context.Background(), &ConsoleIdentity{
		User:   "alice",
		Groups: []string{"acme-users", "other-group"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant != "acme-corp" {
		t.Errorf("got %q, want acme-corp", tenant)
	}
}

func TestResolve_NoMatch(t *testing.T) {
	lister := &mockTenantLister{
		tenants: []TenantInfo{
			{Name: "acme-corp", Groups: []string{"acme-users"}},
		},
	}
	resolver := NewTenantResolver(lister)

	_, err := resolver.Resolve(context.Background(), &ConsoleIdentity{
		User:   "bob",
		Groups: []string{"unrelated-group"},
	})
	if err == nil {
		t.Fatal("expected error for no match")
	}
	if got := err.Error(); !strings.Contains(got, "no tenant matched") {
		t.Errorf("error = %q, want it to contain 'no tenant matched'", got)
	}
}

func TestResolve_MultipleMatches(t *testing.T) {
	lister := &mockTenantLister{
		tenants: []TenantInfo{
			{Name: "acme-corp", Groups: []string{"shared-group"}},
			{Name: "globex", Groups: []string{"shared-group"}},
		},
	}
	resolver := NewTenantResolver(lister)

	_, err := resolver.Resolve(context.Background(), &ConsoleIdentity{
		User:   "charlie",
		Groups: []string{"shared-group"},
	})
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
	got := err.Error()
	if !strings.Contains(got, "multiple tenants") || !strings.Contains(got, "configuration error") {
		t.Errorf("error = %q, want 'multiple tenants' and 'configuration error'", got)
	}
}

func TestResolve_MultipleGroupsOneMatch(t *testing.T) {
	lister := &mockTenantLister{
		tenants: []TenantInfo{
			{Name: "acme-corp", Groups: []string{"acme-users"}},
			{Name: "globex", Groups: []string{"globex-team"}},
		},
	}
	resolver := NewTenantResolver(lister)

	tenant, err := resolver.Resolve(context.Background(), &ConsoleIdentity{
		User:   "alice",
		Groups: []string{"unrelated", "acme-users", "another"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant != "acme-corp" {
		t.Errorf("got %q, want acme-corp", tenant)
	}
}

func TestResolve_EmptyGroups(t *testing.T) {
	lister := &mockTenantLister{
		tenants: []TenantInfo{
			{Name: "acme-corp", Groups: []string{"acme-users"}},
		},
	}
	resolver := NewTenantResolver(lister)

	_, err := resolver.Resolve(context.Background(), &ConsoleIdentity{
		User: "alice",
	})
	if err == nil {
		t.Fatal("expected error for empty groups")
	}
}

func TestTenantMiddleware_Success(t *testing.T) {
	lister := &mockTenantLister{
		tenants: []TenantInfo{
			{Name: "acme-corp", Groups: []string{"acme-users"}},
		},
	}
	resolver := NewTenantResolver(lister)
	mw := TenantMiddleware(resolver)

	var gotTenant string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTenant = TenantFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/portal/api/balance", nil)
	ctx := context.WithValue(req.Context(), contextKey{}, &ConsoleIdentity{
		User:   "alice",
		Groups: []string{"acme-users"},
		Role:   "viewer",
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
	if gotTenant != "acme-corp" {
		t.Errorf("TenantFromContext = %q, want acme-corp", gotTenant)
	}
}

func TestTenantMiddleware_NoIdentity(t *testing.T) {
	lister := &mockTenantLister{}
	resolver := NewTenantResolver(lister)
	mw := TenantMiddleware(resolver)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/portal/api/balance", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("got status %d, want 403", rec.Code)
	}
}

func TestTenantMiddleware_NoMatch(t *testing.T) {
	lister := &mockTenantLister{
		tenants: []TenantInfo{
			{Name: "acme-corp", Groups: []string{"acme-users"}},
		},
	}
	resolver := NewTenantResolver(lister)
	mw := TenantMiddleware(resolver)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/portal/api/balance", nil)
	ctx := context.WithValue(req.Context(), contextKey{}, &ConsoleIdentity{
		User:   "bob",
		Groups: []string{"wrong-group"},
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("got status %d, want 403", rec.Code)
	}
}

func TestTenantFromContext_Empty(t *testing.T) {
	tenant := TenantFromContext(context.Background())
	if tenant != "" {
		t.Errorf("got %q, want empty string", tenant)
	}
}

func TestParseIdPGroups(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"group-a, group-b", 2},
		{"single", 1},
		{"", 0},
		{" spaced , values ", 2},
		{",,,", 0},
	}
	for _, tt := range tests {
		got := ParseIdPGroups(tt.input)
		if len(got) != tt.want {
			t.Errorf("ParseIdPGroups(%q) = %v (len %d), want len %d", tt.input, got, len(got), tt.want)
		}
	}
}
