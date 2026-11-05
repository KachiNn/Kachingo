package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTaskLifecycle(t *testing.T) {
	store := newStore()
	handler := taskHandler(store)
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(`{"title":"Ship feature"}`)))
	if created.Code != http.StatusCreated { t.Fatalf("create status = %d, want %d", created.Code, http.StatusCreated) }
	var task Task
	if err := json.Unmarshal(created.Body.Bytes(), &task); err != nil { t.Fatal(err) }
	if task.ID != 1 || task.Title != "Ship feature" { t.Fatalf("unexpected created task: %+v", task) }

	updated := httptest.NewRecorder()
	handler.ServeHTTP(updated, httptest.NewRequest(http.MethodPatch, "/api/tasks/1", bytes.NewBufferString(`{"done":true}`)))
	if updated.Code != http.StatusOK { t.Fatalf("update status = %d, want %d", updated.Code, http.StatusOK) }
	if err := json.Unmarshal(updated.Body.Bytes(), &task); err != nil { t.Fatal(err) }
	if !task.Done { t.Fatal("task should be marked done") }

	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/api/tasks/1", nil))
	if deleted.Code != http.StatusNoContent { t.Fatalf("delete status = %d, want %d", deleted.Code, http.StatusNoContent) }
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodPatch, "/api/tasks/1", bytes.NewBufferString(`{"done":false}`)))
	if missing.Code != http.StatusNotFound { t.Fatalf("missing status = %d, want %d", missing.Code, http.StatusNotFound) }
}

func TestCreateRejectsBlankTitle(t *testing.T) {
	recorder := httptest.NewRecorder()
	taskHandler(newStore()).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(`{"title":"  "}`)))
	if recorder.Code != http.StatusBadRequest { t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest) }
}
