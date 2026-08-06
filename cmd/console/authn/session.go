package authn

import (
	"context"
	"encoding/json"
	"net/http"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type SessionResponse struct {
	Persona string `json:"persona"`
	User    string `json:"user"`
	Tenant  string `json:"tenant,omitempty"`
}

type SessionHandler struct {
	k8s       ctrlclient.Client
	resolver  *TenantResolver
	namespace string
	demoUser  string
}

func NewSessionHandler(k8s ctrlclient.Client, resolver *TenantResolver, namespace, demoUser string) *SessionHandler {
	return &SessionHandler{
		k8s:       k8s,
		resolver:  resolver,
		namespace: namespace,
		demoUser:  demoUser,
	}
}

func (h *SessionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	identity := IdentityFromContext(r.Context())
	if identity == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	resp := SessionResponse{User: identity.User}

	if h.demoUser != "" && identity.User == h.demoUser {
		resp.Persona = PersonaDemo
		identity.Persona = PersonaDemo
		writeJSON(w, resp)
		return
	}

	allowed, err := h.canListPriceBooks(r.Context(), identity)
	if err != nil {
		http.Error(w, "internal error checking permissions", http.StatusInternalServerError)
		return
	}

	if allowed {
		resp.Persona = PersonaProvider
		identity.Persona = PersonaProvider
		writeJSON(w, resp)
		return
	}

	tenant, err := h.resolver.Resolve(r.Context(), identity)
	if err != nil {
		http.Error(w, "Forbidden: no provider or consumer binding found", http.StatusForbidden)
		return
	}

	resp.Persona = PersonaConsumer
	resp.Tenant = tenant
	identity.Persona = PersonaConsumer
	writeJSON(w, resp)
}

func (h *SessionHandler) canListPriceBooks(ctx context.Context, identity *ConsoleIdentity) (bool, error) {
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   identity.User,
			Groups: identity.Groups,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: h.namespace,
				Verb:      "list",
				Group:     "billing.opendatahub.io",
				Resource:  "pricebooks",
			},
		},
	}

	if err := h.k8s.Create(ctx, sar, &ctrlclient.CreateOptions{
		Raw: &metav1.CreateOptions{},
	}); err != nil {
		return false, err
	}

	return sar.Status.Allowed, nil
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
