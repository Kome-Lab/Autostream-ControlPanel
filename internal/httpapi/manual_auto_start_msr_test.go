//go:build manual_stop_integration

package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	"github.com/example/autostream-control-panel/internal/database"
	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	"github.com/example/autostream-control-panel/internal/manualhold"
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/go-sql-driver/mysql"
)

// These roots require a disposable MariaDB schema. The dedicated MSR lane is
// strict; ordinary Go runs retain the repository's optional SQL-test convention.
func msrSQLStores(t *testing.T) outputSecretStores {
	t.Helper()
	if os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN") == "" && os.Getenv("AUTOSTREAM_MSR_REQUIRED") == "1" {
		t.Fatal("MSR required lane has no disposable MariaDB DSN")
	}
	if raw := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN"); raw != "" {
		cfg, err := mysql.ParseDSN(strings.TrimPrefix(raw, "mysql://"))
		if err != nil || os.Getenv("GITHUB_ACTIONS") != "true" || cfg.Net != "tcp" || cfg.Addr != "127.0.0.1:3306" || cfg.User != "autostream_msr_ci" || cfg.DBName != "autostream_msr_ci" || cfg.Passwd != "synthetic-msr-only" || !cfg.ParseTime || cfg.MultiStatements || cfg.TLSConfig != "" {
			t.Fatal("MSR SQL fixture refuses non-disposable configuration")
		}
	}
	st := preparationClaimStores(t, true)
	migration, err := database.ManualAutoStartHoldMigrationSQL()
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range strings.Split(migration, ";") {
		if strings.TrimSpace(statement) != "" {
			if _, err = st.db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal("explicit disposable 094 migration:", err)
			}
		}
	}
	return st
}

type msrDispatcher struct {
	fakeServiceDispatcher
	mu                    sync.Mutex
	owner                 store.ManualAutoStartHoldStore
	directory             string
	guards                map[string]*manualhold.Guard
	remotes               map[string]msrRemoteTarget
	starts, stops, closes int
	failClose             bool
	present               bool
	botURL                string
	beforeClose           func(context.Context, store.RegisteredService, lifecycle.Record) error
}

func msrProducer(component lifecycle.ComponentID, id string) lifecycle.ProducerRef {
	return lifecycle.ProducerRef{ComponentID: component, ServiceID: lifecycle.ID(id), HostID: "synthetic-msr-host", BootID: "synthetic-msr-boot", ProcessIncarnation: "synthetic-msr-process", WriterID: "manual-start-admission"}
}
func (d *msrDispatcher) StartAdmissionIdentity(_ context.Context, s store.RegisteredService) (lifecycle.ProducerRef, error) {
	d.mu.Lock()
	remote, ok := d.remotes[s.ServiceID]
	d.mu.Unlock()
	if ok {
		return remote.Producer, nil
	}
	component := lifecycle.ComponentID("C04")
	if s.ServiceType == "worker" {
		component = "C06"
	}
	return msrProducer(component, s.ServiceID), nil
}
func (d *msrDispatcher) guard(s store.RegisteredService) (*manualhold.Guard, error) {
	producer, _ := d.StartAdmissionIdentity(context.Background(), s)
	d.mu.Lock()
	defer d.mu.Unlock()
	if g := d.guards[s.ServiceID]; g != nil {
		return g, nil
	}
	g, err := manualhold.New(filepath.Join(d.directory, s.ServiceID), manualhold.InspectorFunc(func(ctx context.Context, r lifecycle.Record) (lifecycle.Record, error) {
		resource, e := manualhold.Resource(r)
		if e != nil {
			return lifecycle.Record{}, e
		}
		a, e := d.owner.InspectAutoStartAdmission(ctx, string(resource.ResourceID), string(r.Header.Correlation.RequestID))
		if e != nil {
			return lifecycle.Record{}, e
		}
		for _, saved := range a.Records {
			if manualhold.Equal(saved, r) {
				return saved, nil
			}
		}
		return lifecycle.Record{}, manualhold.ErrRefused
	}), func() (lifecycle.ProducerRef, error) { return producer, nil })
	if err == nil {
		g.Require = func() bool { return true }
		d.guards[s.ServiceID] = g
	}
	return g, err
}
func (d *msrDispatcher) CloseStartAdmission(ctx context.Context, s store.RegisteredService, r lifecycle.Record) (lifecycle.Record, error) {
	d.mu.Lock()
	d.closes++
	failed := d.failClose
	hook := d.beforeClose
	remote, remotePresent := d.remotes[s.ServiceID]
	d.mu.Unlock()
	if failed {
		return lifecycle.Record{}, manualhold.ErrUnknown
	}
	if hook != nil {
		if e := hook(ctx, s, r); e != nil {
			return lifecycle.Record{}, e
		}
	}
	if remotePresent {
		return remote.Close(ctx, r)
	}
	g, err := d.guard(s)
	if err != nil {
		return lifecycle.Record{}, err
	}
	resource, err := manualhold.Resource(r)
	if err != nil {
		return lifecycle.Record{}, err
	}
	return g.Close(ctx, string(resource.ResourceID), r)
}
func (d *msrDispatcher) ObserveAutoStartCondition(context.Context, store.RegisteredService, string, uint64, string) (servicecall.AutoStartCondition, error) {
	return servicecall.AutoStartCondition{}, manualhold.ErrUnknown
}
func (d *msrDispatcher) Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult {
	d.mu.Lock()
	d.startRequest = req
	d.startedStream = stream
	d.mu.Unlock()
	a, ok := ctx.Value(savedStartAdmissionKey{}).(store.AutoStartAdmission)
	if !ok {
		return []servicecall.DispatchResult{{Code: "missing_frozen_admission", Success: false}}
	}
	for _, service := range services {
		if service.ServiceType != "encoder_recorder" && service.ServiceType != "worker" {
			continue
		}
		var record *lifecycle.Record
		for i := range a.Records {
			if a.Records[i].Header.DomainRef.Resources[0].OwnerID == lifecycle.ID(service.ServiceID) {
				record = &a.Records[i]
			}
		}
		if record == nil {
			return []servicecall.DispatchResult{{ServiceID: service.ServiceID, Code: "missing_target_admission", Success: false}}
		}
		g, err := d.guard(service)
		if err != nil {
			return []servicecall.DispatchResult{{ServiceID: service.ServiceID, Code: "guard_unknown", Success: false}}
		}
		lease, err := g.Begin(ctx, stream.ID, record)
		if err == nil {
			err = lease.BeforeLaunch(ctx)
		}
		if err != nil {
			if lease != nil {
				_ = lease.Finish(err)
			}
			return []servicecall.DispatchResult{{ServiceID: service.ServiceID, Code: "fence_refused", Success: false}}
		}
		d.mu.Lock()
		d.starts++
		d.mu.Unlock()
		if err = lease.Finish(nil); err != nil {
			return []servicecall.DispatchResult{{ServiceID: service.ServiceID, Code: "receipt_unknown", Success: false}}
		}
	}
	out := []servicecall.DispatchResult{}
	for _, s := range services {
		out = append(out, servicecall.DispatchResult{ServiceID: s.ServiceID, ServiceType: s.ServiceType, StatusCode: http.StatusAccepted, Success: true})
	}
	return out
}
func (d *msrDispatcher) Stop(_ context.Context, _ store.Stream, services []store.RegisteredService) []servicecall.DispatchResult {
	d.mu.Lock()
	d.stops++
	d.mu.Unlock()
	out := []servicecall.DispatchResult{}
	for _, s := range services {
		out = append(out, servicecall.DispatchResult{ServiceID: s.ServiceID, ServiceType: s.ServiceType, StatusCode: http.StatusAccepted, Success: true})
	}
	return out
}
func (d *msrDispatcher) counts() (int, int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.starts, d.stops, d.closes
}

type msrHTTPFixture struct {
	*outputSecretFixture
	owner      store.ManualAutoStartHoldStore
	dispatcher *msrDispatcher
	server     *httptest.Server
	client     *http.Client
	tokens     map[string]store.ServiceToken
}

func msrFixture(t *testing.T) *msrHTTPFixture {
	t.Helper()
	return msrFixtureFromStores(t, msrSQLStores(t))
}

func msrFixtureFromStores(t *testing.T, st outputSecretStores) *msrHTTPFixture {
	t.Helper()
	base := newOutputSecretFixture(t, st)
	tokens := map[string]store.ServiceToken{base.serviceID: base.token}
	for _, item := range []struct{ id, kind string }{{"msr-worker", "worker"}, {"msr-bot", "discord_bot"}} {
		if item.kind == "discord_bot" {
			tokens[item.id] = msrRegisterBot(t, base, item.id)
		} else {
			tokens[item.id] = base.register(t, item.id, item.kind)
		}
		assignServiceForTest(t, base.handler, base.cookie, base.csrf, item.id, base.stream.ID)
	}
	for id, token := range tokens {
		if _, e := base.services.Heartbeat(t.Context(), token, store.ServiceHeartbeat{ServiceID: id, Status: "ready", Capabilities: map[string]any{"manual_auto_start_hold_v1": true, "output_relay_mode": "direct", "discord_waiting_monitor_v1": true, "scene_appearance_v1": true, "live_video_cover_v1": true, "discord_resolved_target_v2": true}}); e != nil {
			t.Fatal(e)
		}
	}
	secret, e := base.secrets.UpdateSecret(t.Context(), "discord_bot_token_msr_fixture", "synthetic-msr-credential-only")
	if e != nil {
		t.Fatal(e)
	}
	discord, e := base.profiles.CreateProfile(t.Context(), store.ProfileDiscordConfig, "Synthetic MSR Discord", map[string]any{"service_id": "msr-bot", "bot_token_secret_name": secret.Name, "bot_token_fingerprint": secret.Fingerprint})
	if e != nil {
		t.Fatal(e)
	}
	settingsBody, _ := json.Marshal(base.stream)
	var settings store.StreamSettings
	_ = json.Unmarshal(settingsBody, &settings)
	encoderID, workerID, botID := base.serviceID, "msr-worker", "msr-bot"
	settings.NodeSelections = &store.StreamNodeSelectionUpdate{EncoderServiceID: &encoderID, WorkerServiceID: &workerID, DiscordServiceID: &botID}
	settings.DiscordConfigID = discord.ID
	base.stream, e = base.streams.UpdateStreamSettings(t.Context(), base.stream.ID, settings)
	if e != nil {
		t.Fatal(e)
	}
	visual, err := base.handler.streamVisual.Get(t.Context(), base.stream.ID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := base.auth.GetSession(t.Context(), base.cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = base.handler.streamVisual.Update(t.Context(), base.stream.ID, session.UserID, streamvisual.Update{ExpectedRevision: visual.Revision, DiscordTarget: streamvisual.OptionalDiscordTarget{Set: true, Value: streamvisual.DiscordTarget{Mode: "manual", GuildID: "100000000000000001", TextChannelID: "100000000000000003", VoiceChannelID: "100000000000000002"}}}); err != nil {
		t.Fatal(err)
	}
	owner := base.streams.(store.ManualAutoStartHoldStore)
	d := &msrDispatcher{owner: owner, directory: t.TempDir(), guards: map[string]*manualhold.Guard{}}
	base.handler.dispatcher = d
	base.handler.manualAutoStartProducer = func() (lifecycle.ProducerRef, error) { return msrProducer("C01", "control-panel"), nil }
	server := httptest.NewServer(base.handler)
	t.Cleanup(server.Close)
	return &msrHTTPFixture{outputSecretFixture: base, owner: owner, dispatcher: d, server: server, client: &http.Client{Timeout: 15 * time.Second}, tokens: tokens}
}
func (f *msrHTTPFixture) call(t *testing.T, method, endpoint string, body any, requestID string) (int, []byte, http.Header) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+endpoint, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(f.cookie)
	req.Header.Set("X-CSRF-Token", f.csrf)
	req.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		req.Header.Set("X-AutoStream-Request-ID", requestID)
	}
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data, response.Header
}
func (f *msrHTTPFixture) source(t *testing.T) store.Stream {
	t.Helper()
	stream, e := f.streams.GetStream(t.Context(), f.stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	return stream
}
func (f *msrHTTPFixture) hold(t *testing.T) store.AutoStartHold {
	t.Helper()
	hold, e := f.owner.ObserveAutoStartHold(t.Context(), f.stream.ID)
	if e != nil {
		t.Fatal(e)
	}
	return hold
}
func (f *msrHTTPFixture) snapshot(t *testing.T) []byte {
	t.Helper()
	var histories, attempts, admissions, audits, claims int
	for _, item := range []struct {
		table string
		into  *int
	}{{"stream_auto_start_hold_history", &histories}, {"stream_auto_start_attempts", &attempts}, {"stream_auto_start_admissions", &admissions}, {"audit_logs", &audits}, {"stream_auto_start_claim_requests", &claims}} {
		if e := f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+item.table).Scan(item.into); e != nil {
			t.Fatal(e)
		}
	}
	body, _ := json.Marshal(struct {
		Hold                                         store.AutoStartHold
		History, Attempts, Admissions, Audit, Claims int
	}{f.hold(t), histories, attempts, admissions, audits, claims})
	return body
}
func (f *msrHTTPFixture) stop(t *testing.T) string {
	t.Helper()
	if _, e := f.streams.UpdateStreamStatus(t.Context(), f.stream.ID, "live"); e != nil {
		t.Fatal(e)
	}
	id := "msr-stop-" + outputSecretRandom(t)
	status, data, header := f.call(t, http.MethodPost, "/streams/"+f.stream.ID+"/stop", map[string]any{"expected_updated_at": f.source(t).UpdatedAt.UTC().Format(time.RFC3339Nano)}, id)
	if status != http.StatusAccepted && status != http.StatusOK {
		t.Fatalf("manual stop status=%d body=%s", status, data)
	}
	if header.Get("X-AutoStream-Manual-Hold") == "UNKNOWN" {
		t.Fatalf("target closure was not confirmed: %s", data)
	}
	hold := f.hold(t)
	a, e := f.owner.InspectAutoStartAttempt(t.Context(), f.stream.ID, id)
	if e != nil || !a.Final || !hold.Known || !hold.Held || hold.PendingAttemptID != "" {
		t.Fatalf("manual stop hold/attempt incoherent: hold=%+v final=%v err=%v", hold, a.Final, e)
	}
	history, e := f.owner.ListHoldHistory(t.Context(), f.stream.ID)
	if e != nil || len(history) != 3 {
		t.Fatalf("expected constructor+intent+confirmed history: %d %v", len(history), e)
	}
	for _, h := range history {
		if f.db == nil {
			if h.AuditID == "" {
				t.Fatal("missing critical audit correlation")
			}
			continue
		}
		var count int
		if e = f.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_logs WHERE id=?", h.AuditID).Scan(&count); e != nil || count != 1 {
			t.Fatal("hold history lacks atomic critical audit")
		}
	}
	return id
}
func (f *msrHTTPFixture) resumeBody(t *testing.T, id string) map[string]any {
	t.Helper()
	s, h := f.source(t), f.hold(t)
	return map[string]any{"request_id": id, "expected_hold_revision": h.Revision, "expected_stream_revision": s.UpdatedAt.UTC().Format(time.RFC3339Nano), "expected_settings_revision": s.SettingsRevision, "expected_run_id": s.ArchiveRunID}
}
func (f *msrHTTPFixture) resume(t *testing.T, id string) store.AutoStartHoldAttempt {
	t.Helper()
	status, data, _ := f.call(t, http.MethodPost, "/streams/"+f.stream.ID+"/auto-start/resume", f.resumeBody(t, id), "")
	if status != 200 && status != 202 {
		t.Fatalf("resume status=%d body=%s", status, data)
	}
	a, e := f.owner.InspectAutoStartAttempt(t.Context(), f.stream.ID, id)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func msrSecondStore(t *testing.T, db *sql.DB) store.MariaDBStreamStore {
	t.Helper()
	var name string
	if e := db.QueryRowContext(t.Context(), "SELECT DATABASE()").Scan(&name); e != nil {
		t.Fatal(e)
	}
	cfg, e := mysql.ParseDSN(strings.TrimPrefix(os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN"), "mysql://"))
	if e != nil {
		t.Fatal("invalid disposable DSN")
	}
	cfg.DBName = name
	cfg.ParseTime = true
	other, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = other.Close() })
	return store.NewMariaDBStreamStore(other)
}

func TestMSR01ManualStopPersistsHold(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	h := f.hold(t)
	if h.Revision != 3 || h.Reason != store.AutoStartManualStop {
		t.Fatalf("unexpected durable hold: %+v", h)
	}
	status, body, _ := f.call(t, http.MethodGet, "/streams/"+f.stream.ID+"/auto-start-hold", nil, "")
	if status != 200 || !bytes.Contains(body, []byte("\"held\":true")) {
		t.Fatalf("HTTP hold projection %d %s", status, body)
	}
}
func TestMSR02ControlPanelRestartPreservesHold(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	before := f.hold(t)
	second := msrSecondStore(t, f.db)
	server := httptest.NewServer(NewServer(second, WithAuthStore(f.auth), WithServiceRegistryStore(f.services), WithAuditStore(f.audit)))
	defer server.Close()
	f.server = server
	status, _, _ := f.call(t, http.MethodGet, "/streams/"+f.stream.ID+"/auto-start-hold", nil, "")
	after, e := second.ObserveAutoStartHold(t.Context(), f.stream.ID)
	if status != 200 || e != nil || after != before {
		t.Fatal("independent restarted Server/Store changed durable hold")
	}
}
func TestMSR03DiscordBotRestartPreservesHold(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	msrRunBotProcess(t, f, "restart")
}
func TestMSR04VCReconnectPreservesHold(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	msrRunBotProcess(t, f, "reconnect")
}
func TestMSR05ManualStartWhileHeld(t *testing.T) { f := msrFixture(t); f.stop(t); msrManualStart(t, f) }
func TestMSR06ManualStartPreservesHold(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	before := f.hold(t)
	msrManualStart(t, f)
	if got := f.hold(t); got != before {
		t.Fatal("manual Start cleared or revised Manual Hold")
	}
}
func msrManualStart(t *testing.T, f *msrHTTPFixture) {
	t.Helper()
	if _, e := f.streams.UpdateStreamStatus(t.Context(), f.stream.ID, "ready"); e != nil {
		t.Fatal(e)
	}
	status, data, _ := f.call(t, http.MethodPost, "/streams/"+f.stream.ID+"/start", map[string]any{}, "")
	if status != 200 && status != 202 {
		t.Fatalf("manual Start while held status=%d body=%s", status, data)
	}
	starts, _, _ := f.dispatcher.counts()
	f.dispatcher.mu.Lock()
	runRequest, startedStream := f.dispatcher.startRequest, f.dispatcher.startedStream
	f.dispatcher.mu.Unlock()
	if runRequest.ArchiveRunID == "" || runRequest.ArchiveRunID != startedStream.ArchiveRunID || runRequest.ArchiveStartedAt.IsZero() || runRequest.ArchiveProfileID != "" {
		t.Fatal("managed Start lost real claim Run or changed the selected archive profile")
	}
	if starts != 2 {
		t.Fatalf("target start boundaries=%d want2", starts)
	}
}
func TestMSR07ExplicitResume(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	msrEnableAutomatic(t, f)
	msrResumeWithPendingBotRefresh(t, f)
}

func TestMSR08StaleConfirmationRejected(t *testing.T) {
	t.Run("before_begin", msrStaleConfirmationBeforeBegin)
	t.Run("during_close_before_commit", func(t *testing.T) { msrSettingsDuringClose(t, msrFixture(t)) })
	t.Run("after_commit_before_claim", func(t *testing.T) {
		for _, name := range []string{"current_frame", "settings_changed"} {
			t.Run(name, func(t *testing.T) { msrSettingsAfterResumeCommit(t, name == "settings_changed") })
		}
	})
}

// Embed the concrete SQL store before Server construction so every optional
// product interface remains available. This barrier runs only after the real
// Resume transaction has committed, with no SQL lock held by the paused request.
type msrResumeCommitBarrierStore struct {
	store.MariaDBStreamStore
	requestID string
	committed chan store.AutoStartHoldAttempt
	release   <-chan struct{}
}

func (s *msrResumeCommitBarrierStore) CommitExplicitResume(ctx context.Context, req store.AutoStartHoldRequest, condition lifecycle.Fact[bool], evidence []lifecycle.Record) (store.AutoStartHoldAttempt, error) {
	attempt, err := s.MariaDBStreamStore.CommitExplicitResume(ctx, req, condition, evidence)
	if err != nil || req.RequestID != s.requestID {
		return attempt, err
	}
	select {
	case s.committed <- attempt:
	case <-ctx.Done():
		return attempt, ctx.Err()
	}
	select {
	case <-s.release:
		return attempt, nil
	case <-ctx.Done():
		return attempt, ctx.Err()
	}
}

func msrSettingsAfterResumeCommit(t *testing.T, changeSettings bool) {
	t.Helper()
	st := msrSQLStores(t)
	concrete, ok := st.streams.(store.MariaDBStreamStore)
	if !ok {
		t.Fatal("post-commit barrier requires the concrete disposable SQL store")
	}
	requestID := "msr-after-commit-current"
	if changeSettings {
		requestID = "msr-after-commit-stale"
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	barrier := &msrResumeCommitBarrierStore{MariaDBStreamStore: concrete, requestID: requestID, committed: make(chan store.AutoStartHoldAttempt, 1), release: release}
	st.streams = barrier
	f := msrFixtureFromStores(t, st)
	f.stop(t)
	msrEnableAutomatic(t, f)
	f.dispatcher.mu.Lock()
	f.dispatcher.present = true
	f.dispatcher.mu.Unlock()
	original, held := f.source(t), f.hold(t)
	body, err := json.Marshal(f.resumeBody(t, requestID))
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.server.URL+"/streams/"+f.stream.ID+"/auto-start/resume", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(f.cookie)
	request.Header.Set("X-CSRF-Token", f.csrf)
	request.Header.Set("Content-Type", "application/json")
	type reply struct {
		status int
		body   []byte
		err    error
	}
	response := make(chan reply, 1)
	go func() {
		result, callErr := f.client.Do(request)
		if callErr != nil {
			response <- reply{err: callErr}
			return
		}
		defer result.Body.Close()
		data, readErr := io.ReadAll(io.LimitReader(result.Body, 1<<20))
		response <- reply{status: result.StatusCode, body: data, err: readErr}
	}()
	var committed store.AutoStartHoldAttempt
	select {
	case committed = <-barrier.committed:
	case result := <-response:
		t.Fatalf("Resume returned before the real commit barrier: status=%d body=%s err=%v", result.status, result.body, result.err)
	case <-time.After(10 * time.Second):
		t.Fatal("real Resume commit did not reach the post-commit barrier")
	}
	peerDB, err := sql.Open("mysql", msrProcessDSN(t, f))
	if err != nil {
		t.Fatal(err)
	}
	defer peerDB.Close()
	peerDB.SetMaxOpenConns(1)
	peer := store.NewMariaDBStreamStore(peerDB)
	peerContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	observed, err := peer.InspectAutoStartAttempt(peerContext, f.stream.ID, requestID)
	if err != nil {
		t.Fatal("independent connection could not read the committed original Attempt:", err)
	}
	cleared, err := peer.ObserveAutoStartHold(peerContext, f.stream.ID)
	if err != nil || !observed.Final || observed.StartResultRecorded || !cleared.Known || cleared.Held || cleared.PendingAttemptID != "" || cleared.Revision != held.Revision+2 || cleared != committed.Hold {
		t.Fatalf("post-commit Hold was not independently durable before Claim: attempt=%+v hold=%+v err=%v", observed, cleared, err)
	}
	if !reflect.DeepEqual(observed.Request, committed.Request) || observed.Request.RequestID != requestID || observed.Request.StreamID != original.ID || observed.Request.ExpectedHoldRevision != held.Revision || !observed.Request.ExpectedStreamUpdatedAt.Equal(original.UpdatedAt) || observed.Request.ExpectedSettingsRevision != original.SettingsRevision || observed.Request.ExpectedRunID != original.ArchiveRunID {
		t.Fatal("committed Resume did not preserve the original confirmation frame")
	}
	childID := requestID + ":start"
	if _, err = peer.LookupStartClaimRequest(peerContext, f.stream.ID, childID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("child Claim existed before the commit barrier was released: %v", err)
	}
	if _, err = peer.LookupAutoStartAdmission(peerContext, f.stream.ID, childID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("child admission existed before the commit barrier was released: %v", err)
	}
	if starts, _, _ := f.dispatcher.counts(); starts != 0 {
		t.Fatal("target Start ran before the post-commit barrier")
	}
	if changeSettings {
		raw, marshalErr := json.Marshal(original)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		var settings store.StreamSettings
		if err = json.Unmarshal(raw, &settings); err != nil {
			t.Fatal(err)
		}
		settings.Name = "settings changed after durable Resume commit"
		changed, updateErr := peer.UpdateStreamSettings(peerContext, original.ID, settings)
		if updateErr != nil || changed.Name != settings.Name || changed.SettingsRevision == original.SettingsRevision {
			t.Fatalf("independent settings writer did not commit while Resume was paused: stream=%+v err=%v", changed, updateErr)
		}
	}
	unblock()
	select {
	case result := <-response:
		if result.err != nil || result.status != http.StatusOK {
			t.Fatalf("Resume boundary response: status=%d body=%s err=%v", result.status, result.body, result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Resume did not finish after the post-commit barrier was released")
	}
	finished, err := peer.InspectAutoStartAttempt(t.Context(), f.stream.ID, requestID)
	if err != nil || !finished.Final || !finished.StartResultRecorded || finished.Hold != cleared || !reflect.DeepEqual(finished.Request, committed.Request) || finished.Condition.State != lifecycle.Observed || finished.Condition.Value == nil || !*finished.Condition.Value {
		t.Fatalf("original Resume frame or separate Start result changed: attempt=%+v err=%v", finished, err)
	}
	if finished.HoldCommit.Knowledge != lifecycle.Known || finished.HoldCommit.Outcome.Value == nil || *finished.HoldCommit.Outcome.Value != lifecycle.SuccessAtBoundary {
		t.Fatal("durable Hold release was conflated with the child Start result")
	}
	claim, claimErr := peer.LookupStartClaimRequest(t.Context(), f.stream.ID, childID)
	admission, admissionErr := peer.LookupAutoStartAdmission(t.Context(), f.stream.ID, childID)
	starts, stops, closes := f.dispatcher.counts()
	if changeSettings {
		current := f.source(t)
		if starts != 0 || !errors.Is(claimErr, store.ErrNotFound) || !errors.Is(admissionErr, store.ErrNotFound) || current.Name != "settings changed after durable Resume commit" || current.SettingsRevision == original.SettingsRevision {
			t.Fatalf("stale committed frame reached Claim/admission/dispatch: starts=%d claim=%v admission=%v current=%+v", starts, claimErr, admissionErr, current)
		}
		if finished.StartResult.Knowledge != lifecycle.KnowledgeUnknown || finished.StartResult.Outcome.Value != nil {
			t.Fatal("stale child Start became accepted instead of retaining its separate UNKNOWN result")
		}
	} else if starts != 2 || claimErr != nil || admissionErr != nil || claim.ExpectedSettingsRevision != original.SettingsRevision || !claim.ExpectedStreamUpdatedAt.Equal(original.UpdatedAt) || admission.SettingsRevision != original.SettingsRevision || admission.HoldRevision != cleared.Revision {
		t.Fatalf("current-frame control did not exercise both actual target Starts: starts=%d claim=%+v/%v admission=%+v/%v", starts, claim, claimErr, admission, admissionErr)
	}
	history, err := peer.ListHoldHistory(t.Context(), f.stream.ID)
	if err != nil || len(history) != 5 {
		t.Fatalf("post-commit race changed Hold history: rows=%d err=%v", len(history), err)
	}
	resumeHistory := 0
	for _, event := range history {
		if event.RequestID != requestID {
			continue
		}
		resumeHistory++
		var actor, resource, result, metadata string
		if err = peerDB.QueryRowContext(t.Context(), "SELECT actor_user_id,resource_id,result,metadata FROM audit_logs WHERE id=?", event.AuditID).Scan(&actor, &resource, &result, &metadata); err != nil {
			t.Fatal("Resume history lost its atomic audit:", err)
		}
		var audit struct {
			AttemptID           string `json:"attempt_id"`
			HoldRevision        uint64 `json:"hold_revision"`
			ExternalStartProven *bool  `json:"external_start_proven"`
		}
		if err = json.Unmarshal([]byte(metadata), &audit); err != nil || actor != committed.Request.ActorUserID || resource != f.stream.ID || result != "success" || audit.AttemptID != requestID || audit.HoldRevision != event.Hold.Revision || audit.ExternalStartProven == nil || *audit.ExternalStartProven {
			t.Fatalf("Resume history/audit correlation changed: event=%+v metadata=%s err=%v", event, metadata, err)
		}
	}
	if resumeHistory != 2 {
		t.Fatalf("Resume intent/commit history count=%d want2", resumeHistory)
	}
	beforeInspect := f.snapshot(t)
	status, _, _ := f.call(t, http.MethodGet, "/streams/"+f.stream.ID+"/auto-start/resume-attempts/"+requestID, nil, "")
	afterInspect, inspectErr := peer.InspectAutoStartAttempt(t.Context(), f.stream.ID, requestID)
	afterStarts, afterStops, afterCloses := f.dispatcher.counts()
	if status != http.StatusOK || inspectErr != nil || !reflect.DeepEqual(finished, afterInspect) || !bytes.Equal(beforeInspect, f.snapshot(t)) || starts != afterStarts || stops != afterStops || closes != afterCloses {
		t.Fatal("Inspect mutated the original Attempt/Hold/history/audit or replayed a target effect")
	}
}

func msrStaleConfirmationBeforeBegin(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	body := f.resumeBody(t, "msr-resume-stale")
	if _, e := f.streams.UpdateStreamSettings(t.Context(), f.stream.ID, store.StreamSettings{Name: "changed after confirmation"}); e != nil {
		t.Fatal(e)
	}
	before := f.hold(t)
	status, _, _ := f.call(t, http.MethodPost, "/streams/"+f.stream.ID+"/auto-start/resume", body, "")
	if status != http.StatusConflict || f.hold(t) != before {
		t.Fatal("stale confirmation changed hold")
	}
	if _, e := f.owner.InspectAutoStartAttempt(t.Context(), f.stream.ID, "msr-resume-stale"); !errors.Is(e, store.ErrNotFound) {
		t.Fatal("stale confirmation created an Attempt")
	}
}
func TestMSR10DuplicateResume(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	body := f.resumeBody(t, "msr-resume-once")
	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			status, _, _ := f.call(t, http.MethodPost, "/streams/"+f.stream.ID+"/auto-start/resume", body, "")
			statuses <- status
		}()
	}
	close(start)
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != 200 && status != 202 && status != 409 {
			t.Fatalf("duplicate Resume status=%d", status)
		}
	}
	a, e := f.owner.InspectAutoStartAttempt(t.Context(), f.stream.ID, "msr-resume-once")
	if e != nil || !a.Final || a.Hold.Held {
		t.Fatal("single Resume did not complete")
	}
	history, e := f.owner.ListHoldHistory(t.Context(), f.stream.ID)
	if e != nil || len(history) != 5 {
		t.Fatalf("duplicate history count=%d err=%v", len(history), e)
	}
	before := f.snapshot(t)
	status, _, _ := f.call(t, http.MethodGet, "/streams/"+f.stream.ID+"/auto-start/resume-attempts/msr-resume-once", nil, "")
	if status != 200 || !bytes.Equal(before, f.snapshot(t)) {
		t.Fatal("Inspect mutated original Attempt/hold/history/audit")
	}
	_, _, closes := f.dispatcher.counts()
	if closes != 4 {
		t.Fatalf("duplicate Resume dispatched closures=%d want4 includingStop", closes)
	}
}
func TestMSR13ResumeWithoutStartCondition(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	a := f.resume(t, "msr-resume-condition-false")
	if !a.Final || a.Hold.Held || a.Condition.State != lifecycle.Observed || a.Condition.Value == nil || *a.Condition.Value {
		t.Fatal("clear Hold and false condition were not independent facts")
	}
	starts, _, _ := f.dispatcher.counts()
	if starts != 0 {
		t.Fatal("false condition dispatched Start")
	}
}
func TestMSR14PartialRestoreUnknown(t *testing.T) {
	f := msrFixture(t)
	var original string
	if e := f.db.QueryRowContext(t.Context(), "SELECT body FROM stream_auto_start_holds WHERE stream_id=?", f.stream.ID).Scan(&original); e != nil {
		t.Fatal(e)
	}
	f.stop(t)
	if _, e := f.db.ExecContext(t.Context(), "UPDATE stream_auto_start_holds SET revision=1,body=? WHERE stream_id=?", original, f.stream.ID); e != nil {
		t.Fatal(e)
	}
	before := original
	status, _, _ := f.call(t, http.MethodGet, "/streams/"+f.stream.ID+"/auto-start-hold", nil, "")
	hold, e := f.owner.ObserveAutoStartHold(t.Context(), f.stream.ID)
	if status != 503 || !errors.Is(e, lf.ErrUnknown) || hold.Known || !hold.Held {
		t.Fatal("hold-only restore enabled autoStart")
	}
	var after string
	_ = f.db.QueryRowContext(t.Context(), "SELECT body FROM stream_auto_start_holds WHERE stream_id=?", f.stream.ID).Scan(&after)
	if before != after {
		t.Fatal("Inspect repaired partial restore implicitly")
	}
}
func TestMSR15UnauthorizedResume(t *testing.T) {
	t.Run("initial_denial", msrInitialUnauthorizedResume)
	for _, kind := range []string{"session_revoked_during_close", "role_revoked_during_close"} {
		t.Run(kind, func(t *testing.T) { msrRevokeDuringClose(t, msrFixture(t), kind) })
	}
}
func msrInitialUnauthorizedResume(t *testing.T) {
	f := msrFixture(t)
	f.stop(t)
	before := f.hold(t)
	f.cookie = &http.Cookie{Name: sessionCookieName, Value: "synthetic-invalid-session"}
	status, _, _ := f.call(t, http.MethodPost, "/streams/"+f.stream.ID+"/auto-start/resume", f.resumeBody(t, "msr-denied"), "")
	if status != 401 && status != 403 {
		t.Fatalf("unauthorized Resume status=%d", status)
	}
	if !reflect.DeepEqual(before, f.hold(t)) {
		t.Fatal("unauthorized Resume changed hold")
	}
}

func msrRegisterBot(t *testing.T, f *outputSecretFixture, id string) store.ServiceToken {
	t.Helper()
	token, e := f.services.CreateServiceToken(t.Context(), "discord_bot", []string{"service.register", "service.config.read", "service.secret.resolve", "streams.start", "streams.stop"})
	if e != nil {
		t.Fatal(e)
	}
	registration := store.ServiceRegistration{ServiceID: id, ServiceType: "discord_bot", ServiceName: id, PublicURL: "https://" + id + ".example.com", Version: "v2.0.0", Capabilities: map[string]any{"manual_auto_start_hold_v1": true, "discord_waiting_monitor_v1": true, "scene_appearance_v1": true, "live_video_cover_v1": true, "discord_resolved_target_v2": true}}
	if _, e = f.services.PrecreateService(t.Context(), token, registration); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(registration)
	request := httptest.NewRequest(http.MethodPost, "/services/register", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+token.RawToken)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("synthetic Bot registration status=%d", response.Code)
	}
	return token
}

// Native siblings exercise the real authenticated HTTP handlers without
// claiming MariaDB, independent-host, or real Provider qualification.
func TestManualAutoStartHTTPMemory(t *testing.T) {
	for _, name := range []string{"manual_start_preserves_hold", "explicit_resume", "condition_true", "stale_confirmation", "target_ack_unknown"} {
		t.Run(name, func(t *testing.T) {
			f := msrFixtureFromStores(t, preparationClaimStores(t, false))
			if name == "target_ack_unknown" {
				f.dispatcher.failClose = true
				if _, e := f.streams.UpdateStreamStatus(t.Context(), f.stream.ID, "live"); e != nil {
					t.Fatal(e)
				}
				id := "msr-memory-unknown"
				status, _, header := f.call(t, http.MethodPost, "/streams/"+f.stream.ID+"/stop", map[string]any{}, id)
				hold := f.hold(t)
				if (status != 200 && status != 202) || header.Get("X-AutoStream-Manual-Hold") != "UNKNOWN" || !hold.Held || hold.PendingAttemptID != id {
					t.Fatal("target ACK loss cleared hold or became known")
				}
				return
			}
			f.stop(t)
			before := f.hold(t)
			switch name {
			case "manual_start_preserves_hold":
				msrManualStart(t, f)
				if f.hold(t) != before {
					t.Fatal("manual Start cleared Hold")
				}
			case "explicit_resume":
				a := f.resume(t, "msr-memory-resume")
				if a.Hold.Held || !a.Final || a.Condition.Value == nil || *a.Condition.Value {
					t.Fatal("false-condition Resume failed")
				}
			case "condition_true":
				msrEnableAutomatic(t, f)
				f.dispatcher.mu.Lock()
				f.dispatcher.present = true
				f.dispatcher.mu.Unlock()
				a := f.resume(t, "msr-memory-condition-true")
				starts, _, _ := f.dispatcher.counts()
				if a.Hold.Held || a.Condition.Value == nil || !*a.Condition.Value || starts != 2 {
					t.Fatalf("positive Resume did not dispatch both target Starts: hold=%+v condition=%+v starts=%d", a.Hold, a.Condition, starts)
				}
			case "stale_confirmation":
				body := f.resumeBody(t, "msr-memory-stale")
				if _, e := f.streams.UpdateStreamSettings(t.Context(), f.stream.ID, store.StreamSettings{Name: "changed"}); e != nil {
					t.Fatal(e)
				}
				status, _, _ := f.call(t, http.MethodPost, "/streams/"+f.stream.ID+"/auto-start/resume", body, "")
				if status != 409 || f.hold(t) != before {
					t.Fatal("stale confirmation accepted")
				}
			}
		})
	}
}

func (d *msrDispatcher) ObserveAutoStartPresence(ctx context.Context, _ store.RegisteredService, streamID, epoch string) (servicecall.AutoStartCondition, error) {
	d.mu.Lock()
	botURL, present := d.botURL, d.present
	d.mu.Unlock()
	if botURL != "" {
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, botURL+"/streams/"+url.PathEscape(streamID)+"/auto-start-presence?assignment_epoch="+url.QueryEscape(epoch), nil)
		if e != nil {
			return servicecall.AutoStartCondition{}, e
		}
		request.Header.Set("Authorization", "Bearer synthetic-msr-bot-process-read")
		response, e := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if e != nil {
			return servicecall.AutoStartCondition{}, e
		}
		defer response.Body.Close()
		var value servicecall.AutoStartCondition
		if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&value) != nil {
			return value, manualhold.ErrUnknown
		}
		return value, nil
	}
	streams, ok := d.owner.(store.StreamStore)
	if !ok {
		return servicecall.AutoStartCondition{}, manualhold.ErrUnknown
	}
	stream, e := streams.GetStream(ctx, streamID)
	if e != nil {
		return servicecall.AutoStartCondition{}, e
	}
	return servicecall.AutoStartCondition{StreamID: streamID, AssignmentEpoch: epoch, Known: true, Present: present, StreamRevision: stream.UpdatedAt.UTC().Format(time.RFC3339Nano), SettingsRevision: stream.SettingsRevision, RunID: stream.ArchiveRunID}, nil
}

func msrEnableAutomatic(t *testing.T, f *msrHTTPFixture) {
	t.Helper()
	stream := f.source(t)
	raw, _ := json.Marshal(stream)
	var settings store.StreamSettings
	_ = json.Unmarshal(raw, &settings)
	settings.AutoStartTrigger = autoStartTriggerDiscordVoiceJoin
	if _, e := f.streams.UpdateStreamSettings(t.Context(), stream.ID, settings); e != nil {
		t.Fatal(e)
	}
	if _, e := f.streams.UpdateStreamStatus(t.Context(), stream.ID, "ready"); e != nil {
		t.Fatal(e)
	}
}

func (d *msrDispatcher) InspectStartAdmission(ctx context.Context, service store.RegisteredService, original lifecycle.Record) (servicecall.StartAdmissionInspection, error) {
	d.mu.Lock()
	remote, exists := d.remotes[service.ServiceID]
	d.mu.Unlock()
	if exists {
		service.PublicURL = remote.URL
		client := servicecall.Client{Config: servicecall.Config{Timeout: 5 * time.Second, URLPolicy: netpolicy.ServiceURLPolicy{AllowedHosts: map[string]struct{}{"127.0.0.1": {}}}}, HTTP: &http.Client{Timeout: 5 * time.Second}, RuntimeTokenResolver: func(store.RegisteredService) (string, error) { return remote.Token, nil }}
		return client.InspectStartAdmission(ctx, service, original)
	}
	guard, e := d.guard(service)
	if e != nil {
		return servicecall.StartAdmissionInspection{}, e
	}
	resource, e := manualhold.Resource(original)
	if e != nil {
		return servicecall.StartAdmissionInspection{}, e
	}
	record, outcome, e := guard.Inspect(string(resource.ResourceID), original.Header.Correlation.RequestID)
	if e != nil {
		return servicecall.StartAdmissionInspection{}, e
	}
	return servicecall.StartAdmissionInspection{Record: record, Outcome: outcome}, nil
}

// A real settings writer races after Begin while target ACKs are in flight.
// The committed close intent remains held; the old confirmation cannot clear it.
func msrSettingsDuringClose(t *testing.T, f *msrHTTPFixture) {
	t.Helper()
	f.stop(t)
	msrEnableAutomatic(t, f)
	f.dispatcher.mu.Lock()
	f.dispatcher.present = true
	f.dispatcher.mu.Unlock()
	original := f.source(t)
	var once sync.Once
	f.dispatcher.mu.Lock()
	f.dispatcher.beforeClose = func(context.Context, store.RegisteredService, lifecycle.Record) error {
		var changed error
		once.Do(func() {
			raw, _ := json.Marshal(original)
			var settings store.StreamSettings
			_ = json.Unmarshal(raw, &settings)
			settings.Name = "settings changed during target close"
			_, changed = f.streams.UpdateStreamSettings(t.Context(), original.ID, settings)
		})
		return changed
	}
	f.dispatcher.mu.Unlock()
	requestID := "msr-inflight-settings"
	status, _, _ := f.call(t, http.MethodPost, "/streams/"+f.stream.ID+"/auto-start/resume", f.resumeBody(t, requestID), "")
	a, e := f.owner.InspectAutoStartAttempt(t.Context(), f.stream.ID, requestID)
	starts, _, _ := f.dispatcher.counts()
	if (status != 409 && status != 503) || e != nil || a.Final || !a.Hold.Held || a.Hold.PendingAttemptID != requestID || starts != 0 {
		t.Fatalf("settings race escaped CAS or lost close intent: status=%d final=%v held=%v starts=%d err=%v", status, a.Final, a.Hold.Held, starts, e)
	}
	if f.source(t).SettingsRevision == original.SettingsRevision {
		t.Fatal("inflight settings writer was not exercised")
	}
}

func msrRevokeDuringClose(t *testing.T, f *msrHTTPFixture, kind string) {
	t.Helper()
	f.stop(t)
	roleStore := f.auth.(store.RoleStore)
	role, e := roleStore.CreateRole(t.Context(), "synthetic-msr-resumer", []string{"streams.read", "streams.start"})
	if e != nil {
		t.Fatal(e)
	}
	password := outputSecretRandom(t)
	var user store.User
	if memory, ok := f.auth.(*store.MemoryAuthStore); ok {
		e = memory.AddUser(store.User{Username: "msr-resumer", RoleIDs: []string{role.ID}}, password, []string{"streams.read", "streams.start"})
		if e == nil {
			user, e = memory.FindUserByUsername(t.Context(), "msr-resumer")
		}
	} else {
		user, e = f.auth.(store.UserAdminStore).CreateUser(t.Context(), "msr-resumer", "", password, []string{role.ID})
	}
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.auth.(store.UserAdminStore).SetUserStatus(t.Context(), user.ID, "active"); e != nil {
		t.Fatal(e)
	}
	if e = f.auth.ChangePassword(t.Context(), user.ID, outputSecretRandom(t)); e != nil {
		t.Fatal(e)
	}
	session, e := f.auth.CreateSession(t.Context(), user.ID, time.Hour, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	f.cookie = &http.Cookie{Name: sessionCookieName, Value: session.Token}
	f.csrf = session.CSRFToken
	var once sync.Once
	f.dispatcher.mu.Lock()
	f.dispatcher.beforeClose = func(ctx context.Context, _ store.RegisteredService, _ lifecycle.Record) error {
		var changed error
		once.Do(func() {
			if kind == "session_revoked_during_close" {
				changed = f.auth.DeleteSession(ctx, session.Token)
			} else {
				_, changed = roleStore.UpdateRole(ctx, role.ID, role.Name, []string{"streams.read"})
			}
		})
		return changed
	}
	f.dispatcher.mu.Unlock()
	requestID := "msr-midflight-revoke"
	status, responseBody, _ := f.call(t, http.MethodPost, "/streams/"+f.stream.ID+"/auto-start/resume", f.resumeBody(t, requestID), "")
	if status != 401 && status != 403 {
		t.Fatalf("midflight authority revocation status=%d", status)
	}
	attempt, e := f.owner.InspectAutoStartAttempt(t.Context(), f.stream.ID, requestID)
	starts, _, _ := f.dispatcher.counts()
	if e != nil || attempt.Final || !attempt.Hold.Held || attempt.Hold.PendingAttemptID != requestID || starts != 0 {
		t.Fatalf("revoked operation stage mismatch status=%d response=%s final=%v held=%v pending=%s starts=%d err=%v", status, responseBody, attempt.Final, attempt.Hold.Held, attempt.Hold.PendingAttemptID, starts, e)
	}
	history, e := f.owner.ListHoldHistory(t.Context(), f.stream.ID)
	if e != nil || len(history) != 4 {
		t.Fatal("revoked operation must preserve exactly the committed Resume intent")
	}
}
func TestManualAutoStartMidflightAuthorizationMemory(t *testing.T) {
	for _, kind := range []string{"session_revoked_during_close"} {
		t.Run(kind, func(t *testing.T) {
			msrRevokeDuringClose(t, msrFixtureFromStores(t, preparationClaimStores(t, false)), kind)
		})
	}
	t.Run("settings_after_commit", func(t *testing.T) {
		msrSettingsDuringClose(t, msrFixtureFromStores(t, preparationClaimStores(t, false)))
	})
}
