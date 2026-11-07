package database

import (
	"database/sql"
	"strings"
)

type nodeSelectionActivationBinding struct {
	JobID, TargetVersion, BuildCommit, AgentID, HostID, CurrentVersion string
	Epoch                                                              int64
}

type nodeSelectionActivationJob struct {
	ID, TargetID, TargetType, Operation, TargetVersion, Status                 string
	AgentID, HostID, Transport, CurrentVersion, ArtifactDigest, PreviousDigest string
	Epoch                                                                      int64
	CompletedAt                                                                sql.NullTime
}

func nodeSelectionActivationInstallingJob(j nodeSelectionActivationJob, runningVersion string) bool {
	if j.ID == "" || j.TargetID != "control-panel" || j.TargetType != "control_panel" || j.Operation != "software_update" || j.TargetVersion != runningVersion || j.AgentID == "" || j.HostID == "" || j.Epoch < 1 || j.Transport != "pull_v2" {
		return false
	}
	switch j.Status {
	case "stopping", "installing", "starting", "health_checking", "reconciling":
		return true
	}
	return false
}

func nodeSelectionActivationJobMatches(j nodeSelectionActivationJob, b nodeSelectionActivationBinding) bool {
	return j.ID == b.JobID && j.TargetID == "control-panel" && j.TargetType == "control_panel" && j.Operation == "software_update" && j.TargetVersion == b.TargetVersion && j.AgentID == b.AgentID && j.HostID == b.HostID && j.Epoch == b.Epoch && j.Transport == "pull_v2" && j.CurrentVersion == b.CurrentVersion
}

func nodeSelectionActivationTerminalProof(j nodeSelectionActivationJob) bool {
	validDigest := func(value string) bool {
		value = strings.TrimPrefix(value, "sha256:")
		if len(value) != 64 {
			return false
		}
		for _, c := range value {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
		return true
	}
	return j.CompletedAt.Valid && !j.CompletedAt.Time.IsZero() && validDigest(j.ArtifactDigest) && validDigest(j.PreviousDigest)
}
