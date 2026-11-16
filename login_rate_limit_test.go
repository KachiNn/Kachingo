package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginRateLimiterBlocksAfterFailuresAndResets(t *testing.T) {
	limiter := newLoginRateLimiter(2, time.Minute)
	limiter.now = func() time.Time { return time.Unix(100, 0) }
	handler := loginRateLimitHandler(limiter, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/login", nil)
		req.RemoteAddr = "192.0.2.10:1234"
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status=%d", i, recorder.Code)
		}
		limiter.failed("192.0.2.10")
	}
	blocked := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "192.0.2.10:9999"
	handler.ServeHTTP(blocked, request)
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") != "60" {
		t.Fatalf("blocked status=%d retry=%q", blocked.Code, blocked.Header().Get("Retry-After"))
	}
	otherIP := httptest.NewRecorder()
	otherRequest := httptest.NewRequest(http.MethodPost, "/login", nil)
	otherRequest.RemoteAddr = "192.0.2.11:1234"
	handler.ServeHTTP(otherIP, otherRequest)
	if otherIP.Code != http.StatusUnauthorized {
		t.Fatalf("other IP status=%d", otherIP.Code)
	}
	limiter.succeeded("192.0.2.10")
	allowed, _ := limiter.allow("192.0.2.10")
	if !allowed {
		t.Fatal("successful login did not reset rate limit")
	}
}

func TestLoginRateLimiterReservationsAreConcurrentSafe(t *testing.T) {
	limiter := newLoginRateLimiter(20, time.Minute)
	var wg sync.WaitGroup
	var allowed atomic.Int32
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := limiter.allow("client"); ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 20 {
		t.Fatalf("allowed concurrent attempts=%d, want 20", got)
	}
}

func TestLoginRateLimiterWindowExpires(t *testing.T) {
	limiter := newLoginRateLimiter(1, time.Minute)
	now := time.Unix(100, 0)
	limiter.now = func() time.Time { return now }
	limiter.failed("client")
	if allowed, _ := limiter.allow("client"); allowed {
		t.Fatal("limit should block after failure")
	}
	now = now.Add(time.Minute)
	if allowed, retry := limiter.allow("client"); !allowed || retry != 0 {
		t.Fatalf("expired window allowed=%t retry=%s", allowed, retry)
	}
	if len(limiter.entries) != 1 {
		t.Fatalf("expired entries were not pruned: %d", len(limiter.entries))
	}
}

func TestParseLoginRateLimit(t *testing.T) {
	for input, want := range map[string]int{"": 10, "1": 1, "10000": 10000} {
		got, err := parseLoginRateLimit(input)
		if err != nil || got != want {
			t.Fatalf("parse %q = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"0", "10001", "bad"} {
		if _, err := parseLoginRateLimit(input); err == nil {
			t.Fatalf("parse %q unexpectedly succeeded", input)
		}
	}
}

func TestRetryAfterSecondsRoundsUp(t *testing.T) {
	for duration, want := range map[time.Duration]int{0: 1, time.Second: 1, time.Second + time.Nanosecond: 2, 10 * time.Second: 10} {
		if got := retryAfterSeconds(duration); got != want {
			t.Fatalf("retry seconds for %s = %s, want %s", duration, strconv.Itoa(got), strconv.Itoa(want))
		}
	}
}
