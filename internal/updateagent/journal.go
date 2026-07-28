package updateagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type PendingReport struct {
	JobID  string    `json:"job_id"`
	Report JobReport `json:"report"`
}

type journalData struct {
	ActiveJob        *UpdateJob                  `json:"active_job,omitempty"`
	ActivePlan       *RemotePlan                 `json:"active_plan,omitempty"`
	ActivePortPlan   *SystemdPortReconfigurePlan `json:"active_port_plan,omitempty"`
	NextSeq          uint64                      `json:"next_sequence"`
	Pending          []PendingReport             `json:"pending_reports,omitempty"`
	DeployedVersions map[string]string           `json:"deployed_versions,omitempty"`
}

type Journal struct {
	mu          sync.Mutex
	path        string
	data        journalData
	leaseTokens map[string]string
}

func OpenJournal(stateDir string) (*Journal, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	j := &Journal{path: filepath.Join(stateDir, "journal.json"), data: journalData{NextSeq: 1, DeployedVersions: map[string]string{}}, leaseTokens: map[string]string{}}
	f, err := os.Open(j.path)
	if errors.Is(err, os.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return nil, err
	}
	decodeErr := json.NewDecoder(f).Decode(&j.data)
	closeErr := f.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("decode update journal: %w", decodeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close update journal: %w", closeErr)
	}
	if j.data.NextSeq == 0 {
		j.data.NextSeq = 1
	}
	if j.data.DeployedVersions == nil {
		j.data.DeployedVersions = map[string]string{}
	}
	if j.data.ActiveJob != nil && j.data.ActiveJob.validateOperationUnion() != nil {
		return nil, errors.New("update journal active job operation is invalid")
	}
	if j.data.ActivePlan != nil || j.data.ActivePortPlan != nil {
		if j.data.ActiveJob == nil ||
			(j.data.ActivePlan != nil && j.data.ActivePortPlan != nil) ||
			(j.data.ActivePlan != nil &&
				(j.data.ActiveJob.EffectiveOperation() != updateJobOperationSoftwareUpdate ||
					j.data.ActivePlan.JobID != j.data.ActiveJob.ID ||
					j.data.ActivePlan.Validate() != nil)) ||
			(j.data.ActivePortPlan != nil &&
				(j.data.ActiveJob.EffectiveOperation() != updateJobOperationPortReconfigure ||
					j.data.ActivePortPlan.JobID != j.data.ActiveJob.ID ||
					j.data.ActivePortPlan.Validate() != nil)) {
			return nil, errors.New("update journal active plan binding is invalid")
		}
	}
	// Older journals may contain raw lease tokens. Discard them and immediately
	// rewrite the protected journal without credentials; a fresh process must
	// obtain a new recovery lease instead of reusing a short-lived bearer value.
	scrubbed := false
	if j.data.ActiveJob != nil && j.data.ActiveJob.LeaseToken != "" {
		j.data.ActiveJob.LeaseToken = ""
		scrubbed = true
	}
	if j.data.ActiveJob != nil && !j.data.ActiveJob.ReleaseToken.Empty() {
		j.data.ActiveJob.ReleaseToken = ""
		scrubbed = true
	}
	// A restarted process cannot safely reuse an old execution lease. Drop
	// tokenless pending reports and preserve the active cursor so the next poll
	// obtains a fresh recovery claim and report sequence instead of becoming
	// stuck retrying an unauthorised report forever.
	if len(j.data.Pending) > 0 {
		j.data.Pending = nil
		scrubbed = true
	}
	if scrubbed {
		if err := j.saveLocked(); err != nil {
			return nil, err
		}
	}
	return j, nil
}

func (j *Journal) MarkDeployed(targetID, version string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.data.DeployedVersions == nil {
		j.data.DeployedVersions = map[string]string{}
	}
	j.data.DeployedVersions[targetID] = version
	return j.saveLocked()
}

func (j *Journal) DeployedVersions() map[string]string {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := make(map[string]string, len(j.data.DeployedVersions))
	for target, version := range j.data.DeployedVersions {
		result[target] = version
	}
	return result
}

func (j *Journal) SetActive(job *UpdateJob) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job == nil || job.validateOperationUnion() != nil {
		return errors.New("active update job operation is invalid")
	}
	copy := *job
	if job.PortReconfigure != nil {
		portCopy := *job.PortReconfigure
		portCopy.Docker = cloneDockerPortMutationGrantBinding(job.PortReconfigure.Docker)
		copy.PortReconfigure = &portCopy
	}
	copy.LeaseToken = ""
	copy.ReleaseToken = ""
	if j.data.ActiveJob == nil ||
		j.data.ActiveJob.ID != copy.ID ||
		j.data.ActiveJob.EffectiveOperation() != copy.EffectiveOperation() {
		j.data.ActivePlan = nil
		j.data.ActivePortPlan = nil
	}
	j.data.ActiveJob = &copy
	j.data.NextSeq = job.ReportSequence
	if j.data.NextSeq == 0 {
		j.data.NextSeq = job.Sequence + 1
	}
	if j.data.NextSeq == 0 {
		j.data.NextSeq = 1
	}
	return j.saveLocked()
}

func (j *Journal) SetActivePlan(plan RemotePlan) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.data.ActiveJob == nil ||
		j.data.ActiveJob.EffectiveOperation() != updateJobOperationSoftwareUpdate ||
		j.data.ActiveJob.ID != plan.JobID ||
		plan.Validate() != nil {
		return errors.New("active update plan does not match the journal job")
	}
	copy := plan
	j.data.ActivePlan = &copy
	return j.saveLocked()
}

func (j *Journal) SetActivePortPlan(plan SystemdPortReconfigurePlan) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.data.ActiveJob == nil ||
		j.data.ActiveJob.EffectiveOperation() != updateJobOperationPortReconfigure ||
		j.data.ActiveJob.ID != plan.JobID ||
		plan.Validate() != nil {
		return errors.New("active port reconfiguration plan does not match the journal job")
	}
	copy := plan
	copy.Docker = cloneDockerPortMutationGrantBinding(plan.Docker)
	j.data.ActivePortPlan = &copy
	return j.saveLocked()
}

func (j *Journal) ActivePlan() *RemotePlan {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.data.ActivePlan == nil {
		return nil
	}
	copy := *j.data.ActivePlan
	return &copy
}

func (j *Journal) ActivePortPlan() *SystemdPortReconfigurePlan {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.data.ActivePortPlan == nil {
		return nil
	}
	copy := *j.data.ActivePortPlan
	copy.Docker = cloneDockerPortMutationGrantBinding(j.data.ActivePortPlan.Docker)
	return &copy
}

func (j *Journal) Active() *UpdateJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.data.ActiveJob == nil {
		return nil
	}
	copy := *j.data.ActiveJob
	if j.data.ActiveJob.PortReconfigure != nil {
		portCopy := *j.data.ActiveJob.PortReconfigure
		portCopy.Docker = cloneDockerPortMutationGrantBinding(j.data.ActiveJob.PortReconfigure.Docker)
		copy.PortReconfigure = &portCopy
	}
	return &copy
}

func (j *Journal) Queue(jobID, serviceID, leaseToken string, leaseGeneration uint64, status, code, message string, progress int, artifact, previous string) (JobReport, error) {
	return j.queue(jobID, serviceID, leaseToken, leaseGeneration, status, code, message, progress, artifact, previous, nil)
}

func (j *Journal) QueuePort(
	jobID, serviceID, leaseToken string,
	leaseGeneration uint64,
	status, code, message string,
	progress int,
	result *SystemdPortReconfigureResult,
) (JobReport, error) {
	return j.queue(jobID, serviceID, leaseToken, leaseGeneration, status, code, message, progress, "", "", result)
}

func (j *Journal) queue(
	jobID, serviceID, leaseToken string,
	leaseGeneration uint64,
	status, code, message string,
	progress int,
	artifact, previous string,
	portResult *SystemdPortReconfigureResult,
) (JobReport, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var resultCopy *PortReconfigurationJobReport
	if portResult != nil {
		resultCopy = &PortReconfigurationJobReport{Result: portResult.Result}
	}
	report := JobReport{ServiceID: serviceID, LeaseToken: leaseToken, LeaseGeneration: leaseGeneration, Sequence: j.data.NextSeq, Status: status, Progress: progress, Code: code, Message: message, ArtifactDigest: artifact, PreviousDigest: previous, PortReconfigure: resultCopy}
	j.data.NextSeq++
	stored := report
	stored.LeaseToken = ""
	j.data.Pending = append(j.data.Pending, PendingReport{JobID: jobID, Report: stored})
	j.leaseTokens[pendingLeaseKey(jobID, report.Sequence)] = leaseToken
	return report, j.saveLocked()
}

func (j *Journal) DropJobReports(jobID string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	kept := j.data.Pending[:0]
	for _, pending := range j.data.Pending {
		if pending.JobID != jobID {
			kept = append(kept, pending)
		} else {
			delete(j.leaseTokens, pendingLeaseKey(pending.JobID, pending.Report.Sequence))
		}
	}
	j.data.Pending = append([]PendingReport(nil), kept...)
	return j.saveLocked()
}

func (j *Journal) Pending() []PendingReport {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := append([]PendingReport(nil), j.data.Pending...)
	for index := range result {
		pending := &result[index]
		pending.Report.LeaseToken = j.leaseTokens[pendingLeaseKey(pending.JobID, pending.Report.Sequence)]
	}
	return result
}

func (j *Journal) Ack(jobID string, sequence uint64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.data.Pending) == 0 || j.data.Pending[0].JobID != jobID || j.data.Pending[0].Report.Sequence != sequence {
		return errors.New("journal acknowledgements must be ordered")
	}
	acked := j.data.Pending[0]
	delete(j.leaseTokens, pendingLeaseKey(acked.JobID, acked.Report.Sequence))
	j.data.Pending = append([]PendingReport(nil), j.data.Pending[1:]...)
	if (acked.Report.Status == "succeeded" || acked.Report.Status == "rolled_back" || acked.Report.Status == "failed") && j.data.ActiveJob != nil && j.data.ActiveJob.ID == jobID {
		j.data.ActiveJob = nil
		j.data.ActivePlan = nil
		j.data.ActivePortPlan = nil
	}
	return j.saveLocked()
}

func pendingLeaseKey(jobID string, sequence uint64) string {
	return fmt.Sprintf("%s:%d", jobID, sequence)
}

func (j *Journal) ClearActive() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.data.ActiveJob = nil
	j.data.ActivePlan = nil
	j.data.ActivePortPlan = nil
	return j.saveLocked()
}

func (j *Journal) saveLocked() error {
	tmp := j.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encErr := json.NewEncoder(f).Encode(j.data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if encErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		return errors.New("persist update journal")
	}
	if err := os.Rename(tmp, j.path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(j.path))
}
