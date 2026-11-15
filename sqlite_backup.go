package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	sqlite "modernc.org/sqlite"
)

type sqliteBackup interface {
	Step(int32) (bool, error)
	Finish() error
}

type sqliteOnlineBackup interface {
	NewBackup(string) (*sqlite.Backup, error)
}

func sqliteFileURI(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = filepath.Clean(path)
	}
	return "file:" + filepath.ToSlash(absolute)
}

func runOnlineBackup(ctx context.Context, source *sql.DB, targetURI string) (finalErr error) {
	conn, err := source.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var backup sqliteBackup
	err = conn.Raw(func(raw any) error {
		sqliteConn, ok := raw.(sqliteOnlineBackup)
		if !ok {
			return errors.New("SQLite driver does not support online backup")
		}
		var err error
		backup, err = sqliteConn.NewBackup(targetURI)
		return err
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := backup.Finish(); finalErr == nil && err != nil {
			finalErr = err
		}
	}()
	for {
		more, err := backup.Step(-1)
		if err != nil {
			var coded interface{ Code() int }
			if !errors.As(err, &coded) || (coded.Code()&0xff != 5 && coded.Code()&0xff != 6) {
				return err
			}
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if !more {
			return nil
		}
	}
}

func verifySQLiteFile(path string) error {
	db, err := sql.Open("sqlite", sqliteFileURI(path)+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", result)
	}
	return nil
}

func createSQLiteTempPath(target string) (string, error) {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, ".kachingo-db-*.tmp")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func backupDatabase(ctx context.Context, databasePath, backupPath string) (finalErr error) {
	if sameSQLitePath(databasePath, backupPath) {
		return errors.New("backup path must differ from database path")
	}
	if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("SQLite database does not exist: %s", databasePath)
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(backupPath); err == nil {
		return fmt.Errorf("backup file already exists: %s", backupPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source, err := openDatabase(databasePath)
	if err != nil {
		return fmt.Errorf("open SQLite database: %w", err)
	}
	defer source.Close()
	tempPath, err := createSQLiteTempPath(backupPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tempPath)
		_ = os.Remove(tempPath + "-wal")
		_ = os.Remove(tempPath + "-shm")
	}()
	if err := runOnlineBackup(ctx, source, sqliteFileURI(tempPath)); err != nil {
		return fmt.Errorf("create SQLite backup: %w", err)
	}
	if err := verifySQLiteFile(tempPath); err != nil {
		return err
	}
	if _, err := os.Stat(backupPath); err == nil {
		return fmt.Errorf("backup file already exists: %s", backupPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(tempPath, backupPath); err != nil {
		return err
	}
	return nil
}

func restoreDatabase(ctx context.Context, backupPath, databasePath string) (finalErr error) {
	if sameSQLitePath(databasePath, backupPath) {
		return errors.New("restore path must differ from backup path")
	}
	if _, err := os.Stat(databasePath); err == nil {
		return fmt.Errorf("restore target already exists: %s", databasePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := verifySQLiteFile(backupPath); err != nil {
		return fmt.Errorf("invalid backup: %w", err)
	}
	tempPath, err := createSQLiteTempPath(databasePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tempPath)
		_ = os.Remove(tempPath + "-wal")
		_ = os.Remove(tempPath + "-shm")
	}()
	source, err := sql.Open("sqlite", sqliteFileURI(backupPath)+"?mode=ro")
	if err != nil {
		return err
	}
	defer source.Close()
	if err := source.PingContext(ctx); err != nil {
		return err
	}
	if err := runOnlineBackup(ctx, source, sqliteFileURI(tempPath)); err != nil {
		return fmt.Errorf("restore SQLite database: %w", err)
	}
	if err := verifySQLiteFile(tempPath); err != nil {
		return err
	}
	if _, err := os.Stat(databasePath); err == nil {
		return fmt.Errorf("restore target already exists: %s", databasePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(tempPath, databasePath); err != nil {
		return err
	}
	return nil
}

func sameSQLitePath(first, second string) bool {
	firstAbs, err := filepath.Abs(first)
	if err != nil {
		firstAbs = filepath.Clean(first)
	}
	secondAbs, err := filepath.Abs(second)
	if err != nil {
		secondAbs = filepath.Clean(second)
	}
	return filepath.Clean(firstAbs) == filepath.Clean(secondAbs)
}

func runDatabaseCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: kachingo <backup|restore> [options]")
	}
	switch args[0] {
	case "backup":
		flags := flag.NewFlagSet("backup", flag.ContinueOnError)
		databasePath := flags.String("database", envOrDefault("DATABASE_FILE", "data/kachingo.db"), "SQLite database file")
		backupPath := flags.String("file", "", "backup file to create")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if *backupPath == "" || flags.NArg() != 0 {
			return errors.New("usage: kachingo backup --file PATH [--database PATH]")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := backupDatabase(ctx, *databasePath, *backupPath); err != nil {
			return err
		}
		fmt.Printf("SQLite backup created at %s\n", *backupPath)
		return nil
	case "restore":
		flags := flag.NewFlagSet("restore", flag.ContinueOnError)
		backupPath := flags.String("file", "", "SQLite backup file to restore")
		databasePath := flags.String("database", envOrDefault("DATABASE_FILE", "data/kachingo.db"), "new SQLite database path")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if *backupPath == "" || flags.NArg() != 0 {
			return errors.New("usage: kachingo restore --file PATH [--database PATH]")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := restoreDatabase(ctx, *backupPath, *databasePath); err != nil {
			return err
		}
		fmt.Printf("SQLite database restored to %s\n", *databasePath)
		return nil
	default:
		return fmt.Errorf("unknown command %q; expected backup or restore", args[0])
	}
}
