package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWebSocketHandshakeThroughRequestLogging(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/ws-gateway.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := newWSHub(sqliteAuthRepository{db: db})
	go hub.run()
	v1 := &v1Server{db: db, wsHub: hub}
	token := registerUser(t, v1.routes(), "ws-gateway@example.com")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", v1.wsHandler(hub))
	mux.Handle("/api/v1/", v1.routes())
	server := httptest.NewServer(requestLogging(mux))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial through requestLogging failed: %v", err)
	}
	defer conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status=%d, want 101", resp.StatusCode)
	}
}
