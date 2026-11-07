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
	if count != 2 {
		t.Fatalf("applied migrations=%d, want 2", count)
	}
	rows, err := db.Query("PRAGMA table_info(project_tasks)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	foundVersion := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "version" {
			foundVersion = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !foundVersion {
		t.Fatal("task version migration was not applied")
	}
}
