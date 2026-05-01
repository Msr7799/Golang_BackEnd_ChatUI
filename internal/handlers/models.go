package handlers

import (
	"context"
	"net/http"
	"time"

	"chat-ui-go-backend/internal/hf"
	"chat-ui-go-backend/internal/types"
)

type ModelsHandler struct {
	hfClient       *hf.Client
	requestTimeout time.Duration
}

func NewModelsHandler(hfClient *hf.Client, requestTimeout time.Duration) *ModelsHandler {
	return &ModelsHandler{hfClient: hfClient, requestTimeout: requestTimeout}
}

func (h *ModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimeout)
	defer cancel()

	status, body, err := h.hfClient.Models(ctx)
	if err != nil {
		types.WriteError(w, r, http.StatusBadGateway, "upstream models request failed")
		return
	}
	if status < 200 || status >= 300 {
		types.WriteError(w, r, http.StatusBadGateway, "upstream models request was rejected")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
