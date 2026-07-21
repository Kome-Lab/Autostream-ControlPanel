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

func TestMariaDBUpdateAgentRegistrationSmoke(t *testing.T) {
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
	token, err := auth.CreateServiceToken(ctx, "update_agent", []string{"service.register", "service.heartbeat", "updates.claim", "updates.report", "updates.authorize"})
	if err != nil {
		t.Fatalf("create update_agent token after migration: %v", err)
	}
	serviceID := "updater-mariadb-smoke-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	capabilities := map[string]any{"managed_targets": []any{"control-panel"}, "deployment_modes": map[string]any{"control-panel": "systemd"}}
	registration := store.ServiceRegistration{ServiceID: serviceID, ServiceType: "update_agent", ServiceName: "MariaDB smoke updater", PublicURL: "https://updater.example.com", Version: "v1.0.0", Capabilities: map[string]any{}}
	if _, err := auth.PrecreateService(ctx, token, registration); err != nil {
		t.Fatalf("precreate update_agent after migration: %v", err)
	}
	registration.Capabilities = capabilities
	registered, err := auth.RegisterService(ctx, token, registration)
	if err != nil {
		t.Fatalf("register update_agent after migration: %v", err)
	}
	if registered.ServiceType != "update_agent" || len(registered.Capabilities) == 0 {
		t.Fatalf("registered update_agent did not retain TOFU capabilities: %#v", registered)
	}
	stageNow := time.Now().UTC()
	configureToken := "mariadb-staged-configure-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := auth.SetServiceConfigureToken(ctx, serviceID, security.HashToken(configureToken), stageNow.Add(time.Hour)); err != nil {
		t.Fatalf("set MariaDB updater configure token: %v", err)
	}
	staged, err := auth.StageServiceNodeConfiguration(ctx, serviceID, configureToken, stageNow, func(string) (string, string, error) {
		return "mariadb-staged-ciphertext", "mariadb-staged-nonce", nil
	})
	if err != nil {
		t.Fatalf("stage MariaDB updater configuration: %v", err)
	}
	stagedService, err := auth.GetService(ctx, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if stagedService.TokenID != token.ID || stagedService.StagedNodeTokenID != staged.Token.ID || stagedService.ConfigureTokenUsedAt == nil {
		t.Fatalf("MariaDB stage changed active identity: %#v", stagedService)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, token.RawToken, "updates.claim"); err != nil {
		t.Fatalf("MariaDB old token stopped before activation: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, staged.Token.RawToken, "updates.claim"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("MariaDB staged token authenticated before activation: %v", err)
	}
	activatedToken, registered, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(ctx, serviceID, staged.Token.ID, staged.ActivationToken, stageNow.Add(time.Second), store.ServiceRuntimeReport{Version: "v1.1.0", Hostname: "mariadb-updater", OS: "linux", Arch: "amd64"})
	if err != nil || alreadyActivated || activatedToken.ID != staged.Token.ID || registered.TokenID != staged.Token.ID {
		t.Fatalf("activate MariaDB updater configuration: token=%#v service=%#v already=%v err=%v", activatedToken, registered, alreadyActivated, err)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, token.RawToken, "service.heartbeat"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("MariaDB old token survived activation: %v", err)
	}
	if _, err := auth.AuthenticateServiceToken(ctx, staged.Token.RawToken, "updates.claim"); err != nil {
		t.Fatalf("MariaDB staged token did not activate: %v", err)
	}
	if _, _, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(ctx, serviceID, staged.Token.ID, staged.ActivationToken, stageNow.Add(2*time.Second), store.ServiceRuntimeReport{}); err != nil || !alreadyActivated {
		t.Fatalf("MariaDB activation replay: already=%v err=%v", alreadyActivated, err)
	}
	token = staged.Token
	outstandingConfigureToken := "mariadb-outstanding-configure-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := auth.SetServiceConfigureToken(ctx, serviceID, security.HashToken(outstandingConfigureToken), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("set outstanding MariaDB updater configure token: %v", err)
	}
	token, registered, err = auth.RotateServiceNodeToken(ctx, serviceID, token.ID, func(string) (string, string, error) {
		return "mariadb-rotated-ciphertext", "mariadb-rotated-nonce", nil
	})
	if err != nil {
		t.Fatalf("rotate MariaDB updater runtime token: %v", err)
	}
	if registered.ConfigureTokenExpiresAt != nil || registered.ConfigureTokenUsedAt != nil || registered.StagedNodeTokenID != "" {
		t.Fatalf("MariaDB runtime rotation retained configure/staging metadata: %#v", registered)
	}
	if _, err := auth.ConsumeServiceConfigureToken(ctx, serviceID, outstandingConfigureToken, time.Now().UTC()); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("MariaDB runtime rotation left configure token usable: %v", err)
	}
	if _, err := auth.AssignServiceToStream(ctx, serviceID, "stream-not-used", "mariadb-smoke"); !errors.Is(err, store.ErrInvalidServiceAssignment) {
		t.Fatalf("MariaDB update_agent assignment err = %v", err)
	}
	heartbeat, err := auth.Heartbeat(ctx, token, store.ServiceHeartbeat{ServiceID: serviceID, Status: "online", Version: "v1.0.0", Capabilities: capabilities, Metrics: map[string]any{"heartbeat": 1}})
	if err != nil {
		t.Fatalf("heartbeat/metric write for update_agent after migration: %v", err)
	}
	if heartbeat.LastHeartbeatAt == nil || heartbeat.ServiceType != "update_agent" {
		t.Fatalf("update_agent heartbeat was not persisted: %#v", heartbeat)
	}

	streams := store.NewMariaDBStreamStore(db)
	stream, err := streams.CreateStream(ctx, "system update busy check")
	if err != nil {
		t.Fatalf("create stream for active lookup: %v", err)
	}
	if _, err := streams.UpdateStreamStatus(ctx, stream.ID, "live"); err != nil {
		t.Fatalf("mark stream active: %v", err)
	}
	if active, err := streams.HasActiveStream(ctx); err != nil || !active {
		t.Fatalf("unbounded active stream lookup = %v, %v", active, err)
	}

	updates := store.NewMariaDBSystemUpdateStore(db)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	eligible := map[string]string{"worker-a-" + suffix: "systemd", "worker-b-" + suffix: "systemd"}
	for targetID := range eligible {
		_, created, err := updates.CreateSystemUpdateJob(ctx, store.CreateSystemUpdateJobParams{
			TargetID: targetID, TargetServiceType: "worker", AgentServiceID: serviceID,
			DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: store.SystemUpdateStrategyWhenIdle,
			IdempotencyKey: "mariadb-claim-" + targetID, RequestedByUserID: "mariadb-smoke",
		})
		if err != nil || !created {
			t.Fatalf("create concurrent claim fixture %q = %v, %v", targetID, created, err)
		}
	}
	type claimResult struct {
		claim store.SystemUpdateClaim
		err   error
	}
	claimResults := make(chan claimResult, 2)
	for range 2 {
		go func() {
			claim, _, err := updates.ClaimSystemUpdateJob(ctx, serviceID, "", "", eligible, time.Now().UTC(), 2*time.Minute)
			claimResults <- claimResult{claim: claim, err: err}
		}()
	}
	claimed, refused := 0, 0
	var successfulClaim store.SystemUpdateClaim
	for range 2 {
		result := <-claimResults
		switch {
		case result.err == nil:
			claimed++
			successfulClaim = result.claim
		case errors.Is(result.err, store.ErrNotFound):
			refused++
		default:
			t.Fatalf("parallel MariaDB claim returned unexpected error: %v", result.err)
		}
	}
	if claimed != 1 || refused != 1 {
		t.Fatalf("parallel MariaDB claims = claimed %d, refused %d; want 1 each", claimed, refused)
	}

	hostTargets := map[string]string{
		"host-a-" + suffix: "worker-host-a-" + suffix,
		"host-b-" + suffix: "worker-host-b-" + suffix,
	}
	hostEligible := make(map[string]string, len(hostTargets))
	for hostID, targetID := range hostTargets {
		hostEligible[targetID] = "systemd"
		_, created, err := updates.CreateSystemUpdateJob(ctx, store.CreateSystemUpdateJobParams{
			TargetID: targetID, TargetServiceType: "worker", AgentServiceID: serviceID, ExecutionHostID: hostID,
			DeploymentMode: "systemd", CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", Strategy: store.SystemUpdateStrategyWhenIdle,
			IdempotencyKey: "mariadb-host-claim-" + targetID, RequestedByUserID: "mariadb-smoke",
		})
		if err != nil || !created {
			t.Fatalf("create host claim fixture %q = %v, %v", targetID, created, err)
		}
	}
	type hostClaimResult struct {
		hostID string
		claim  store.SystemUpdateClaim
		err    error
	}
	hostClaimResults := make(chan hostClaimResult, len(hostTargets))
	for hostID := range hostTargets {
		hostID := hostID
		go func() {
			claim, _, err := updates.ClaimSystemUpdateJob(ctx, serviceID, hostID, "", hostEligible, time.Now().UTC(), 2*time.Minute)
			hostClaimResults <- hostClaimResult{hostID: hostID, claim: claim, err: err}
		}()
	}
	for range hostTargets {
		result := <-hostClaimResults
		if result.err != nil || result.claim.Job.ExecutionHostID != result.hostID || result.claim.Job.TargetID != hostTargets[result.hostID] {
			t.Fatalf("parallel MariaDB host claim %q = %#v, err=%v", result.hostID, result.claim, result.err)
		}
		if _, applied, err := updates.ReportSystemUpdateJob(ctx, result.claim.Job.ID, store.SystemUpdateReport{
			AgentServiceID: serviceID, LeaseToken: result.claim.LeaseToken, LeaseGeneration: result.claim.LeaseGeneration,
			Sequence: result.claim.ReportSequence, Status: store.SystemUpdateStatusSucceeded, Progress: 100,
		}, time.Now().UTC(), 5*time.Minute); err != nil || !applied {
			t.Fatalf("complete MariaDB host claim %q: applied=%v err=%v", result.hostID, applied, err)
		}
	}
	now := time.Now().UTC()
	if _, _, err := updates.ReportSystemUpdateJob(ctx, successfulClaim.Job.ID, store.SystemUpdateReport{AgentServiceID: serviceID, LeaseToken: successfulClaim.LeaseToken, LeaseGeneration: successfulClaim.LeaseGeneration, Sequence: successfulClaim.ReportSequence, Status: store.SystemUpdateStatusInstalling, Progress: 70}, now, 5*time.Minute); err != nil {
		t.Fatalf("move MariaDB job to installing: %v", err)
	}
	if err := updates.AuthorizeSystemUpdateMutation(ctx, successfulClaim.Job.ID, store.SystemUpdateAuthorization{AgentServiceID: serviceID, LeaseToken: successfulClaim.LeaseToken, LeaseGeneration: successfulClaim.LeaseGeneration, TargetID: successfulClaim.Job.TargetID, TargetVersion: successfulClaim.Job.TargetVersion, DeploymentMode: successfulClaim.Job.DeploymentMode}, now.Add(time.Second)); err != nil {
		t.Fatalf("authorize MariaDB installing mutation: %v", err)
	}
	for _, referencedServiceID := range []string{successfulClaim.Job.TargetID, serviceID} {
		if active, err := updates.HasActiveSystemUpdateReference(ctx, referencedServiceID); err != nil || !active {
			t.Fatalf("MariaDB active update reference for %s = %v, %v", referencedServiceID, active, err)
		}
	}

	binding := store.SystemUpdateMutationGrantBinding{
		HostID: successfulClaim.Job.ExecutionHostID, TargetID: successfulClaim.Job.TargetID,
		TargetVersion: successfulClaim.Job.TargetVersion, DeploymentMode: successfulClaim.Job.DeploymentMode,
		Operation: store.SystemUpdateMutationOperationApply, PlanSHA256: strings.Repeat("a", 64),
		SessionID: "mariadb-mutation-" + suffix,
	}
	issued, err := updates.IssueSystemUpdateMutationGrant(ctx, successfulClaim.Job.ID, store.IssueSystemUpdateMutationGrantParams{
		AgentServiceID: serviceID, LeaseToken: successfulClaim.LeaseToken,
		LeaseGeneration: successfulClaim.LeaseGeneration, Binding: binding,
	}, now.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatalf("issue MariaDB mutation grant: %v", err)
	}
	const consumers = 12
	type consumeResult struct {
		replayed bool
		err      error
	}
	startConsume := make(chan struct{})
	consumeResults := make(chan consumeResult, consumers)
	for range consumers {
		go func() {
			<-startConsume
			_, replayed, err := updates.ConsumeSystemUpdateMutationGrant(ctx, successfulClaim.Job.ID, issued.GrantToken, successfulClaim.LeaseGeneration, binding, now.Add(2*time.Second))
			consumeResults <- consumeResult{replayed: replayed, err: err}
		}()
	}
	close(startConsume)
	firstConsume, replayedConsumes := 0, 0
	for range consumers {
		result := <-consumeResults
		if result.err != nil {
			t.Fatalf("parallel MariaDB grant consume: %v", result.err)
		}
		if result.replayed {
			replayedConsumes++
		} else {
			firstConsume++
		}
	}
	if firstConsume != 1 || replayedConsumes != consumers-1 {
		t.Fatalf("parallel MariaDB grant consume first=%d replayed=%d", firstConsume, replayedConsumes)
	}
}
