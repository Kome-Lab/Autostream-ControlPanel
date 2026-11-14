package operationalauthorization

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"go/format"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

//go:embed testdata/v2_admin.go.txt
var lastAdminV2Source []byte

func TestOperationalLastAdminSourceBoundary(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	frozen := strings.ReplaceAll(string(lastAdminV2Source), "\r\n", "\n")
	digest := sha256.Sum256([]byte(frozen))
	if hex.EncodeToString(digest[:]) != "17f3e56070217a10bebd8d6c3b2890198c42df892835d55697be36558ad2b536" {
		t.Fatal("fixed v2 admin fixture changed")
	}
	changes := [][2]string{
		{"}\n\nfunc (s MariaDBAuthStore) UpdateUser(ctx context.Context, id string, patch UserPatch) (User, error) {\n\ttx, err := s.db.BeginTx(ctx, nil)\n\tif err != nil {\n\t\treturn User{}, err\n\t}\n\tdefer tx.Rollback()\n\tif patch.Username != \"\" {\n\t\tif _, err := tx.ExecContext(ctx, `UPDATE users SET username = ?, updated_at = ? WHERE id = ?`, patch.Username, time.Now().UTC(), id); err != nil {\n\t\t\treturn User{}, err\n", "}\n\nfunc (s MariaDBAuthStore) UpdateUser(ctx context.Context, id string, patch UserPatch) (User, error) {\n\ttx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})\n\tif err != nil {\n\t\treturn User{}, err\n\t}\n\tdefer tx.Rollback()\n\tif patch.RoleIDs != nil {\n\t\ttarget, err := lockLastAdminUser(ctx, tx, id)\n\t\tif err != nil {\n\t\t\treturn User{}, err\n\t\t}\n\t\tretainsSuper := false\n\t\tfor _, roleID := range patch.RoleIDs {\n\t\t\tvar name string\n\t\t\tif err := tx.QueryRowContext(ctx, \"SELECT name FROM roles WHERE id=?\", roleID).Scan(&name); err != nil {\n\t\t\t\treturn User{}, err\n\t\t\t}\n\t\t\tretainsSuper = retainsSuper || name == \"super_admin\"\n\t\t}\n\t\tif target.super && target.status == \"active\" && !retainsSuper {\n\t\t\tif err := requireAnotherActiveSuperAdmin(ctx, tx); err != nil {\n\t\t\t\treturn User{}, err\n\t\t\t}\n\t\t}\n\t}\n\tif patch.Username != \"\" {\n\t\tif _, err := tx.ExecContext(ctx, `UPDATE users SET username = ?, updated_at = ? WHERE id = ?`, patch.Username, time.Now().UTC(), id); err != nil {\n\t\t\treturn User{}, err\n"},
		{"\t\t\t}\n\t\t}\n\t}\n\tif err := tx.Commit(); err != nil {\n\t\treturn User{}, err\n\t}\n\treturn s.GetUser(ctx, id)\n}\n\nfunc (s MariaDBAuthStore) SetUserStatus(ctx context.Context, id, status string) (User, error) {\n\thasSuperAdmin, err := s.UserHasRole(ctx, id, \"super_admin\")\n\tif err != nil {\n\t\treturn User{}, err\n\t}\n\tif hasSuperAdmin && (status == \"disabled\" || status == \"locked\") {\n\t\tcount, err := s.CountActiveSuperAdmins(ctx)\n\t\tif err != nil {\n\t\t\treturn User{}, err\n\t\t}\n\t\tif count <= 1 {\n\t\t\treturn User{}, ErrLastSuperAdmin\n\t\t}\n\t}\n\tresult, err := s.db.ExecContext(ctx, `UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now().UTC(), id)\n\tif err != nil {\n\t\treturn User{}, err\n\t}\n", "\t\t\t}\n\t\t}\n\t}\n\tif err := commitUserAdministration(tx); err != nil {\n\t\treturn User{}, err\n\t}\n\treturn s.GetUser(ctx, id)\n}\n\nfunc (s MariaDBAuthStore) SetUserStatus(ctx context.Context, id, status string) (User, error) {\n\ttx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})\n\tif err != nil {\n\t\treturn User{}, err\n\t}\n\tdefer tx.Rollback()\n\ttarget, err := lockLastAdminUser(ctx, tx, id)\n\tif err != nil {\n\t\treturn User{}, err\n\t}\n\t// Retain the legacy disabled/locked refusal, including inactive Role holders.\n\tif target.super && (status == \"disabled\" || status == \"locked\" || (target.status == \"active\" && status != \"active\")) {\n\t\tif err := requireAnotherActiveSuperAdmin(ctx, tx); err != nil {\n\t\t\treturn User{}, err\n\t\t}\n\t}\n\tresult, err := tx.ExecContext(ctx, `UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now().UTC(), id)\n\tif err != nil {\n\t\treturn User{}, err\n\t}\n"},
		{"\tif affected == 0 {\n\t\treturn User{}, ErrNotFound\n\t}\n\treturn s.GetUser(ctx, id)\n}\n\nfunc (s MariaDBAuthStore) DeleteUser(ctx context.Context, id string) error {\n\tuser, err := s.GetUser(ctx, id)\n\tif err != nil {\n\t\treturn err\n\t}\n\tif hasRole(user, \"super_admin\") && user.Status == \"active\" {\n\t\tcount, err := s.CountActiveSuperAdmins(ctx)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif count <= 1 {\n\t\t\treturn ErrLastSuperAdmin\n\t\t}\n\t}\n\ttx, err := s.db.BeginTx(ctx, nil)\n\tif err != nil {\n\t\treturn err\n\t}\n\tdefer tx.Rollback()\n\tfor _, query := range []string{\n\t\t`DELETE FROM sessions WHERE user_id = ?`,\n\t\t`DELETE FROM oauth_user_links WHERE user_id = ?`,\n", "\tif affected == 0 {\n\t\treturn User{}, ErrNotFound\n\t}\n\tif err := commitUserAdministration(tx); err != nil {\n\t\treturn User{}, err\n\t}\n\treturn s.GetUser(ctx, id)\n}\n\nfunc (s MariaDBAuthStore) DeleteUser(ctx context.Context, id string) error {\n\ttx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})\n\tif err != nil {\n\t\treturn err\n\t}\n\tdefer tx.Rollback()\n\ttarget, err := lockLastAdminUser(ctx, tx, id)\n\tif err != nil {\n\t\treturn err\n\t}\n\tif target.super && target.status == \"active\" {\n\t\tif err := requireAnotherActiveSuperAdmin(ctx, tx); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\tfor _, query := range []string{\n\t\t`DELETE FROM sessions WHERE user_id = ?`,\n\t\t`DELETE FROM oauth_user_links WHERE user_id = ?`,\n"},
		{"\tif affected == 0 {\n\t\treturn ErrNotFound\n\t}\n\treturn tx.Commit()\n}\n\nfunc (s MariaDBAuthStore) ResetPassword(ctx context.Context, id, temporaryPassword string) error {\n", "\tif affected == 0 {\n\t\treturn ErrNotFound\n\t}\n\treturn commitUserAdministration(tx)\n}\n\nfunc (s MariaDBAuthStore) ResetPassword(ctx context.Context, id, temporaryPassword string) error {\n"},
		{"\tif err != nil {\n\t\treturn err\n\t}\n\tresult, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, status = 'pending_password_change', updated_at = ? WHERE id = ?`, hash, time.Now().UTC(), id)\n\tif err != nil {\n\t\treturn err\n\t}\n", "\tif err != nil {\n\t\treturn err\n\t}\n\ttx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})\n\tif err != nil {\n\t\treturn err\n\t}\n\tdefer tx.Rollback()\n\ttarget, err := lockLastAdminUser(ctx, tx, id)\n\tif err != nil {\n\t\treturn err\n\t}\n\tif target.super && target.status == \"active\" {\n\t\tif err := requireAnotherActiveSuperAdmin(ctx, tx); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\tresult, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, status = 'pending_password_change', updated_at = ? WHERE id = ?`, hash, time.Now().UTC(), id)\n\tif err != nil {\n\t\treturn err\n\t}\n"},
		{"\tif affected == 0 {\n\t\treturn ErrNotFound\n\t}\n\treturn nil\n}\n\nfunc (s MariaDBAuthStore) CountActiveSuperAdmins(ctx context.Context) (int, error) {\n", "\tif affected == 0 {\n\t\treturn ErrNotFound\n\t}\n\treturn commitUserAdministration(tx)\n}\n\nfunc (s MariaDBAuthStore) CountActiveSuperAdmins(ctx context.Context) (int, error) {\n"},
	}
	for i, ch := range changes {
		if strings.Count(frozen, ch[0]) != 1 {
			t.Fatalf("admin hook %d has no unique v2 match", i)
		}
		frozen = strings.Replace(frozen, ch[0], ch[1], 1)
	}
	expected, err := format.Source([]byte(frozen))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(root, "internal/store/admin.go"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := format.Source(actual)
	if err != nil || !bytes.Equal(canonical, expected) {
		t.Fatal("admin Store changes exceed reviewed transaction hooks")
	}
	for _, pair := range [][2]string{{"if patch.RoleIDs != nil", "if false"}, {"requireAnotherActiveSuperAdmin(ctx, tx)", "bypassLastAdmin(ctx, tx)"}, {"DELETE FROM sessions WHERE user_id = ?", "DELETE FROM sessions"}} {
		if !strings.Contains(string(actual), pair[0]) {
			t.Fatal("tamper probe not anchored")
		}
		changed := strings.Replace(string(actual), pair[0], pair[1], 1)
		changedSource, e := format.Source([]byte(changed))
		if e == nil && bytes.Equal(changedSource, expected) {
			t.Fatal("unauthorized Store edit passed")
		}
	}
	migration, err := os.ReadFile(filepath.Join(root, "internal/database/migrations/093_user_admin_guard.sql"))
	if err != nil {
		t.Fatal(err)
	}
	md := sha256.Sum256([]byte(strings.ReplaceAll(string(migration), "\r\n", "\n")))
	if hex.EncodeToString(md[:]) != "4feba2b60385d37eab73224705cf9d1a3587a7bc7324108400ec685cd789cee1" {
		t.Fatal("last-admin migration is not the reviewed singleton lock-row schema")
	}
}
