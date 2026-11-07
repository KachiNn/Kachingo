package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadinessRequiresDatabase(t *testing.T) {
	db, err := openDatabase(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	handler := readinessHandler(db)
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("ready status=%d body=%s", ready.Code, ready.Body.String())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	notReady := httptest.NewRecorder()
	handler.ServeHTTP(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed database status=%d body=%s", notReady.Code, notReady.Body.String())
	}
}
