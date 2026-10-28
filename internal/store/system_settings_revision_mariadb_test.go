package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/go-sql-driver/mysql"
)

func systemSettingsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("real MariaDB not configured; no database PASS claimed")
	}
	isolatedDSN := os.Getenv("AUTOSTREAM_MARIADB_ISOLATED_TEST_DSN")
	if strings.TrimSpace(isolatedDSN) == "" {
		t.Fatal("isolated MariaDB test configuration is required when database validation is enabled")
	}
	cfg, err := mysql.ParseDSN(strings.TrimPrefix(isolatedDSN, "mysql://"))
	if err != nil {
		t.Fatal("invalid isolated test DSN")
	}
	cfg.DBName, cfg.ParseTime = "", true
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "autostream_sys89_" + strings.ReplaceAll(newUUID(), "-", "")
	if _, err = admin.ExecContext(t.Context(), "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.WithoutCancel(t.Context()), "DROP DATABASE `"+name+"`") })
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	if err = database.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

type systemSettingsCASResult struct {
	stamp string
	label string
	err   error
}

func TestMariaDBSystemSettingsInitialAndExistingRowCAS(t *testing.T) {
	db := systemSettingsTestDB(t)
	var precision int
	if err := db.QueryRowContext(t.Context(), `SELECT DATETIME_PRECISION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='system_settings' AND COLUMN_NAME='updated_at'`).Scan(&precision); err != nil || precision != 6 {
		t.Fatal("system settings authority is not stored at microsecond precision")
	}
	app, security := NewMariaDBAppSettingsStore(db), NewMariaDBSecuritySettingsStore(db)
	for _, kind := range []string{"app", "security"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			read := func() systemSettingsCASResult {
				if kind == "app" {
					row, err := app.GetAppSettings(ctx)
					return systemSettingsCASResult{row.UpdatedAt, row.AppName, err}
				}
				row, err := security.GetSecuritySettings(ctx)
				return systemSettingsCASResult{row.UpdatedAt, strconv.Itoa(row.PasswordMinLength), err}
			}
			write := func(expected *string, tag int) systemSettingsCASResult {
				if kind == "app" {
					row, err := app.UpdateAppSettingsRevision(ctx, AppSettingsMutation{Settings: AppSettings{AppName: "Panel " + strconv.Itoa(tag), Timezone: "Asia/Tokyo"}, ExpectedUpdatedAt: expected}, nil)
					return systemSettingsCASResult{row.UpdatedAt, row.AppName, err}
				}
				input := defaultSecurityConfig
				input.PasswordMinLength = 12 + tag
				row, err := security.UpdateSecuritySettingsRevision(ctx, input, expected)
				return systemSettingsCASResult{row.UpdatedAt, strconv.Itoa(row.PasswordMinLength), err}
			}
			initial := read()
			if initial.err != nil || initial.stamp != UnconfiguredSystemSettingsRevision {
				t.Fatal("isolated settings row was not initially unconfigured")
			}
			unlock := systemSettingsInitialInsertBarrier(t, ctx, db, kind)
			outcomes := make(chan systemSettingsCASResult, 2)
			for tag := 1; tag <= 2; tag++ {
				go func(tag int) { outcomes <- write(&initial.stamp, tag) }(tag)
			}
			waitForSystemSettingsQueries(t, ctx, db, "INSERT INTO system_settings", 2)
			unlock()
			winner := systemSettingsOneWinner(t, outcomes)
			actual := read()
			if actual.err != nil || actual.stamp != winner.stamp || actual.label != winner.label {
				t.Fatal("initial winner receipt differs from its stored settings row")
			}
			// Hold an actual row lock and observe both real store SELECTs waiting,
			// rather than relying on goroutine timing as concurrency evidence.
			future := time.Date(2035, 1, 1, 0, 0, 0, 123456000, time.UTC)
			if _, err := db.ExecContext(ctx, `UPDATE system_settings SET updated_at=? WHERE name=?`, future, kind); err != nil {
				t.Fatal(err)
			}
			baseline := resourceUpdatedAt(future)
			blocker, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			var locked string
			if err = blocker.QueryRowContext(ctx, `SELECT name FROM system_settings WHERE name=? FOR UPDATE`, kind).Scan(&locked); err != nil {
				t.Fatal(err)
			}
			for tag := 3; tag <= 4; tag++ {
				go func(tag int) { outcomes <- write(&baseline, tag) }(tag)
			}
			waitForSystemSettingsQueries(t, ctx, db, "SELECT updated_at FROM system_settings", 2)
			if err = blocker.Commit(); err != nil {
				t.Fatal(err)
			}
			winner = systemSettingsOneWinner(t, outcomes)
			actual = read()
			if actual.err != nil || actual.stamp != winner.stamp || actual.label != winner.label || actual.stamp != resourceUpdatedAt(future.Add(time.Microsecond)) {
				t.Fatal("competing saved-row edits did not have one monotonic winner")
			}
			legacy := write(nil, 5)
			if legacy.err != nil || legacy.stamp != resourceUpdatedAt(future.Add(2*time.Microsecond)) {
				t.Fatal("legacy unconditional writer did not advance the same authority")
			}
		})
	}
}

func systemSettingsOneWinner(t *testing.T, results <-chan systemSettingsCASResult) systemSettingsCASResult {
	t.Helper()
	wins, conflicts := 0, 0
	var winner systemSettingsCASResult
	for range 2 {
		select {
		case result := <-results:
			if result.err == nil {
				wins++
				winner = result
			} else if errors.Is(result.err, ErrRevisionConflict) {
				conflicts++
			} else {
				t.Fatal("unexpected database settings mutation failure")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("settings mutation did not finish within its bounded lock phase")
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("shared database settings baseline did not have exactly one winner")
	}
	return winner
}

func waitForSystemSettingsQueries(t *testing.T, ctx context.Context, db *sql.DB, prefix string, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE DB=DATABASE() AND ID<>CONNECTION_ID() AND INFO LIKE ?`, prefix+"%").Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= count {
			t.Logf("observed %d concurrent real settings queries at the database lock barrier", waiting)
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("settings lock observation cancelled")
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("both settings operations did not reach the actual database lock barrier")
}

func systemSettingsInitialInsertBarrier(t *testing.T, ctx context.Context, db *sql.DB, kind string) func() {
	t.Helper()
	name := "system89_" + strings.ReplaceAll(newUUID(), "-", "")
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var acquired int
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK(?,10)`, name).Scan(&acquired); err != nil || acquired != 1 {
		t.Fatal("initial settings fixture lock unavailable")
	}
	// The trigger only schedules two legitimate initial writes. It changes no
	// data or outcome; the normal unique key and store CAS select the winner.
	statement := "CREATE TRIGGER `" + name + "` BEFORE INSERT ON system_settings FOR EACH ROW BEGIN IF NEW.name='" + kind + "' THEN DO GET_LOCK('" + name + "',10); DO RELEASE_LOCK('" + name + "'); END IF; END"
	if _, err = db.ExecContext(ctx, statement); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	released := false
	unlock := func() {
		if released {
			return
		}
		released = true
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `DO RELEASE_LOCK(?)`, name)
		_ = conn.Close()
	}
	t.Cleanup(func() {
		unlock()
		_, _ = db.ExecContext(context.WithoutCancel(ctx), "DROP TRIGGER IF EXISTS `"+name+"`")
	})
	return unlock
}
