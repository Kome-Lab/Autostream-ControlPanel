package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/store"
)

type mutationGrantHTTPFixture struct {
	handler http.Handler
	auth    *store.MemoryAuthStore
	token   store.ServiceToken
	job     store.SystemUpdateJob
	claim   store.SystemUpdateClaim
	body    map[string]any
}

func TestSystemUpdateMutationGrantHTTPLifecycleAndSecretSafeAudit(t *testing.T) {
	fixture := newMutationGrantHTTPFixture(t, []string{"service.register", "updates.authorize"})
	issueResponse := issueMutationGrantForHTTPTest(t, fixture)
	if issueResponse.Code != http.StatusCreated {
		t.Fatalf("issue = %d %s", issueResponse.Code, issueResponse.Body.String())
	}
	if issueResponse.Header().Get("Cache-Control") != "no-store" || issueResponse.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("issue cache headers = %#v", issueResponse.Header())
	}
	var issued struct {
		GrantToken string    `json:"grant_token"`
		ExpiresAt  time.Time `json:"expires_at"`
	}
	issueBody := append([]byte(nil), issueResponse.Body.Bytes()...)
	if err := json.Unmarshal(issueBody, &issued); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.GrantToken, "ast_mutation_") || time.Until(issued.ExpiresAt) <= 0 || time.Until(issued.ExpiresAt) > store.SystemUpdateMutationGrantMaxTTL+time.Second {
		t.Fatalf("issued response = %#v", issued)
	}
	var publicResponse map[string]any
	if err := json.Unmarshal(issueBody, &publicResponse); err != nil {
		t.Fatal(err)
	}
	if len(publicResponse) != 2 || publicResponse["grant_token"] == nil || publicResponse["expires_at"] == nil {
		t.Fatalf("issue response must contain only grant_token and expires_at: %#v", publicResponse)
	}
	consumeBody := mutationGrantConsumeBody(fixture.claim.LeaseGeneration)
	consumeBody["session_id"] = "session-apply-wrong"
	wrong := consumeMutationGrantForHTTPTest(t, fixture.handler, fixture.job.ID, issued.GrantToken, consumeBody)
	if wrong.Code != http.StatusConflict || !strings.Contains(wrong.Body.String(), "system_update_mutation_grant_conflict") {
		t.Fatalf("binding mismatch consume = %d %s", wrong.Code, wrong.Body.String())
	}

	consumeBody = mutationGrantConsumeBody(fixture.claim.LeaseGeneration)
	wrongGeneration := mutationGrantConsumeBody(fixture.claim.LeaseGeneration + 1)
	wrongLease := consumeMutationGrantForHTTPTest(t, fixture.handler, fixture.job.ID, issued.GrantToken, wrongGeneration)
	if wrongLease.Code != http.StatusConflict {
		t.Fatalf("lease generation mismatch consume = %d %s", wrongLease.Code, wrongLease.Body.String())
	}
	first := consumeMutationGrantForHTTPTest(t, fixture.handler, fixture.job.ID, issued.GrantToken, consumeBody)
	if first.Code != http.StatusNoContent || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("first consume = %d %s headers=%#v", first.Code, first.Body.String(), first.Header())
	}
	replay := consumeMutationGrantForHTTPTest(t, fixture.handler, fixture.job.ID, issued.GrantToken, consumeBody)
	if replay.Code != http.StatusNoContent {
		t.Fatalf("exact consume replay = %d %s", replay.Code, replay.Body.String())
	}
	differentReplayBody := mutationGrantConsumeBody(fixture.claim.LeaseGeneration)
	differentReplayBody["session_id"] = "session-http-other"
	differentReplay := consumeMutationGrantForHTTPTest(t, fixture.handler, fixture.job.ID, issued.GrantToken, differentReplayBody)
	if differentReplay.Code != http.StatusConflict {
		t.Fatalf("different binding replay = %d %s", differentReplay.Code, differentReplay.Body.String())
	}

	events := fixture.auth.AuditEvents()
	issueAudits, consumeAudits := 0, 0
	for _, event := range events {
		switch event.Action {
		case "system_updates.mutation_grant.issue":
			issueAudits++
		case "system_updates.mutation_grant.consume":
			consumeAudits++
		}
		encoded := fmt.Sprintf("%#v", event)
		if strings.Contains(encoded, issued.GrantToken) || strings.Contains(encoded, fixture.claim.LeaseToken) {
			t.Fatalf("grant or lease token leaked into audit: %#v", event)
		}
	}
	if issueAudits != 1 || consumeAudits != 5 {
		t.Fatalf("grant audit counts issue=%d consume=%d events=%#v", issueAudits, consumeAudits, events)
	}
}

func TestSystemUpdateMutationGrantHTTPRequiresScopeAndGrantBearer(t *testing.T) {
	fixture := newMutationGrantHTTPFixture(t, []string{"service.register"})
	res := issueMutationGrantForHTTPTest(t, fixture)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "missing_service_scope") {
		t.Fatalf("missing scope issue = %d %s", res.Code, res.Body.String())
	}

	consumeBody := mutationGrantConsumeBody(fixture.claim.LeaseGeneration)
	consumeBody["grant_token"] = "ast_mutation_" + strings.Repeat("A", 43)
	consume := httptest.NewRequest(http.MethodPost, "/services/update-jobs/"+fixture.job.ID+"/mutation-grants/consume", bytes.NewReader(mustJSONForMutationGrantTest(t, consumeBody)))
	consumeResponse := httptest.NewRecorder()
	fixture.handler.ServeHTTP(consumeResponse, consume)
	if consumeResponse.Code != http.StatusUnauthorized || !strings.Contains(consumeResponse.Body.String(), "system_update_mutation_grant_required") || consumeResponse.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("missing grant bearer consume = %d %s", consumeResponse.Code, consumeResponse.Body.String())
	}
}

func TestSystemUpdateMutationGrantHTTPRejectsWrongStateAndMalformedBinding(t *testing.T) {
	fixture := newMutationGrantHTTPFixture(t, []string{"service.register", "updates.authorize"})
	fixture.body["operation"] = "reconcile"
	wrongState := issueMutationGrantForHTTPTest(t, fixture)
	if wrongState.Code != http.StatusConflict || !strings.Contains(wrongState.Body.String(), "system_update_mutation_grant_state_invalid") {
		t.Fatalf("wrong operation state issue = %d %s", wrongState.Code, wrongState.Body.String())
	}
	fixture.body["operation"] = "apply"
	fixture.body["plan_sha256"] = strings.Repeat("A", 64)
	malformed := issueMutationGrantForHTTPTest(t, fixture)
	if malformed.Code != http.StatusBadRequest || !strings.Contains(malformed.Body.String(), "invalid_system_update_mutation_grant") {
		t.Fatalf("malformed binding issue = %d %s", malformed.Code, malformed.Body.String())
	}
}

func TestSystemUpdateMutationGrantHTTPConsumeErrorMatchesContract(t *testing.T) {
	fixture := newMutationGrantHTTPFixture(t, []string{"service.register", "updates.authorize"})
	issuedResponse := issueMutationGrantForHTTPTest(t, fixture)
	if issuedResponse.Code != http.StatusCreated {
		t.Fatalf("issue = %d %s", issuedResponse.Code, issuedResponse.Body.String())
	}
	var issued struct {
		GrantToken string `json:"grant_token"`
	}
	if err := json.NewDecoder(issuedResponse.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	body := mutationGrantConsumeBody(fixture.claim.LeaseGeneration)
	body["plan_sha256"] = strings.Repeat("A", 64)
	res := consumeMutationGrantForHTTPTest(t, fixture.handler, fixture.job.ID, issued.GrantToken, body)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_system_update_mutation_grant_consumption") {
		t.Fatalf("malformed consume = %d %s", res.Code, res.Body.String())
	}
}

func TestSystemUpdateMutationGrantHTTPConcurrentExactConsumeIsIdempotent(t *testing.T) {
	fixture := newMutationGrantHTTPFixture(t, []string{"service.register", "updates.authorize"})
	issuedResponse := issueMutationGrantForHTTPTest(t, fixture)
	if issuedResponse.Code != http.StatusCreated {
		t.Fatalf("issue = %d %s", issuedResponse.Code, issuedResponse.Body.String())
	}
	var issued struct {
		GrantToken string `json:"grant_token"`
	}
	if err := json.NewDecoder(issuedResponse.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	const workers = 20
	start := make(chan struct{})
	results := make(chan int, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res := consumeMutationGrantForHTTPTest(t, fixture.handler, fixture.job.ID, issued.GrantToken, mutationGrantConsumeBody(fixture.claim.LeaseGeneration))
			results <- res.Code
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for status := range results {
		if status != http.StatusNoContent {
			t.Fatalf("concurrent consume status = %d", status)
		}
	}
}

func newMutationGrantHTTPFixture(t *testing.T, scopes []string) mutationGrantHTTPFixture {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "update_agent", scopes)
	if err != nil {
		t.Fatal(err)
	}
	registration := store.ServiceRegistration{ServiceID: "updater-central", ServiceType: "update_agent", ServiceName: "Central Updater", PublicURL: "https://updater.example.com", Version: "v2.0.0"}
	if _, err := auth.PrecreateService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(t.Context(), token, registration); err != nil {
		t.Fatal(err)
	}
	updates := store.NewMemorySystemUpdateStore()
	job, _, err := updates.CreateSystemUpdateJob(t.Context(), store.CreateSystemUpdateJobParams{
		TargetID: "worker-01", TargetServiceType: "worker", AgentServiceID: "updater-central", ExecutionHostID: "host-01",
		DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		Strategy: store.SystemUpdateStrategyWhenIdle, IdempotencyKey: "mutation-grant-http", RequestedByUserID: "user-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim, _, err := updates.ClaimSystemUpdateJob(t.Context(), "updater-central", "host-01", "", map[string]string{"worker-01": "systemd"}, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := updates.ReportSystemUpdateJob(t.Context(), job.ID, store.SystemUpdateReport{
		AgentServiceID: "updater-central", LeaseToken: claim.LeaseToken, LeaseGeneration: claim.LeaseGeneration,
		Sequence: claim.ReportSequence, Status: store.SystemUpdateStatusInstalling, Progress: 65,
	}, now.Add(time.Millisecond), 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithSystemUpdateStore(updates))
	body := mutationGrantConsumeBody(claim.LeaseGeneration)
	body["service_id"] = "updater-central"
	body["lease_token"] = claim.LeaseToken
	body["lease_generation"] = claim.LeaseGeneration
	return mutationGrantHTTPFixture{handler: handler, auth: auth, token: token, job: job, claim: claim, body: body}
}

func mutationGrantConsumeBody(leaseGeneration int64) map[string]any {
	return map[string]any{
		"lease_generation": leaseGeneration,
		"host_id":          "host-01", "target_id": "worker-01", "target_version": "v1.1.0", "deployment_mode": "systemd",
		"operation": "apply", "plan_sha256": strings.Repeat("a", 64), "session_id": "session-http-0001",
	}
}

func issueMutationGrantForHTTPTest(t *testing.T, fixture mutationGrantHTTPFixture) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/"+fixture.job.ID+"/mutation-grants", bytes.NewReader(mustJSONForMutationGrantTest(t, fixture.body)))
	req.Header.Set("Authorization", "Bearer "+fixture.token.RawToken)
	res := httptest.NewRecorder()
	fixture.handler.ServeHTTP(res, req)
	return res
}

func consumeMutationGrantForHTTPTest(t *testing.T, handler http.Handler, jobID, grantToken string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/update-jobs/"+jobID+"/mutation-grants/consume", bytes.NewReader(mustJSONForMutationGrantTest(t, body)))
	req.Header.Set("Authorization", "Bearer "+grantToken)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func mustJSONForMutationGrantTest(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
