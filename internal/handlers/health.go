package handlers

import (
	"net/http"
	"time"

	"chat-ui-go-backend/internal/types"
)

func Health(w http.ResponseWriter, r *http.Request) {
	types.WriteJSON(w, http.StatusOK, types.HealthResponse{
		Status: "ok",
		Time:   time.Now().UTC(),
	})
}
