package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	var storedTokenDigest string
	if err := db.QueryRow("SELECT token_hash FROM sessions WHERE user_id=?", 1).Scan(&storedTokenDigest); err != nil {
		t.Fatal(err)
	}
	if storedTokenDigest == owner || len(storedTokenDigest) != 64 {
		t.Fatal("session token was not stored as a SHA-256 digest")
	}
	if duplicate := callAPI(handler, http.MethodPost, "/api/v1/auth/register", "", apiJSON(t, map[string]string{"email": "OWNER@example.com", "name": "Duplicate", "password": "secure-password-123"})); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate email status=%d", duplicate.Code)
	}
	wrongPassword := callAPI(handler, http.MethodPost, "/api/v1/auth/login", "", apiJSON(t, map[string]string{"email": "owner@example.com", "password": "wrong-password-000"}))
	if wrongPassword.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d", wrongPassword.Code)
	}
	if unknownEmail := callAPI(handler, http.MethodPost, "/api/v1/auth/login", "", apiJSON(t, map[string]string{"email": "missing@example.com", "password": "wrong-password-000"})); unknownEmail.Code != http.StatusUnauthorized || unknownEmail.Body.String() != wrongPassword.Body.String() {
		t.Fatalf("unknown email response differs: status=%d body=%s", unknownEmail.Code, unknownEmail.Body.String())
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
	renamed := callAPI(handler, http.MethodPatch, projectPath, owner, apiJSON(t, map[string]string{"name": "Go Interview Prep"}))
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename project status=%d body=%s", renamed.Code, renamed.Body.String())
	}
	var renamedProject project
	decodeResponse(t, renamed, &renamedProject)
	if renamedProject.Name != "Go Interview Prep" || renamedProject.Role != "owner" {
		t.Fatalf("unexpected renamed project: %+v", renamedProject)
	}
	if viewerRename := callAPI(handler, http.MethodPatch, projectPath, viewer, apiJSON(t, map[string]string{"name": "forbidden"})); viewerRename.Code != http.StatusNotFound {
		t.Fatalf("viewer rename status=%d", viewerRename.Code)
	}
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
	memberPath := membersPath + "/" + strconv.FormatInt(members[1].User.ID, 10)
	if viewerRoleChange := callAPI(handler, http.MethodPatch, memberPath, viewer, apiJSON(t, map[string]string{"role": "editor"})); viewerRoleChange.Code != http.StatusNotFound {
		t.Fatalf("viewer role change status=%d", viewerRoleChange.Code)
	}
	if ownerRoleChange := callAPI(handler, http.MethodPatch, membersPath+"/"+strconv.FormatInt(p.ID, 10), owner, apiJSON(t, map[string]string{"role": "viewer"})); ownerRoleChange.Code != http.StatusConflict {
		t.Fatalf("owner role change status=%d", ownerRoleChange.Code)
	}
	if changed := callAPI(handler, http.MethodPatch, memberPath, owner, apiJSON(t, map[string]string{"role": "editor"})); changed.Code != http.StatusNoContent {
		t.Fatalf("change member role status=%d body=%s", changed.Code, changed.Body.String())
	}
	updatedMembers := callAPI(handler, http.MethodGet, membersPath, owner, "")
	var updatedMemberList []projectMember
	decodeResponse(t, updatedMembers, &updatedMemberList)
	if updatedMemberList[1].Role != "editor" {
		t.Fatalf("member role was not updated: %+v", updatedMemberList[1])
	}
	if invalidRole := callAPI(handler, http.MethodPatch, memberPath, owner, apiJSON(t, map[string]string{"role": "owner"})); invalidRole.Code != http.StatusBadRequest {
		t.Fatalf("invalid role status=%d", invalidRole.Code)
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
	var outsiderID int64
	if err := db.QueryRow("SELECT id FROM users WHERE email=?", "outsider@example.com").Scan(&outsiderID); err != nil {
		t.Fatal(err)
	}
	invalidAssignee := callAPI(handler, http.MethodPost, projectPath+"/tasks", owner, apiJSON(t, map[string]any{"title": "Bad assignee", "assignee_id": outsiderID}))
	if invalidAssignee.Code != http.StatusBadRequest {
		t.Fatalf("non-member assignee status=%d body=%s", invalidAssignee.Code, invalidAssignee.Body.String())
	}
	invalidDate := callAPI(handler, http.MethodPost, projectPath+"/tasks", owner, apiJSON(t, map[string]any{"title": "Bad date", "due_date": "2026-02-30"}))
	if invalidDate.Code != http.StatusBadRequest {
		t.Fatalf("invalid due date status=%d", invalidDate.Code)
	}
	createdTask := callAPI(handler, http.MethodPost, projectPath+"/tasks", owner, apiJSON(t, map[string]any{"title": "Review concurrency", "priority": "high", "assignee_id": members[1].User.ID, "due_date": "2000-01-01"}))
	if createdTask.Code != http.StatusCreated {
		t.Fatalf("create task status=%d body=%s", createdTask.Code, createdTask.Body.String())
	}
	var task projectTask
	decodeResponse(t, createdTask, &task)
	if task.AssigneeID == nil || *task.AssigneeID != members[1].User.ID || task.DueDate == nil || *task.DueDate != "2000-01-01" {
		t.Fatalf("assignment or due date missing: %+v", task)
	}
	listed := callAPI(handler, http.MethodGet, projectPath+"/tasks?status=open&limit=10&assignee_id="+strconv.FormatInt(members[1].User.ID, 10)+"&overdue=true", viewer, "")
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
	specialTitle := "Release 100%_ready!\\now"
	specialTask := callAPI(handler, http.MethodPost, projectPath+"/tasks", owner, apiJSON(t, map[string]string{"title": specialTitle}))
	if specialTask.Code != http.StatusCreated {
		t.Fatalf("create special task status=%d body=%s", specialTask.Code, specialTask.Body.String())
	}
	var searchable projectTask
	decodeResponse(t, specialTask, &searchable)
	for _, keyword := range []string{"%_ready!", "_ready", "!", "!\\now"} {
		path := projectPath + "/tasks?q=" + url.QueryEscape(keyword)
		response := callAPI(handler, http.MethodGet, path, owner, "")
		var result struct {
			Items []projectTask `json:"items"`
			Total int           `json:"total"`
		}
		if response.Code != http.StatusOK {
			t.Fatalf("search %q status=%d body=%s", keyword, response.Code, response.Body.String())
		}
		decodeResponse(t, response, &result)
		if result.Total != 1 || len(result.Items) != 1 || result.Items[0].Title != specialTitle {
			t.Fatalf("search %q returned %+v", keyword, result)
		}
	}
	if removed := callAPI(handler, http.MethodDelete, projectPath+"/tasks/"+strconv.FormatInt(searchable.ID, 10), owner, ""); removed.Code != http.StatusNoContent {
		t.Fatalf("cleanup special task status=%d body=%s", removed.Code, removed.Body.String())
	}
	taskPath := projectPath + "/tasks/" + strconv.FormatInt(task.ID, 10)
	fetched := callAPI(handler, http.MethodGet, taskPath, viewer, "")
	if fetched.Code != http.StatusOK {
		t.Fatalf("get task status=%d body=%s", fetched.Code, fetched.Body.String())
	}
	var fetchedTask projectTask
	decodeResponse(t, fetched, &fetchedTask)
	if fetchedTask.ID != task.ID || fetchedTask.Version != task.Version {
		t.Fatalf("unexpected fetched task: %+v", fetchedTask)
	}
	if outsiderTask := callAPI(handler, http.MethodGet, taskPath, outsider, ""); outsiderTask.Code != http.StatusNotFound {
		t.Fatalf("outsider task status=%d", outsiderTask.Code)
	}
	commentsPath := taskPath + "/comments"
	if got := callAPI(handler, http.MethodGet, commentsPath, viewer, ""); got.Code != http.StatusOK || got.Body.String() != "[]\n" {
		t.Fatalf("empty comments status=%d body=%s", got.Code, got.Body.String())
	}
	if got := callAPI(handler, http.MethodPost, commentsPath, viewer, apiJSON(t, map[string]string{"body": "viewer cannot write"})); got.Code != http.StatusForbidden {
		t.Fatalf("viewer comment status=%d", got.Code)
	}
	if got := callAPI(handler, http.MethodPost, commentsPath, owner, apiJSON(t, map[string]string{"body": "   "})); got.Code != http.StatusBadRequest {
		t.Fatalf("empty comment status=%d", got.Code)
	}
	longComment := strings.Repeat("x", 5001)
	if got := callAPI(handler, http.MethodPost, commentsPath, owner, apiJSON(t, map[string]string{"body": longComment})); got.Code != http.StatusBadRequest {
		t.Fatalf("long comment status=%d", got.Code)
	}
	firstComment := callAPI(handler, http.MethodPost, commentsPath, owner, apiJSON(t, map[string]string{"body": "  Need a race test  "}))
	if firstComment.Code != http.StatusCreated {
		t.Fatalf("comment create status=%d body=%s", firstComment.Code, firstComment.Body.String())
	}
	secondComment := callAPI(handler, http.MethodPost, commentsPath, owner, apiJSON(t, map[string]string{"body": "Discuss lock ordering"}))
	if secondComment.Code != http.StatusCreated {
		t.Fatalf("second comment status=%d", secondComment.Code)
	}
	var comments []taskComment
	commentList := callAPI(handler, http.MethodGet, commentsPath, viewer, "")
	decodeResponse(t, commentList, &comments)
	if len(comments) != 2 || comments[0].Body != "Need a race test" || comments[0].AuthorID == nil || comments[0].AuthorName == nil || comments[1].Body != "Discuss lock ordering" {
		t.Fatalf("unexpected comments: %+v", comments)
	}
	if got := callAPI(handler, http.MethodGet, commentsPath, outsider, ""); got.Code != http.StatusNotFound {
		t.Fatalf("outsider comments status=%d", got.Code)
	}
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
	eventsResponse := callAPI(handler, http.MethodGet, taskPath+"/events", viewer, "")
	if eventsResponse.Code != http.StatusOK {
		t.Fatalf("events status=%d body=%s", eventsResponse.Code, eventsResponse.Body.String())
	}
	var events []taskEvent
	decodeResponse(t, eventsResponse, &events)
	if len(events) != 2 || events[0].EventType != "created" || events[0].ActorID == nil || events[1].ActorID == nil || *events[0].ActorID != *events[1].ActorID || events[1].EventType != "status_changed" || events[1].FromStatus == nil || *events[1].FromStatus != "open" || events[1].ToStatus != "done" {
		t.Fatalf("unexpected task history: %+v", events)
	}
	if outsiderEvents := callAPI(handler, http.MethodGet, taskPath+"/events", outsider, ""); outsiderEvents.Code != http.StatusNotFound {
		t.Fatalf("outsider events status=%d", outsiderEvents.Code)
	}
	clearFields := callAPI(handler, http.MethodPatch, taskPath, owner, apiJSON(t, map[string]any{"assignee_id": nil, "due_date": nil, "version": current.Version}))
	if clearFields.Code != http.StatusOK {
		t.Fatalf("clear fields status=%d body=%s", clearFields.Code, clearFields.Body.String())
	}
	var cleared projectTask
	decodeResponse(t, clearFields, &cleared)
	if cleared.AssigneeID != nil || cleared.DueDate != nil {
		t.Fatalf("nullable fields not cleared: %+v", cleared)
	}
	reassign := callAPI(handler, http.MethodPatch, taskPath, owner, apiJSON(t, map[string]any{"assignee_id": members[1].User.ID, "due_date": "2099-12-31", "version": cleared.Version}))
	if reassign.Code != http.StatusOK {
		t.Fatalf("reassign status=%d body=%s", reassign.Code, reassign.Body.String())
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
	if malformed := callAPI(handler, http.MethodGet, "/api/v1/me", "not-a-real-token", ""); malformed.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status=%d", malformed.Code)
	}
	if _, err := db.Exec("UPDATE sessions SET expires_at=? WHERE token_hash=?", "2000-01-01T00:00:00Z", tokenDigest(outsider)); err != nil {
		t.Fatal(err)
	}
	if expired := callAPI(handler, http.MethodGet, "/api/v1/me", outsider, ""); expired.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status=%d", expired.Code)
	}
	assignedFilter := callAPI(handler, http.MethodGet, projectPath+"/tasks?assignee_id="+strconv.FormatInt(members[1].User.ID, 10), owner, "")
	var assignedPage struct {
		Items []projectTask `json:"items"`
		Total int           `json:"total"`
	}
	decodeResponse(t, assignedFilter, &assignedPage)
	if assignedPage.Total != 1 {
		t.Fatalf("assigned filter total=%d", assignedPage.Total)
	}
	unassignedFilter := callAPI(handler, http.MethodGet, projectPath+"/tasks?unassigned=true", owner, "")
	var unassignedPage struct {
		Items []projectTask `json:"items"`
		Total int           `json:"total"`
	}
	decodeResponse(t, unassignedFilter, &unassignedPage)
	if unassignedPage.Total != 0 {
		t.Fatalf("unassigned filter total=%d", unassignedPage.Total)
	}
	removeAssignedMember := callAPI(handler, http.MethodDelete, membersPath+"/"+strconv.FormatInt(members[1].User.ID, 10), owner, "")
	if removeAssignedMember.Code != http.StatusNoContent {
		t.Fatalf("remove assigned member status=%d", removeAssignedMember.Code)
	}
	afterRemoval, err := (&sqliteProjectTaskRepository{db: db}).Get(context.Background(), p.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRemoval.AssigneeID != nil || afterRemoval.Version <= current.Version {
		t.Fatalf("removed assignee was retained or version did not advance: %+v", afterRemoval)
	}
	if deleted := callAPI(handler, http.MethodDelete, taskPath, owner, ""); deleted.Code != http.StatusNoContent {
		t.Fatalf("delete task status=%d", deleted.Code)
	}
	var remainingComments int
	if err := db.QueryRow("SELECT COUNT(*) FROM task_comments WHERE task_id=?", task.ID).Scan(&remainingComments); err != nil {
		t.Fatal(err)
	}
	if remainingComments != 0 {
		t.Fatalf("task deletion retained %d comments", remainingComments)
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
