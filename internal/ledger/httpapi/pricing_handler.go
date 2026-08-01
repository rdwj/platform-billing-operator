package httpapi

import "net/http"

type pricingHandler struct{}

func (h *pricingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeError(w, http.StatusNotImplemented, "pricing endpoint not yet implemented")
}
