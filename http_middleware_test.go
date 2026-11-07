package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestLoggingAddsRequestID(t *testing.T) {
	handler := requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Request-ID") != "client-trace-1" {
			t.Fatal("request id header not preserved")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-ID", "client-trace-1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d", recorder.Code)
	}
	if recorder.Header().Get("X-Request-ID") != "client-trace-1" {
		t.Fatalf("response request id=%q", recorder.Header().Get("X-Request-ID"))
	}
}

func TestRequestLoggingReplacesOversizedRequestID(t *testing.T) {
	handler := requestLogging(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", string(make([]byte, 128)))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	id := recorder.Header().Get("X-Request-ID")
	if len(id) != 32 {
		t.Fatalf("generated request id length=%d, want 32", len(id))
	}
}
