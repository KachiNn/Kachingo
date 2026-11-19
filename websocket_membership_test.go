package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketStopsBroadcastAfterMemberRemoval(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/ws-membership.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	hub := newWSHub(sqliteAuthRepository{db: db})
	go hub.run()
	v1 := &v1Server{db: db, wsHub: hub}
	handler := v1.routes()
	ownerToken := registerUser(t, handler, "ws-membership-owner@example.com")
	memberToken := registerUser(t, handler, "ws-membership-member@example.com")

	created := callAPI(handler, http.MethodPost, "/api/v1/projects", ownerToken, apiJSON(t, map[string]string{"name": "Membership"}))
	if created.Code != http.StatusCreated {
		t.Fatalf("create project status=%d body=%s", created.Code, created.Body.String())
	}
	var p project
	decodeResponse(t, created, &p)
	projectPath := "/api/v1/projects/" + strconv.FormatInt(p.ID, 10)
	if added := callAPI(handler, http.MethodPost, projectPath+"/members", ownerToken, apiJSON(t, map[string]string{"email": "ws-membership-member@example.com", "role": "viewer"})); added.Code != http.StatusCreated {
		t.Fatalf("add member status=%d body=%s", added.Code, added.Body.String())
	}
	member, err := sqliteAuthRepository{db: db}.UserForSession(context.Background(), tokenDigest(memberToken), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(v1.wsHandler(hub))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ownerConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?token="+ownerToken, nil)
	if err != nil {
		t.Fatalf("owner dial failed: %v", err)
	}
	defer ownerConn.Close()
	memberConn, _, err := websocket.DefaultDialer.Dial(wsURL+"?token="+memberToken, nil)
	if err != nil {
		t.Fatalf("member dial failed: %v", err)
	}
	defer memberConn.Close()
	time.Sleep(50 * time.Millisecond)

	removePath := projectPath + "/members/" + strconv.FormatInt(member.ID, 10)
	if removed := callAPI(handler, http.MethodDelete, removePath, ownerToken, ""); removed.Code != http.StatusNoContent {
		t.Fatalf("remove member status=%d body=%s", removed.Code, removed.Body.String())
	}

	task := callAPI(handler, http.MethodPost, projectPath+"/tasks", ownerToken, apiJSON(t, map[string]string{"title": "after removal"}))
	if task.Code != http.StatusCreated {
		t.Fatalf("create task status=%d body=%s", task.Code, task.Body.String())
	}

	ownerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var ownerMsg wsMessage
	if err := ownerConn.ReadJSON(&ownerMsg); err != nil {
		t.Fatalf("remaining member did not receive event: %v", err)
	}
	if ownerMsg.Type != "task_created" || ownerMsg.ProjectID != p.ID {
		t.Fatalf("unexpected owner event: %+v", ownerMsg)
	}

	memberConn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, _, err := memberConn.ReadMessage(); err == nil {
		t.Fatal("removed member received a project event")
	}
}
