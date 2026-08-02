package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

type keysHandler struct {
	store *ledger.Store
}

type keyResponse struct {
	KeyName    string  `json:"key_name"`
	TenantID   string  `json:"tenant_id"`
	Enabled    bool    `json:"enabled"`
	Source     string  `json:"source"`
	CreatedAt  string  `json:"created_at"`
	DisabledAt *string `json:"disabled_at,omitempty"`
}

type keysListResponse struct {
	Tenant string        `json:"tenant"`
	Keys   []keyResponse `json:"keys"`
}

type registerKeyRequest struct {
	Tenant  string `json:"tenant"`
	KeyName string `json:"key_name"`
	Source  string `json:"source"`
}

type keyActionRequest struct {
	Tenant  string `json:"tenant"`
	KeyName string `json:"key_name"`
}

func (h *keysHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Path != "/v1/keys" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h.handleList(w, r)
	case http.MethodPost:
		h.handlePost(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *keysHandler) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		writeError(w, http.StatusBadRequest, "tenant query parameter is required")
		return
	}

	keys, err := h.store.ListKeys(r.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := keysListResponse{
		Tenant: tenant,
		Keys:   make([]keyResponse, 0, len(keys)),
	}
	for _, k := range keys {
		kr := keyResponse{
			KeyName:   k.KeyName,
			TenantID:  k.TenantID,
			Enabled:   k.Enabled,
			Source:    k.Source,
			CreatedAt: k.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if k.DisabledAt != nil {
			s := k.DisabledAt.UTC().Format("2006-01-02T15:04:05Z")
			kr.DisabledAt = &s
		}
		resp.Keys = append(resp.Keys, kr)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *keysHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch path {
	case "/v1/keys":
		h.handleRegister(w, r)
	case "/v1/keys/disable":
		h.handleSetEnabled(w, r, false)
	case "/v1/keys/enable":
		h.handleSetEnabled(w, r, true)
	default:
		writeError(w, http.StatusNotFound, "unknown keys endpoint")
	}
}

func (h *keysHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Tenant == "" || req.KeyName == "" {
		writeError(w, http.StatusBadRequest, "tenant and key_name are required")
		return
	}
	if req.Source == "" {
		req.Source = "manual"
	}

	if err := h.store.RegisterKey(r.Context(), req.Tenant, req.KeyName, req.Source); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"status":   "registered",
		"tenant":   req.Tenant,
		"key_name": req.KeyName,
	})
}

func (h *keysHandler) handleSetEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	var req keyActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Tenant == "" || req.KeyName == "" {
		writeError(w, http.StatusBadRequest, "tenant and key_name are required")
		return
	}

	if err := h.store.SetKeyEnabled(r.Context(), req.Tenant, req.KeyName, enabled); err != nil {
		if errors.Is(err, ledger.ErrKeyNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	action := "disabled"
	if enabled {
		action = "enabled"
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":   action,
		"tenant":   req.Tenant,
		"key_name": req.KeyName,
	})
}
