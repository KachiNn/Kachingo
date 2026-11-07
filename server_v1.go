package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type v1Server struct{ db *sql.DB }

func (s *v1Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/register", s.register)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.auth(s.logout))
	mux.HandleFunc("GET /api/v1/me", s.auth(s.me))
	mux.HandleFunc("GET /api/v1/projects", s.auth(s.listProjects))
	mux.HandleFunc("POST /api/v1/projects", s.auth(s.createProject))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/members", s.auth(s.addMember))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/tasks", s.auth(s.listTasks))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/tasks", s.auth(s.createTask))
	mux.HandleFunc("PATCH /api/v1/projects/{projectID}/tasks/{taskID}", s.auth(s.updateTask))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/tasks/{taskID}", s.auth(s.deleteTask))
	return mux
}

func (s *v1Server) auth(next func(http.ResponseWriter, *http.Request, identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeJSON(w, 401, map[string]string{"error": "bearer token required"})
			return
		}
		var user identity
		err := s.db.QueryRowContext(r.Context(), "SELECT u.id,u.email,u.name FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?", tokenDigest(parts[1]), time.Now().UTC().Format(time.RFC3339Nano)).Scan(&user.ID, &user.Email, &user.Name)
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "invalid or expired session"})
			return
		}
		next(w, r, user)
	}
}
func (s *v1Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if decodeJSON(w, r, &in) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
		return
	}
	email, name, err := validateRegistration(in.Email, in.Name, in.Password)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not secure password"})
		return
	}
	res, err := s.db.ExecContext(r.Context(), "INSERT INTO users(email,name,password_hash) VALUES(?,?,?)", email, name, hash)
	if err != nil {
		writeJSON(w, 409, map[string]string{"error": "email is already registered"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, identity{ID: id, Email: email, Name: name})
}
func (s *v1Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if decodeJSON(w, r, &in) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
		return
	}
	var user identity
	var hash string
	err := s.db.QueryRowContext(r.Context(), "SELECT id,email,name,password_hash FROM users WHERE email=?", strings.TrimSpace(strings.ToLower(in.Email))).Scan(&user.ID, &user.Email, &user.Name, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		writeJSON(w, 401, map[string]string{"error": "invalid email or password"})
		return
	}
	token, digest, err := newSessionToken()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not create session"})
		return
	}
	expires := time.Now().UTC().Add(sessionLifetime).Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(r.Context(), "INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)", digest, user.ID, expires); err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not create session"})
		return
	}
	writeJSON(w, 200, map[string]any{"token": token, "token_type": "Bearer", "expires_at": expires, "user": user})
}
func (s *v1Server) logout(w http.ResponseWriter, r *http.Request, _ identity) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	_, _ = s.db.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash=?", tokenDigest(parts[1]))
	w.WriteHeader(204)
}
func (s *v1Server) me(w http.ResponseWriter, _ *http.Request, user identity) { writeJSON(w, 200, user) }
func (s *v1Server) listProjects(w http.ResponseWriter, r *http.Request, user identity) {
	rows, err := s.db.QueryContext(r.Context(), "SELECT p.id,p.name,m.role,p.created_at FROM projects p JOIN project_members m ON m.project_id=p.id WHERE m.user_id=? ORDER BY p.id", user.ID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	items := make([]project, 0)
	for rows.Next() {
		var p project
		if err := rows.Scan(&p.ID, &p.Name, &p.Role, &p.CreatedAt); err != nil {
			writeJSON(w, 500, map[string]string{"error": "database error"})
			return
		}
		items = append(items, p)
	}
	if rows.Err() != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 200, items)
}
func (s *v1Server) createProject(w http.ResponseWriter, r *http.Request, user identity) {
	var in struct {
		Name string `json:"name"`
	}
	if decodeJSON(w, r, &in) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 120 {
		writeJSON(w, 400, map[string]string{"error": "project name must contain 1 to 120 bytes"})
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), "INSERT INTO projects(name,owner_id) VALUES(?,?)", in.Name, user.ID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	id, _ := res.LastInsertId()
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO project_members(project_id,user_id,role) VALUES(?,?,'owner')", id, user.ID); err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	var p project
	_ = s.db.QueryRowContext(r.Context(), "SELECT id,name,created_at FROM projects WHERE id=?", id).Scan(&p.ID, &p.Name, &p.CreatedAt)
	p.Role = "owner"
	writeJSON(w, 201, p)
}
func parseProjectID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("projectID"), 10, 64)
	return id, err == nil && id > 0
}
func (s *v1Server) role(ctx context.Context, projectID, userID int64) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, "SELECT role FROM project_members WHERE project_id=? AND user_id=?", projectID, userID).Scan(&role)
	return role, err
}
func (s *v1Server) addMember(w http.ResponseWriter, r *http.Request, user identity) {
	pid, ok := parseProjectID(r)
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "invalid project id"})
		return
	}
	role, err := s.role(r.Context(), pid, user.ID)
	if err != nil || role != "owner" {
		writeJSON(w, 404, map[string]string{"error": "project not found"})
		return
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if decodeJSON(w, r, &in) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Role != "editor" && in.Role != "viewer" {
		writeJSON(w, 400, map[string]string{"error": "role must be editor or viewer"})
		return
	}
	var target identity
	err = s.db.QueryRowContext(r.Context(), "SELECT id,email,name FROM users WHERE email=?", in.Email).Scan(&target.ID, &target.Email, &target.Name)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "user not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	if _, err = s.db.ExecContext(r.Context(), "INSERT INTO project_members(project_id,user_id,role) VALUES(?,?,?)", pid, target.ID, in.Role); err != nil {
		writeJSON(w, 409, map[string]string{"error": "user is already a project member"})
		return
	}
	writeJSON(w, 201, map[string]any{"user": target, "role": in.Role})
}
func (s *v1Server) authorize(w http.ResponseWriter, r *http.Request, user identity, write bool) (int64, bool) {
	pid, ok := parseProjectID(r)
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "invalid project id"})
		return 0, false
	}
	role, err := s.role(r.Context(), pid, user.ID)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "project not found"})
		return 0, false
	}
	if write && role == "viewer" {
		writeJSON(w, 403, map[string]string{"error": "viewer role is read-only"})
		return 0, false
	}
	return pid, true
}
func (s *v1Server) listTasks(w http.ResponseWriter, r *http.Request, user identity) {
	pid, ok := s.authorize(w, r, user, false)
	if !ok {
		return
	}
	limit, offset := 50, 0
	var err error
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > 100 {
			writeJSON(w, 400, map[string]string{"error": "limit must be between 1 and 100"})
			return
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		offset, err = strconv.Atoi(v)
		if err != nil || offset < 0 {
			writeJSON(w, 400, map[string]string{"error": "offset must not be negative"})
			return
		}
	}
	status := r.URL.Query().Get("status")
	if status != "" && !validStatus(status) {
		writeJSON(w, 400, map[string]string{"error": "invalid task status"})
		return
	}
	q := "%" + strings.TrimSpace(r.URL.Query().Get("q")) + "%"
	var total int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM project_tasks WHERE project_id=? AND (?='' OR status=?) AND title LIKE ?", pid, status, status, q).Scan(&total); err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT id,project_id,title,description,status,priority,created_at,updated_at FROM project_tasks WHERE project_id=? AND (?='' OR status=?) AND title LIKE ? ORDER BY id LIMIT ? OFFSET ?", pid, status, status, q, limit, offset)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	items := make([]projectTask, 0)
	for rows.Next() {
		var t projectTask
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt); err != nil {
			writeJSON(w, 500, map[string]string{"error": "database error"})
			return
		}
		items = append(items, t)
	}
	if rows.Err() != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}
func (s *v1Server) createTask(w http.ResponseWriter, r *http.Request, user identity) {
	pid, ok := s.authorize(w, r, user, true)
	if !ok {
		return
	}
	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	if decodeJSON(w, r, &in) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || len(in.Title) > 200 {
		writeJSON(w, 400, map[string]string{"error": "title must contain 1 to 200 bytes"})
		return
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	if in.Priority != "low" && in.Priority != "normal" && in.Priority != "high" {
		writeJSON(w, 400, map[string]string{"error": "invalid priority"})
		return
	}
	res, err := s.db.ExecContext(r.Context(), "INSERT INTO project_tasks(project_id,title,description,priority) VALUES(?,?,?,?)", pid, in.Title, in.Description, in.Priority)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	id, _ := res.LastInsertId()
	var t projectTask
	err = s.db.QueryRowContext(r.Context(), "SELECT id,project_id,title,description,status,priority,created_at,updated_at FROM project_tasks WHERE id=?", id).Scan(&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 201, t)
}
func (s *v1Server) updateTask(w http.ResponseWriter, r *http.Request, user identity) {
	pid, ok := s.authorize(w, r, user, true)
	if !ok {
		return
	}
	tid, err := strconv.ParseInt(r.PathValue("taskID"), 10, 64)
	if err != nil || tid < 1 {
		writeJSON(w, 400, map[string]string{"error": "invalid task id"})
		return
	}
	var in struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Status      *string `json:"status"`
		Priority    *string `json:"priority"`
	}
	if decodeJSON(w, r, &in) != nil || in.Title == nil && in.Description == nil && in.Status == nil && in.Priority == nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body or no fields"})
		return
	}
	var t projectTask
	err = s.db.QueryRowContext(r.Context(), "SELECT id,project_id,title,description,status,priority,created_at,updated_at FROM project_tasks WHERE id=? AND project_id=?", tid, pid).Scan(&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	if in.Title != nil {
		t.Title = strings.TrimSpace(*in.Title)
		if t.Title == "" || len(t.Title) > 200 {
			writeJSON(w, 400, map[string]string{"error": "title must contain 1 to 200 bytes"})
			return
		}
	}
	if in.Description != nil {
		t.Description = *in.Description
	}
	if in.Status != nil {
		t.Status = *in.Status
		if !validStatus(t.Status) {
			writeJSON(w, 400, map[string]string{"error": "invalid status"})
			return
		}
	}
	if in.Priority != nil {
		t.Priority = *in.Priority
		if t.Priority != "low" && t.Priority != "normal" && t.Priority != "high" {
			writeJSON(w, 400, map[string]string{"error": "invalid priority"})
			return
		}
	}
	if _, err = s.db.ExecContext(r.Context(), "UPDATE project_tasks SET title=?,description=?,status=?,priority=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND project_id=?", t.Title, t.Description, t.Status, t.Priority, tid, pid); err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	_ = s.db.QueryRowContext(r.Context(), "SELECT updated_at FROM project_tasks WHERE id=?", tid).Scan(&t.UpdatedAt)
	writeJSON(w, 200, t)
}
func (s *v1Server) deleteTask(w http.ResponseWriter, r *http.Request, user identity) {
	pid, ok := s.authorize(w, r, user, true)
	if !ok {
		return
	}
	tid, err := strconv.ParseInt(r.PathValue("taskID"), 10, 64)
	if err != nil || tid < 1 {
		writeJSON(w, 400, map[string]string{"error": "invalid task id"})
		return
	}
	res, err := s.db.ExecContext(r.Context(), "DELETE FROM project_tasks WHERE id=? AND project_id=?", tid, pid)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}
	w.WriteHeader(204)
}
func validStatus(status string) bool {
	return status == "open" || status == "in_progress" || status == "done"
}
