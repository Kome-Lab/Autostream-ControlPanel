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
	"reflect"
	"sync"
	"testing"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	"github.com/example/autostream-control-panel/internal/store"
)

// Keep Stream and service-registry stores concrete for their sealed same-DB
// snapshot checks. Pause the existing fresh authenticated Session read only
// after observing the real committed Resume, before its child Start/Claim.
type msrResumeCommitBarrierStore struct {
	store.MariaDBAuthStore
	streams   store.MariaDBStreamStore
	db        *sql.DB
	requestID string
	committed chan store.AutoStartHoldAttempt
	release   <-chan struct{}
	mu        sync.Mutex
	paused    bool
}

var _ identityLifecycleOwner = (*msrResumeCommitBarrierStore)(nil)

func (s *msrResumeCommitBarrierStore) GetSession(ctx context.Context, token string) (store.Session, error) {
	session, err := s.MariaDBAuthStore.GetSession(ctx, token)
	if err != nil {
		return session, err
	}
	var streamID string
	err = s.db.QueryRowContext(ctx, "SELECT stream_id FROM stream_auto_start_attempts WHERE attempt_id=?", s.requestID).Scan(&streamID)
	if errors.Is(err, sql.ErrNoRows) {
		return session, nil
	}
	if err != nil {
		return session, err
	}
	attempt, err := s.streams.InspectAutoStartAttempt(ctx, streamID, s.requestID)
	if err != nil {
		return session, err
	}
	if attempt.Request.Operation != store.AutoStartExplicitResume || attempt.Request.ActorUserID != session.UserID || !attempt.Final || !attempt.Hold.Known || attempt.Hold.Held || attempt.StartResultRecorded {
		return session, nil
	}
	s.mu.Lock()
	pause := !s.paused
	s.paused = true
	s.mu.Unlock()
	if !pause {
		return session, nil
	}
	select {
	case s.committed <- attempt:
	case <-ctx.Done():
		return session, ctx.Err()
	}
	select {
	case <-s.release:
		return session, nil
	case <-ctx.Done():
		return session, ctx.Err()
	}
}

func msrSettingsAfterResumeCommit(t *testing.T, changeSettings bool) {
	t.Helper()
	st := msrSQLStores(t)
	concrete, ok := st.streams.(store.MariaDBStreamStore)
	if !ok {
		t.Fatal("post-commit barrier requires the concrete disposable SQL store")
	}
	concreteAuth, ok := st.auth.(store.MariaDBAuthStore)
	if !ok {
		t.Fatal("post-commit Session barrier requires the concrete disposable Auth store")
	}
	requestID := "msr-after-commit-current"
	if changeSettings {
		requestID = "msr-after-commit-stale"
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	barrier := &msrResumeCommitBarrierStore{MariaDBAuthStore: concreteAuth, streams: concrete, db: st.db, requestID: requestID, committed: make(chan store.AutoStartHoldAttempt, 1), release: release}
	st.auth = barrier
	f := msrFixtureFromStores(t, st)
	if _, ok := f.handler.streams.(store.MariaDBStreamStore); !ok {
		t.Fatal("Session barrier changed the sealed concrete Stream store")
	}
	if _, ok := f.handler.services.(store.MariaDBAuthStore); !ok {
		t.Fatal("Session barrier changed the sealed concrete service-registry store")
	}
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
