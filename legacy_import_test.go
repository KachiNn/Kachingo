package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func legacyImportFixture(t *testing.T, role string) (*sql.DB, int64, int64, string, []byte) {
	t.Helper()
	db, err := openDatabase(filepath.Join(t.TempDir(), "import.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("INSERT INTO users(email,name,password_hash) VALUES('owner@example.com','Owner','hash')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO projects(name,owner_id) VALUES('Project',1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO project_members(project_id,user_id,role) VALUES(1,1,?)", role); err != nil {
		t.Fatal(err)
	}
	data := []byte("{\"next_id\":3,\"tasks\":{\"1\":{\"id\":1,\"title\":\"Done task\",\"done\":true,\"created_at\":\"2026-04-09T12:30:00Z\"},\"2\":{\"id\":2,\"title\":\"Open task\",\"done\":false,\"created_at\":\"2026-04-10T12:30:00Z\"}}}")
	path := filepath.Join(t.TempDir(), "tasks.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return db, 1, 1, path, data
}

func TestImportLegacyTasksMapsAndPreservesSource(t *testing.T) {
	db, projectID, actorID, path, original := legacyImportFixture(t, "owner")
	count, err := importLegacyTasks(context.Background(), db, path, projectID, actorID)
	if err != nil || count != 2 {
		t.Fatalf("import = %d, %v; want 2, nil", count, err)
	}
	var status, priority, createdAt string
	if err := db.QueryRow("SELECT status,priority,created_at FROM project_tasks WHERE title='Done task'").Scan(&status, &priority, &createdAt); err != nil {
		t.Fatal(err)
	}
	if status != "done" || priority != "normal" || createdAt != "2026-04-09T12:30:00Z" {
		t.Fatalf("imported fields = %q %q %q", status, priority, createdAt)
	}
	var events int
	if err := db.QueryRow("SELECT COUNT(*) FROM task_events WHERE project_id=? AND actor_id=?", projectID, actorID).Scan(&events); err != nil || events != 2 {
		t.Fatalf("events = %d, %v", events, err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != string(original) {
		t.Fatalf("source changed: %v", err)
	}
	if _, err := importLegacyTasks(context.Background(), db, path, projectID, actorID); !errors.Is(err, errLegacyImportAlreadyApplied) {
		t.Fatalf("repeat import error = %v", err)
	}
}

func TestImportLegacyTasksRequiresWriteRole(t *testing.T) {
	db, projectID, actorID, path, _ := legacyImportFixture(t, "viewer")
	if _, err := importLegacyTasks(context.Background(), db, path, projectID, actorID); err == nil {
		t.Fatal("viewer import succeeded")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM project_tasks").Scan(&count); err != nil || count != 0 {
		t.Fatalf("tasks after denied import = %d, %v", count, err)
	}
}

func TestImportLegacyTasksRollsBackInvalidBatch(t *testing.T) {
	db, projectID, actorID, path, _ := legacyImportFixture(t, "editor")
	if _, err := db.Exec("CREATE TRIGGER reject_second_import BEFORE INSERT ON project_tasks WHEN NEW.title='Open task' BEGIN SELECT RAISE(ABORT, 'test failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := importLegacyTasks(context.Background(), db, path, projectID, actorID); err == nil {
		t.Fatal("triggered import failure was ignored")
	}
	var tasks, imports int
	if err := db.QueryRow("SELECT COUNT(*) FROM project_tasks").Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM legacy_task_imports").Scan(&imports); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 || imports != 0 {
		t.Fatalf("rollback left tasks=%d imports=%d", tasks, imports)
	}
}
