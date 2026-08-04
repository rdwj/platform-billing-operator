package authn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestResolveRole(t *testing.T) {
	rc := RoleConfig{
		Admin:    []string{"billing-admins", "super-admins"},
		Operator: []string{"billing-ops"},
		Viewer:   []string{"finance-ro"},
	}

	tests := []struct {
		groups []string
		want   string
	}{
		{[]string{"billing-admins"}, "admin"},
		{[]string{"super-admins"}, "admin"},
		{[]string{"billing-ops"}, "operator"},
		{[]string{"finance-ro"}, "viewer"},
		{[]string{"billing-ops", "billing-admins"}, "admin"},
		{[]string{"unknown-group"}, ""},
		{nil, ""},
		{[]string{}, ""},
	}

	for _, tt := range tests {
		got := rc.ResolveRole(tt.groups)
		if got != tt.want {
			t.Errorf("ResolveRole(%v) = %q, want %q", tt.groups, got, tt.want)
		}
	}
}

func TestParseRoleConfig(t *testing.T) {
	t.Setenv("CONSOLE_ROLES_ADMIN", "admins, super-admins")
	t.Setenv("CONSOLE_ROLES_OPERATOR", "ops")
	t.Setenv("CONSOLE_ROLES_VIEWER", "")

	rc := ParseRoleConfig()

	if len(rc.Admin) != 2 || rc.Admin[0] != "admins" || rc.Admin[1] != "super-admins" {
		t.Errorf("Admin = %v, want [admins super-admins]", rc.Admin)
	}
	if len(rc.Operator) != 1 || rc.Operator[0] != "ops" {
		t.Errorf("Operator = %v, want [ops]", rc.Operator)
	}
	if rc.Viewer != nil {
		t.Errorf("Viewer = %v, want nil", rc.Viewer)
	}
}

func TestMiddleware_NoAuth(t *testing.T) {
	auth, err := New(context.Background(), Config{Mode: ""})
	if err != nil {
		t.Fatal(err)
	}

	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := IdentityFromContext(r.Context())
		if id != nil {
			t.Error("expected nil identity for no-auth mode")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
}

func TestMiddleware_HealthzBypass(t *testing.T) {
	auth, err := New(context.Background(), Config{Mode: "openshift-oauth"})
	if err != nil {
		t.Fatal(err)
	}

	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: got status %d, want 200", path, rec.Code)
		}
	}
}

func TestMiddleware_ForwardedHeaders(t *testing.T) {
	auth, err := New(context.Background(), Config{
		Mode: "openshift-oauth",
		RoleConfig: RoleConfig{
			Admin: []string{"billing-admins"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := IdentityFromContext(r.Context())
		if id == nil {
			t.Fatal("expected identity")
		}
		if id.User != "testuser" {
			t.Errorf("User = %q, want testuser", id.User)
		}
		if id.Role != "admin" {
			t.Errorf("Role = %q, want admin", id.Role)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Forwarded-User", "testuser")
	req.Header.Set("X-Forwarded-Groups", "billing-admins,other-group")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
}

func TestMiddleware_ForwardedHeaders_Missing(t *testing.T) {
	auth, err := New(context.Background(), Config{Mode: "openshift-oauth"})
	if err != nil {
		t.Fatal(err)
	}

	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", rec.Code)
	}
}

func TestMiddleware_OIDC(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}

	jwks := jose.JSONWebKeySet{
		Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"},
		},
	}

	var issuerURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"issuer":   issuerURL,
			"jwks_uri": issuerURL + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks) //nolint:errcheck
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	issuerURL = srv.URL

	claims := jwt.Claims{
		Issuer:   issuerURL,
		Subject:  "user@example.com",
		Audience: jwt.Audience{"test-client"},
		IssuedAt: jwt.NewNumericDate(time.Now()),
		Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	customClaims := map[string]interface{}{
		"groups": []string{"billing-ops"},
	}

	rawToken, err := jwt.Signed(signer).Claims(claims).Claims(customClaims).Serialize()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("OIDC_ISSUER_URL", issuerURL)
	t.Setenv("OIDC_CLIENT_ID", "test-client")

	auth, err := New(context.Background(), Config{
		Mode: "oidc",
		RoleConfig: RoleConfig{
			Operator: []string{"billing-ops"},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := IdentityFromContext(r.Context())
		if id == nil {
			t.Fatal("expected identity")
		}
		if id.User != "user@example.com" {
			t.Errorf("User = %q, want user@example.com", id.User)
		}
		if id.Role != "operator" {
			t.Errorf("Role = %q, want operator", id.Role)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", rawToken))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
}

func TestMiddleware_OIDC_InvalidToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	jwks := jose.JSONWebKeySet{
		Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"},
		},
	}

	var issuerURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"issuer":   issuerURL,
			"jwks_uri": issuerURL + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks) //nolint:errcheck
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	issuerURL = srv.URL

	t.Setenv("OIDC_ISSUER_URL", issuerURL)
	t.Setenv("OIDC_CLIENT_ID", "test-client")

	auth, err := New(context.Background(), Config{Mode: "oidc"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", rec.Code)
	}
}
