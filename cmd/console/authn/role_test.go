package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireProviderRole(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name     string
		identity *ConsoleIdentity
		method   string
		wantCode int
	}{
		{"admin POST", &ConsoleIdentity{Role: "admin"}, "POST", 200},
		{"admin GET", &ConsoleIdentity{Role: "admin"}, "GET", 200},
		{"admin DELETE", &ConsoleIdentity{Role: "admin"}, "DELETE", 200},
		{"operator POST", &ConsoleIdentity{Role: "operator"}, "POST", 200},
		{"operator GET", &ConsoleIdentity{Role: "operator"}, "GET", 200},
		{"viewer GET", &ConsoleIdentity{Role: "viewer"}, "GET", 200},
		{"viewer HEAD", &ConsoleIdentity{Role: "viewer"}, "HEAD", 200},
		{"viewer OPTIONS", &ConsoleIdentity{Role: "viewer"}, "OPTIONS", 200},
		{"viewer POST", &ConsoleIdentity{Role: "viewer"}, "POST", 403},
		{"viewer DELETE", &ConsoleIdentity{Role: "viewer"}, "DELETE", 403},
		{"viewer PUT", &ConsoleIdentity{Role: "viewer"}, "PUT", 403},
		{"no role GET", &ConsoleIdentity{Role: ""}, "GET", 403},
		{"no role POST", &ConsoleIdentity{Role: ""}, "POST", 403},
		{"nil identity GET", nil, "GET", 403},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := RequireProviderRole(ok)

			req := httptest.NewRequest(tt.method, "/api/tenants", nil)
			if tt.identity != nil {
				ctx := context.WithValue(req.Context(), contextKey{}, tt.identity)
				req = req.WithContext(ctx)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("got status %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}
