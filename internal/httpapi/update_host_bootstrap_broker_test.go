package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBootstrapBrokerCreateIsIdempotentAndKeepsSecretsOutOfPublicJobs(t *testing.T) {
	now := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	broker := newUpdateHostBootstrapBroker(func() time.Time { return now }, nil)
	envelope := []byte(`{"private_key":"credential-must-not-leak"}`)
	params := UpdateHostBootstrapCreateParams{
		UpdaterID:               "updater-a",
		ExpectedRevision:        7,
		ClientJobID:             "client-job-a",
		IdempotencyKey:          "idem-a",
		RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
		HostIDs:                 []string{" host-b ", "host-a", "host-b"},
		Envelope:                envelope,
	}

	created, replayed, err := broker.Create(params)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if replayed {
		t.Fatal("first Create() unexpectedly reported an idempotent replay")
	}
	if created.ID != "client-job-a" || created.Status != UpdateHostBootstrapStatusQueued {
		t.Fatalf("created job = %#v", created)
	}
	if got, want := created.HostIDs, []string{"host-a", "host-b"}; !equalStrings(got, want) {
		t.Fatalf("HostIDs = %q, want %q", got, want)
	}
	if len(created.Hosts) != 2 || created.Hosts[0].HostID != "host-a" || created.Hosts[0].Status != UpdateHostBootstrapHostStatusQueued {
		t.Fatalf("Hosts = %#v", created.Hosts)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatalf("json.Marshal(created): %v", err)
	}
	for _, forbidden := range []string{"credential-must-not-leak", "private_key", "lease_token", "envelope"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("public job leaked %q: %s", forbidden, encoded)
		}
	}

	replayedJob, replayed, err := broker.Create(params)
	if err != nil {
		t.Fatalf("idempotent Create() error = %v", err)
	}
	if !replayed || replayedJob.ID != created.ID || !replayedJob.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("idempotent replay = (%#v, %v), want original job", replayedJob, replayed)
	}

	changed := params
	changed.Envelope = []byte(`{"private_key":"different"}`)
	if _, _, err := broker.Create(changed); !errors.Is(err, ErrUpdateHostBootstrapIdempotencyConflict) {
		t.Fatalf("same idempotency key with changed envelope error = %v", err)
	}
	changedRecipient := params
	changedRecipient.RecipientKeyFingerprint = bootstrapBrokerRecipientFingerprint(2)
	if _, _, err := broker.Create(changedRecipient); !errors.Is(err, ErrUpdateHostBootstrapIdempotencyConflict) {
		t.Fatalf("same idempotency key with changed recipient fingerprint error = %v", err)
	}
	second := params
	second.ClientJobID = "client-job-b"
	second.IdempotencyKey = "idem-b"
	if _, _, err := broker.Create(second); !errors.Is(err, ErrUpdateHostBootstrapActiveJob) {
		t.Fatalf("second active job error = %v", err)
	}

	envelope[0] = 'X'
	recipientFingerprint := bootstrapBrokerRecipientFingerprint(1)
	claim, err := broker.Claim("updater-a", 7, recipientFingerprint, recipientFingerprint)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if got := string(claim.Envelope); got != `{"private_key":"credential-must-not-leak"}` {
		t.Fatalf("Claim().Envelope = %q", got)
	}
}

func TestBootstrapBrokerClaimBindsLeaseAndAcceptWipesEnvelope(t *testing.T) {
	now := time.Date(2026, 7, 27, 11, 0, 0, 0, time.UTC)
	broker := newUpdateHostBootstrapBroker(func() time.Time { return now }, nil)
	job := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
		UpdaterID: "updater-a", ExpectedRevision: 9, ClientJobID: "job-a",
		IdempotencyKey: "idem-a", RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
		HostIDs: []string{"host-a"}, Envelope: []byte("opaque-secret"),
	})

	recipientFingerprint := bootstrapBrokerRecipientFingerprint(1)
	if _, err := broker.Claim("updater-b", 9, recipientFingerprint, recipientFingerprint); !errors.Is(err, ErrUpdateHostBootstrapNotFound) {
		t.Fatalf("wrong-updater Claim() error = %v", err)
	}
	if _, err := broker.Claim("updater-a", 10, recipientFingerprint, recipientFingerprint); !errors.Is(err, ErrUpdateHostBootstrapBinding) {
		t.Fatalf("wrong-revision Claim() error = %v", err)
	}
	first, err := broker.Claim("updater-a", 9, recipientFingerprint, recipientFingerprint)
	if err != nil {
		t.Fatalf("first Claim() error = %v", err)
	}
	if first.LeaseToken == "" || string(first.Envelope) != "opaque-secret" || first.Job.Status != UpdateHostBootstrapStatusClaimed {
		t.Fatalf("first claim = %#v", first)
	}

	second, err := broker.Claim("updater-a", 9, recipientFingerprint, recipientFingerprint)
	if err != nil {
		t.Fatalf("retry Claim() error = %v", err)
	}
	if second.LeaseToken == first.LeaseToken || string(second.Envelope) != "opaque-secret" {
		t.Fatalf("retry claim did not rotate the lease while redelivering the envelope: %#v", second)
	}
	if _, err := broker.Accept(job.ID, "updater-a", 9, first.LeaseToken); !errors.Is(err, ErrUpdateHostBootstrapLeaseInvalid) {
		t.Fatalf("rotated lease remained valid: %v", err)
	}
	if _, err := broker.Accept(job.ID, "updater-b", 9, second.LeaseToken); !errors.Is(err, ErrUpdateHostBootstrapLeaseInvalid) {
		t.Fatalf("wrong updater accepted lease: %v", err)
	}
	if _, err := broker.Accept(job.ID, "updater-a", 10, second.LeaseToken); !errors.Is(err, ErrUpdateHostBootstrapLeaseInvalid) {
		t.Fatalf("wrong revision accepted lease: %v", err)
	}

	broker.mu.Lock()
	storedEnvelope := broker.jobs[job.ID].envelope
	broker.mu.Unlock()
	accepted, err := broker.Accept(job.ID, "updater-a", 9, second.LeaseToken)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if accepted.Status != UpdateHostBootstrapStatusRunning {
		t.Fatalf("Accept().Status = %q", accepted.Status)
	}
	if !allZero(storedEnvelope) {
		t.Fatalf("stored envelope was not wiped: %q", storedEnvelope)
	}
	broker.mu.Lock()
	envelopeLen := len(broker.jobs[job.ID].envelope)
	broker.mu.Unlock()
	if envelopeLen != 0 {
		t.Fatalf("stored envelope length after Accept() = %d", envelopeLen)
	}
	if _, err := broker.Claim("updater-a", 9, recipientFingerprint, recipientFingerprint); !errors.Is(err, ErrUpdateHostBootstrapTransition) {
		t.Fatalf("Claim() after Accept() error = %v", err)
	}
	if replay, err := broker.Accept(job.ID, "updater-a", 9, second.LeaseToken); err != nil || replay.Status != UpdateHostBootstrapStatusRunning {
		t.Fatalf("idempotent Accept() = (%#v, %v)", replay, err)
	}
}

func TestBootstrapBrokerReportsPerHostProgressAndDerivesBatchStatus(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	broker := newUpdateHostBootstrapBroker(func() time.Time { return now }, nil)
	job := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
		UpdaterID: "updater-a", ExpectedRevision: 3, ClientJobID: "job-batch",
		IdempotencyKey: "idem-batch", RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
		HostIDs: []string{"host-b", "host-a"}, Envelope: []byte("credential"),
	})
	recipientFingerprint := bootstrapBrokerRecipientFingerprint(1)
	claim, err := broker.Claim("updater-a", 3, recipientFingerprint, recipientFingerprint)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := broker.Accept(job.ID, "updater-a", 3, claim.LeaseToken); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	now = now.Add(time.Second)
	running, err := broker.Report(UpdateHostBootstrapReportParams{
		JobID: job.ID, UpdaterID: "updater-a", ExpectedRevision: 3, LeaseToken: claim.LeaseToken,
		HostID: "host-a", Status: UpdateHostBootstrapHostStatusInstalling, Progress: 70,
	})
	if err != nil {
		t.Fatalf("installing Report() error = %v", err)
	}
	if running.Status != UpdateHostBootstrapStatusRunning || running.Progress != 35 {
		t.Fatalf("running job = %#v", running)
	}
	if _, err := broker.Report(UpdateHostBootstrapReportParams{
		JobID: job.ID, UpdaterID: "updater-a", ExpectedRevision: 3, LeaseToken: "wrong",
		HostID: "host-a", Status: UpdateHostBootstrapHostStatusSucceeded, Progress: 100,
	}); !errors.Is(err, ErrUpdateHostBootstrapLeaseInvalid) {
		t.Fatalf("wrong-lease Report() error = %v", err)
	}

	now = now.Add(time.Second)
	if _, err := broker.Report(UpdateHostBootstrapReportParams{
		JobID: job.ID, UpdaterID: "updater-a", ExpectedRevision: 3, LeaseToken: claim.LeaseToken,
		HostID: "host-a", Status: UpdateHostBootstrapHostStatusSucceeded, Progress: 100,
	}); err != nil {
		t.Fatalf("success Report() error = %v", err)
	}
	now = now.Add(time.Second)
	partial, err := broker.Report(UpdateHostBootstrapReportParams{
		JobID: job.ID, UpdaterID: "updater-a", ExpectedRevision: 3, LeaseToken: claim.LeaseToken,
		HostID: "host-b", Status: UpdateHostBootstrapHostStatusFailed, Progress: 100,
		Code: "ssh_failed", Message: "connection refused",
	})
	if err != nil {
		t.Fatalf("failure Report() error = %v", err)
	}
	if partial.Status != UpdateHostBootstrapStatusPartialFailed || partial.Progress != 100 {
		t.Fatalf("terminal job = %#v", partial)
	}
	if partial.Hosts[1].Code != "ssh_failed" || partial.Hosts[1].Message != "connection refused" {
		t.Fatalf("host failure detail = %#v", partial.Hosts)
	}
	broker.mu.Lock()
	terminalRecord := broker.jobs[job.ID]
	terminalLeaseSurvived := terminalRecord.hasLease || !allZero(terminalRecord.leaseHash[:]) ||
		terminalRecord.leaseUpdaterID != "" || terminalRecord.leaseRevision != 0
	broker.mu.Unlock()
	if terminalLeaseSurvived {
		t.Fatal("terminal job retained its mutation lease")
	}
	if _, err := broker.Report(UpdateHostBootstrapReportParams{
		JobID: job.ID, UpdaterID: "updater-a", ExpectedRevision: 3, LeaseToken: claim.LeaseToken,
		HostID: "host-a", Status: UpdateHostBootstrapHostStatusInstalling, Progress: 70,
	}); !errors.Is(err, ErrUpdateHostBootstrapLeaseInvalid) {
		t.Fatalf("terminal job accepted another report: %v", err)
	}
	if _, err := broker.Report(UpdateHostBootstrapReportParams{
		JobID: job.ID, UpdaterID: "updater-a", ExpectedRevision: 3, LeaseToken: claim.LeaseToken,
		HostID: "host-b", Status: UpdateHostBootstrapHostStatusFailed, Progress: 100,
		Code: "ssh_failed", Message: "connection refused",
	}); !errors.Is(err, ErrUpdateHostBootstrapLeaseInvalid) {
		t.Fatalf("terminal duplicate report was acknowledged without a lease verifier: %v", err)
	}

	replacement := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
		UpdaterID: "updater-a", ExpectedRevision: 3, ClientJobID: "job-next",
		IdempotencyKey: "idem-next", RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
		HostIDs: []string{"host-c"}, Envelope: []byte("next-credential"),
	})
	if replacement.Status != UpdateHostBootstrapStatusQueued {
		t.Fatalf("replacement job = %#v", replacement)
	}
}

func TestBootstrapBrokerExpiryAndCancelWipeCredential(t *testing.T) {
	now := time.Date(2026, 7, 27, 13, 0, 0, 0, time.UTC)
	broker := newUpdateHostBootstrapBroker(func() time.Time { return now }, nil)
	expiring := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
		UpdaterID: "updater-a", ExpectedRevision: 1, ClientJobID: "job-expire",
		IdempotencyKey: "idem-expire", RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
		HostIDs: []string{"host-a"}, Envelope: []byte("expire-me"),
	})
	broker.mu.Lock()
	expiringEnvelope := broker.jobs[expiring.ID].envelope
	broker.mu.Unlock()

	now = now.Add(UpdateHostBootstrapCredentialTTL)
	jobs, err := broker.List("updater-a")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].Status != UpdateHostBootstrapStatusCredentialExpired {
		t.Fatalf("expired jobs = %#v", jobs)
	}
	if len(jobs[0].Hosts) != 1 ||
		jobs[0].Hosts[0].Status != UpdateHostBootstrapHostStatusFailed ||
		jobs[0].Hosts[0].Progress != 100 ||
		jobs[0].Hosts[0].Code != "bootstrap_credential_expired" {
		t.Fatalf("credential-expired job retained an active child host: %#v", jobs[0].Hosts)
	}
	if !allZero(expiringEnvelope) {
		t.Fatalf("expired envelope was not wiped: %q", expiringEnvelope)
	}
	recipientFingerprint := bootstrapBrokerRecipientFingerprint(1)
	if _, err := broker.Claim("updater-a", 1, recipientFingerprint, recipientFingerprint); !errors.Is(err, ErrUpdateHostBootstrapNotFound) {
		t.Fatalf("Claim() after expiry error = %v", err)
	}

	canceling := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
		UpdaterID: "updater-a", ExpectedRevision: 1, ClientJobID: "job-cancel",
		IdempotencyKey: "idem-cancel", RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
		HostIDs: []string{"host-b"}, Envelope: []byte("cancel-me"),
	})
	broker.mu.Lock()
	cancelingEnvelope := broker.jobs[canceling.ID].envelope
	broker.mu.Unlock()
	canceled, err := broker.Cancel(canceling.ID, "updater-a", 1)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if canceled.Status != UpdateHostBootstrapStatusCanceled || !allZero(cancelingEnvelope) {
		t.Fatalf("Cancel() = %#v, wiped=%v", canceled, allZero(cancelingEnvelope))
	}
}

func TestBootstrapBrokerExecutionExpiryFailsUnfinishedHostsAndReleasesUpdater(t *testing.T) {
	for _, test := range []struct {
		name       string
		completeA  bool
		wantStatus UpdateHostBootstrapStatus
	}{
		{name: "all unfinished", wantStatus: UpdateHostBootstrapStatusFailed},
		{name: "partial completion", completeA: true, wantStatus: UpdateHostBootstrapStatusPartialFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)
			broker := newUpdateHostBootstrapBroker(func() time.Time { return now }, nil)
			job := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
				UpdaterID: "updater-a", ExpectedRevision: 4, ClientJobID: "job-execution-expiry",
				IdempotencyKey: "idem-execution-expiry", RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
				HostIDs: []string{"host-b", "host-a"}, Envelope: []byte("credential"),
			})
			recipientFingerprint := bootstrapBrokerRecipientFingerprint(1)
			claim, err := broker.Claim("updater-a", 4, recipientFingerprint, recipientFingerprint)
			if err != nil {
				t.Fatalf("Claim() error = %v", err)
			}
			accepted, err := broker.Accept(job.ID, "updater-a", 4, claim.LeaseToken)
			if err != nil {
				t.Fatalf("Accept() error = %v", err)
			}
			if want := now.Add(UpdateHostBootstrapExecutionIdleTTL); !accepted.ExpiresAt.Equal(want) {
				t.Fatalf("Accept().ExpiresAt = %v, want %v", accepted.ExpiresAt, want)
			}
			if test.completeA {
				if _, err := broker.Report(UpdateHostBootstrapReportParams{
					JobID: job.ID, UpdaterID: "updater-a", ExpectedRevision: 4, LeaseToken: claim.LeaseToken,
					HostID: "host-a", Status: UpdateHostBootstrapHostStatusSucceeded, Progress: 100,
				}); err != nil {
					t.Fatalf("host-a success Report() error = %v", err)
				}
			}

			now = now.Add(UpdateHostBootstrapExecutionIdleTTL)
			jobs, err := broker.List("updater-a")
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if len(jobs) != 1 || jobs[0].Status != test.wantStatus || jobs[0].Progress != 100 {
				t.Fatalf("expired execution jobs = %#v", jobs)
			}
			for _, host := range jobs[0].Hosts {
				if test.completeA && host.HostID == "host-a" {
					if host.Status != UpdateHostBootstrapHostStatusSucceeded {
						t.Fatalf("completed host was overwritten: %#v", host)
					}
					continue
				}
				if host.Status != UpdateHostBootstrapHostStatusFailed || host.Progress != 100 ||
					host.Code != "bootstrap_execution_expired" ||
					host.Message != "bootstrap execution expired before all hosts reported completion" {
					t.Fatalf("unfinished host after execution expiry = %#v", host)
				}
			}

			broker.mu.Lock()
			record := broker.jobs[job.ID]
			leaseSurvived := record.hasLease || !allZero(record.leaseHash[:]) || record.leaseUpdaterID != "" || record.leaseRevision != 0
			envelopeSurvived := len(record.envelope) != 0
			broker.mu.Unlock()
			if leaseSurvived || envelopeSurvived {
				t.Fatalf("execution expiry retained secret state: lease=%v envelope=%v", leaseSurvived, envelopeSurvived)
			}
			encoded, err := json.Marshal(jobs[0])
			if err != nil {
				t.Fatalf("json.Marshal(expired job): %v", err)
			}
			if bytes.Contains(encoded, []byte(claim.LeaseToken)) || bytes.Contains(encoded, []byte("credential")) {
				t.Fatalf("expired public job leaked secret material: %s", encoded)
			}

			replacement := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
				UpdaterID: "updater-a", ExpectedRevision: 4, ClientJobID: "job-after-execution-expiry",
				IdempotencyKey: "idem-after-execution-expiry", RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
				HostIDs: []string{"host-c"}, Envelope: []byte("new-credential"),
			})
			if replacement.Status != UpdateHostBootstrapStatusQueued {
				t.Fatalf("replacement job = %#v", replacement)
			}
		})
	}
}

func TestBootstrapBrokerExecutionReportsRefreshIdleDeadlineButNotAbsoluteDeadline(t *testing.T) {
	now := time.Date(2026, 7, 27, 16, 0, 0, 0, time.UTC)
	startedAt := now
	broker := newUpdateHostBootstrapBroker(func() time.Time { return now }, nil)
	recipientFingerprint := bootstrapBrokerRecipientFingerprint(1)
	job := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
		UpdaterID: "updater-a", ExpectedRevision: 5, ClientJobID: "job-long-running",
		IdempotencyKey: "idem-long-running", RecipientKeyFingerprint: recipientFingerprint,
		HostIDs: []string{"host-a"}, Envelope: []byte("credential"),
	})
	claim, err := broker.Claim("updater-a", 5, recipientFingerprint, recipientFingerprint)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := broker.Accept(job.ID, "updater-a", 5, claim.LeaseToken); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	for now.Before(startedAt.Add(UpdateHostBootstrapExecutionMaxTTL - 20*time.Minute)) {
		now = now.Add(20 * time.Minute)
		reported, err := broker.Report(UpdateHostBootstrapReportParams{
			JobID: job.ID, UpdaterID: "updater-a", ExpectedRevision: 5, LeaseToken: claim.LeaseToken,
			HostID: "host-a", Status: UpdateHostBootstrapHostStatusConnecting, Progress: 10,
		})
		if err != nil {
			t.Fatalf("progress Report() at %v error = %v", now, err)
		}
		wantExpiry := now.Add(UpdateHostBootstrapExecutionIdleTTL)
		absoluteDeadline := startedAt.Add(UpdateHostBootstrapExecutionMaxTTL)
		if wantExpiry.After(absoluteDeadline) {
			wantExpiry = absoluteDeadline
		}
		if !reported.ExpiresAt.Equal(wantExpiry) {
			t.Fatalf("Report().ExpiresAt at %v = %v, want %v", now, reported.ExpiresAt, wantExpiry)
		}
	}

	now = startedAt.Add(UpdateHostBootstrapExecutionMaxTTL)
	jobs, err := broker.List("updater-a")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].Status != UpdateHostBootstrapStatusFailed ||
		jobs[0].Hosts[0].Code != updateHostBootstrapExecutionExpiredCode {
		t.Fatalf("absolute deadline did not terminate execution: %#v", jobs)
	}
}

func TestBootstrapBrokerRecipientFingerprintMismatchFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name     string
		client   string
		current  string
		preclaim bool
	}{
		{
			name:    "client fingerprint differs from job",
			client:  bootstrapBrokerRecipientFingerprint(2),
			current: bootstrapBrokerRecipientFingerprint(1),
		},
		{
			name:     "current updater fingerprint differs from client and job",
			client:   bootstrapBrokerRecipientFingerprint(1),
			current:  bootstrapBrokerRecipientFingerprint(2),
			preclaim: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 27, 17, 0, 0, 0, time.UTC)
			broker := newUpdateHostBootstrapBroker(func() time.Time { return now }, nil)
			job := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
				UpdaterID: "updater-a", ExpectedRevision: 6, ClientJobID: "job-key-rotation",
				IdempotencyKey:          "idem-key-rotation",
				RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
				HostIDs:                 []string{"host-a"}, Envelope: []byte("credential"),
			})
			broker.mu.Lock()
			storedEnvelope := broker.jobs[job.ID].envelope
			broker.mu.Unlock()
			if test.preclaim {
				recipientFingerprint := bootstrapBrokerRecipientFingerprint(1)
				if _, err := broker.Claim("updater-a", 6, recipientFingerprint, recipientFingerprint); err != nil {
					t.Fatalf("initial Claim() error = %v", err)
				}
			}

			if _, err := broker.Claim("updater-a", 6, test.client, test.current); !errors.Is(err, ErrUpdateHostBootstrapRecipientKeyMismatch) {
				t.Fatalf("Claim() error = %v", err)
			}
			jobs, err := broker.List("updater-a")
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if len(jobs) != 1 || jobs[0].Status != UpdateHostBootstrapStatusCredentialExpired ||
				jobs[0].Hosts[0].Status != UpdateHostBootstrapHostStatusFailed ||
				jobs[0].Hosts[0].Code != "bootstrap_recipient_key_changed" {
				t.Fatalf("recipient mismatch did not terminalize job: %#v", jobs)
			}
			if !allZero(storedEnvelope) {
				t.Fatalf("recipient mismatch did not wipe envelope: %q", storedEnvelope)
			}
			broker.mu.Lock()
			record := broker.jobs[job.ID]
			leaseSurvived := record.hasLease || !allZero(record.leaseHash[:]) ||
				record.leaseUpdaterID != "" || record.leaseRevision != 0
			broker.mu.Unlock()
			if leaseSurvived {
				t.Fatal("recipient mismatch retained a previously issued lease")
			}
			if active, err := broker.HasActiveJob("updater-a"); err != nil || active {
				t.Fatalf("recipient mismatch retained active job: active=%v err=%v", active, err)
			}
		})
	}
}

func TestBootstrapBrokerRejectsUnsafeReportMessages(t *testing.T) {
	broker := newUpdateHostBootstrapBroker(func() time.Time {
		return time.Date(2026, 7, 27, 18, 0, 0, 0, time.UTC)
	}, nil)
	valid := UpdateHostBootstrapReportParams{
		JobID: "job-a", UpdaterID: "updater-a", ExpectedRevision: 1, LeaseToken: "lease",
		HostID: "host-a", Status: UpdateHostBootstrapHostStatusFailed, Progress: 100,
		Code: "ssh_failed", Message: "connection refused",
	}
	for name, message := range map[string]string{
		"C0":            "failed\nnext line",
		"DEL":           "failed\u007fhidden",
		"bidi override": "failed\u202esecret",
		"bidi isolate":  "failed\u2066secret\u2069",
	} {
		t.Run(name, func(t *testing.T) {
			params := valid
			params.Message = message
			if _, err := broker.Report(params); !errors.Is(err, ErrUpdateHostBootstrapInvalid) {
				t.Fatalf("Report() error = %v", err)
			}
		})
	}
}

func TestBootstrapBrokerPrunesTerminalJobsAndIdempotency(t *testing.T) {
	now := time.Date(2026, 7, 27, 19, 0, 0, 0, time.UTC)
	broker := newUpdateHostBootstrapBroker(func() time.Time { return now }, nil)
	recipientFingerprint := bootstrapBrokerRecipientFingerprint(1)
	for index := 0; index < UpdateHostBootstrapMaxRetainedJobsPerUpdater+10; index++ {
		jobID := "job-retained-" + time.Unix(int64(index), 0).UTC().Format("150405")
		idempotencyKey := "idem-retained-" + time.Unix(int64(index), 0).UTC().Format("150405")
		job := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
			UpdaterID: "updater-a", ExpectedRevision: 1, ClientJobID: jobID,
			IdempotencyKey: idempotencyKey, RecipientKeyFingerprint: recipientFingerprint,
			HostIDs: []string{"host-a"}, Envelope: []byte("credential"),
		})
		if _, err := broker.Cancel(job.ID, "updater-a", 1); err != nil {
			t.Fatalf("Cancel(%d) error = %v", index, err)
		}
		now = now.Add(time.Second)
	}

	jobs, err := broker.List("updater-a")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != UpdateHostBootstrapListLimit {
		t.Fatalf("List() length = %d, want %d", len(jobs), UpdateHostBootstrapListLimit)
	}
	broker.mu.Lock()
	retainedJobs := len(broker.jobs)
	retainedIdempotency := len(broker.idempotency)
	broker.mu.Unlock()
	if retainedJobs > UpdateHostBootstrapMaxRetainedJobsPerUpdater ||
		retainedIdempotency != retainedJobs {
		t.Fatalf("retained state is unbounded or inconsistent: jobs=%d idempotency=%d", retainedJobs, retainedIdempotency)
	}

	now = now.Add(UpdateHostBootstrapTerminalRetentionTTL)
	jobs, err = broker.List("updater-a")
	if err != nil {
		t.Fatalf("List() after retention error = %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expired terminal jobs survived retention: %#v", jobs)
	}
	broker.mu.Lock()
	retainedJobs = len(broker.jobs)
	retainedIdempotency = len(broker.idempotency)
	activeJobs := len(broker.activeByUpdater)
	broker.mu.Unlock()
	if retainedJobs != 0 || retainedIdempotency != 0 || activeJobs != 0 {
		t.Fatalf("retention cleanup incomplete: jobs=%d idempotency=%d active=%d", retainedJobs, retainedIdempotency, activeJobs)
	}
}

func TestBootstrapBrokerBoundsTerminalJobsAcrossUpdaters(t *testing.T) {
	now := time.Date(2026, 7, 27, 20, 0, 0, 0, time.UTC)
	broker := newUpdateHostBootstrapBroker(func() time.Time { return now }, nil)
	recipientFingerprint := bootstrapBrokerRecipientFingerprint(1)
	for index := 0; index < UpdateHostBootstrapMaxRetainedJobs+10; index++ {
		updaterID := fmt.Sprintf("updater-%04d", index)
		jobID := fmt.Sprintf("job-global-%04d", index)
		job := mustCreateBootstrapJob(t, broker, UpdateHostBootstrapCreateParams{
			UpdaterID: updaterID, ExpectedRevision: 1, ClientJobID: jobID,
			IdempotencyKey: "idem-global", RecipientKeyFingerprint: recipientFingerprint,
			HostIDs: []string{"host-a"}, Envelope: []byte("credential"),
		})
		if _, err := broker.Cancel(job.ID, updaterID, 1); err != nil {
			t.Fatalf("Cancel(%d) error = %v", index, err)
		}
		now = now.Add(time.Millisecond)
	}

	broker.mu.Lock()
	retainedJobs := len(broker.jobs)
	retainedIdempotency := len(broker.idempotency)
	broker.mu.Unlock()
	if retainedJobs > UpdateHostBootstrapMaxRetainedJobs || retainedIdempotency != retainedJobs {
		t.Fatalf("global retained state is unbounded or inconsistent: jobs=%d idempotency=%d", retainedJobs, retainedIdempotency)
	}
}

func TestBootstrapBrokerRejectsUnboundedOrMalformedInputs(t *testing.T) {
	broker := newUpdateHostBootstrapBroker(func() time.Time {
		return time.Date(2026, 7, 27, 14, 0, 0, 0, time.UTC)
	}, nil)
	valid := UpdateHostBootstrapCreateParams{
		UpdaterID: "updater-a", ExpectedRevision: 1, ClientJobID: "job-valid",
		IdempotencyKey: "idem-valid", RecipientKeyFingerprint: bootstrapBrokerRecipientFingerprint(1),
		HostIDs: []string{"host-a"}, Envelope: []byte("credential"),
	}
	cases := map[string]func(*UpdateHostBootstrapCreateParams){
		"empty recipient fingerprint": func(p *UpdateHostBootstrapCreateParams) { p.RecipientKeyFingerprint = "" },
		"invalid recipient fingerprint": func(p *UpdateHostBootstrapCreateParams) {
			p.RecipientKeyFingerprint = "SHA256:not-canonical"
		},
		"empty envelope": func(p *UpdateHostBootstrapCreateParams) { p.Envelope = nil },
		"oversize envelope": func(p *UpdateHostBootstrapCreateParams) {
			p.Envelope = make([]byte, UpdateHostBootstrapMaxEnvelopeBytes+1)
		},
		"invalid updater":  func(p *UpdateHostBootstrapCreateParams) { p.UpdaterID = "bad updater" },
		"invalid revision": func(p *UpdateHostBootstrapCreateParams) { p.ExpectedRevision = 0 },
		"invalid job id":   func(p *UpdateHostBootstrapCreateParams) { p.ClientJobID = "../job" },
		"empty host":       func(p *UpdateHostBootstrapCreateParams) { p.HostIDs = []string{" "} },
		"too many hosts": func(p *UpdateHostBootstrapCreateParams) {
			p.HostIDs = make([]string, UpdateHostBootstrapMaxHosts+1)
			for i := range p.HostIDs {
				p.HostIDs[i] = "host-" + strings.Repeat("a", i/26) + string(rune('a'+i%26))
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			params := valid
			mutate(&params)
			if _, _, err := broker.Create(params); !errors.Is(err, ErrUpdateHostBootstrapInvalid) {
				t.Fatalf("Create() error = %v", err)
			}
		})
	}
}

func mustCreateBootstrapJob(t *testing.T, broker *UpdateHostBootstrapBroker, params UpdateHostBootstrapCreateParams) UpdateHostBootstrapJob {
	t.Helper()
	job, replayed, err := broker.Create(params)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if replayed {
		t.Fatal("Create() unexpectedly replayed")
	}
	return job
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func allZero(value []byte) bool {
	for _, b := range value {
		if b != 0 {
			return false
		}
	}
	return true
}

func bootstrapBrokerRecipientFingerprint(fill byte) string {
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, sha256.Size))
}
