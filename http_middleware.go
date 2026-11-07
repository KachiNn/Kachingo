package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	if recorder.status != 0 {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}
func (recorder *statusRecorder) Write(data []byte) (int, error) {
	if recorder.status == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	n, err := recorder.ResponseWriter.Write(data)
	recorder.bytes += n
	return n, err
}
func (recorder *statusRecorder) Unwrap() http.ResponseWriter { return recorder.ResponseWriter }

func requestID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "unavailable"
	}
	return hex.EncodeToString(data[:])
}
func requestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if len(id) == 0 || len(id) > 64 {
			id = requestID()
		}
		w.Header().Set("X-Request-ID", id)
		recorder := &statusRecorder{ResponseWriter: w}
		started := time.Now()
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("http_request request_id=%q method=%q path=%q status=%d bytes=%d duration_ms=%d", id, r.Method, r.URL.Path, status, recorder.bytes, time.Since(started).Milliseconds())
	})
}
