package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func sarClient(t *testing.T, allowed bool, sarErr error) ctrlclient.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := authorizationv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, client ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.CreateOption) error {
				if sarErr != nil {
					return sarErr
				}
				if sar, ok := obj.(*authorizationv1.SubjectAccessReview); ok {
					sar.Status.Allowed = allowed
					return nil
				}
				return client.Create(ctx, obj, opts...)
			},
		}).
		Build()
}

func sessionRequest(identity *ConsoleIdentity) *http.Request {
	req := httptest.NewRequest("GET", "/api/session", nil)
	if identity != nil {
		ctx := context.WithValue(req.Context(), contextKey{}, identity)
		req = req.WithContext(ctx)
	}
	return req
}

func TestSessionHandler_DemoUser(t *testing.T) {
	k8s := sarClient(t, false, nil)
	lister := &mockTenantLister{}
	resolver := NewTenantResolver(lister)
	h := NewSessionHandler(k8s, resolver, "billing", "demo@example.com")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(&ConsoleIdentity{
		User:   "demo@example.com",
		Groups: []string{"users"},
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	var resp SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Persona != PersonaDemo {
		t.Errorf("persona = %q, want %q", resp.Persona, PersonaDemo)
	}
	if resp.User != "demo@example.com" {
		t.Errorf("user = %q, want demo@example.com", resp.User)
	}
	if resp.Tenant != "" {
		t.Errorf("tenant = %q, want empty", resp.Tenant)
	}
}

func TestSessionHandler_Provider(t *testing.T) {
	k8s := sarClient(t, true, nil)
	lister := &mockTenantLister{}
	resolver := NewTenantResolver(lister)
	h := NewSessionHandler(k8s, resolver, "billing", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(&ConsoleIdentity{
		User:   "admin@example.com",
		Groups: []string{"billing-admins"},
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	var resp SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Persona != PersonaProvider {
		t.Errorf("persona = %q, want %q", resp.Persona, PersonaProvider)
	}
	if resp.Tenant != "" {
		t.Errorf("tenant = %q, want empty", resp.Tenant)
	}
}

func TestSessionHandler_Consumer(t *testing.T) {
	k8s := sarClient(t, false, nil)
	lister := &mockTenantLister{
		tenants: []TenantInfo{
			{Name: "acme-corp", Groups: []string{"acme-users"}},
		},
	}
	resolver := NewTenantResolver(lister)
	h := NewSessionHandler(k8s, resolver, "billing", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(&ConsoleIdentity{
		User:   "alice@acme.com",
		Groups: []string{"acme-users"},
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	var resp SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Persona != PersonaConsumer {
		t.Errorf("persona = %q, want %q", resp.Persona, PersonaConsumer)
	}
	if resp.Tenant != "acme-corp" {
		t.Errorf("tenant = %q, want acme-corp", resp.Tenant)
	}
}

func TestSessionHandler_NoBinding(t *testing.T) {
	k8s := sarClient(t, false, nil)
	lister := &mockTenantLister{
		tenants: []TenantInfo{
			{Name: "acme-corp", Groups: []string{"acme-users"}},
		},
	}
	resolver := NewTenantResolver(lister)
	h := NewSessionHandler(k8s, resolver, "billing", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(&ConsoleIdentity{
		User:   "nobody@example.com",
		Groups: []string{"unrelated-group"},
	}))

	if rec.Code != http.StatusForbidden {
		t.Errorf("got status %d, want 403", rec.Code)
	}
}

func TestSessionHandler_NoIdentity(t *testing.T) {
	k8s := sarClient(t, false, nil)
	lister := &mockTenantLister{}
	resolver := NewTenantResolver(lister)
	h := NewSessionHandler(k8s, resolver, "billing", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", rec.Code)
	}
}

func TestSessionHandler_SARError(t *testing.T) {
	k8s := sarClient(t, false, fmt.Errorf("connection refused"))
	lister := &mockTenantLister{}
	resolver := NewTenantResolver(lister)
	h := NewSessionHandler(k8s, resolver, "billing", "")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(&ConsoleIdentity{
		User:   "admin@example.com",
		Groups: []string{"billing-admins"},
	}))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500", rec.Code)
	}
}

func TestSessionHandler_DemoUserTakesPriority(t *testing.T) {
	// Even if the demo user has provider RBAC, they get the demo toggle
	k8s := sarClient(t, true, nil)
	lister := &mockTenantLister{}
	resolver := NewTenantResolver(lister)
	h := NewSessionHandler(k8s, resolver, "billing", "admin@example.com")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sessionRequest(&ConsoleIdentity{
		User:   "admin@example.com",
		Groups: []string{"billing-admins"},
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	var resp SessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Persona != PersonaDemo {
		t.Errorf("persona = %q, want %q (demo user takes priority)", resp.Persona, PersonaDemo)
	}
}
