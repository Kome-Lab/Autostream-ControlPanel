package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/store"
)

type mariaDBConfigureMigrationFixture struct {
	name          string
	serviceType   string
	scopes        []string
	configureUsed bool
	wantRevoked   bool
	serviceID     string
	tokenID       string
	nodeCipher    string
	nodeNonce     string
}

func TestOAuthMigrationFixturesUseExplicitPublicHostAllowlist(t *testing.T) {
	configureMariaDBOAuthMigrationFixtureURLPolicy(t)
	if err := netpolicy.ServiceURLPolicyFromEnv().ValidateURL("https://legacy_discord.example.com:18443"); err != nil {
		t.Fatalf("MariaDB migration fixture endpoint must be explicitly allowed: %v", err)
	}
}

func TestMariaDBOAuthAccountRefreshStatusMigrationReapplyAndStaleCAS(t *testing.T) {
	db, ctx := openMariaDBOAuthMigrationTest(t)

	// The normal runner must remain safe after all migrations have been
	// recorded, and 064 itself must tolerate a replay after its DDL completed
	// but before schema_migrations was recorded.
	if err := RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatalf("reapply embedded migrations: %v", err)
	}
	replayEmbeddedMariaDBMigration(
		t,
		ctx,
		db,
		"migrations/064_oauth_account_access_token_refresh_status.sql",
	)
	replayEmbeddedMariaDBMigration(
		t,
		ctx,
		db,
		"migrations/064_oauth_account_access_token_refresh_status.sql",
	)

	var (
		migrationCount int
		columnCount    int
	)
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM schema_migrations
		 WHERE id = '064_oauth_account_access_token_refresh_status.sql'`,
	).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM information_schema.columns
		 WHERE table_schema = DATABASE()
		   AND table_name = 'oauth_accounts'
		   AND column_name = 'token_revision'`,
	).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 || columnCount != 1 {
		t.Fatalf(
			"064 migration reapply state = migrations:%d token_revision_columns:%d",
			migrationCount,
			columnCount,
		)
	}

	const fixtureRefreshToken = "test-fixture-oauth-refresh-token-not-production"
	integrations := store.NewMariaDBIntegrationStore(
		db,
		"mariadb-oauth-refresh-status-fixture-key",
	)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	provider, err := integrations.CreateOAuthProvider(ctx, store.OAuthProvider{
		ProviderType: "google",
		Name:         "MariaDB OAuth refresh status " + suffix,
		Enabled:      true,
		ClientID:     "mariadb-oauth-refresh-status-client",
		RedirectURI:  "https://control.example.com/oauth/callback",
	})
	if err != nil {
		t.Fatalf("create OAuth provider: %v", err)
	}
	account, err := integrations.CreateOAuthAccount(ctx, store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: provider.ProviderType,
		AccountLabel: "MariaDB OAuth refresh status account",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube.force-ssl"},
		RefreshToken: fixtureRefreshToken,
	})
	if err != nil {
		t.Fatalf("create OAuth account: %v", err)
	}
	stale, err := integrations.GetOAuthAccountForDispatch(ctx, account.ID)
	if err != nil {
		t.Fatalf("load OAuth account before re-link: %v", err)
	}
	if stale.TokenRevision == 0 {
		t.Fatal("064 migration did not initialize the OAuth token revision")
	}

	// A successful re-link may receive the same refresh token from Google. It
	// must still advance the durable generation so a refresh started before the
	// re-link cannot write either token metadata or a rotated credential.
	if _, err := integrations.UpdateOAuthAccount(ctx, store.OAuthAccount{
		ID:           account.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.ProviderType,
		AccountLabel: "MariaDB OAuth refresh status account",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube.force-ssl"},
		RefreshToken: fixtureRefreshToken,
	}); err != nil {
		t.Fatalf("re-link OAuth account: %v", err)
	}

	attemptedAt := time.Date(2026, time.August, 9, 3, 0, 0, 0, time.UTC)
	for name, record := range map[string]func() error{
		"attempt": func() error {
			_, err := integrations.RecordOAuthAccountTokenRefreshAttempt(
				ctx, account.ID, stale.TokenRevision, attemptedAt,
			)
			return err
		},
		"failure": func() error {
			_, err := integrations.RecordOAuthAccountTokenRefreshFailure(
				ctx,
				account.ID,
				stale.TokenRevision,
				store.OAuthTokenRefreshFailureReauthorizationRequired,
				true,
				attemptedAt,
			)
			return err
		},
		"success": func() error {
			_, err := integrations.RecordOAuthAccountTokenRefresh(
				ctx,
				account.ID,
				stale.TokenRevision,
				"test-fixture-stale-rotated-refresh-token-not-production",
				attemptedAt,
			)
			return err
		},
	} {
		if err := record(); !errors.Is(err, store.ErrConflict) {
			t.Fatalf("stale OAuth refresh %s error = %v, want ErrConflict", name, err)
		}
	}

	updated, err := integrations.GetOAuthAccountForDispatch(ctx, account.ID)
	if err != nil {
		t.Fatalf("load OAuth account after stale refresh: %v", err)
	}
	if updated.TokenRevision <= stale.TokenRevision {
		t.Fatalf(
			"OAuth re-link did not advance revision: before=%d after=%d",
			stale.TokenRevision,
			updated.TokenRevision,
		)
	}
	if updated.RefreshToken != fixtureRefreshToken {
		t.Fatal("stale refresh modified the re-linked OAuth credential")
	}
	if updated.AccessTokenRefreshedAt != "" ||
		updated.AccessTokenRefreshAttemptedAt != "" ||
		updated.AccessTokenRefreshFailedAt != "" ||
		updated.AccessTokenRefreshFailureCode != "" ||
		updated.AccessTokenRefreshRelinkRequired {
		t.Fatalf("stale refresh wrote OAuth refresh status: %#v", updated)
	}
}

func TestMariaDBLegacyDiscordConfigureTokenMigrationReplaysSafely(t *testing.T) {
	db, ctx := openMariaDBOAuthMigrationTest(t)
	auth := store.NewMariaDBAuthStore(db)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)

	fixtures := []mariaDBConfigureMigrationFixture{
		{
			name:        "legacy_discord",
			serviceType: "discord_bot",
			scopes: []string{
				"service.register", "service.heartbeat", "service.config.read",
				"discord.status.write", "streams.start",
			},
			wantRevoked: true,
		},
		{
			name:        "paired_discord",
			serviceType: "discord_bot",
			scopes: []string{
				"service.register", "service.heartbeat", "service.config.read",
				"discord.status.write", "streams.start", "streams.stop",
			},
		},
		{
			name:        "consumed_discord",
			serviceType: "discord_bot",
			scopes: []string{
				"service.register", "service.heartbeat", "service.config.read",
				"discord.status.write", "streams.start",
			},
			configureUsed: true,
		},
		{
			name:        "worker",
			serviceType: "worker",
			scopes: []string{
				"service.register", "service.heartbeat", "service.config.read",
				"worker.events.write",
			},
		},
	}

	for index := range fixtures {
		current := &fixtures[index]
		current.serviceID = "migration-" + current.name + "-" + suffix
		current.nodeCipher = "test-fixture-node-ciphertext-" + current.name
		current.nodeNonce = "test-fixture-node-nonce-" + current.name
		token, err := auth.CreateServiceToken(ctx, current.serviceType, current.scopes)
		if err != nil {
			t.Fatalf("create %s service token: %v", current.name, err)
		}
		current.tokenID = token.ID
		if _, err := auth.PrecreateService(ctx, token, store.ServiceRegistration{
			ServiceID:   current.serviceID,
			ServiceType: current.serviceType,
			ServiceName: "MariaDB migration fixture " + current.name,
			Host:        current.name + ".example.com",
			Port:        18443 + index,
			SSLEnabled:  true,
			PublicURL:   fmt.Sprintf("https://%s.example.com:%d", current.name, 18443+index),
			Version:     "v1.0.0",
		}); err != nil {
			t.Fatalf("precreate %s service: %v", current.name, err)
		}
		if _, err := auth.SetServiceConfigureToken(
			ctx,
			current.serviceID,
			strings.Repeat("a", 64),
			time.Now().UTC().Add(time.Hour),
		); err != nil {
			t.Fatalf("seed %s Configure Token: %v", current.name, err)
		}
		if current.configureUsed {
			if _, err := db.ExecContext(
				ctx,
				`UPDATE services SET configure_token_used_at = UTC_TIMESTAMP(6) WHERE service_id = ?`,
				current.serviceID,
			); err != nil {
				t.Fatalf("mark %s Configure Token used: %v", current.name, err)
			}
		}
		if _, err := db.ExecContext(
			ctx,
			`UPDATE services
			 SET node_token_ciphertext = ?, node_token_nonce = ?
			 WHERE service_id = ?`,
			current.nodeCipher,
			current.nodeNonce,
			current.serviceID,
		); err != nil {
			t.Fatalf("seed %s runtime-token metadata: %v", current.name, err)
		}
	}

	replayEmbeddedMariaDBMigration(
		t,
		ctx,
		db,
		"migrations/065_revoke_legacy_discord_configure_tokens.sql",
	)
	for _, current := range fixtures {
		assertMariaDBConfigureMigrationFixture(t, ctx, db, current)
	}
	// A retry after migration bookkeeping or host interruption must preserve the
	// exact same boundary: old pending Configure Tokens stay revoked and no
	// active Node Runtime Token is altered.
	replayEmbeddedMariaDBMigration(
		t,
		ctx,
		db,
		"migrations/065_revoke_legacy_discord_configure_tokens.sql",
	)
	for _, current := range fixtures {
		assertMariaDBConfigureMigrationFixture(t, ctx, db, current)
	}
}

func openMariaDBOAuthMigrationTest(t *testing.T) (*sql.DB, context.Context) {
	return openMariaDBMigrationTest(t, true)
}

func openMariaDBMigrationTest(t *testing.T, applyEmbedded bool) (*sql.DB, context.Context) {
	t.Helper()
	configureMariaDBOAuthMigrationFixtureURLPolicy(t)
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	db, err := OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if applyEmbedded {
		if err := RunEmbeddedMigrations(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	return db, ctx
}

func configureMariaDBOAuthMigrationFixtureURLPolicy(t *testing.T) {
	t.Helper()
	// The fixtures use isolated example.com endpoints. Keep the production
	// public-host allowlist enabled while permitting those explicit test hosts.
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "true")
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
}

func replayEmbeddedMariaDBMigration(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	path string,
) {
	t.Helper()
	body, err := embeddedMigrations.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, statement := range splitSQLStatements(string(body)) {
		if _, err := connection.ExecContext(ctx, statement); err != nil {
			t.Fatalf("replay %s: %v\n%s", path, err, statement)
		}
	}
}

func assertMariaDBConfigureMigrationFixture(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture mariaDBConfigureMigrationFixture,
) {
	t.Helper()
	var (
		configureHash string
		expiresAt     sql.NullTime
		usedAt        sql.NullTime
		nodeCipher    string
		nodeNonce     string
		tokenID       string
	)
	if err := db.QueryRowContext(
		ctx,
		`SELECT COALESCE(configure_token_hash, ''), configure_token_expires_at,
		        configure_token_used_at, COALESCE(node_token_ciphertext, ''),
		        COALESCE(node_token_nonce, ''), token_id
		 FROM services
		 WHERE service_id = ?`,
		fixture.serviceID,
	).Scan(&configureHash, &expiresAt, &usedAt, &nodeCipher, &nodeNonce, &tokenID); err != nil {
		t.Fatalf("load %s migration fixture: %v", fixture.name, err)
	}
	if fixture.wantRevoked {
		if configureHash != "" || expiresAt.Valid {
			t.Fatalf("legacy pending Configure Token was not revoked for %s", fixture.name)
		}
	} else if configureHash == "" || !expiresAt.Valid {
		t.Fatalf("Configure Token was incorrectly revoked for %s", fixture.name)
	}
	if usedAt.Valid != fixture.configureUsed {
		t.Fatalf("Configure Token used state changed for %s", fixture.name)
	}
	if nodeCipher != fixture.nodeCipher || nodeNonce != fixture.nodeNonce || tokenID != fixture.tokenID {
		t.Fatalf("runtime token metadata changed for %s", fixture.name)
	}
}
