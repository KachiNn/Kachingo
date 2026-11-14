package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

var errLegacyImportAlreadyApplied = errors.New("legacy task file was already imported into this project")

func importLegacyTasks(ctx context.Context, db *sql.DB, sourcePath string, projectID, actorID int64) (int, error) {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return 0, fmt.Errorf("read legacy task file: %w", err)
	}
	var state diskState
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&state); err != nil {
		return 0, fmt.Errorf("decode legacy task file: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return 0, errors.New("legacy task file must contain one JSON value")
	}
	if state.Tasks == nil {
		state.Tasks = map[int]Task{}
	}
	ids := make([]int, 0, len(state.Tasks))
	for id, task := range state.Tasks {
		if id < 1 || task.ID != id || strings.TrimSpace(task.Title) == "" {
			return 0, fmt.Errorf("invalid legacy task record %d", id)
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)
	fingerprint := sha256.Sum256(data)
	fingerprintText := hex.EncodeToString(fingerprint[:])
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var role string
	if err := tx.QueryRowContext(ctx, "SELECT role FROM project_members WHERE project_id=? AND user_id=?", projectID, actorID).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, errors.New("actor is not a member of the target project")
		}
		return 0, err
	}
	if role != "owner" && role != "editor" {
		return 0, errors.New("actor must be a project owner or editor")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO legacy_task_imports(source_sha256,project_id,actor_id,task_count) VALUES(?,?,?,?)", fingerprintText, projectID, actorID, len(ids)); err != nil {
		var count int
		if queryErr := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM legacy_task_imports WHERE source_sha256=? AND project_id=?", fingerprintText, projectID).Scan(&count); queryErr == nil && count > 0 {
			return 0, errLegacyImportAlreadyApplied
		}
		return 0, err
	}
	for _, id := range ids {
		task := state.Tasks[id]
		status := "open"
		if task.Done {
			status = "done"
		}
		createdAt := task.CreatedAt.UTC().Format(time.RFC3339Nano)
		if task.CreatedAt.IsZero() {
			createdAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO project_tasks(project_id,title,status,priority,created_at,updated_at) VALUES(?,?,?,'normal',?,?)", projectID, task.Title, status, createdAt, createdAt)
		if err != nil {
			return 0, fmt.Errorf("import legacy task %d: %w", id, err)
		}
		taskID, err := result.LastInsertId()
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO task_events(project_id,task_id,actor_id,event_type,to_status,created_at) VALUES(?,?,?,'created',?,?)", projectID, taskID, actorID, status, createdAt); err != nil {
			return 0, fmt.Errorf("record imported task %d: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func runLegacyImport(args []string) error {
	flags := flag.NewFlagSet("import-legacy", flag.ContinueOnError)
	file := flags.String("file", envOrDefault("DATA_FILE", "data/tasks.json"), "legacy JSON task file")
	databaseFile := flags.String("database", envOrDefault("DATABASE_FILE", "data/kachingo.db"), "SQLite database file")
	projectID := flags.Int64("project-id", 0, "target project ID")
	actorID := flags.Int64("actor-id", 0, "owner/editor user ID recorded as importer")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *projectID < 1 || *actorID < 1 {
		return errors.New("usage: kachingo import-legacy --project-id ID --actor-id ID [--file PATH] [--database PATH]")
	}
	db, err := openDatabase(*databaseFile)
	if err != nil {
		return fmt.Errorf("open SQLite database: %w", err)
	}
	defer db.Close()
	count, err := importLegacyTasks(context.Background(), db, *file, *projectID, *actorID)
	if err != nil {
		return err
	}
	fmt.Printf("Imported %d legacy tasks into project %d. Source file was left unchanged.\n", count, *projectID)
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
