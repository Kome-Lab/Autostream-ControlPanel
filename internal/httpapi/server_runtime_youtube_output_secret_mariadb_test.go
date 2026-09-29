package httpapi

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/go-sql-driver/mysql"
)

func TestSelectedYouTubeOutputSecretMariaDB(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	runSelectedYouTubeOutputSecretCases(t, func(t *testing.T) outputSecretStores {
		cfg, err := mysql.ParseDSN(strings.TrimPrefix(dsn, "mysql://"))
		if err != nil {
			t.Fatal("invalid MariaDB test DSN")
		}
		cfg.DBName = ""
		cfg.ParseTime = true
		admin, err := sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			t.Fatal("open test MariaDB")
		}
		t.Cleanup(func() { _ = admin.Close() })
		name := "autostream075_auth_" + outputSecretRandom(t)[:16]
		if _, err = admin.ExecContext(t.Context(), "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
			t.Fatal(err)
		}
		// Each case owns a new schema; no pre-existing application data is touched.
		t.Cleanup(func() {
			if _, err := admin.Exec("DROP DATABASE `" + name + "`"); err != nil {
				t.Error("drop owned test schema failed")
			}
		})
		cfg.DBName = name
		db, err := sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			t.Fatal("open owned test schema")
		}
		t.Cleanup(func() { _ = db.Close() })
		if err = database.RunEmbeddedMigrations(t.Context(), db); err != nil {
			t.Fatal(err)
		}
		key := outputSecretRandom(t)
		auth := store.NewMariaDBAuthStoreWithSecretKey(db, key)
		return outputSecretStores{auth, auth, store.NewMariaDBAuditStore(db), store.NewMariaDBStreamStore(db), store.NewMariaDBProfileStore(db), store.NewMariaDBSecretStore(db, key), store.NewMariaDBRuntimeSecretLeaseStore(db)}
	})
}
