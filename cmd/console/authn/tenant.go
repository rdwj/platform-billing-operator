package authn

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const IdPGroupsAnnotation = "billing.opendatahub.io/idp-groups"

type tenantContextKey struct{}

type TenantInfo struct {
	Name   string
	Groups []string
}

type TenantLister interface {
	ListTenantAccounts(ctx context.Context) ([]TenantInfo, error)
}

type TenantResolver struct {
	lister TenantLister
}

func NewTenantResolver(lister TenantLister) *TenantResolver {
	return &TenantResolver{lister: lister}
}

func (r *TenantResolver) Resolve(ctx context.Context, identity *ConsoleIdentity) (string, error) {
	tenants, err := r.lister.ListTenantAccounts(ctx)
	if err != nil {
		return "", fmt.Errorf("listing tenant accounts: %w", err)
	}

	userGroups := make(map[string]struct{}, len(identity.Groups))
	for _, g := range identity.Groups {
		userGroups[g] = struct{}{}
	}

	var matched []string
	for _, t := range tenants {
		for _, tg := range t.Groups {
			if _, ok := userGroups[tg]; ok {
				matched = append(matched, t.Name)
				break
			}
		}
	}

	switch len(matched) {
	case 0:
		return "", fmt.Errorf("no tenant matched groups %v", identity.Groups)
	case 1:
		return matched[0], nil
	default:
		return "", fmt.Errorf("multiple tenants matched groups: %s (configuration error)",
			strings.Join(matched, ", "))
	}
}

func TenantMiddleware(resolver *TenantResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := IdentityFromContext(r.Context())
			if identity == nil {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			tenant, err := resolver.Resolve(r.Context(), identity)
			if err != nil {
				http.Error(w, fmt.Sprintf("Forbidden: %s", err), http.StatusForbidden)
				return
			}

			ctx := context.WithValue(r.Context(), tenantContextKey{}, tenant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func TenantFromContext(ctx context.Context) string {
	t, _ := ctx.Value(tenantContextKey{}).(string)
	return t
}

func ParseIdPGroups(annotation string) []string {
	if annotation == "" {
		return nil
	}
	parts := strings.Split(annotation, ",")
	groups := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			groups = append(groups, s)
		}
	}
	return groups
}
