package main

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type loginAttempt struct {
	windowStarted time.Time
	failures      int
	reserved      int
}

type loginRateLimiter struct {
	mu      sync.Mutex
	entries map[string]loginAttempt
	limit   int
	window  time.Duration
	now     func() time.Time
}

func (l *loginRateLimiter) pruneExpired(now time.Time) {
	for key, entry := range l.entries {
		if now.Sub(entry.windowStarted) >= l.window {
			delete(l.entries, key)
		}
	}
}

func newLoginRateLimiter(limit int, window time.Duration) *loginRateLimiter {
	if limit < 1 {
		limit = 10
	}
	if window <= 0 {
		window = time.Minute
	}
	return &loginRateLimiter{entries: make(map[string]loginAttempt), limit: limit, window: window, now: time.Now}
}

func (l *loginRateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneExpired(now)
	entry, ok := l.entries[key]
	if !ok {
		l.entries[key] = loginAttempt{windowStarted: now, reserved: 1}
		return true, 0
	}
	if entry.failures+entry.reserved < l.limit {
		entry.reserved++
		l.entries[key] = entry
		return true, 0
	}
	return false, l.window - now.Sub(entry.windowStarted)
}

func (l *loginRateLimiter) failed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneExpired(now)
	entry, ok := l.entries[key]
	if !ok {
		l.entries[key] = loginAttempt{windowStarted: now, failures: 1}
		return
	}
	if entry.reserved > 0 {
		entry.reserved--
	}
	entry.failures++
	l.entries[key] = entry
}

func (l *loginRateLimiter) succeeded(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

func (l *loginRateLimiter) canceled(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if ok && entry.reserved > 0 {
		entry.reserved--
		l.entries[key] = entry
	}
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func parseLoginRateLimit(value string) (int, error) {
	if value == "" {
		return 10, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 10000 {
		return 0, errors.New("LOGIN_RATE_LIMIT must be an integer from 1 to 10000")
	}
	return limit, nil
}

func retryAfterSeconds(duration time.Duration) int {
	seconds := int((duration + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func loginRateLimitHandler(limiter *loginRateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := clientAddress(r)
		if limiter != nil {
			allowed, retry := limiter.allow(key)
			if !allowed {
				seconds := retryAfterSeconds(retry)
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many login attempts; try again later"})
				return
			}
			defer func() {
				if r.Context().Err() != nil {
					limiter.canceled(key)
				}
			}()
		}
		next(w, r)
	}
}
