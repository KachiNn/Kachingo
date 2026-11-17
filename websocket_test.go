package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketAuth(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/ws-auth.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := newWSHub(sqliteAuthRepository{db: db})
	go hub.run()
	v1 := &v1Server{db: db, wsHub: hub}
	handler := v1.wsHandler(hub)
	ownerToken := registerUser(t, v1.routes(), "ws-owner@example.com")
	
	t.Run("missing token", func(t *testing.T) {
		server := httptest.NewServer(handler)
		defer server.Close()
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
		_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err == nil {
			t.Fatal("expected dial to fail without token")
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status=%d, want 401", resp.StatusCode)
		}
	})
	
	t.Run("invalid token", func(t *testing.T) {
		server := httptest.NewServer(handler)
		defer server.Close()
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?token=invalid-token-123"
		_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err == nil {
			t.Fatal("expected dial to fail with invalid token")
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status=%d, want 401", resp.StatusCode)
		}
	})
	
	t.Run("valid token", func(t *testing.T) {
		server := httptest.NewServer(handler)
		defer server.Close()
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?token=" + ownerToken
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		defer conn.Close()
		if resp.StatusCode != http.StatusSwitchingProtocols {
			t.Errorf("status=%d, want 101", resp.StatusCode)
		}
	})
}

func TestWebSocketBroadcast(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/ws-broadcast.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := newWSHub(sqliteAuthRepository{db: db})
	go hub.run()
	v1 := &v1Server{db: db, wsHub: hub}
	
	ownerToken := registerUser(t, v1.routes(), "ws-broadcast-owner@example.com")
	viewerToken := registerUser(t, v1.routes(), "ws-broadcast-viewer@example.com")
	
	createResp := callAPI(v1.routes(), http.MethodPost, "/api/v1/projects", ownerToken, apiJSON(t, map[string]string{"name": "WebSocket Test"}))
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create project failed: %d", createResp.Code)
	}
	var project struct{ ID int64 `json:"id"` }
	decodeResponse(t, createResp, &project)
	
	addResp := callAPI(v1.routes(), http.MethodPost, "/api/v1/projects/1/members", ownerToken, apiJSON(t, map[string]any{"email": "ws-broadcast-viewer@example.com", "role": "viewer"}))
	if addResp.Code != http.StatusCreated {
		t.Fatalf("add member failed: %d", addResp.Code)
	}
	
	server := httptest.NewServer(v1.wsHandler(hub))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	
	ownerConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?token="+ownerToken, nil)
	if err != nil {
		t.Fatalf("owner dial failed: %v", err)
	}
	defer ownerConn.Close()
	
	viewerConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?token="+viewerToken, nil)
	if err != nil {
		t.Fatalf("viewer dial failed: %v", err)
	}
	defer viewerConn.Close()
	
	time.Sleep(100 * time.Millisecond)
	
	taskResp := callAPI(v1.routes(), http.MethodPost, "/api/v1/projects/1/tasks", ownerToken, apiJSON(t, map[string]string{"title": "WebSocket task"}))
	if taskResp.Code != http.StatusCreated {
		t.Fatalf("create task failed: %d", taskResp.Code)
	}
	
	ownerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var ownerMsg wsMessage
	if err := ownerConn.ReadJSON(&ownerMsg); err != nil {
		t.Fatalf("owner read message failed: %v", err)
	}
	if ownerMsg.Type != "task_created" {
		t.Errorf("owner message type=%s, want task_created", ownerMsg.Type)
	}
	if ownerMsg.ProjectID != project.ID {
		t.Errorf("owner message project_id=%d, want %d", ownerMsg.ProjectID, project.ID)
	}
	
	viewerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var viewerMsg wsMessage
	if err := viewerConn.ReadJSON(&viewerMsg); err != nil {
		t.Fatalf("viewer read message failed: %v", err)
	}
	if viewerMsg.Type != "task_created" {
		t.Errorf("viewer message type=%s, want task_created", viewerMsg.Type)
	}
}

func TestWebSocketTaskUpdate(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/ws-update.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := newWSHub(sqliteAuthRepository{db: db})
	go hub.run()
	v1 := &v1Server{db: db, wsHub: hub}
	
	ownerToken := registerUser(t, v1.routes(), "ws-update-owner@example.com")
	
	callAPI(v1.routes(), http.MethodPost, "/api/v1/projects", ownerToken, apiJSON(t, map[string]string{"name": "Update Test"}))
	taskResp := callAPI(v1.routes(), http.MethodPost, "/api/v1/projects/1/tasks", ownerToken, apiJSON(t, map[string]string{"title": "Initial"}))
	var task struct {
		ID      int64 `json:"id"`
		Version int64 `json:"version"`
	}
	decodeResponse(t, taskResp, &task)
	
	server := httptest.NewServer(v1.wsHandler(hub))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?token=" + ownerToken
	
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()
	
	time.Sleep(100 * time.Millisecond)
	
	callAPI(v1.routes(), http.MethodPatch, "/api/v1/projects/1/tasks/1", ownerToken, apiJSON(t, map[string]any{"title": "Updated", "version": task.Version}))
	
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg wsMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read message failed: %v", err)
	}
	if msg.Type != "task_updated" {
		t.Errorf("message type=%s, want task_updated", msg.Type)
	}
}

func TestWebSocketCommentCreated(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/ws-comment.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := newWSHub(sqliteAuthRepository{db: db})
	go hub.run()
	v1 := &v1Server{db: db, wsHub: hub}
	
	ownerToken := registerUser(t, v1.routes(), "ws-comment-owner@example.com")
	
	callAPI(v1.routes(), http.MethodPost, "/api/v1/projects", ownerToken, apiJSON(t, map[string]string{"name": "Comment Test"}))
	callAPI(v1.routes(), http.MethodPost, "/api/v1/projects/1/tasks", ownerToken, apiJSON(t, map[string]string{"title": "Task"}))
	
	server := httptest.NewServer(v1.wsHandler(hub))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?token=" + ownerToken
	
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()
	
	time.Sleep(100 * time.Millisecond)
	
	callAPI(v1.routes(), http.MethodPost, "/api/v1/projects/1/tasks/1/comments", ownerToken, apiJSON(t, map[string]string{"body": "Test comment"}))
	
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg wsMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read message failed: %v", err)
	}
	if msg.Type != "comment_created" {
		t.Errorf("message type=%s, want comment_created", msg.Type)
	}
}
