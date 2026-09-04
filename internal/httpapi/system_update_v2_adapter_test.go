package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	contracts "github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
)

type systemUpdateV2HTTPFixture struct {
	handler *Server
	updates *store.MemorySystemUpdateStore
	token   store.ServiceToken
	job     store.SystemUpdateJob
	lease   contracts.UpdaterLeaseEnvelope
}

func newSystemUpdateV2HTTPFixture(t *testing.T) systemUpdateV2HTTPFixture {
	t.Helper()
	ctx := t.Context()
	auth := store.NewMemoryAuthStore()
	workerToken, err := auth.CreateServiceToken(ctx, "worker", []string{"service.register", "service.heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, workerToken, store.ServiceRegistration{
		ServiceID: "worker-v2", ServiceType: "worker", ServiceName: "Worker V2",
		PublicURL: "https://worker-v2.example.com", Version: "v1.0.0",
	})
	updates := store.NewMemorySystemUpdateStore()
	ownership, err := updates.SwitchSystemUpdateExecutionHost(
		ctx,
		"host-v2",
		0,
		store.SystemUpdateTransportPullV2,
		"updater-v2",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := map[string]any{
		"host_agent":             true,
		"observe_only":           false,
		"update_executor":        true,
		"mutation_enabled":       true,
		"transport_mode":         store.SystemUpdateTransportPullV2,
		"agent_protocol_version": "2",
		"execution_host_id":      "host-v2",
		"ownership_epoch":        ownership.OwnershipEpoch,
	}
	token := registerPullSystemUpdateAgentForOwnershipTest(
		t,
		auth,
		"updater-v2",
		"host-v2",
		ownership.OwnershipEpoch,
		capabilities,
	)
	job, created, err := updates.CreateSystemUpdateJob(ctx, store.CreateSystemUpdateJobParams{
		TargetID:          "worker-v2",
		TargetServiceType: "worker",
		AgentServiceID:    "updater-v2",
		ExecutionHostID:   "host-v2",
		DeploymentMode:    "systemd",
		CurrentVersion:    "v1.0.0",
		TargetVersion:     "v1.1.0",
		Strategy:          store.SystemUpdateStrategyWhenIdle,
		IdempotencyKey:    "updater-v2-http-fixture",
		RequestedByUserID: "admin",
	})
	if err != nil || !created {
		t.Fatalf("create v2 job: created=%v err=%v", created, err)
	}
	claim, clear, err := updates.ClaimSystemUpdateJobV2(
		ctx,
		"updater-v2",
		"host-v2",
		"",
		1,
		ownership.OwnershipEpoch,
		map[string]string{"worker-v2": "systemd"},
		time.Now().UTC(),
		systemUpdateExecutionLeaseTTL,
	)
	if err != nil || clear || claim.Job.ID != job.ID || claim.ReportSequence != 1 {
		t.Fatalf("claim v2 job: claim=%#v clear=%v err=%v", claim, clear, err)
	}
	handler := NewServer(
		store.NewMemoryStreamStore(),
		WithAuthStore(auth),
		WithServiceRegistryStore(auth),
		WithSystemUpdateStore(updates),
	)
	lease, err := handler.systemUpdateV2Lease(ctx, claim.Job)
	if err != nil {
		t.Fatal(err)
	}
	return systemUpdateV2HTTPFixture{
		handler: handler,
		updates: updates,
		token:   token,
		job:     claim.Job,
		lease:   lease,
	}
}

func TestSystemUpdateV2ContractBoundaryHardensEveryResponse(t *testing.T) {
	handler := NewServer(store.NewMemoryStreamStore())
	tests := []struct {
		name   string
		method string
		major  []string
		want   int
	}{
		{name: "auth error", method: http.MethodPost, major: []string{"2"}, want: http.StatusInternalServerError},
		{name: "wrong major", method: http.MethodPost, major: []string{"3"}, want: http.StatusUpgradeRequired},
		{name: "duplicate major", method: http.MethodPost, major: []string{"2", "2"}, want: http.StatusUpgradeRequired},
		{name: "method error", method: http.MethodGet, major: []string{"2"}, want: http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/services/update-jobs/claim", strings.NewReader(`{}`))
			for _, value := range test.major {
				req.Header.Add(systemUpdateContractMajorHeader, value)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != test.want || res.Header().Get(systemUpdateContractMajorHeader) != "2" ||
				!strings.Contains(strings.ToLower(res.Header().Get("Cache-Control")), "no-store") {
				t.Fatalf("v2 boundary = status %d major %q cache %q body %s", res.Code, res.Header().Get(systemUpdateContractMajorHeader), res.Header().Get("Cache-Control"), res.Body.String())
			}
		})
	}
}

func TestSystemUpdateV2LeaseReportAndMutationGrantRoundTrip(t *testing.T) {
	fixture := newSystemUpdateV2HTTPFixture(t)
	leasePayload, err := json.Marshal(fixture.lease)
	if err != nil || contracts.ValidateUpdaterLeaseEnvelope(time.Now().UTC(), leasePayload) != nil {
		t.Fatalf("projected lease is invalid: err=%v body=%s", err, leasePayload)
	}
	authorization := fixture.lease.Command.MutationAuthorization
	if authorization.Target.ServiceID != "worker-v2" || authorization.Target.ServiceType != contracts.SystemUpdateTargetWorker ||
		authorization.Target.ExpectedConfigRevision != 1 || authorization.DesiredRevision != 1 ||
		authorization.Fence != fixture.job.OwnershipEpoch || fixture.lease.LeaseGeneration != fixture.job.LeaseGeneration ||
		bytes.Contains(leasePayload, []byte("ast_update_")) || bytes.Contains(leasePayload, []byte("release_token")) {
		t.Fatalf("projected lease identity or secret boundary is wrong: %s", leasePayload)
	}

	progress := contracts.UpdaterProgressEnvelope{
		ProtocolVersion:    2,
		CommandID:          fixture.lease.Command.CommandID,
		JobID:              authorization.JobID,
		UpdaterID:          authorization.UpdaterID,
		HostID:             authorization.HostID,
		LeaseID:            fixture.lease.LeaseID,
		LeaseGeneration:    fixture.lease.LeaseGeneration,
		Sequence:           1,
		Phase:              "executing",
		Progress:           70,
		DesiredRevision:    authorization.DesiredRevision,
		Fence:              authorization.Fence,
		AuditCorrelationID: fixture.lease.Command.AuditCorrelationID,
		ObservedAt:         time.Now().UTC(),
	}
	postSystemUpdateV2JSON(t, fixture.handler, fixture.token.RawToken, "/services/update-jobs/"+fixture.job.ID+"/report", progress, http.StatusOK)
	installing, err := fixture.updates.GetSystemUpdateJob(t.Context(), fixture.job.ID)
	if err != nil || installing.Status != store.SystemUpdateStatusInstalling || installing.Sequence != 1 ||
		installing.LeaseExpiresAt == nil || !installing.LeaseExpiresAt.Equal(fixture.lease.LeaseExpiresAt) {
		t.Fatalf("stored v2 progress = %#v err=%v", installing, err)
	}

	binding := contracts.UpdaterMutationGrantBinding{
		Lease:     fixture.lease,
		Operation: contracts.UpdaterMutationApply,
		SessionID: "session:updater-v2:0001",
	}
	issue := contracts.UpdaterMutationGrantIssueRequest{Binding: binding}
	issuePayload, _ := json.Marshal(issue)
	issueReq := httptest.NewRequest(http.MethodPost, "/services/update-jobs/"+fixture.job.ID+"/mutation-grants", bytes.NewReader(issuePayload))
	issueReq.Header.Set("Authorization", "Bearer "+fixture.token.RawToken)
	issueReq.Header.Set(systemUpdateContractMajorHeader, "2")
	issueRes := httptest.NewRecorder()
	fixture.handler.ServeHTTP(issueRes, issueReq)
	if issueRes.Code != http.StatusCreated || issueRes.Header().Get(systemUpdateContractMajorHeader) != "2" ||
		!strings.Contains(strings.ToLower(issueRes.Header().Get("Cache-Control")), "no-store") {
		t.Fatalf("issue v2 grant = %d headers=%v body=%s", issueRes.Code, issueRes.Header(), issueRes.Body.String())
	}
	var grant contracts.UpdaterMutationGrantIssueResponse
	if json.Unmarshal(issueRes.Body.Bytes(), &grant) != nil ||
		contracts.ValidateUpdaterMutationGrantIssueResponseForLease(time.Now().UTC(), fixture.lease, grant) != nil {
		t.Fatalf("invalid v2 grant response: %s", issueRes.Body.String())
	}

	consume := contracts.UpdaterMutationGrantConsumeRequest{Binding: binding}
	consumePayload, _ := json.Marshal(consume)
	for attempt := 0; attempt < 2; attempt++ {
		consumeReq := httptest.NewRequest(http.MethodPost, "/services/update-jobs/"+fixture.job.ID+"/mutation-grants/consume", bytes.NewReader(consumePayload))
		consumeReq.Header.Set("Authorization", "Bearer "+grant.GrantToken)
		consumeReq.Header.Set(systemUpdateContractMajorHeader, "2")
		consumeRes := httptest.NewRecorder()
		fixture.handler.ServeHTTP(consumeRes, consumeReq)
		if consumeRes.Code != http.StatusNoContent || consumeRes.Body.Len() != 0 {
			t.Fatalf("consume v2 grant attempt %d = %d %s", attempt+1, consumeRes.Code, consumeRes.Body.String())
		}
	}

	mutated := binding
	mutated.Lease.LeaseID = "lease:invented-valid-identifier"
	mutatedPayload, _ := json.Marshal(contracts.UpdaterMutationGrantIssueRequest{Binding: mutated})
	mutatedReq := httptest.NewRequest(http.MethodPost, "/services/update-jobs/"+fixture.job.ID+"/mutation-grants", bytes.NewReader(mutatedPayload))
	mutatedReq.Header.Set("Authorization", "Bearer "+fixture.token.RawToken)
	mutatedReq.Header.Set(systemUpdateContractMajorHeader, "2")
	mutatedRes := httptest.NewRecorder()
	fixture.handler.ServeHTTP(mutatedRes, mutatedReq)
	if mutatedRes.Code != http.StatusConflict || !strings.Contains(mutatedRes.Body.String(), "updater_v2_lease_binding_mismatch") {
		t.Fatalf("invented v2 lease = %d %s", mutatedRes.Code, mutatedRes.Body.String())
	}

	result := contracts.UpdaterResultEnvelope{
		ProtocolVersion:        2,
		CommandID:              fixture.lease.Command.CommandID,
		JobID:                  authorization.JobID,
		UpdaterID:              authorization.UpdaterID,
		HostID:                 authorization.HostID,
		LeaseID:                fixture.lease.LeaseID,
		LeaseGeneration:        fixture.lease.LeaseGeneration,
		IdempotencyKey:         fixture.lease.Command.IdempotencyKey,
		CanonicalPayloadDigest: fixture.lease.Command.CanonicalPayloadDigest,
		AuthorizationID:        authorization.AuthorizationID,
		DesiredRevision:        authorization.DesiredRevision,
		AppliedRevision:        authorization.DesiredRevision,
		Fence:                  authorization.Fence,
		Outcome:                contracts.UpdaterOutcomeSucceeded,
		Status:                 contracts.SystemUpdateSucceeded,
		AutomaticResendAllowed: false,
		AuditCorrelationID:     fixture.lease.Command.AuditCorrelationID,
		Evidence: []contracts.UpdaterEvidence{{
			EvidenceCode:     "application_probe_verified",
			ObservedAt:       time.Now().UTC(),
			ObservedRevision: authorization.DesiredRevision,
		}},
	}
	for attempt := 0; attempt < 2; attempt++ {
		postSystemUpdateV2JSON(t, fixture.handler, fixture.token.RawToken, "/services/update-jobs/"+fixture.job.ID+"/report", result, http.StatusOK)
	}
	completed, err := fixture.updates.GetSystemUpdateJob(t.Context(), fixture.job.ID)
	if err != nil || completed.Status != store.SystemUpdateStatusSucceeded || completed.Sequence != 2 || completed.CompletedAt == nil {
		t.Fatalf("stored v2 result = %#v err=%v", completed, err)
	}
}

func postSystemUpdateV2JSON(t *testing.T, handler http.Handler, token, path string, body any, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(systemUpdateContractMajorHeader, "2")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != wantStatus || res.Header().Get(systemUpdateContractMajorHeader) != "2" ||
		!strings.Contains(strings.ToLower(res.Header().Get("Cache-Control")), "no-store") {
		t.Fatalf("POST %s = %d headers=%v body=%s", path, res.Code, res.Header(), res.Body.String())
	}
	return res
}
