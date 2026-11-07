package main

import (
	"context"
	"database/sql"
	"testing"
)

func TestDatabaseMigrationsAreIdempotent(t *testing.T) {
	db, err := openDatabase(t.TempDir() + "/migration.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrateDatabase(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("applied migrations=%d, want 3", count)
	}
	rows, err := db.Query("PRAGMA table_info(project_tasks)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	foundVersion, foundAssignee, foundDueDate := false, false, false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		switch name {
		case "version":
			foundVersion = true
		case "assignee_id":
			foundAssignee = true
		case "due_date":
			foundDueDate = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !foundVersion || !foundAssignee || !foundDueDate {
		t.Fatalf("task migrations missing fields: version=%t assignee=%t due_date=%t", foundVersion, foundAssignee, foundDueDate)
	}
}
