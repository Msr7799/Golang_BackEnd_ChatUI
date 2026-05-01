package handlers

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"chat-ui-go-backend/internal/types"
)

type TavilyHandler struct {
	apiKey         string
	mcpURL         string
	requestTimeout time.Duration
	client         *http.Client
}

func NewTavilyHandler(apiKey, mcpURL string, requestTimeout time.Duration) *TavilyHandler {
	return &TavilyHandler{
		apiKey:         apiKey,
		mcpURL:         mcpURL,
		requestTimeout: requestTimeout,
		client:         &http.Client{Timeout: requestTimeout},
	}
}

func (h *TavilyHandler) MCP(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		types.WriteError(w, r, http.StatusServiceUnavailable, "tavily is not configured")
		return
	}

	target, err := h.targetURL()
	if err != nil {
		types.WriteError(w, r, http.StatusInternalServerError, "invalid tavily configuration")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, r.Method, target, r.Body)
	if err != nil {
		types.WriteError(w, r, http.StatusBadRequest, "invalid tavily request")
		return
	}
	copySafeHeaders(req.Header, r.Header)
	// حماية مهمة: لا نضع Tavily API key داخل URL حتى لا يظهر في logs البروكسي.
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := h.client.Do(req)
	if err != nil {
		types.WriteError(w, r, http.StatusBadGateway, "tavily request failed")
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
			if readErr != io.EOF {
				return
			}
			return
		}
	}
}

func (h *TavilyHandler) targetURL() (string, error) {
	raw := strings.TrimSpace(h.mcpURL)
	if raw == "" {
		raw = "https://mcp.tavily.com/mcp/"
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Del("tavilyApiKey")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
