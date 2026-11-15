package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteBackupAndRestore(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "source.db")
	backupPath := filepath.Join(dir, "backups", "source.db")
	restoredPath := filepath.Join(dir, "restored", "source.db")
	db, err := openDatabase(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO users(email,name,password_hash) VALUES('backup@example.com','Backup User','hash')"); err != nil {
		t.Fatal(err)
	}
	if err := backupDatabase(context.Background(), databasePath, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := verifySQLiteFile(backupPath); err != nil {
		t.Fatal(err)
	}
	if err := backupDatabase(context.Background(), databasePath, backupPath); err == nil {
		t.Fatal("backup overwrote existing file")
	}
	if err := restoreDatabase(context.Background(), backupPath, restoredPath); err != nil {
		t.Fatal(err)
	}
	if err := verifySQLiteFile(restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := openDatabase(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var name string
	if err := restored.QueryRow("SELECT name FROM users WHERE email='backup@example.com'").Scan(&name); err != nil || name != "Backup User" {
		t.Fatalf("restored user = %q, %v", name, err)
	}
	if err := restoreDatabase(context.Background(), backupPath, restoredPath); err == nil {
		t.Fatal("restore overwrote existing database")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRejectsCorruptBackupAndPreservesTarget(t *testing.T) {
	dir := t.TempDir()
	backupPath := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(backupPath, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "restored.db")
	if err := restoreDatabase(context.Background(), backupPath, target); err == nil {
		t.Fatal("corrupt backup accepted")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target state after failed restore: %v", err)
	}
}

func TestBackupDoesNotCreateMissingSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "missing.db")
	err := backupDatabase(context.Background(), source, filepath.Join(dir, "backup.db"))
	if err == nil {
		t.Fatal("backup of missing database succeeded")
	}
	if _, statErr := os.Stat(source); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing source was created: %v", statErr)
	}
}

func TestRestoreRequiresMissingTarget(t *testing.T) {
	dir := t.TempDir()
	backupPath := filepath.Join(dir, "backup.db")
	db, err := openDatabase(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.db")
	if err := os.WriteFile(target, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreDatabase(context.Background(), backupPath, target); err == nil {
		t.Fatal("existing restore target accepted")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "preserve" {
		t.Fatalf("existing target modified: %v", err)
	}
}
