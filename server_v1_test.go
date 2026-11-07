package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func apiJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func callAPI(handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
func decodeResponse(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
	}
}
func registerUser(t *testing.T, handler http.Handler, email string) string {
	t.Helper()
	created := callAPI(handler, http.MethodPost, "/api/v1/auth/register", "", apiJSON(t, map[string]string{"email": email, "name": "Test User", "password": "secure-password-123"}))
	if created.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", created.Code, created.Body.String())
	}
	logged := callAPI(handler, http.MethodPost, "/api/v1/auth/login", "", apiJSON(t, map[string]string{"email": email, "password": "secure-password-123"}))
	if logged.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", logged.Code, logged.Body.String())
	}
	var result struct {
		Token string `json:"token"`
	}
	decodeResponse(t, logged, &result)
	if result.Token == "" {
		t.Fatal("login returned empty token")
	}
	return result.Token
}

func TestProjectCollaborationAuthorization(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/app.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := (&v1Server{db: db}).routes()
	owner := registerUser(t, handler, "owner@example.com")
	var passwordHash string
	if err := db.QueryRow("SELECT password_hash FROM users WHERE email=?", "owner@example.com").Scan(&passwordHash); err != nil {
		t.Fatal(err)
	}
	if passwordHash == "secure-password-123" || !strings.HasPrefix(passwordHash, "$2") {
		t.Fatal("password was not stored as a bcrypt hash")
	}
	viewer := registerUser(t, handler, "viewer@example.com")
	outsider := registerUser(t, handler, "outsider@example.com")
	if got := callAPI(handler, http.MethodGet, "/api/v1/projects", "", "").Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", got)
	}
	created := callAPI(handler, http.MethodPost, "/api/v1/projects", owner, apiJSON(t, map[string]string{"name": "Interview Prep"}))
	if created.Code != http.StatusCreated {
		t.Fatalf("create project status=%d body=%s", created.Code, created.Body.String())
	}
	var p project
	decodeResponse(t, created, &p)
	projectPath := "/api/v1/projects/" + strconv.FormatInt(p.ID, 10)
	added := callAPI(handler, http.MethodPost, projectPath+"/members", owner, apiJSON(t, map[string]string{"email": "viewer@example.com", "role": "viewer"}))
	if added.Code != http.StatusCreated {
		t.Fatalf("add member status=%d body=%s", added.Code, added.Body.String())
	}
	membersPath := projectPath + "/members"
	listedMembers := callAPI(handler, http.MethodGet, membersPath, viewer, "")
	if listedMembers.Code != http.StatusOK {
		t.Fatalf("list members status=%d", listedMembers.Code)
	}
	var members []projectMember
	decodeResponse(t, listedMembers, &members)
	if len(members) != 2 || members[0].Role != "owner" || members[1].Role != "viewer" {
		t.Fatalf("unexpected members: %+v", members)
	}
	removeOwner := callAPI(handler, http.MethodDelete, membersPath+"/"+strconv.FormatInt(p.ID, 10), owner, "")
	if removeOwner.Code != http.StatusConflict {
		t.Fatalf("owner removal status=%d", removeOwner.Code)
	}
	removeViewer := callAPI(handler, http.MethodDelete, membersPath+"/"+strconv.FormatInt(members[1].User.ID, 10), owner, "")
	if removeViewer.Code != http.StatusNoContent {
		t.Fatalf("member removal status=%d body=%s", removeViewer.Code, removeViewer.Body.String())
	}
	removedAccess := callAPI(handler, http.MethodGet, projectPath+"/tasks", viewer, "")
	if removedAccess.Code != http.StatusNotFound {
		t.Fatalf("removed member still has access: status=%d", removedAccess.Code)
	}
	if addedAgain := callAPI(handler, http.MethodPost, membersPath, owner, apiJSON(t, map[string]string{"email": "viewer@example.com", "role": "viewer"})); addedAgain.Code != http.StatusCreated {
		t.Fatalf("re-add member status=%d body=%s", addedAgain.Code, addedAgain.Body.String())
	}
	createdTask := callAPI(handler, http.MethodPost, projectPath+"/tasks", owner, apiJSON(t, map[string]string{"title": "Review concurrency", "priority": "high"}))
	if createdTask.Code != http.StatusCreated {
		t.Fatalf("create task status=%d body=%s", createdTask.Code, createdTask.Body.String())
	}
	var task projectTask
	decodeResponse(t, createdTask, &task)
	listed := callAPI(handler, http.MethodGet, projectPath+"/tasks?status=open&limit=10", viewer, "")
	if listed.Code != http.StatusOK {
		t.Fatalf("viewer list status=%d body=%s", listed.Code, listed.Body.String())
	}
	var page struct {
		Items []projectTask `json:"items"`
		Total int           `json:"total"`
	}
	decodeResponse(t, listed, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("unexpected page: %+v", page)
	}
	taskPath := projectPath + "/tasks/" + strconv.FormatInt(task.ID, 10)
	missingVersion := callAPI(handler, http.MethodPatch, taskPath, owner, apiJSON(t, map[string]string{"status": "done"}))
	if missingVersion.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing version status=%d body=%s", missingVersion.Code, missingVersion.Body.String())
	}
	updateBody := apiJSON(t, map[string]any{"status": "done", "version": task.Version})
	updated := callAPI(handler, http.MethodPatch, taskPath, owner, updateBody)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}
	var current projectTask
	decodeResponse(t, updated, &current)
	if current.Version != task.Version+1 {
		t.Fatalf("updated version=%d, want %d", current.Version, task.Version+1)
	}
	stale := callAPI(handler, http.MethodPatch, taskPath, owner, updateBody)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale update status=%d body=%s", stale.Code, stale.Body.String())
	}
	denied := callAPI(handler, http.MethodPatch, taskPath, viewer, apiJSON(t, map[string]any{"status": "open", "version": current.Version}))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("viewer update status=%d body=%s", denied.Code, denied.Body.String())
	}
	isolated := callAPI(handler, http.MethodGet, projectPath+"/tasks", outsider, "")
	if isolated.Code != http.StatusNotFound {
		t.Fatalf("outsider status=%d", isolated.Code)
	}
	loggedOut := callAPI(handler, http.MethodPost, "/api/v1/auth/logout", viewer, "")
	if loggedOut.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d", loggedOut.Code)
	}
	expired := callAPI(handler, http.MethodGet, "/api/v1/me", viewer, "")
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("logged out session status=%d", expired.Code)
	}
}
