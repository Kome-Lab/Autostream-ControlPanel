package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

func RunEmbeddedMigrations(ctx context.Context, db *sql.DB) error {
	return runMigrationsFS(ctx, db, embeddedMigrations, "migrations")
}

func RunMigrations(ctx context.Context, db *sql.DB, dir string) error {
	return runMigrationsOS(ctx, db, dir)
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
  id VARCHAR(255) PRIMARY KEY,
  applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func runMigrationsOS(ctx context.Context, db *sql.DB, dir string) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, file := range files {
		id := filepath.Base(file)
		applied, err := migrationApplied(ctx, db, id)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		for _, stmt := range splitSQLStatements(string(body)) {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply %s: %w", id, err)
			}
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (id) VALUES (?)", id); err != nil {
			return fmt.Errorf("record migration %s: %w", id, err)
		}
	}
	return nil
}

func runMigrationsFS(ctx context.Context, db *sql.DB, source fs.FS, dir string) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return err
	}
	entries, err := fs.ReadDir(source, dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		id := entry.Name()
		applied, err := migrationApplied(ctx, db, id)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := fs.ReadFile(source, filepath.ToSlash(filepath.Join(dir, entry.Name())))
		if err != nil {
			return err
		}
		for _, stmt := range splitSQLStatements(string(body)) {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply %s: %w", id, err)
			}
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (id) VALUES (?)", id); err != nil {
			return fmt.Errorf("record migration %s: %w", id, err)
		}
	}
	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, id string) (bool, error) {
	var got string
	err := db.QueryRowContext(ctx, "SELECT id FROM schema_migrations WHERE id = ?", id).Scan(&got)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func splitSQLStatements(sqlText string) []string {
	parts := strings.Split(sqlText, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}
