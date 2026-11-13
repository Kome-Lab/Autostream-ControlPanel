package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	evidence "github.com/Kome-Lab/Autostream-Contracts/v3evidence"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/v3authorization"
)

// Only the existing CP authentication boundary is doubled here. No signer,
// clock, v3 authority, provider, credential or server process is installed.
type v3AuthorizationSessionFixture struct {
	store.AuthStore
	status string
}

func (f v3AuthorizationSessionFixture) GetSession(ctx context.Context, token string) (store.Session, error) {
	if ctx.Err() != nil {
		return store.Session{}, ctx.Err()
	}
	if token != "synthetic-session" {
		return store.Session{}, store.ErrUnauthorized
	}
	return store.Session{UserID: "synthetic-user", CSRFTokenHash: security.HashToken("synthetic-csrf")}, nil
}

func (f v3AuthorizationSessionFixture) GetUser(context.Context, string) (store.User, error) {
	return store.User{ID: "synthetic-user", Status: f.status, Roles: []string{"super_admin"}}, nil
}

func (f v3AuthorizationSessionFixture) GetUserPermissions(context.Context, string) ([]string, error) {
	// A v2 administrator wildcard still cannot supply missing v3 dependencies.
	return []string{"*", "system_updates.execute"}, nil
}

func v3AuthorizationHTTPFixtureRequest(path, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "synthetic-session"})
	request.Header.Set("X-CSRF-Token", "synthetic-csrf")
	return request
}

func v3AuthorizationHTTPFixtureBodies(t *testing.T) (string, string) {
	t.Helper()
	issue, err := json.Marshal(v3authorization.IssueRequest{
		OriginalRequest: json.RawMessage(`{"binding":"synthetic-binding-only"}`),
		Frame: evidence.BackendReadFrame{
			Issuer: "backend", ServingIncarnation: "incarnation", Generation: "generation",
			SnapshotID: "snapshot", ObservationProof: "observation",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	query, err := json.Marshal(v3authorization.Lookup{
		Scope:     evidence.Scope{Realm: "realm", Host: "host", Resource: "resource", Backend: "backend"},
		Execution: evidence.Execution{RequestID: "request", ExecutionKey: "execution", OperationID: "operation", AttemptID: "attempt", StepID: "step"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(issue), string(query)
}

func TestV3AuthorizationHandlerAuthenticationAndClosedComposition(t *testing.T) {
	issue, lookup := v3AuthorizationHTTPFixtureBodies(t)
	for _, endpoint := range []struct{ path, body string }{
		{"/v3/cp-authorizations", issue},
		{"/v3/cp-authorizations/lookup", lookup},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			for _, tc := range []struct {
				name, status string
				cookie, csrf bool
				want         int
			}{
				{"unauthenticated", "active", false, true, http.StatusUnauthorized},
				{"csrf-required", "active", true, false, http.StatusForbidden},
				{"password-change", "pending_password_change", true, true, http.StatusForbidden},
				{"disabled", "disabled", true, true, http.StatusUnauthorized},
				{"locked", "locked", true, true, http.StatusUnauthorized},
				{"other-status", "pending", true, true, http.StatusForbidden},
				{"v2-admin-is-not-v3-authority", "active", true, true, http.StatusServiceUnavailable},
			} {
				t.Run(tc.name, func(t *testing.T) {
					server := &Server{auth: v3AuthorizationSessionFixture{status: tc.status}}
					handler := server.V3AuthorizationHandler(nil)
					request := v3AuthorizationHTTPFixtureRequest(endpoint.path, endpoint.body)
					if !tc.cookie {
						request.Header.Del("Cookie")
					}
					if !tc.csrf {
						request.Header.Del("X-CSRF-Token")
					}
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != tc.want || response.Header().Get("Cache-Control") != "no-store" {
						t.Fatalf("status=%d, cache=%q, want=%d", response.Code, response.Header().Get("Cache-Control"), tc.want)
					}
					if response.Header().Get("X-Evidence-Result") != "" {
						t.Fatal("failed request appeared to issue evidence")
					}
				})
			}
		})
	}
}

func TestV3AuthorizationHandlerStrictBodyBoundary(t *testing.T) {
	server := &Server{auth: v3AuthorizationSessionFixture{status: "active"}}
	handler := server.V3AuthorizationHandler(nil)
	for name, body := range map[string]string{
		"empty":     "",
		"missing":   `{}`,
		"extra":     `{"original_request":{},"backend_read_frame":{},"principal":"attacker"}`,
		"duplicate": `{"original_request":{},"original_request":{},"backend_read_frame":{}}`,
		"case":      `{"Original_Request":{},"backend_read_frame":{}}`,
		"null":      `{"original_request":null,"backend_read_frame":{}}`,
		"trailing":  `{} {}`,
		"oversize":  strings.Repeat(" ", maxControlRequestBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, v3AuthorizationHTTPFixtureRequest("/v3/cp-authorizations", body))
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "v3_authorization_invalid") {
				t.Fatalf("invalid body status=%d", response.Code)
			}
		})
	}
}

func TestV3AuthorizationResultDeliversExactBytesWithoutRedaction(t *testing.T) {
	// Deliberate whitespace and an authorization-named field detect accidental
	// JSON reserialization/redaction. This tests transport, not authenticity.
	wire := []byte(" {\"cp_authorization\": {\"decision\":\"AUTHORIZED\"},\"proof\":\"synthetic-proof\"} \n")
	for _, status := range []v3authorization.Status{v3authorization.Issued, v3authorization.Replay} {
		for _, applicability := range []v3authorization.Applicability{
			v3authorization.Current, v3authorization.Expired, v3authorization.Revoked, v3authorization.ApplicabilityUnknown,
		} {
			response := httptest.NewRecorder()
			writeV3AuthorizationResult(response, v3authorization.Result{Status: status, Envelope: wire, Applicability: applicability})
			want := http.StatusOK
			if status == v3authorization.Issued {
				want = http.StatusCreated
			}
			if response.Code != want || !bytes.Equal(response.Body.Bytes(), wire) {
				t.Fatal("evidence transport rewrote bytes or status")
			}
			if response.Header().Get("X-Evidence-Result") != string(status) || response.Header().Get("X-Evidence-Applicability") != string(applicability) {
				t.Fatal("historical evidence applicability was lost")
			}
		}
	}
}

func TestV3AuthorizationResultErrorsAreSafeAndNotNegativeEvidence(t *testing.T) {
	for _, tc := range []struct {
		status v3authorization.Status
		want   int
	}{
		{v3authorization.Denied, http.StatusForbidden},
		{v3authorization.Conflict, http.StatusConflict},
		{v3authorization.Pending, http.StatusAccepted},
		{v3authorization.Unknown, http.StatusServiceUnavailable},
		{v3authorization.Unavailable, http.StatusServiceUnavailable},
		{v3authorization.Invalid, http.StatusBadRequest},
		{v3authorization.Status("unexpected"), http.StatusServiceUnavailable},
	} {
		response := httptest.NewRecorder()
		writeV3AuthorizationResult(response, v3authorization.Result{
			Status: tc.status, Code: "/private/db SELECT key=secret", Envelope: []byte("not-evidence"),
		})
		if response.Code != tc.want || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "not-evidence") {
			t.Fatal("unsafe dependency details leaked or status mismatch")
		}
		if response.Header().Get("X-Evidence-Result") != "" {
			t.Fatal("HTTP error represented as issued negative evidence")
		}
	}
	for _, result := range []v3authorization.Result{
		{Status: v3authorization.Issued},
		{Status: v3authorization.Replay, Envelope: []byte(`{}`), AuthorizesExecution: true},
	} {
		response := httptest.NewRecorder()
		writeV3AuthorizationResult(response, result)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatal("invalid success escaped the transport boundary")
		}
	}
}

func TestV3AuthorizationNotRegisteredByDefault(t *testing.T) {
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(v3AuthorizationSessionFixture{status: "active"}))
	for _, path := range []string{"/v3/cp-authorizations", "/v3/cp-authorizations/lookup"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, v3AuthorizationHTTPFixtureRequest(path, `{}`))
		if response.Code != http.StatusNotFound {
			t.Fatalf("v3 handler was enabled by default: %d", response.Code)
		}
	}
}

// This fixture implements only explicit test dependencies. Its in-memory Store,
// byte-allowlist Integrity, clock and v3 authority are NOT production providers.
// The actual product Service, Codec, session boundary and Handler run below.
type v3AuthorizationHTTPDependencies struct {
	authorityMu sync.Mutex
	mu          sync.Mutex
	intent      v3authorization.Intent
	record      *v3authorization.Record
	wire        []byte
	claims      int
	finishes    int
	seals       int
	denyRead    bool
}

func (f *v3AuthorizationHTTPDependencies) Resolve(ctx context.Context, raw json.RawMessage) (v3authorization.Intent, error) {
	f.authorityMu.Lock()
	defer f.authorityMu.Unlock()
	if !bytes.Equal(raw, []byte(`{"synthetic":true}`)) {
		return v3authorization.Intent{}, v3authorization.ErrConflict
	}
	return f.intent, ctx.Err()
}

func (f *v3AuthorizationHTTPDependencies) decision() v3authorization.Decision {
	return v3authorization.Decision{
		Principal: "synthetic-user", Scope: f.intent.Scope, Action: v3authorization.Action,
		PolicyRevision: "policy", TrustRevision: "trust", AuthorityRevision: "authority-1", TargetRevision: "target-1", State: "ALLOWED",
	}
}

func (f *v3AuthorizationHTTPDependencies) Read(ctx context.Context, _ v3authorization.Principal, _ v3authorization.Intent) (v3authorization.Decision, error) {
	f.authorityMu.Lock()
	defer f.authorityMu.Unlock()
	return f.decision(), ctx.Err()
}

func (f *v3AuthorizationHTTPDependencies) WithCurrent(ctx context.Context, _ v3authorization.Principal, _ v3authorization.Intent, expected v3authorization.Decision, fn func(v3authorization.Decision) error) error {
	f.authorityMu.Lock()
	defer f.authorityMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	current := f.decision()
	if current != expected {
		return v3authorization.ErrConflict
	}
	return fn(current)
}

func (f *v3AuthorizationHTTPDependencies) AuthorizeRead(ctx context.Context, principal v3authorization.Principal, lookup v3authorization.Lookup) error {
	f.authorityMu.Lock()
	defer f.authorityMu.Unlock()
	if f.denyRead || principal.ID != "synthetic-user" || lookup.Scope != f.intent.Scope || lookup.Execution != f.intent.Execution {
		return v3authorization.ErrDenied
	}
	return ctx.Err()
}

func (f *v3AuthorizationHTTPDependencies) Prepare(ctx context.Context, _ v3authorization.Intent, _ v3authorization.Decision) (evidence.Validity, error) {
	return evidence.Validity{
		Status: "SPECIFIED", Issued: &evidence.TimePoint{TimeBase: "synthetic-clock", BootID: "boot", Tick: 10},
		Expiry: &evidence.TimePoint{TimeBase: "synthetic-clock", BootID: "boot", Tick: 100}, QualificationProof: "synthetic-qualification",
	}, ctx.Err()
}

func (f *v3AuthorizationHTTPDependencies) Seal(ctx context.Context, envelope evidence.Envelope) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seals++
	envelope.Integrity = evidence.Integrity{ProfileID: "synthetic-only", KeyID: "synthetic-key", KeyRevision: "synthetic-revision", Proof: "synthetic-byte-allowlist"}
	wire, err := evidence.Encode(envelope)
	if err != nil {
		return nil, err
	}
	f.wire = append([]byte(nil), wire...)
	return wire, ctx.Err()
}

func (f *v3AuthorizationHTTPDependencies) Verify(ctx context.Context, envelope evidence.Envelope, wire []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	decoded, err := evidence.Decode(wire)
	if err != nil || !bytes.Equal(wire, f.wire) || !reflect.DeepEqual(envelope, decoded) {
		return v3authorization.ErrUnavailable
	}
	return ctx.Err()
}

func (f *v3AuthorizationHTTPDependencies) Inspect(ctx context.Context, _ evidence.Envelope, _ []byte) (v3authorization.Applicability, error) {
	return v3authorization.Current, ctx.Err()
}

func v3AuthorizationHTTPCloneRecord(record v3authorization.Record) v3authorization.Record {
	record.Immutable = append([]byte(nil), record.Immutable...)
	record.Envelope = append([]byte(nil), record.Envelope...)
	return record
}

func (f *v3AuthorizationHTTPDependencies) Claim(ctx context.Context, record v3authorization.Record) (v3authorization.Record, v3authorization.ClaimStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	if f.record != nil {
		if !bytes.Equal(f.record.Immutable, record.Immutable) {
			return v3AuthorizationHTTPCloneRecord(*f.record), v3authorization.Collided, nil
		}
		return v3AuthorizationHTTPCloneRecord(*f.record), v3authorization.Existing, ctx.Err()
	}
	copy := v3AuthorizationHTTPCloneRecord(record)
	f.record = &copy
	return v3AuthorizationHTTPCloneRecord(copy), v3authorization.Claimed, ctx.Err()
}

func (f *v3AuthorizationHTTPDependencies) Finish(ctx context.Context, record v3authorization.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.record == nil || f.record.State != "PENDING" || f.record.ClaimID != record.ClaimID || !bytes.Equal(f.record.Immutable, record.Immutable) {
		return v3authorization.ErrConflict
	}
	f.finishes++
	copy := v3AuthorizationHTTPCloneRecord(record)
	f.record = &copy
	return ctx.Err()
}

func (f *v3AuthorizationHTTPDependencies) Lookup(ctx context.Context, lookup v3authorization.Lookup) (v3authorization.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.record == nil || f.record.Intent.Scope != lookup.Scope || f.record.Intent.Execution != lookup.Execution {
		return v3authorization.Record{}, v3authorization.ErrNotFound
	}
	return v3AuthorizationHTTPCloneRecord(*f.record), ctx.Err()
}

func TestV3AuthorizationHTTPServiceIssueSaveReadAndPrincipalBinding(t *testing.T) {
	f := &v3AuthorizationHTTPDependencies{intent: v3authorization.Intent{
		Scope:     evidence.Scope{Realm: "realm", Host: "host", Resource: "resource", Backend: "backend"},
		Execution: evidence.Execution{RequestID: "request", ExecutionKey: "execution", OperationID: "operation", AttemptID: "attempt", StepID: "step"},
		Binding:   "synthetic-http-binding-not-a-production-profile", Action: v3authorization.Action,
		RequestingPrincipal: "synthetic-user", AuthorizationIssuer: "cp", CandidateRevision: "candidate", CandidateContentPin: "content",
		ExpectedRevision: "old-revision", ExpectedStateVersion: 1,
		Required: v3authorization.GuardRequirement{Policy: "policy", Trust: "trust", Membership: "membership", OwnershipEpoch: "owner", FenceEpoch: "fence", Generation: "generation", BootID: "boot", ProcessIncarnation: "process"},
		TimeBase: "synthetic-clock", DeadlineTick: 90, OriginalBudget: "budget",
	}}
	service := v3authorization.NewService(v3authorization.Config{Issuer: "cp", Audience: "executor"}, v3authorization.Dependencies{
		Resolver: f, Authority: f, Validity: f, Integrity: f, Applicability: f, Store: f,
	})
	server := &Server{auth: v3AuthorizationSessionFixture{status: "active"}}
	handler := server.V3AuthorizationHandler(service)
	request := v3authorization.IssueRequest{
		OriginalRequest: json.RawMessage(`{"synthetic":true}`),
		Frame:           evidence.BackendReadFrame{Issuer: "backend", ServingIncarnation: "incarnation", Generation: "generation", SnapshotID: "snapshot", ObservationProof: "observation"},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	issued := httptest.NewRecorder()
	handler.ServeHTTP(issued, v3AuthorizationHTTPFixtureRequest("/v3/cp-authorizations", string(body)))
	if issued.Code != http.StatusCreated || issued.Header().Get("X-Evidence-Result") != "ISSUED" {
		t.Fatalf("actual Service issue status=%d body=%s", issued.Code, issued.Body.String())
	}
	envelope, err := evidence.Decode(issued.Body.Bytes())
	if err != nil || envelope.Kind != evidence.CPAuthorizationKind || envelope.CPAuthorization == nil || envelope.CPAuthorization.Decision != "AUTHORIZED" {
		t.Fatal("handler did not return actual Service's canonical CP Authorization")
	}
	if f.record == nil || f.record.State != "ISSUED" || !bytes.Equal(f.record.Envelope, issued.Body.Bytes()) || f.seals != 1 || f.finishes != 1 {
		t.Fatal("issue response was not the single saved result")
	}
	lookup, err := json.Marshal(v3authorization.Lookup{Scope: f.intent.Scope, Execution: f.intent.Execution, EvidenceID: envelope.EvidenceID})
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		read := httptest.NewRecorder()
		handler.ServeHTTP(read, v3AuthorizationHTTPFixtureRequest("/v3/cp-authorizations/lookup", string(lookup)))
		if read.Code != http.StatusOK || read.Header().Get("X-Evidence-Result") != "REPLAY" || !bytes.Equal(read.Body.Bytes(), issued.Body.Bytes()) {
			t.Fatal("read did not return the exact saved bytes")
		}
	}
	if f.claims != 1 || f.seals != 1 || f.finishes != 1 {
		t.Fatal("read generated or saved new evidence")
	}
	replayed := httptest.NewRecorder()
	handler.ServeHTTP(replayed, v3AuthorizationHTTPFixtureRequest("/v3/cp-authorizations", string(body)))
	if replayed.Code != http.StatusOK || replayed.Header().Get("X-Evidence-Result") != "REPLAY" || !bytes.Equal(replayed.Body.Bytes(), issued.Body.Bytes()) || f.seals != 1 || f.finishes != 1 {
		t.Fatal("issue replay generated another result")
	}
	f.authorityMu.Lock()
	f.intent.RequestingPrincipal = "different-user"
	f.authorityMu.Unlock()
	spoofed := httptest.NewRecorder()
	handler.ServeHTTP(spoofed, v3AuthorizationHTTPFixtureRequest("/v3/cp-authorizations", string(body)))
	if spoofed.Code != http.StatusForbidden || f.seals != 1 || f.finishes != 1 {
		t.Fatal("request principal replaced the server-established principal")
	}
	f.authorityMu.Lock()
	f.denyRead = true
	f.authorityMu.Unlock()
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, v3AuthorizationHTTPFixtureRequest("/v3/cp-authorizations/lookup", string(lookup)))
	if denied.Code != http.StatusForbidden || bytes.Equal(denied.Body.Bytes(), issued.Body.Bytes()) || f.seals != 1 || f.finishes != 1 {
		t.Fatal("current read authorization was bypassed")
	}
}
