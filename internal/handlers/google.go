package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"chat-ui-go-backend/internal/types"
)

type GoogleHandler struct {
	apiKey         string
	baseURL        string
	requestTimeout time.Duration
	streamTimeout  time.Duration
	client         *http.Client
}

func NewGoogleHandler(apiKey, baseURL string, requestTimeout, streamTimeout time.Duration) *GoogleHandler {
	return &GoogleHandler{
		apiKey:         apiKey,
		baseURL:        strings.TrimRight(baseURL, "/"),
		requestTimeout: requestTimeout,
		streamTimeout:  streamTimeout,
		client: &http.Client{
			Timeout: streamTimeout,
		},
	}
}

func (h *GoogleHandler) Proxy(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		types.WriteError(w, r, http.StatusServiceUnavailable, "google studio is not configured")
		return
	}

	timeout := h.requestTimeout
	if strings.Contains(r.URL.Path, ":streamGenerateContent") {
		timeout = h.streamTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	path := strings.TrimPrefix(r.URL.Path, "/v1/google")
	target := h.baseURL + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, target, r.Body)
	if err != nil {
		types.WriteError(w, r, http.StatusBadRequest, "invalid google request")
		return
	}
	copySafeHeaders(req.Header, r.Header)
	req.Header.Set("x-goog-api-key", h.apiKey)
	if r.Method == http.MethodPost && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := h.client.Do(req)
	if err != nil {
		types.WriteError(w, r, http.StatusBadGateway, "google studio request failed")
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	flusher, canFlush := w.(http.Flusher)
	buffer := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func copySafeHeaders(dst, src http.Header) {
	for key, values := range src {
		lower := strings.ToLower(key)
		if lower == "authorization" || lower == "host" || lower == "content-length" || strings.HasPrefix(lower, "x-goog-") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		lower := strings.ToLower(key)
		if lower == "content-length" || lower == "transfer-encoding" {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
