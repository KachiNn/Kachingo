package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := newStore(filepath.Join(t.TempDir(), "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestTaskLifecycle(t *testing.T) {
	store := testStore(t)
	handler := taskHandler(store)
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(`{"title":"Ship feature"}`)))
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", created.Code, http.StatusCreated)
	}
	var task Task
	if err := json.Unmarshal(created.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.ID != 1 || task.Title != "Ship feature" {
		t.Fatalf("unexpected created task: %+v", task)
	}

	updated := httptest.NewRecorder()
	handler.ServeHTTP(updated, httptest.NewRequest(http.MethodPatch, "/api/tasks/1", bytes.NewBufferString(`{"done":true}`)))
	if updated.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d", updated.Code, http.StatusOK)
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if !task.Done {
		t.Fatal("task should be marked done")
	}

	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/api/tasks/1", nil))
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", deleted.Code, http.StatusNoContent)
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodPatch, "/api/tasks/1", bytes.NewBufferString(`{"done":false}`)))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want %d", missing.Code, http.StatusNotFound)
	}
}

func TestDataSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	store, err := newStore(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.create("持久化任务")
	if err != nil {
		t.Fatal(err)
	}
	done := true
	if _, err := store.update(created.ID, nil, &done); err != nil {
		t.Fatal(err)
	}
	reopened, err := newStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tasks := reopened.list()
	if len(tasks) != 1 || tasks[0].Title != "持久化任务" || !tasks[0].Done {
		t.Fatalf("persisted tasks = %+v", tasks)
	}
	second, err := reopened.create("第二项")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID <= created.ID {
		t.Fatalf("new id = %d, want greater than %d", second.ID, created.ID)
	}
}

func TestCreateRejectsInvalidJSON(t *testing.T) {
	for _, body := range []string{`{"title":"valid"} {"title":"extra"}`, `{"title":`} {
		recorder := httptest.NewRecorder()
		taskHandler(testStore(t)).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/tasks", bytes.NewBufferString(body)))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want %d", body, recorder.Code, http.StatusBadRequest)
		}
	}
}

func TestCreateRejectsOversizedBody(t *testing.T) {
	recorder := httptest.NewRecorder()
	body := strings.Repeat(" ", (1<<20)+1)
	taskHandler(testStore(t)).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}
