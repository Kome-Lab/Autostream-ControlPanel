package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBSTPortV2LeaseExpirySurvivesReportAndReclaim(t *testing.T) {
	f := newMariaDBSTPortV2Fixture(t)
	job := f.create(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "lease-precision")
	claimTime := time.Now().UTC().Truncate(time.Second).Add(123456789 * time.Nanosecond)
	const leaseTTL = time.Minute

	// Reproduce the old claim's unrounded value through the real column type.
	// This witnesses the precision loss without weakening the lease comparison.
	unrounded := claimTime.Add(leaseTTL)
	var persisted time.Time
	if err := f.db.QueryRowContext(f.ctx, `SELECT CAST(? AS DATETIME(6))`, unrounded).Scan(&persisted); err != nil {
		t.Fatal("read MariaDB lease precision witness failed")
	}
	oldJSON, oldErr := json.Marshal(unrounded)
	persistedJSON, persistedErr := json.Marshal(persisted)
	if oldErr != nil || persistedErr != nil || unrounded.Equal(persisted) || string(oldJSON) == string(persistedJSON) || persisted.Nanosecond()%1000 != 0 {
		t.Fatal("unrounded claim did not reproduce the DATETIME(6) lease mismatch")
	}
	t.Log("ST-PORT lease precision witness: unrounded_json_mismatch=true persisted_microseconds=true")

	activeID := ""
	for _, phase := range []string{"fresh", "same_job_reclaim"} {
		t.Run(phase, func(t *testing.T) {
			generation := int64(1)
			operation := store.SystemUpdateMutationOperationPortReconfigure
			if activeID != "" {
				generation = job.LeaseGeneration
				operation = store.SystemUpdateMutationOperationPortReconfigureReconcile
			}
			claim, clear, err := f.updates.ClaimSystemUpdateJobV2(f.ctx, job.AgentServiceID, job.ExecutionHostID, activeID, generation, job.OwnershipEpoch, map[string]string{job.TargetID: "systemd"}, claimTime, leaseTTL)
			if err != nil || clear || claim.Job.ID != job.ID || claim.Job.LeaseExpiresAt == nil {
				t.Fatal("claim with nanosecond input failed")
			}
			loaded, err := f.updates.GetSystemUpdateJob(f.ctx, job.ID)
			if err != nil || loaded.LeaseExpiresAt == nil || !claim.LeaseExpiresAt.Equal(*loaded.LeaseExpiresAt) || !claim.Job.LeaseExpiresAt.Equal(*loaded.LeaseExpiresAt) || claim.LeaseExpiresAt.Nanosecond() != 123456000 || !claim.LeaseExpiresAt.After(claimTime) || claim.LeaseExpiresAt.After(claimTime.Add(leaseTTL)) {
				t.Fatal("returned lease expiry differs from persisted authority or its lifetime")
			}
			status := store.SystemUpdateStatusInstalling
			if activeID != "" {
				status = store.SystemUpdateStatusReconciling
			}
			reported, applied, err := f.updates.ReportSystemUpdateJob(f.ctx, job.ID, mariaDBSTPortV2Report(claim.Job, 1, status, nil), claimTime.Add(time.Second), leaseTTL)
			if err != nil || !applied || reported.LeaseExpiresAt == nil || !reported.LeaseExpiresAt.Equal(claim.LeaseExpiresAt) {
				t.Fatal("v2 progress changed its immutable lease expiry")
			}
			loaded, err = f.updates.GetSystemUpdateJob(f.ctx, job.ID)
			if err != nil || loaded.LeaseExpiresAt == nil || !loaded.LeaseExpiresAt.Equal(claim.LeaseExpiresAt) {
				t.Fatal("v2 progress persisted a different lease expiry")
			}
			f.consume(t, loaded, operation, "session-lease-precision-"+phase)
			job = loaded
			activeID = job.ID
			claimTime = claimTime.Add(2 * time.Second)
		})
	}
}
