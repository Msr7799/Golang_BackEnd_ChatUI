package middleware

import (
	"net/http"
	"sync"
	"time"

	"chat-ui-go-backend/internal/auth"
	"chat-ui-go-backend/internal/types"
)

type userWindow struct {
	start time.Time
	count int
}

type RateLimiter struct {
	mu       sync.Mutex
	limit    int
	windows  map[string]userWindow
	interval time.Duration
}

func NewRateLimiter(limitPerMinute int) *RateLimiter {
	return &RateLimiter{
		limit:    limitPerMinute,
		windows:  map[string]userWindow{},
		interval: time.Minute,
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok || user.UID == "" {
			types.WriteError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}

		if !rl.allow(user.UID) {
			w.Header().Set("Retry-After", "60")
			types.WriteError(w, r, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) allow(uid string) bool {
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	window := rl.windows[uid]
	if window.start.IsZero() || now.Sub(window.start) >= rl.interval {
		rl.windows[uid] = userWindow{start: now, count: 1}
		return true
	}

	if window.count >= rl.limit {
		return false
	}

	window.count++
	rl.windows[uid] = window
	return true
}
