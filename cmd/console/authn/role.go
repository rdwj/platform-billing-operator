package authn

import (
	"net/http"
)

func RequireProviderRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := IdentityFromContext(r.Context())
		if identity == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		switch identity.Role {
		case RoleAdmin, RoleOperator:
			next.ServeHTTP(w, r)
		case RoleViewer:
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Forbidden: viewer role has read-only access", http.StatusForbidden)
		default:
			http.Error(w, "Forbidden: provider access requires admin, operator, or viewer role", http.StatusForbidden)
		}
	})
}
