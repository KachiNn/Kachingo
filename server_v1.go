package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

type patchField[T any] struct {
	Present bool
	Value   *T
}

func (field *patchField[T]) UnmarshalJSON(data []byte) error {
	field.Present = true
	if string(data) == "null" {
		field.Value = nil
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	field.Value = &value
	return nil
}

func validateDueDate(value *string) error {
	if value == nil {
		return nil
	}
	parsed, err := time.Parse("2006-01-02", *value)
	if err != nil || parsed.Format("2006-01-02") != *value {
		return errors.New("due_date must use YYYY-MM-DD")
	}
	return nil
}

const dummyBcryptHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

type v1Server struct {
	db       *sql.DB
	authRepo authRepository
	projects projectRepository
	tasks    projectTaskRepository
}

func (s *v1Server) routes() http.Handler {
	if s.authRepo == nil {
		s.authRepo = sqliteAuthRepository{db: s.db}
	}
	if s.projects == nil {
		s.projects = sqliteProjectRepository{db: s.db}
	}
	if s.tasks == nil {
		s.tasks = sqliteProjectTaskRepository{db: s.db}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/register", s.register)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.auth(s.logout))
	mux.HandleFunc("GET /api/v1/me", s.auth(s.me))
	mux.HandleFunc("GET /api/v1/projects", s.auth(s.listProjects))
	mux.HandleFunc("POST /api/v1/projects", s.auth(s.createProject))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/members", s.auth(s.listMembers))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/members", s.auth(s.addMember))
	mux.HandleFunc("DELETE /api/v1/projects/{projectID}/members/{userID}", s.auth(s.removeMember))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/tasks", s.auth(s.listTasks))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/tasks", s.auth(s.createTask))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/tasks/{taskID}", s.auth(s.getTask))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/tasks/{taskID}/events", s.auth(s.listTaskEvents))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/tasks/{taskID}/comments", s.auth(s.listTaskComments))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/tasks/{taskID}/comments", s.auth(s.createTaskComment))
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
		user, err := s.authRepo.UserForSession(r.Context(), tokenDigest(parts[1]), time.Now())
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
	user, err := s.authRepo.CreateUser(r.Context(), email, name, hash)
	if errors.Is(err, errEmailAlreadyRegistered) {
		writeJSON(w, 409, map[string]string{"error": "email is already registered"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 201, user)
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
	user, hash, err := s.authRepo.AccountByEmail(r.Context(), strings.TrimSpace(strings.ToLower(in.Email)))
	if err != nil {
		hash = dummyBcryptHash
	}
	passwordErr := bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password))
	if err != nil || passwordErr != nil {
		writeJSON(w, 401, map[string]string{"error": "invalid email or password"})
		return
	}
	token, digest, err := newSessionToken()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not create session"})
		return
	}
	expires := time.Now().UTC().Add(sessionLifetime)
	if err := s.authRepo.CreateSession(r.Context(), user.ID, digest, expires); err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not create session"})
		return
	}
	writeJSON(w, 200, map[string]any{"token": token, "token_type": "Bearer", "expires_at": expires.Format(time.RFC3339Nano), "user": user})
}
func (s *v1Server) logout(w http.ResponseWriter, r *http.Request, _ identity) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if err := s.authRepo.RevokeSession(r.Context(), tokenDigest(parts[1])); err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not revoke session"})
		return
	}
	w.WriteHeader(204)
}
func (s *v1Server) me(w http.ResponseWriter, _ *http.Request, user identity) { writeJSON(w, 200, user) }
func (s *v1Server) listProjects(w http.ResponseWriter, r *http.Request, user identity) {
	items, err := s.projects.ListForUser(r.Context(), user.ID)
	if err != nil {
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
	p, err := s.projects.Create(r.Context(), user.ID, in.Name)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 201, p)
}
func parseProjectID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("projectID"), 10, 64)
	return id, err == nil && id > 0
}
func (s *v1Server) role(ctx context.Context, projectID, userID int64) (string, error) {
	return s.projects.Role(ctx, projectID, userID)
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
	target, err := s.projects.UserByEmail(r.Context(), in.Email)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "user not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	if err := s.projects.AddMember(r.Context(), pid, target.ID, in.Role); errors.Is(err, errMemberAlreadyAdded) {
		writeJSON(w, 409, map[string]string{"error": "user is already a project member"})
		return
	} else if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 201, map[string]any{"user": target, "role": in.Role})
}
func (s *v1Server) listMembers(w http.ResponseWriter, r *http.Request, user identity) {
	projectID, ok := s.authorize(w, r, user, false)
	if !ok {
		return
	}
	items, err := s.projects.ListMembers(r.Context(), projectID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 200, items)
}

func (s *v1Server) removeMember(w http.ResponseWriter, r *http.Request, user identity) {
	projectID, ok := parseProjectID(r)
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "invalid project id"})
		return
	}
	role, err := s.role(r.Context(), projectID, user.ID)
	if err != nil || role != "owner" {
		writeJSON(w, 404, map[string]string{"error": "project not found"})
		return
	}
	memberID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil || memberID < 1 {
		writeJSON(w, 400, map[string]string{"error": "invalid user id"})
		return
	}
	removed, err := s.projects.RemoveMember(r.Context(), projectID, memberID)
	if errors.Is(err, errProjectOwnerProtected) {
		writeJSON(w, 409, map[string]string{"error": "project owner cannot be removed"})
		return
	}
	if errors.Is(err, sql.ErrNoRows) || !removed && err == nil {
		writeJSON(w, 404, map[string]string{"error": "project member not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	w.WriteHeader(204)
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
	assigneeID := int64(0)
	if raw := r.URL.Query().Get("assignee_id"); raw != "" {
		assigneeID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || assigneeID < 1 {
			writeJSON(w, 400, map[string]string{"error": "invalid assignee_id"})
			return
		}
	}
	unassignedRaw := r.URL.Query().Get("unassigned")
	if unassignedRaw != "" && unassignedRaw != "true" && unassignedRaw != "false" {
		writeJSON(w, 400, map[string]string{"error": "unassigned must be true or false"})
		return
	}
	unassigned := unassignedRaw == "true"
	if assigneeID > 0 && unassigned {
		writeJSON(w, 400, map[string]string{"error": "assignee_id and unassigned cannot be combined"})
		return
	}
	overdueRaw := r.URL.Query().Get("overdue")
	if overdueRaw != "" && overdueRaw != "true" && overdueRaw != "false" {
		writeJSON(w, 400, map[string]string{"error": "overdue must be true or false"})
		return
	}
	overdue := overdueRaw == "true"
	page, err := s.tasks.List(r.Context(), pid, status, strings.TrimSpace(r.URL.Query().Get("q")), assigneeID, unassigned, overdue, limit, offset)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 200, page)
}
func (s *v1Server) createTask(w http.ResponseWriter, r *http.Request, user identity) {
	pid, ok := s.authorize(w, r, user, true)
	if !ok {
		return
	}
	var in struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Priority    string  `json:"priority"`
		AssigneeID  *int64  `json:"assignee_id"`
		DueDate     *string `json:"due_date"`
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
	if in.AssigneeID != nil && *in.AssigneeID < 1 {
		writeJSON(w, 400, map[string]string{"error": "invalid assignee_id"})
		return
	}
	if err := validateDueDate(in.DueDate); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	task, err := s.tasks.Create(r.Context(), pid, user.ID, in.Title, in.Description, in.Priority, in.AssigneeID, in.DueDate)
	if errors.Is(err, errAssigneeNotProjectMember) {
		writeJSON(w, 400, map[string]string{"error": "assignee must be a project member"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 201, task)
}
func (s *v1Server) getTask(w http.ResponseWriter, r *http.Request, user identity) {
	projectID, ok := s.authorize(w, r, user, false)
	if !ok {
		return
	}
	taskID, ok := s.taskIDForRequest(w, r, projectID)
	if !ok {
		return
	}
	task, err := s.tasks.Get(r.Context(), projectID, taskID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 200, task)
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
		Title       *string            `json:"title"`
		Description *string            `json:"description"`
		Status      *string            `json:"status"`
		Priority    *string            `json:"priority"`
		AssigneeID  patchField[int64]  `json:"assignee_id"`
		DueDate     patchField[string] `json:"due_date"`
		Version     *int64             `json:"version"`
	}
	if decodeJSON(w, r, &in) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
		return
	}
	if in.Version == nil || *in.Version < 1 {
		writeJSON(w, 428, map[string]string{"error": "current task version is required"})
		return
	}
	if in.Title == nil && in.Description == nil && in.Status == nil && in.Priority == nil && !in.AssigneeID.Present && !in.DueDate.Present {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body or no fields"})
		return
	}
	t, err := s.tasks.Get(r.Context(), pid, tid)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	previousStatus := t.Status
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
	if in.AssigneeID.Present {
		t.AssigneeID = in.AssigneeID.Value
	}
	if t.AssigneeID != nil && *t.AssigneeID < 1 {
		writeJSON(w, 400, map[string]string{"error": "invalid assignee_id"})
		return
	}
	if in.DueDate.Present {
		t.DueDate = in.DueDate.Value
	}
	if err := validateDueDate(t.DueDate); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	updated, err := s.tasks.Update(r.Context(), t, *in.Version, user.ID, previousStatus)
	if errors.Is(err, errAssigneeNotProjectMember) {
		writeJSON(w, 400, map[string]string{"error": "assignee must be a project member"})
		return
	}
	if errors.Is(err, errTaskVersionConflict) {
		writeJSON(w, 409, map[string]string{"error": "task was changed by another request"})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 200, updated)
}
func (s *v1Server) taskIDForRequest(w http.ResponseWriter, r *http.Request, projectID int64) (int64, bool) {
	taskID, err := strconv.ParseInt(r.PathValue("taskID"), 10, 64)
	if err != nil || taskID < 1 {
		writeJSON(w, 400, map[string]string{"error": "invalid task id"})
		return 0, false
	}
	if _, err := s.tasks.Get(r.Context(), projectID, taskID); errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return 0, false
	} else if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return 0, false
	}
	return taskID, true
}

func (s *v1Server) listTaskComments(w http.ResponseWriter, r *http.Request, user identity) {
	projectID, ok := s.authorize(w, r, user, false)
	if !ok {
		return
	}
	taskID, ok := s.taskIDForRequest(w, r, projectID)
	if !ok {
		return
	}
	comments, err := s.tasks.ListComments(r.Context(), projectID, taskID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 200, comments)
}

func (s *v1Server) createTaskComment(w http.ResponseWriter, r *http.Request, user identity) {
	projectID, ok := s.authorize(w, r, user, true)
	if !ok {
		return
	}
	taskID, ok := s.taskIDForRequest(w, r, projectID)
	if !ok {
		return
	}
	var input struct {
		Body string `json:"body"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
		return
	}
	body := strings.TrimSpace(input.Body)
	if body == "" || utf8.RuneCountInString(body) > 5000 {
		writeJSON(w, 400, map[string]string{"error": "comment must contain 1 to 5000 characters"})
		return
	}
	comment, err := s.tasks.CreateComment(r.Context(), projectID, taskID, user.ID, body)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 201, comment)
}

func (s *v1Server) listTaskEvents(w http.ResponseWriter, r *http.Request, user identity) {
	pid, ok := s.authorize(w, r, user, false)
	if !ok {
		return
	}
	tid, err := strconv.ParseInt(r.PathValue("taskID"), 10, 64)
	if err != nil || tid < 1 {
		writeJSON(w, 400, map[string]string{"error": "invalid task id"})
		return
	}
	if _, err := s.tasks.Get(r.Context(), pid, tid); errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	} else if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	events, err := s.tasks.Events(r.Context(), pid, tid)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	writeJSON(w, 200, events)
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
	deleted, err := s.tasks.Delete(r.Context(), pid, tid)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database error"})
		return
	}
	if !deleted {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}
	w.WriteHeader(204)
}
func validStatus(status string) bool {
	return status == "open" || status == "in_progress" || status == "done"
}
