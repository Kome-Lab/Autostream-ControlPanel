package store_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBUpdaterPolicyStoreRevisions(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	auth := store.NewMariaDBAuthStore(db)
	token, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register"})
	if err != nil {
		t.Fatal(err)
	}
	serviceID := "updater-policy-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := auth.PrecreateService(ctx, token, store.ServiceRegistration{
		ServiceID:   serviceID,
		ServiceType: "update_agent",
		ServiceName: "Policy test updater",
		PublicURL:   "https://updater.example.com",
		Version:     "v1.0.0",
		Capabilities: map[string]any{
			"managed_targets":  []any{},
			"deployment_modes": map[string]any{},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, store.UpdaterGitHubReleaseTokenSecretName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM secrets WHERE name = ?`, store.UpdaterGitHubReleaseTokenSecretName)
	})

	const encryptionKey = "updater-policy-mariadb-test-encryption-key"
	policies := store.NewMariaDBUpdaterPolicyAdminStore(db, encryptionKey)
	input := store.UpdaterPolicy{
		UpdaterID:                serviceID,
		PollIntervalSeconds:      15,
		HeartbeatIntervalSeconds: 30,
		Hosts: []store.UpdaterPolicyHost{{
			HostID:        "host-a",
			Name:          "Studio A",
			Address:       "host-a.example.com",
			Port:          55850,
			User:          "autostream-update-host",
			Arch:          "amd64",
			HostPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8g",
		}},
		Targets: []store.UpdaterPolicyTarget{{
			TargetID:       "worker-a",
			HostID:         "host-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}
	originalToken := "github_pat_original_release_token"
	created, tokenStatus, err := policies.SaveUpdaterPolicyAndReleaseToken(ctx, serviceID, 0, input, &originalToken)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || !tokenStatus.Configured || tokenStatus.Fingerprint == "" {
		t.Fatalf("created policy/token status = %#v / %#v", created, tokenStatus)
	}
	storedToken, err := policies.GetUpdaterReleaseTokenValue(ctx)
	if err != nil || storedToken != originalToken {
		t.Fatalf("stored token = %q, %v", storedToken, err)
	}
	var ciphertext, nonce, valueHash string
	if err := db.QueryRowContext(
		ctx,
		`SELECT ciphertext, nonce, value_hash FROM secrets WHERE name = ?`,
		store.UpdaterGitHubReleaseTokenSecretName,
	).Scan(&ciphertext, &nonce, &valueHash); err != nil {
		t.Fatal(err)
	}
	if ciphertext == originalToken || strings.Contains(ciphertext, originalToken) || nonce == "" || valueHash != tokenStatus.Fingerprint {
		t.Fatalf("release token was not stored encrypted: ciphertext=%q nonce_empty=%v hash=%q", ciphertext, nonce == "", valueHash)
	}

	replacementToken := "github_pat_replacement_must_not_win"
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(ctx, serviceID, 0, input, &replacementToken); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate create error = %v, want ErrConflict", err)
	}
	storedToken, err = policies.GetUpdaterReleaseTokenValue(ctx)
	if err != nil || storedToken != originalToken {
		t.Fatalf("stale CAS mutated token = %q, %v", storedToken, err)
	}

	input.PollIntervalSeconds = 20
	updated, preservedStatus, err := policies.SaveUpdaterPolicyAndReleaseToken(ctx, serviceID, created.Revision, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.PollIntervalSeconds != 20 ||
		!preservedStatus.Configured || preservedStatus.Fingerprint != tokenStatus.Fingerprint {
		t.Fatalf("updated policy/preserved token status = %#v / %#v", updated, preservedStatus)
	}
	storedToken, err = policies.GetUpdaterReleaseTokenValue(ctx)
	if err != nil || storedToken != originalToken {
		t.Fatalf("omitted token was not preserved = %q, %v", storedToken, err)
	}
	wrongKeyStore := store.NewMariaDBUpdaterPolicyAdminStore(db, "wrong-encryption-key")
	if _, err := wrongKeyStore.GetUpdaterReleaseTokenStatus(ctx); err == nil {
		t.Fatal("release token with a wrong encryption key was reported usable")
	}
	unsafeStoredToken := "github_pat_\U0001f4a5"
	unsafeCiphertext, unsafeNonce, err := security.EncryptSecret(unsafeStoredToken, encryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		ctx,
		`UPDATE secrets SET ciphertext = ?, nonce = ?, value_hash = ? WHERE name = ?`,
		unsafeCiphertext,
		unsafeNonce,
		security.SecretFingerprint(unsafeStoredToken),
		store.UpdaterGitHubReleaseTokenSecretName,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := policies.GetUpdaterReleaseTokenStatus(ctx); err == nil {
		t.Fatal("non-ASCII stored release token was reported usable")
	}
	got, err := policies.GetUpdaterPolicy(ctx, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != updated.Revision || got.API.Host != "127.0.0.1" || got.API.Port != 8090 {
		t.Fatalf("loaded policy = %#v", got)
	}
	listed, err := policies.ListUpdaterPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, policy := range listed {
		if policy.UpdaterID == serviceID {
			found = policy.Revision == 2
			break
		}
	}
	if !found {
		t.Fatalf("saved policy %q was not listed at revision 2", serviceID)
	}

	deleteToken := ""
	deleted, deletedStatus, err := policies.SaveUpdaterPolicyAndReleaseToken(ctx, serviceID, updated.Revision, input, &deleteToken)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Revision != 3 || deletedStatus.Configured {
		t.Fatalf("deleted policy/token status = %#v / %#v", deleted, deletedStatus)
	}
	if _, err := policies.GetUpdaterReleaseTokenValue(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("explicit empty token was not deleted: %v", err)
	}
}
