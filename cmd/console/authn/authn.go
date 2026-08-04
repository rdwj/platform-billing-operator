package authn

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	ModeOIDC           = "oidc"
	ModeOpenShiftOAuth = "openshift-oauth"

	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

type contextKey struct{}

type ConsoleIdentity struct {
	User   string
	Groups []string
	Role   string // RoleAdmin, RoleOperator, RoleViewer, or ""
}

type RoleConfig struct {
	Admin    []string
	Operator []string
	Viewer   []string
}

type Config struct {
	Mode       string // "oidc", "openshift-oauth", or ""
	RoleConfig RoleConfig
}

type Authenticator struct {
	cfg      Config
	verifier *oidc.IDTokenVerifier
}

func New(ctx context.Context, cfg Config) (*Authenticator, error) {
	a := &Authenticator{cfg: cfg}

	if cfg.Mode == ModeOIDC {
		issuer := os.Getenv("OIDC_ISSUER_URL")
		if issuer == "" {
			return nil, fmt.Errorf("OIDC_ISSUER_URL is required when AUTH_MODE=oidc")
		}
		clientID := os.Getenv("OIDC_CLIENT_ID")
		if clientID == "" {
			return nil, fmt.Errorf("OIDC_CLIENT_ID is required when AUTH_MODE=oidc")
		}

		provider, err := oidc.NewProvider(ctx, issuer)
		if err != nil {
			return nil, fmt.Errorf("OIDC discovery for %s: %w", issuer, err)
		}
		a.verifier = provider.Verifier(&oidc.Config{ClientID: clientID})
	}

	return a, nil
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	if a.cfg.Mode == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}

		var id *ConsoleIdentity
		var err error

		switch a.cfg.Mode {
		case ModeOIDC:
			id, err = a.identityFromJWT(r)
		case ModeOpenShiftOAuth:
			id, err = identityFromHeaders(r)
		}

		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if id == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		id.Role = a.cfg.RoleConfig.ResolveRole(id.Groups)
		ctx := context.WithValue(r.Context(), contextKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Authenticator) identityFromJWT(r *http.Request) (*ConsoleIdentity, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return nil, fmt.Errorf("missing bearer token")
	}
	rawToken := strings.TrimPrefix(auth, "Bearer ")

	token, err := a.verifier.Verify(r.Context(), rawToken)
	if err != nil {
		return nil, fmt.Errorf("token verification: %w", err)
	}

	var claims map[string]interface{}
	if err := token.Claims(&claims); err != nil {
		return nil, fmt.Errorf("extracting claims: %w", err)
	}

	groupsClaim := os.Getenv("OIDC_GROUPS_CLAIM")
	if groupsClaim == "" {
		groupsClaim = "groups"
	}

	var groups []string
	if raw, ok := claims[groupsClaim]; ok {
		if arr, ok := raw.([]interface{}); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					groups = append(groups, s)
				}
			}
		}
	}

	return &ConsoleIdentity{
		User:   token.Subject,
		Groups: groups,
	}, nil
}

func identityFromHeaders(r *http.Request) (*ConsoleIdentity, error) {
	user := r.Header.Get("X-Forwarded-User")
	if user == "" {
		return nil, fmt.Errorf("missing X-Forwarded-User header")
	}

	var groups []string
	if g := r.Header.Get("X-Forwarded-Groups"); g != "" {
		for _, part := range strings.Split(g, ",") {
			if s := strings.TrimSpace(part); s != "" {
				groups = append(groups, s)
			}
		}
	}

	return &ConsoleIdentity{
		User:   user,
		Groups: groups,
	}, nil
}

func IdentityFromContext(ctx context.Context) *ConsoleIdentity {
	id, _ := ctx.Value(contextKey{}).(*ConsoleIdentity)
	return id
}

func (rc RoleConfig) ResolveRole(groups []string) string {
	groupSet := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		groupSet[g] = struct{}{}
	}

	for _, admin := range rc.Admin {
		if _, ok := groupSet[admin]; ok {
			return RoleAdmin
		}
	}
	for _, op := range rc.Operator {
		if _, ok := groupSet[op]; ok {
			return RoleOperator
		}
	}
	for _, v := range rc.Viewer {
		if _, ok := groupSet[v]; ok {
			return RoleViewer
		}
	}
	return ""
}

func ParseRoleConfig() RoleConfig {
	return RoleConfig{
		Admin:    splitEnv("CONSOLE_ROLES_ADMIN"),
		Operator: splitEnv("CONSOLE_ROLES_OPERATOR"),
		Viewer:   splitEnv("CONSOLE_ROLES_VIEWER"),
	}
}

func splitEnv(key string) []string {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			result = append(result, s)
		}
	}
	return result
}
