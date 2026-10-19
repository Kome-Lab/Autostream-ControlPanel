package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/go-sql-driver/mysql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Pass through actual SQL unchanged. Gate only the first real lock call after
// discovery, then let the normal guarded assignment commit before that lock.
type cb16GateKey struct{}
type cb16SnapshotGate struct {
	reached, release chan struct{}
	once             sync.Once
	fired            atomic.Bool
	attempts         atomic.Int32
	firstStream      string
}
type cb16Connector struct {
	base driver.Connector
	gate *cb16SnapshotGate
}
type cb16Connection struct {
	driver.Conn
	gate *cb16SnapshotGate
}

func (c cb16Connector) Driver() driver.Driver { return c.base.Driver() }
func (c cb16Connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &cb16Connection{conn, c.gate}, nil
}
func (c *cb16Connection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if ctx.Value(cb16GateKey{}) == c.gate {
		c.gate.attempts.Add(1)
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *cb16Connection) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if ctx.Value(cb16GateKey{}) == c.gate && strings.Contains(q, "FROM streams WHERE id = ? FOR UPDATE") && c.gate.fired.CompareAndSwap(false, true) {
		if len(args) != 1 {
			return nil, errors.New("fixture stream lock argument count")
		}
		s, ok := args[0].Value.(string)
		if !ok {
			return nil, errors.New("fixture lock argument type")
		}
		c.gate.firstStream = s
		close(c.gate.reached)
		select {
		case <-c.gate.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c *cb16Connection) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *cb16Connection) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}
func (c *cb16Connection) Ping(ctx context.Context) error { return c.Conn.(driver.Pinger).Ping(ctx) }
func (c *cb16Connection) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}
func (c *cb16Connection) IsValid() bool { return c.Conn.(driver.Validator).IsValid() }
func (c *cb16Connection) CheckNamedValue(v *driver.NamedValue) error {
	if check, ok := c.Conn.(driver.NamedValueChecker); ok {
		return check.CheckNamedValue(v)
	}
	return driver.ErrSkip
}

func TestUI188CB16MariaDBHTTPFreshSnapshot(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if dsn == "" {
		t.Skip("real MariaDB not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	base, err := database.OpenFromEnv(t.Context())
	if err != nil {
		t.Fatal("real DB unavailable")
	}
	defer base.Close()
	if database.RunEmbeddedMigrations(t.Context(), base) != nil {
		t.Fatal("existing migrations failed")
	}
	gate := &cb16SnapshotGate{reached: make(chan struct{}), release: make(chan struct{})}
	defer gate.once.Do(func() { close(gate.release) })
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("fixture DSN")
	}
	connector, err := mysql.NewConnector(config)
	if err != nil {
		t.Fatal("fixture connector")
	}
	db := sql.OpenDB(cb16Connector{connector, gate})
	db.SetMaxOpenConns(8)
	defer db.Close()
	a := store.NewMariaDBAuthStore(db)
	streams := store.NewMariaDBStreamStore(db)
	profiles := store.NewMariaDBProfileStore(db)
	secrets := store.NewMariaDBSecretStore(db, "cb16-http-test-only-key")
	leases := store.NewMariaDBRuntimeSecretLeaseStore(db)
	id := "cb16-http-" + time.Now().UTC().Format("150405.000000000")
	token, err := a.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve"})
	if err != nil {
		t.Fatal("fixture token")
	}
	registration := store.ServiceRegistration{ServiceID: id, ServiceType: "discord_bot", ServiceName: id, Host: "bot.example.com", Port: 443, SSLEnabled: true, PublicURL: "https://bot.example.com", Capabilities: map[string]any{}}
	if _, err = a.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal("fixture precreate")
	}
	if _, err = a.RegisterService(t.Context(), token, registration); err != nil {
		t.Fatal("fixture registration")
	}
	secret, err := secrets.UpdateSecret(t.Context(), "discord_bot_token_"+id, "synthetic-cb16-http-value")
	if err != nil {
		t.Fatal("fixture secret")
	}
	profile, err := profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, id, map[string]any{"service_id": id, "bot_token_secret_name": secret.Name, "bot_token_fingerprint": secret.Fingerprint, "guild_id": "100000000000000001", "voice_channel_id": "100000000000000003"})
	if err != nil {
		t.Fatal("fixture profile")
	}
	current, err := streams.CreateStream(t.Context(), "HTTP current A")
	if err != nil {
		t.Fatal("fixture current")
	}
	other, err := streams.CreateStream(t.Context(), "HTTP retained created B")
	if err != nil {
		t.Fatal("fixture other")
	}
	for _, s := range []store.Stream{current, other} {
		if _, err = streams.UpdateStreamSettings(t.Context(), s.ID, store.StreamSettings{DiscordConfigID: profile.ID, AutoStartTrigger: "discord_voice_join"}); err != nil {
			t.Fatal("fixture settings")
		}
	}
	if _, err = a.AssignServiceToStreamGuarded(t.Context(), store.ServiceAssignmentMutation{ServiceID: id, StreamID: current.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal("fixture assign A")
	}
	handler := NewServer(streams, WithAuthStore(a), WithServiceRegistryStore(a), WithAuditStore(store.NewMariaDBAuditStore(db)), WithProfileStore(profiles), WithSecretStore(secrets), WithRuntimeSecretLeaseStore(leases))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		handler.ServeHTTP(w, r.WithContext(context.WithValue(ctx, cb16GateKey{}, gate)))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	type reply struct {
		status int
		body   []byte
		cache  string
		err    error
	}
	done := make(chan reply, 1)
	go func() {
		r, e := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/services/runtime-config?service_id="+id, nil)
		if e != nil {
			done <- reply{err: e}
			return
		}
		r.Header.Set("Authorization", "Bearer "+token.RawToken)
		response, e := server.Client().Do(r)
		if e != nil {
			done <- reply{err: e}
			return
		}
		defer response.Body.Close()
		body, e := io.ReadAll(response.Body)
		done <- reply{response.StatusCode, body, response.Header.Get("Cache-Control"), e}
	}()
	select {
	case <-gate.reached:
	case <-ctx.Done():
		t.Fatal("HTTP real first-lock not reached")
	}
	if gate.firstStream != current.ID {
		t.Fatal("HTTP did not discover A")
	}
	mutation := store.NewMariaDBAuthStore(base)
	if _, err = mutation.AssignServiceToStreamGuarded(ctx, store.ServiceAssignmentMutation{ServiceID: id, StreamID: other.ID, AssignmentRole: "primary"}); err != nil {
		t.Fatal("HTTP concurrent real assignment B")
	}
	gate.once.Do(func() { close(gate.release) })
	var got reply
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("HTTP completion")
	}
	var failure struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(got.body, &failure)
	t.Logf("real_HTTP_status=%d safe_error_code=%q snapshot_attempts=%d", got.status, failure.Code, gate.attempts.Load())
	if got.err != nil || got.status != 200 {
		t.Fatal("real HTTP must converge to fresh current B")
	}
	if got.cache != "no-store" || strings.Contains(string(got.body), "synthetic-cb16-http-value") || strings.Contains(string(got.body), token.RawToken) {
		t.Fatal("HTTP cache or secret exposure")
	}
	var payload serviceRuntimeConfigResponse
	if json.Unmarshal(got.body, &payload) != nil || payload.CurrentDiscordBindings == nil || len(payload.CurrentDiscordBindings.Bindings) != 1 || len(payload.Assignments) != 1 {
		t.Fatal("HTTP explicit binding shape")
	}
	binding := payload.CurrentDiscordBindings.Bindings[0]
	if binding.StreamID != other.ID || !binding.Ready || !binding.AssignmentEpoch.Equal(payload.Assignments[0].AssignedAt) || gate.attempts.Load() != 2 {
		t.Fatal("HTTP mixed or stale current tuple")
	}
	var leaseCount int
	if base.QueryRowContext(ctx, "SELECT COUNT(*) FROM runtime_secret_leases WHERE service_id=?", id).Scan(&leaseCount) != nil || leaseCount != 0 {
		t.Fatal("HTTP Snapshot created lease")
	}
	t.Log("real_HTTP_current_B=true same_epoch=true secret_leases=0 response_request_count=1")
}
