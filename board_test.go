package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type boardTaskRepoStub struct {
	projectTaskRepository
	err error
}

func (s boardTaskRepoStub) Board(context.Context, int64, int) ([]boardColumn, error) {
	return nil, s.err
}

type boardProjectRepoStub struct {
	projectRepository
	role string
	err  error
}

func (s boardProjectRepoStub) Role(context.Context, int64, int64) (string, error) {
	return s.role, s.err
}

func TestBoardEndpoint(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/board.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &v1Server{db: db}
	h := s.routes()
	owner := registerUser(t, h, "board-owner@example.com")
	created := callAPI(h, http.MethodPost, "/api/v1/projects", owner, apiJSON(t, map[string]string{"name": "Board"}))
	if created.Code != http.StatusCreated {
		t.Fatalf("project status=%d", created.Code)
	}
	for _, title := range []string{"one", "two", "three"} {
		resp := callAPI(h, http.MethodPost, "/api/v1/projects/1/tasks", owner, apiJSON(t, map[string]string{"title": title}))
		if resp.Code != http.StatusCreated {
			t.Fatalf("task status=%d", resp.Code)
		}
	}
	resp := callAPI(h, http.MethodGet, "/api/v1/projects/1/board?limit=2", owner, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("board status=%d body=%s", resp.Code, resp.Body.String())
	}
	var columns []boardColumn
	decodeResponse(t, resp, &columns)
	if len(columns) != 3 || columns[0].Status != "open" || columns[0].Total != 3 || len(columns[0].Items) != 2 {
		t.Fatalf("unexpected board: %+v", columns)
	}
	if columns[1].Status != "in_progress" || columns[2].Status != "done" || len(columns[1].Items) != 0 || len(columns[2].Items) != 0 {
		t.Fatalf("unexpected empty columns: %+v", columns)
	}
	if got := callAPI(h, http.MethodGet, "/api/v1/projects/1/board?limit=0", owner, "").Code; got != http.StatusBadRequest {
		t.Fatalf("limit status=%d", got)
	}
	outsider := registerUser(t, h, "board-outsider@example.com")
	if got := callAPI(h, http.MethodGet, "/api/v1/projects/1/board", outsider, "").Code; got != http.StatusNotFound {
		t.Fatalf("outsider status=%d", got)
	}
}

func TestBoardEndpointDatabaseError(t *testing.T) {
	s := &v1Server{projects: boardProjectRepoStub{role: "owner"}, tasks: boardTaskRepoStub{err: errors.New("boom")}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/1/board", nil)
	req.SetPathValue("projectID", "1")
	rec := httptest.NewRecorder()
	s.board(rec, req, identity{ID: 1})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
