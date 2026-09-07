package store

import (
	"testing"
	"time"

	"github.com/example/autostream-contracts/pkg/contracts"
)

func TestMemorySTPortV2LeaseExpirySurvivesReportAndReclaim(t *testing.T) {
	f := newSTPortV2Fixture(t)
	job := f.create(t, contracts.SystemUpdatePortModeLocalOnly, 18084, 0, "lease-precision")
	claimTime := time.Now().UTC().Truncate(time.Second).Add(123456789 * time.Nanosecond)
	activeID := ""
	for _, phase := range []string{"fresh", "same_job_reclaim"} {
		t.Run(phase, func(t *testing.T) {
			generation := int64(1)
			operation := SystemUpdateMutationOperationPortReconfigure
			if activeID != "" {
				generation = job.LeaseGeneration
				operation = SystemUpdateMutationOperationPortReconfigureReconcile
			}
			claim, clear, err := f.updates.ClaimSystemUpdateJobV2(t.Context(), job.AgentServiceID, job.ExecutionHostID, activeID, generation, job.OwnershipEpoch, map[string]string{job.TargetID: "systemd"}, claimTime, time.Minute)
			if err != nil || clear || claim.Job.ID != job.ID || claim.Job.LeaseExpiresAt == nil {
				t.Fatal("claim with nanosecond input failed")
			}
			loaded, err := f.updates.GetSystemUpdateJob(t.Context(), job.ID)
			if err != nil || loaded.LeaseExpiresAt == nil || !claim.LeaseExpiresAt.Equal(*loaded.LeaseExpiresAt) || !claim.Job.LeaseExpiresAt.Equal(*loaded.LeaseExpiresAt) || claim.LeaseExpiresAt.Nanosecond() != 123456000 || !claim.LeaseExpiresAt.After(claimTime) || claim.LeaseExpiresAt.After(claimTime.Add(time.Minute)) {
				t.Fatal("memory lease differs from persisted authority or DATETIME(6) lifetime")
			}
			status := SystemUpdateStatusInstalling
			if activeID != "" {
				status = SystemUpdateStatusReconciling
			}
			reported, applied, err := f.updates.ReportSystemUpdateJob(t.Context(), job.ID, stPortReport(claim.Job, 1, status, nil), claimTime.Add(time.Second), time.Minute)
			if err != nil || !applied || reported.LeaseExpiresAt == nil || !reported.LeaseExpiresAt.Equal(claim.LeaseExpiresAt) {
				t.Fatal("v2 progress changed its immutable lease expiry")
			}
			loaded, err = f.updates.GetSystemUpdateJob(t.Context(), job.ID)
			if err != nil || loaded.LeaseExpiresAt == nil || !loaded.LeaseExpiresAt.Equal(claim.LeaseExpiresAt) {
				t.Fatal("v2 progress stored a different lease expiry")
			}
			f.consume(t, loaded, operation, "session-lease-precision-"+phase)
			job = loaded
			activeID = job.ID
			claimTime = claimTime.Add(2 * time.Second)
		})
	}
}
