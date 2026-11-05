package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var errTaskNotFound = errors.New("task not found")

type Task struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

type diskState struct {
	NextID int          `json:"next_id"`
	Tasks  map[int]Task `json:"tasks"`
}

type Store struct {
	mu     sync.RWMutex
	path   string
	nextID int
	tasks  map[int]Task
}

func newStore(path string) (*Store, error) {
	s := &Store{path: path, nextID: 1, tasks: make(map[int]Task)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var state diskState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.Tasks == nil {
		state.Tasks = make(map[int]Task)
	}
	maxID := 0
	for id := range state.Tasks {
		if id > maxID {
			maxID = id
		}
	}
	s.tasks = state.Tasks
	s.nextID = state.NextID
	if s.nextID <= maxID {
		s.nextID = maxID + 1
	}
	if s.nextID < 1 {
		s.nextID = 1
	}
	return s, nil
}

func (s *Store) persistLocked() error {
	data, err := json.MarshalIndent(diskState{NextID: s.nextID, Tasks: s.tasks}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tasks-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

func (s *Store) list() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) create(title string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := Task{ID: s.nextID, Title: title, CreatedAt: time.Now().UTC()}
	s.tasks[task.ID] = task
	s.nextID++
	if err := s.persistLocked(); err != nil {
		delete(s.tasks, task.ID)
		s.nextID--
		return Task{}, err
	}
	return task, nil
}

func (s *Store) update(id int, title *string, done *bool) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return Task{}, errTaskNotFound
	}
	previous := task
	if title != nil {
		task.Title = *title
	}
	if done != nil {
		task.Done = *done
	}
	s.tasks[id] = task
	if err := s.persistLocked(); err != nil {
		s.tasks[id] = previous
		return Task{}, err
	}
	return task, nil
}

func (s *Store) delete(id int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return false, nil
	}
	delete(s.tasks, id)
	if err := s.persistLocked(); err != nil {
		s.tasks[id] = task
		return false, err
	}
	return true, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
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
				var input struct {
					Title string `json:"title"`
				}
				if err := decodeJSON(w, r, &input); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是有效的单个 JSON 对象"})
					return
				}
				input.Title = strings.TrimSpace(input.Title)
				if input.Title == "" {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "任务标题不能为空"})
					return
				}
				task, err := store.create(input.Title)
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存任务失败"})
					return
				}
				writeJSON(w, http.StatusCreated, task)
			default:
				w.Header().Set("Allow", "GET, POST")
				writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "不支持的请求方法"})
			}
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/tasks/") {
			http.NotFound(w, r)
			return
		}
		id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/tasks/"))
		if err != nil || id < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "任务 ID 无效"})
			return
		}
		switch r.Method {
		case http.MethodPatch:
			var input struct {
				Title *string `json:"title"`
				Done  *bool   `json:"done"`
			}
			if err := decodeJSON(w, r, &input); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是有效的单个 JSON 对象"})
				return
			}
			if input.Title == nil && input.Done == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请至少提供 title 或 done 字段"})
				return
			}
			if input.Title != nil {
				trimmed := strings.TrimSpace(*input.Title)
				if trimmed == "" {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "任务标题不能为空"})
					return
				}
				input.Title = &trimmed
			}
			task, err := store.update(id, input.Title, input.Done)
			if errors.Is(err, errTaskNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
				return
			}
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存任务失败"})
				return
			}
			writeJSON(w, http.StatusOK, task)
		case http.MethodDelete:
			deleted, err := store.delete(id)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存任务失败"})
				return
			}
			if !deleted {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "PATCH, DELETE")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "不支持的请求方法"})
		}
	}
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	dataFile := os.Getenv("DATA_FILE")
	if dataFile == "" {
		dataFile = "data/tasks.json"
	}
	store, err := newStore(dataFile)
	if err != nil {
		log.Fatalf("加载任务数据失败（%s）：%v", dataFile, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/tasks", taskHandler(store))
	mux.HandleFunc("/api/tasks/", taskHandler(store))
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stopped
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	log.Printf("任务 API 正在监听 %s（数据文件：%s）", addr, dataFile)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
