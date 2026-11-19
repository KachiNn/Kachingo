package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebAssetsServedWithSecurityHeaders(t *testing.T) {
	mux := http.NewServeMux()
	registerWeb(mux)
	cases := []struct{ path, contentType, contains string }{
		{"/", "text/html", "Kachingo"},
		{"/style.css", "text/css", ".board"},
		{"/app.js", "text/javascript", "WebSocket"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d", tc.path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, tc.contentType) {
			t.Fatalf("%s content-type=%q", tc.path, got)
		}
		if !strings.Contains(rec.Body.String(), tc.contains) {
			t.Fatalf("%s body missing %q", tc.path, tc.contains)
		}
		if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s missing security headers: %v", tc.path, rec.Header())
		}
	}
}

func TestWebRoutesDoNotShadowOtherPaths(t *testing.T) {
	mux := http.NewServeMux()
	registerWeb(mux)
	for _, path := range []string{"/unknown", "/web/app.js", "/api/v1/me"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d, want 404", path, rec.Code)
		}
	}
}

func TestWebScriptAvoidsHTMLInjectionSinks(t *testing.T) {
	data, err := webFiles.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if !strings.Contains(script, "comment_created") {
		t.Fatal("app.js must handle comment_created events without rebuilding the board")
	}
	for _, sink := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval("} {
		if strings.Contains(script, sink) {
			t.Fatalf("app.js uses unsafe sink %q; render user content with textContent", sink)
		}
	}
}
