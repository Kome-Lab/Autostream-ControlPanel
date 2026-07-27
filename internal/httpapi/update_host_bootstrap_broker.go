package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	UpdateHostBootstrapCredentialTTL             = 5 * time.Minute
	UpdateHostBootstrapExecutionIdleTTL          = time.Hour
	UpdateHostBootstrapExecutionMaxTTL           = 72 * time.Hour
	UpdateHostBootstrapTerminalRetentionTTL      = 24 * time.Hour
	UpdateHostBootstrapMaxRetainedJobs           = 1024
	UpdateHostBootstrapMaxRetainedJobsPerUpdater = 128
	UpdateHostBootstrapListLimit                 = 100
	UpdateHostBootstrapMaxEnvelopeBytes          = 64 * 1024
	UpdateHostBootstrapMaxHosts                  = 128
)

const (
	updateHostBootstrapExecutionExpiredCode     = "bootstrap_execution_expired"
	updateHostBootstrapExecutionExpiredMessage  = "bootstrap execution expired before all hosts reported completion"
	updateHostBootstrapCredentialExpiredCode    = "bootstrap_credential_expired"
	updateHostBootstrapCredentialExpiredMessage = "bootstrap credential expired before the updater accepted the job"
	updateHostBootstrapRecipientChangedCode     = "bootstrap_recipient_key_changed"
	updateHostBootstrapRecipientChangedMessage  = "bootstrap encryption recipient changed before the updater claimed the job"
	updateHostBootstrapCanceledCode             = "bootstrap_canceled"
	updateHostBootstrapCanceledMessage          = "bootstrap job was canceled before host setup completed"
)

type UpdateHostBootstrapStatus string

const (
	UpdateHostBootstrapStatusQueued            UpdateHostBootstrapStatus = "queued"
	UpdateHostBootstrapStatusClaimed           UpdateHostBootstrapStatus = "claimed"
	UpdateHostBootstrapStatusRunning           UpdateHostBootstrapStatus = "running"
	UpdateHostBootstrapStatusSucceeded         UpdateHostBootstrapStatus = "succeeded"
	UpdateHostBootstrapStatusFailed            UpdateHostBootstrapStatus = "failed"
	UpdateHostBootstrapStatusPartialFailed     UpdateHostBootstrapStatus = "partial_failed"
	UpdateHostBootstrapStatusCredentialExpired UpdateHostBootstrapStatus = "credential_expired"
	UpdateHostBootstrapStatusCanceled          UpdateHostBootstrapStatus = "canceled"
)

type UpdateHostBootstrapHostStatus string

const (
	UpdateHostBootstrapHostStatusQueued     UpdateHostBootstrapHostStatus = "queued"
	UpdateHostBootstrapHostStatusConnecting UpdateHostBootstrapHostStatus = "connecting"
	UpdateHostBootstrapHostStatusUploading  UpdateHostBootstrapHostStatus = "uploading"
	UpdateHostBootstrapHostStatusVerifying  UpdateHostBootstrapHostStatus = "verifying"
	UpdateHostBootstrapHostStatusInstalling UpdateHostBootstrapHostStatus = "installing"
	UpdateHostBootstrapHostStatusProbing    UpdateHostBootstrapHostStatus = "probing"
	UpdateHostBootstrapHostStatusSucceeded  UpdateHostBootstrapHostStatus = "succeeded"
	UpdateHostBootstrapHostStatusFailed     UpdateHostBootstrapHostStatus = "failed"
)

var (
	ErrUpdateHostBootstrapInvalid              = errors.New("invalid update host bootstrap job")
	ErrUpdateHostBootstrapNotFound             = errors.New("update host bootstrap job not found")
	ErrUpdateHostBootstrapActiveJob            = errors.New("an active update host bootstrap job already exists for the updater")
	ErrUpdateHostBootstrapIdempotencyConflict  = errors.New("update host bootstrap idempotency conflict")
	ErrUpdateHostBootstrapBinding              = errors.New("update host bootstrap updater or revision mismatch")
	ErrUpdateHostBootstrapRecipientKeyMismatch = errors.New("update host bootstrap recipient key mismatch")
	ErrUpdateHostBootstrapLeaseInvalid         = errors.New("update host bootstrap lease is invalid")
	ErrUpdateHostBootstrapTransition           = errors.New("invalid update host bootstrap state transition")
)

var (
	updateHostBootstrapIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,190}$`)
	updateHostBootstrapCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// UpdateHostBootstrapCreateParams contains the immutable identity and the
// one-time opaque credential envelope for a batch bootstrap job. The broker
// copies Envelope and never exposes it through UpdateHostBootstrapJob.
type UpdateHostBootstrapCreateParams struct {
	UpdaterID               string
	ExpectedRevision        int64
	ClientJobID             string
	IdempotencyKey          string
	RecipientKeyFingerprint string
	HostIDs                 []string
	Envelope                []byte
}

// UpdateHostBootstrapHost is the public progress of one host in a batch.
type UpdateHostBootstrapHost struct {
	HostID    string                        `json:"host_id"`
	Status    UpdateHostBootstrapHostStatus `json:"status"`
	Progress  int                           `json:"progress"`
	Code      string                        `json:"code,omitempty"`
	Message   string                        `json:"message,omitempty"`
	UpdatedAt time.Time                     `json:"updated_at"`
}

// UpdateHostBootstrapJob is safe for list/admin responses. It deliberately has
// no credential envelope, lease token, or lease hash field.
type UpdateHostBootstrapJob struct {
	ID               string                    `json:"job_id"`
	IdempotencyKey   string                    `json:"idempotency_key"`
	UpdaterID        string                    `json:"updater_id"`
	ExpectedRevision int64                     `json:"expected_revision"`
	Status           UpdateHostBootstrapStatus `json:"status"`
	Progress         int                       `json:"progress"`
	HostIDs          []string                  `json:"host_ids"`
	Hosts            []UpdateHostBootstrapHost `json:"hosts"`
	CreatedAt        time.Time                 `json:"created_at"`
	UpdatedAt        time.Time                 `json:"updated_at"`
	ExpiresAt        time.Time                 `json:"expires_at"`
}

// UpdateHostBootstrapClaim is the only result that contains the one-time
// envelope and clear lease token. Callers must keep it out of logs and wipe
// Envelope after writing the no-store response or consuming it locally.
type UpdateHostBootstrapClaim struct {
	Job        UpdateHostBootstrapJob `json:"job"`
	Envelope   []byte                 `json:"envelope"`
	LeaseToken string                 `json:"lease_token"`
}

type UpdateHostBootstrapReportParams struct {
	JobID            string
	UpdaterID        string
	ExpectedRevision int64
	LeaseToken       string
	HostID           string
	Status           UpdateHostBootstrapHostStatus
	Progress         int
	Code             string
	Message          string
}

type updateHostBootstrapJobRecord struct {
	job                     UpdateHostBootstrapJob
	envelope                []byte
	requestFingerprint      [sha256.Size]byte
	recipientKeyFingerprint string
	leaseHash               [sha256.Size]byte
	leaseUpdaterID          string
	leaseRevision           int64
	hasLease                bool
	executionDeadline       time.Time
	terminalAt              time.Time
}

// UpdateHostBootstrapBroker owns only process-local state. In particular, it
// has no persistence hook: a panel restart intentionally requires the
// credential envelope to be entered again.
type UpdateHostBootstrapBroker struct {
	mu sync.Mutex

	now    func() time.Time
	random io.Reader

	fingerprintKey  [sha256.Size]byte
	jobs            map[string]*updateHostBootstrapJobRecord
	idempotency     map[string]string
	activeByUpdater map[string]string
}

func NewUpdateHostBootstrapBroker() *UpdateHostBootstrapBroker {
	return newUpdateHostBootstrapBroker(func() time.Time { return time.Now().UTC() }, rand.Reader)
}

func newUpdateHostBootstrapBroker(now func() time.Time, randomSource io.Reader) *UpdateHostBootstrapBroker {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if randomSource == nil {
		randomSource = rand.Reader
	}
	broker := &UpdateHostBootstrapBroker{
		now:             now,
		random:          randomSource,
		jobs:            make(map[string]*updateHostBootstrapJobRecord),
		idempotency:     make(map[string]string),
		activeByUpdater: make(map[string]string),
	}
	if _, err := io.ReadFull(broker.random, broker.fingerprintKey[:]); err != nil {
		panic("initialize update host bootstrap broker entropy: " + err.Error())
	}
	return broker
}

func (b *UpdateHostBootstrapBroker) Create(params UpdateHostBootstrapCreateParams) (UpdateHostBootstrapJob, bool, error) {
	normalized, err := normalizeUpdateHostBootstrapCreateParams(params)
	if err != nil {
		return UpdateHostBootstrapJob{}, false, err
	}
	defer wipeUpdateHostBootstrapBytes(normalized.Envelope)
	fingerprint, err := b.fingerprint(normalized)
	if err != nil {
		return UpdateHostBootstrapJob{}, false, err
	}
	now := b.nowUTC()
	idempotencyKey := updateHostBootstrapIdempotencyMapKey(normalized.UpdaterID, normalized.IdempotencyKey)

	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(now)
	if existingID, ok := b.idempotency[idempotencyKey]; ok {
		existing := b.jobs[existingID]
		if existing == nil || subtle.ConstantTimeCompare(existing.requestFingerprint[:], fingerprint[:]) != 1 {
			return UpdateHostBootstrapJob{}, false, ErrUpdateHostBootstrapIdempotencyConflict
		}
		return publicUpdateHostBootstrapJob(existing.job), true, nil
	}
	if _, exists := b.jobs[normalized.ClientJobID]; exists {
		return UpdateHostBootstrapJob{}, false, ErrUpdateHostBootstrapIdempotencyConflict
	}
	if activeID, ok := b.activeByUpdater[normalized.UpdaterID]; ok {
		if active := b.jobs[activeID]; active != nil && updateHostBootstrapStatusActive(active.job.Status) {
			return UpdateHostBootstrapJob{}, false, ErrUpdateHostBootstrapActiveJob
		}
		delete(b.activeByUpdater, normalized.UpdaterID)
	}

	hosts := make([]UpdateHostBootstrapHost, len(normalized.HostIDs))
	for i, hostID := range normalized.HostIDs {
		hosts[i] = UpdateHostBootstrapHost{
			HostID: hostID, Status: UpdateHostBootstrapHostStatusQueued, UpdatedAt: now,
		}
	}
	job := UpdateHostBootstrapJob{
		ID: normalized.ClientJobID, IdempotencyKey: normalized.IdempotencyKey,
		UpdaterID: normalized.UpdaterID, ExpectedRevision: normalized.ExpectedRevision,
		Status: UpdateHostBootstrapStatusQueued, HostIDs: append([]string(nil), normalized.HostIDs...),
		Hosts: hosts, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(UpdateHostBootstrapCredentialTTL),
	}
	record := &updateHostBootstrapJobRecord{
		job: job, envelope: append([]byte(nil), normalized.Envelope...), requestFingerprint: fingerprint,
		recipientKeyFingerprint: normalized.RecipientKeyFingerprint,
	}
	b.jobs[job.ID] = record
	b.idempotency[idempotencyKey] = job.ID
	b.activeByUpdater[job.UpdaterID] = job.ID
	return publicUpdateHostBootstrapJob(job), false, nil
}

// HasActiveJob reports whether updaterID currently owns a queued, claimed, or
// running bootstrap job. Expiry is applied while holding the broker lock so
// callers do not block policy changes on stale jobs.
func (b *UpdateHostBootstrapBroker) HasActiveJob(updaterID string) (bool, error) {
	updaterID = strings.TrimSpace(updaterID)
	if !updateHostBootstrapIDPattern.MatchString(updaterID) {
		return false, ErrUpdateHostBootstrapInvalid
	}
	now := b.nowUTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(now)
	activeID, ok := b.activeByUpdater[updaterID]
	if !ok {
		return false, nil
	}
	record := b.jobs[activeID]
	if record == nil || !updateHostBootstrapStatusActive(record.job.Status) {
		delete(b.activeByUpdater, updaterID)
		return false, nil
	}
	return true, nil
}

func (b *UpdateHostBootstrapBroker) Claim(
	updaterID string,
	expectedRevision int64,
	recipientKeyFingerprint string,
	currentRecipientKeyFingerprint string,
) (UpdateHostBootstrapClaim, error) {
	updaterID = strings.TrimSpace(updaterID)
	recipientKeyFingerprint = strings.TrimSpace(recipientKeyFingerprint)
	currentRecipientKeyFingerprint = strings.TrimSpace(currentRecipientKeyFingerprint)
	if !updateHostBootstrapIDPattern.MatchString(updaterID) || expectedRevision <= 0 ||
		!validUpdateHostBootstrapRecipientKeyFingerprint(recipientKeyFingerprint) {
		return UpdateHostBootstrapClaim{}, ErrUpdateHostBootstrapInvalid
	}
	now := b.nowUTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(now)

	jobID, ok := b.activeByUpdater[updaterID]
	if !ok {
		return UpdateHostBootstrapClaim{}, ErrUpdateHostBootstrapNotFound
	}
	record := b.jobs[jobID]
	if record == nil {
		delete(b.activeByUpdater, updaterID)
		return UpdateHostBootstrapClaim{}, ErrUpdateHostBootstrapNotFound
	}
	if record.job.UpdaterID != updaterID || record.job.ExpectedRevision != expectedRevision {
		return UpdateHostBootstrapClaim{}, ErrUpdateHostBootstrapBinding
	}
	if record.recipientKeyFingerprint != recipientKeyFingerprint ||
		currentRecipientKeyFingerprint != recipientKeyFingerprint {
		terminalizeUpdateHostBootstrapJob(
			record,
			UpdateHostBootstrapStatusCredentialExpired,
			updateHostBootstrapRecipientChangedCode,
			updateHostBootstrapRecipientChangedMessage,
			now,
		)
		delete(b.activeByUpdater, record.job.UpdaterID)
		b.pruneLocked(now)
		return UpdateHostBootstrapClaim{}, ErrUpdateHostBootstrapRecipientKeyMismatch
	}
	if record.job.Status != UpdateHostBootstrapStatusQueued && record.job.Status != UpdateHostBootstrapStatusClaimed {
		return UpdateHostBootstrapClaim{}, ErrUpdateHostBootstrapTransition
	}
	if len(record.envelope) == 0 {
		return UpdateHostBootstrapClaim{}, ErrUpdateHostBootstrapTransition
	}
	rawLease, err := b.newLeaseToken()
	if err != nil {
		return UpdateHostBootstrapClaim{}, err
	}
	record.leaseHash = sha256.Sum256([]byte(rawLease))
	record.leaseUpdaterID = updaterID
	record.leaseRevision = expectedRevision
	record.hasLease = true
	record.job.Status = UpdateHostBootstrapStatusClaimed
	record.job.UpdatedAt = now
	return UpdateHostBootstrapClaim{
		Job: publicUpdateHostBootstrapJob(record.job), Envelope: append([]byte(nil), record.envelope...), LeaseToken: rawLease,
	}, nil
}

func (b *UpdateHostBootstrapBroker) Accept(jobID, updaterID string, expectedRevision int64, leaseToken string) (UpdateHostBootstrapJob, error) {
	jobID = strings.TrimSpace(jobID)
	updaterID = strings.TrimSpace(updaterID)
	if !updateHostBootstrapIDPattern.MatchString(jobID) || !updateHostBootstrapIDPattern.MatchString(updaterID) || expectedRevision <= 0 || leaseToken == "" {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapInvalid
	}
	now := b.nowUTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(now)

	record := b.jobs[jobID]
	if record == nil {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapNotFound
	}
	if !record.validLease(updaterID, expectedRevision, leaseToken) {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapLeaseInvalid
	}
	if record.job.Status == UpdateHostBootstrapStatusRunning {
		return publicUpdateHostBootstrapJob(record.job), nil
	}
	if record.job.Status != UpdateHostBootstrapStatusClaimed {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapTransition
	}
	wipeUpdateHostBootstrapBytes(record.envelope)
	record.envelope = nil
	record.job.Status = UpdateHostBootstrapStatusRunning
	record.job.UpdatedAt = now
	record.executionDeadline = now.Add(UpdateHostBootstrapExecutionMaxTTL)
	record.job.ExpiresAt = updateHostBootstrapIdleDeadline(now, record.executionDeadline)
	return publicUpdateHostBootstrapJob(record.job), nil
}

func (b *UpdateHostBootstrapBroker) Report(params UpdateHostBootstrapReportParams) (UpdateHostBootstrapJob, error) {
	normalized, err := normalizeUpdateHostBootstrapReportParams(params)
	if err != nil {
		return UpdateHostBootstrapJob{}, err
	}
	now := b.nowUTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(now)

	record := b.jobs[normalized.JobID]
	if record == nil {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapNotFound
	}
	// A terminal transition destroys the lease verifier. A response-lost retry
	// therefore fails closed instead of retaining a verifier solely to
	// acknowledge duplicate terminal reports.
	if !record.validLease(normalized.UpdaterID, normalized.ExpectedRevision, normalized.LeaseToken) {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapLeaseInvalid
	}
	hostIndex := updateHostBootstrapHostIndex(record.job.Hosts, normalized.HostID)
	if hostIndex < 0 {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapBinding
	}
	current := record.job.Hosts[hostIndex]
	next := UpdateHostBootstrapHost{
		HostID: normalized.HostID, Status: normalized.Status, Progress: normalized.Progress,
		Code: normalized.Code, Message: normalized.Message, UpdatedAt: now,
	}
	if updateHostBootstrapHostStatusTerminal(next.Status) {
		next.Progress = 100
	}
	if updateHostBootstrapStatusTerminal(record.job.Status) {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapLeaseInvalid
	}
	if record.job.Status != UpdateHostBootstrapStatusRunning || !validUpdateHostBootstrapHostTransition(current, next) {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapTransition
	}
	record.job.Hosts[hostIndex] = next
	record.job.UpdatedAt = now
	if deriveUpdateHostBootstrapJobProgress(record) {
		record.terminalAt = now
		clearUpdateHostBootstrapSecretState(record)
		delete(b.activeByUpdater, record.job.UpdaterID)
		b.pruneLocked(now)
	} else {
		record.job.ExpiresAt = updateHostBootstrapIdleDeadline(now, record.executionDeadline)
	}
	return publicUpdateHostBootstrapJob(record.job), nil
}

func (b *UpdateHostBootstrapBroker) List(updaterID string) ([]UpdateHostBootstrapJob, error) {
	updaterID = strings.TrimSpace(updaterID)
	if !updateHostBootstrapIDPattern.MatchString(updaterID) {
		return nil, ErrUpdateHostBootstrapInvalid
	}
	now := b.nowUTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(now)

	jobs := make([]UpdateHostBootstrapJob, 0)
	for _, record := range b.jobs {
		if record.job.UpdaterID == updaterID {
			jobs = append(jobs, publicUpdateHostBootstrapJob(record.job))
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID > jobs[j].ID
		}
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	if len(jobs) > UpdateHostBootstrapListLimit {
		jobs = jobs[:UpdateHostBootstrapListLimit]
	}
	return jobs, nil
}

func (b *UpdateHostBootstrapBroker) Cancel(jobID, updaterID string, expectedRevision int64) (UpdateHostBootstrapJob, error) {
	jobID = strings.TrimSpace(jobID)
	updaterID = strings.TrimSpace(updaterID)
	if !updateHostBootstrapIDPattern.MatchString(jobID) || !updateHostBootstrapIDPattern.MatchString(updaterID) || expectedRevision <= 0 {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapInvalid
	}
	now := b.nowUTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expireLocked(now)

	record := b.jobs[jobID]
	if record == nil {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapNotFound
	}
	if record.job.UpdaterID != updaterID || record.job.ExpectedRevision != expectedRevision {
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapBinding
	}
	if updateHostBootstrapStatusTerminal(record.job.Status) {
		if record.job.Status == UpdateHostBootstrapStatusCanceled {
			return publicUpdateHostBootstrapJob(record.job), nil
		}
		return UpdateHostBootstrapJob{}, ErrUpdateHostBootstrapTransition
	}
	terminalizeUpdateHostBootstrapJob(
		record,
		UpdateHostBootstrapStatusCanceled,
		updateHostBootstrapCanceledCode,
		updateHostBootstrapCanceledMessage,
		now,
	)
	delete(b.activeByUpdater, record.job.UpdaterID)
	b.pruneLocked(now)
	return publicUpdateHostBootstrapJob(record.job), nil
}

func (b *UpdateHostBootstrapBroker) expireLocked(now time.Time) {
	for _, record := range b.jobs {
		if record.job.ExpiresAt.After(now) {
			continue
		}
		switch record.job.Status {
		case UpdateHostBootstrapStatusQueued, UpdateHostBootstrapStatusClaimed:
			terminalizeUpdateHostBootstrapJob(
				record,
				UpdateHostBootstrapStatusCredentialExpired,
				updateHostBootstrapCredentialExpiredCode,
				updateHostBootstrapCredentialExpiredMessage,
				now,
			)
			delete(b.activeByUpdater, record.job.UpdaterID)
		case UpdateHostBootstrapStatusRunning:
			failUnfinishedUpdateHostBootstrapHosts(
				record,
				updateHostBootstrapExecutionExpiredCode,
				updateHostBootstrapExecutionExpiredMessage,
				now,
			)
			record.job.UpdatedAt = now
			deriveUpdateHostBootstrapJobProgress(record)
			record.terminalAt = now
			clearUpdateHostBootstrapSecretState(record)
			delete(b.activeByUpdater, record.job.UpdaterID)
		}
	}
	b.pruneLocked(now)
}

func terminalizeUpdateHostBootstrapJob(
	record *updateHostBootstrapJobRecord,
	status UpdateHostBootstrapStatus,
	code string,
	message string,
	now time.Time,
) {
	failUnfinishedUpdateHostBootstrapHosts(record, code, message, now)
	record.job.Status = status
	record.job.Progress = 100
	record.job.UpdatedAt = now
	record.terminalAt = now
	clearUpdateHostBootstrapSecretState(record)
}

func failUnfinishedUpdateHostBootstrapHosts(
	record *updateHostBootstrapJobRecord,
	code string,
	message string,
	now time.Time,
) {
	for i := range record.job.Hosts {
		if updateHostBootstrapHostStatusTerminal(record.job.Hosts[i].Status) {
			continue
		}
		record.job.Hosts[i].Status = UpdateHostBootstrapHostStatusFailed
		record.job.Hosts[i].Progress = 100
		record.job.Hosts[i].Code = code
		record.job.Hosts[i].Message = message
		record.job.Hosts[i].UpdatedAt = now
	}
}

func (b *UpdateHostBootstrapBroker) pruneLocked(now time.Time) {
	for _, record := range b.jobs {
		if !updateHostBootstrapStatusTerminal(record.job.Status) {
			continue
		}
		terminalAt := updateHostBootstrapTerminalAt(record)
		if !terminalAt.Add(UpdateHostBootstrapTerminalRetentionTTL).After(now) {
			b.deleteRecordLocked(record)
		}
	}

	byUpdater := make(map[string][]*updateHostBootstrapJobRecord)
	allTerminal := make([]*updateHostBootstrapJobRecord, 0, len(b.jobs))
	for _, record := range b.jobs {
		if !updateHostBootstrapStatusTerminal(record.job.Status) {
			continue
		}
		byUpdater[record.job.UpdaterID] = append(byUpdater[record.job.UpdaterID], record)
		allTerminal = append(allTerminal, record)
	}
	for _, records := range byUpdater {
		sortUpdateHostBootstrapRecordsOldestFirst(records)
		for len(records) > UpdateHostBootstrapMaxRetainedJobsPerUpdater {
			b.deleteRecordLocked(records[0])
			records = records[1:]
		}
	}

	allTerminal = allTerminal[:0]
	for _, record := range b.jobs {
		if updateHostBootstrapStatusTerminal(record.job.Status) {
			allTerminal = append(allTerminal, record)
		}
	}
	sortUpdateHostBootstrapRecordsOldestFirst(allTerminal)
	for len(allTerminal) > UpdateHostBootstrapMaxRetainedJobs {
		b.deleteRecordLocked(allTerminal[0])
		allTerminal = allTerminal[1:]
	}
}

func (b *UpdateHostBootstrapBroker) deleteRecordLocked(record *updateHostBootstrapJobRecord) {
	if record == nil {
		return
	}
	clearUpdateHostBootstrapSecretState(record)
	delete(b.jobs, record.job.ID)
	idempotencyKey := updateHostBootstrapIdempotencyMapKey(record.job.UpdaterID, record.job.IdempotencyKey)
	if b.idempotency[idempotencyKey] == record.job.ID {
		delete(b.idempotency, idempotencyKey)
	}
	if b.activeByUpdater[record.job.UpdaterID] == record.job.ID {
		delete(b.activeByUpdater, record.job.UpdaterID)
	}
}

func sortUpdateHostBootstrapRecordsOldestFirst(records []*updateHostBootstrapJobRecord) {
	sort.Slice(records, func(i, j int) bool {
		left := updateHostBootstrapTerminalAt(records[i])
		right := updateHostBootstrapTerminalAt(records[j])
		if left.Equal(right) {
			if records[i].job.CreatedAt.Equal(records[j].job.CreatedAt) {
				return records[i].job.ID < records[j].job.ID
			}
			return records[i].job.CreatedAt.Before(records[j].job.CreatedAt)
		}
		return left.Before(right)
	})
}

func updateHostBootstrapTerminalAt(record *updateHostBootstrapJobRecord) time.Time {
	if !record.terminalAt.IsZero() {
		return record.terminalAt
	}
	return record.job.UpdatedAt
}

func updateHostBootstrapIdleDeadline(now, absoluteDeadline time.Time) time.Time {
	idleDeadline := now.Add(UpdateHostBootstrapExecutionIdleTTL)
	if !absoluteDeadline.IsZero() && idleDeadline.After(absoluteDeadline) {
		return absoluteDeadline
	}
	return idleDeadline
}

func clearUpdateHostBootstrapSecretState(record *updateHostBootstrapJobRecord) {
	wipeUpdateHostBootstrapBytes(record.envelope)
	record.envelope = nil
	record.hasLease = false
	record.leaseHash = [sha256.Size]byte{}
	record.leaseUpdaterID = ""
	record.leaseRevision = 0
}

func (b *UpdateHostBootstrapBroker) fingerprint(params UpdateHostBootstrapCreateParams) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		UpdaterID               string   `json:"updater_id"`
		ExpectedRevision        int64    `json:"expected_revision"`
		ClientJobID             string   `json:"client_job_id"`
		IdempotencyKey          string   `json:"idempotency_key"`
		RecipientKeyFingerprint string   `json:"recipient_key_fingerprint"`
		HostIDs                 []string `json:"host_ids"`
		Envelope                []byte   `json:"envelope"`
	}{
		UpdaterID: params.UpdaterID, ExpectedRevision: params.ExpectedRevision,
		ClientJobID: params.ClientJobID, IdempotencyKey: params.IdempotencyKey,
		RecipientKeyFingerprint: params.RecipientKeyFingerprint,
		HostIDs:                 params.HostIDs,
		Envelope:                params.Envelope,
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	mac := hmac.New(sha256.New, b.fingerprintKey[:])
	_, _ = mac.Write(payload)
	var result [sha256.Size]byte
	copy(result[:], mac.Sum(nil))
	wipeUpdateHostBootstrapBytes(payload)
	return result, nil
}

func (b *UpdateHostBootstrapBroker) newLeaseToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(b.random, raw); err != nil {
		return "", err
	}
	token := "ast_bootstrap_" + base64.RawURLEncoding.EncodeToString(raw)
	wipeUpdateHostBootstrapBytes(raw)
	return token, nil
}

func (b *UpdateHostBootstrapBroker) nowUTC() time.Time {
	return b.now().UTC()
}

func (record *updateHostBootstrapJobRecord) validLease(updaterID string, expectedRevision int64, leaseToken string) bool {
	if !record.hasLease || record.leaseUpdaterID != updaterID || record.leaseRevision != expectedRevision ||
		record.job.UpdaterID != updaterID || record.job.ExpectedRevision != expectedRevision {
		return false
	}
	actual := sha256.Sum256([]byte(leaseToken))
	return subtle.ConstantTimeCompare(actual[:], record.leaseHash[:]) == 1
}

func normalizeUpdateHostBootstrapCreateParams(params UpdateHostBootstrapCreateParams) (UpdateHostBootstrapCreateParams, error) {
	params.UpdaterID = strings.TrimSpace(params.UpdaterID)
	params.ClientJobID = strings.TrimSpace(params.ClientJobID)
	params.IdempotencyKey = strings.TrimSpace(params.IdempotencyKey)
	if !updateHostBootstrapIDPattern.MatchString(params.UpdaterID) || params.ExpectedRevision <= 0 ||
		!updateHostBootstrapIDPattern.MatchString(params.ClientJobID) ||
		params.IdempotencyKey == "" || len(params.IdempotencyKey) > 128 || containsUpdateHostBootstrapControl(params.IdempotencyKey) ||
		!validUpdateHostBootstrapRecipientKeyFingerprint(params.RecipientKeyFingerprint) ||
		len(params.Envelope) == 0 || len(params.Envelope) > UpdateHostBootstrapMaxEnvelopeBytes ||
		len(params.HostIDs) == 0 || len(params.HostIDs) > UpdateHostBootstrapMaxHosts {
		return UpdateHostBootstrapCreateParams{}, ErrUpdateHostBootstrapInvalid
	}
	hostSet := make(map[string]struct{}, len(params.HostIDs))
	for _, rawHostID := range params.HostIDs {
		hostID := strings.TrimSpace(rawHostID)
		if !updateHostBootstrapIDPattern.MatchString(hostID) {
			return UpdateHostBootstrapCreateParams{}, ErrUpdateHostBootstrapInvalid
		}
		hostSet[hostID] = struct{}{}
	}
	params.HostIDs = make([]string, 0, len(hostSet))
	for hostID := range hostSet {
		params.HostIDs = append(params.HostIDs, hostID)
	}
	sort.Strings(params.HostIDs)
	params.Envelope = append([]byte(nil), params.Envelope...)
	return params, nil
}

func validUpdateHostBootstrapRecipientKeyFingerprint(value string) bool {
	if value != strings.TrimSpace(value) || !strings.HasPrefix(value, "SHA256:") {
		return false
	}
	encoded := strings.TrimPrefix(value, "SHA256:")
	decoded, err := base64.RawStdEncoding.DecodeString(encoded)
	return err == nil && len(decoded) == sha256.Size &&
		base64.RawStdEncoding.EncodeToString(decoded) == encoded
}

func normalizeUpdateHostBootstrapReportParams(params UpdateHostBootstrapReportParams) (UpdateHostBootstrapReportParams, error) {
	params.JobID = strings.TrimSpace(params.JobID)
	params.UpdaterID = strings.TrimSpace(params.UpdaterID)
	params.LeaseToken = strings.TrimSpace(params.LeaseToken)
	params.HostID = strings.TrimSpace(params.HostID)
	params.Code = strings.TrimSpace(params.Code)
	params.Message = strings.TrimSpace(params.Message)
	if !updateHostBootstrapIDPattern.MatchString(params.JobID) || !updateHostBootstrapIDPattern.MatchString(params.UpdaterID) ||
		params.ExpectedRevision <= 0 || params.LeaseToken == "" || len(params.LeaseToken) > 256 ||
		!updateHostBootstrapIDPattern.MatchString(params.HostID) || !updateHostBootstrapHostStatusReportable(params.Status) ||
		params.Progress < 0 || params.Progress > 100 || len(params.Message) > 500 ||
		containsUpdateHostBootstrapUnsafeText(params.Message) ||
		(params.Code != "" && !updateHostBootstrapCodePattern.MatchString(params.Code)) {
		return UpdateHostBootstrapReportParams{}, ErrUpdateHostBootstrapInvalid
	}
	return params, nil
}

func deriveUpdateHostBootstrapJobProgress(record *updateHostBootstrapJobRecord) bool {
	totalProgress, succeeded, failed := 0, 0, 0
	for _, host := range record.job.Hosts {
		totalProgress += host.Progress
		switch host.Status {
		case UpdateHostBootstrapHostStatusSucceeded:
			succeeded++
		case UpdateHostBootstrapHostStatusFailed:
			failed++
		}
	}
	record.job.Progress = totalProgress / len(record.job.Hosts)
	if succeeded+failed != len(record.job.Hosts) {
		record.job.Status = UpdateHostBootstrapStatusRunning
		return false
	}
	switch {
	case succeeded == len(record.job.Hosts):
		record.job.Status = UpdateHostBootstrapStatusSucceeded
	case failed == len(record.job.Hosts):
		record.job.Status = UpdateHostBootstrapStatusFailed
	default:
		record.job.Status = UpdateHostBootstrapStatusPartialFailed
	}
	return true
}

func validUpdateHostBootstrapHostTransition(current, next UpdateHostBootstrapHost) bool {
	if updateHostBootstrapHostStatusTerminal(current.Status) {
		return sameUpdateHostBootstrapHostReport(current, next)
	}
	if next.Status == UpdateHostBootstrapHostStatusFailed {
		return true
	}
	currentRank, currentOK := updateHostBootstrapHostStatusRank(current.Status)
	nextRank, nextOK := updateHostBootstrapHostStatusRank(next.Status)
	if !currentOK || !nextOK {
		return false
	}
	if nextRank < currentRank {
		return false
	}
	return nextRank != currentRank || next.Progress >= current.Progress
}

func sameUpdateHostBootstrapHostReport(current, next UpdateHostBootstrapHost) bool {
	return current.HostID == next.HostID && current.Status == next.Status && current.Progress == next.Progress &&
		current.Code == next.Code && current.Message == next.Message
}

func updateHostBootstrapHostStatusRank(status UpdateHostBootstrapHostStatus) (int, bool) {
	switch status {
	case UpdateHostBootstrapHostStatusQueued:
		return 0, true
	case UpdateHostBootstrapHostStatusConnecting:
		return 1, true
	case UpdateHostBootstrapHostStatusVerifying:
		return 2, true
	case UpdateHostBootstrapHostStatusUploading:
		return 3, true
	case UpdateHostBootstrapHostStatusInstalling:
		return 4, true
	case UpdateHostBootstrapHostStatusProbing:
		return 5, true
	case UpdateHostBootstrapHostStatusSucceeded:
		return 6, true
	default:
		return 0, false
	}
}

func updateHostBootstrapHostStatusReportable(status UpdateHostBootstrapHostStatus) bool {
	switch status {
	case UpdateHostBootstrapHostStatusConnecting, UpdateHostBootstrapHostStatusUploading,
		UpdateHostBootstrapHostStatusVerifying, UpdateHostBootstrapHostStatusInstalling,
		UpdateHostBootstrapHostStatusProbing, UpdateHostBootstrapHostStatusSucceeded,
		UpdateHostBootstrapHostStatusFailed:
		return true
	default:
		return false
	}
}

func updateHostBootstrapHostStatusTerminal(status UpdateHostBootstrapHostStatus) bool {
	return status == UpdateHostBootstrapHostStatusSucceeded || status == UpdateHostBootstrapHostStatusFailed
}

func updateHostBootstrapStatusActive(status UpdateHostBootstrapStatus) bool {
	return status == UpdateHostBootstrapStatusQueued || status == UpdateHostBootstrapStatusClaimed || status == UpdateHostBootstrapStatusRunning
}

func updateHostBootstrapStatusTerminal(status UpdateHostBootstrapStatus) bool {
	switch status {
	case UpdateHostBootstrapStatusSucceeded, UpdateHostBootstrapStatusFailed,
		UpdateHostBootstrapStatusPartialFailed, UpdateHostBootstrapStatusCredentialExpired,
		UpdateHostBootstrapStatusCanceled:
		return true
	default:
		return false
	}
}

func updateHostBootstrapHostIndex(hosts []UpdateHostBootstrapHost, hostID string) int {
	index := sort.Search(len(hosts), func(i int) bool { return hosts[i].HostID >= hostID })
	if index < len(hosts) && hosts[index].HostID == hostID {
		return index
	}
	return -1
}

func updateHostBootstrapIdempotencyMapKey(updaterID, idempotencyKey string) string {
	return updaterID + "\x00" + idempotencyKey
}

func publicUpdateHostBootstrapJob(job UpdateHostBootstrapJob) UpdateHostBootstrapJob {
	job.HostIDs = append([]string(nil), job.HostIDs...)
	job.Hosts = append([]UpdateHostBootstrapHost(nil), job.Hosts...)
	return job
}

func containsUpdateHostBootstrapControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func containsUpdateHostBootstrapUnsafeText(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f || updateHostBootstrapDangerousBidiControl(r) {
			return true
		}
	}
	return false
}

func updateHostBootstrapDangerousBidiControl(r rune) bool {
	switch r {
	case '\u061c', '\u200e', '\u200f',
		'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	default:
		return false
	}
}

func wipeUpdateHostBootstrapBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
