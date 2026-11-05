package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Task struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	mu     sync.RWMutex
	nextID int
	tasks  map[int]Task
}

func newStore() *Store { return &Store{nextID: 1, tasks: make(map[int]Task)} }

func (s *Store) list() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks { out = append(out, task) }
	return out
}

func (s *Store) create(title string) Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := Task{ID: s.nextID, Title: title, CreatedAt: time.Now().UTC()}
	s.nextID++
	s.tasks[task.ID] = task
	return task
}

func (s *Store) update(id int, title *string, done *bool) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok { return Task{}, errors.New("task not found") }
	if title != nil { task.Title = *title }
	if done != nil { task.Done = *done }
	s.tasks[id] = task
	return task, nil
}

func (s *Store) delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok { return false }
	delete(s.tasks, id)
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func taskHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tasks" {
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, http.StatusOK, store.list())
			case http.MethodPost:
				var input struct { Title string `json:"title"` }
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"}); return
				}
				input.Title = strings.TrimSpace(input.Title)
				if input.Title == "" { writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title is required"}); return }
				writeJSON(w, http.StatusCreated, store.create(input.Title))
			default:
				w.Header().Set("Allow", "GET, POST")
				writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			}
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/tasks/") { http.NotFound(w, r); return }
		id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/tasks/"))
		if err != nil || id < 1 { writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid task id"}); return }
		switch r.Method {
		case http.MethodPatch:
			var input struct { Title *string `json:"title"`; Done *bool `json:"done"` }
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"}); return
			}
			if input.Title == nil && input.Done == nil { writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provide title or done"}); return }
			if input.Title != nil { trimmed := strings.TrimSpace(*input.Title); if trimmed == "" { writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title cannot be empty"}); return }; input.Title = &trimmed }
			task, err := store.update(id, input.Title, input.Done)
			if err != nil { writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"}); return }
			writeJSON(w, http.StatusOK, task)
		case http.MethodDelete:
			if !store.delete(id) { writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"}); return }
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "PATCH, DELETE")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	}
}

func main() {
	mux := http.NewServeMux()
	store := newStore()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]string{"status": "ok"}) })
	mux.HandleFunc("/api/tasks", taskHandler(store))
	mux.HandleFunc("/api/tasks/", taskHandler(store))
	addr := os.Getenv("ADDR")
	if addr == "" { addr = ":8080" }
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-stopped; ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second); defer cancel(); _ = server.Shutdown(ctx) }()
	log.Printf("task API listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed { log.Fatal(err) }
}
