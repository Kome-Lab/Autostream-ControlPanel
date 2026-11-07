package database

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestNodeSelectionActivationRequiresExactInstallingJob(t *testing.T) {
	valid := nodeSelectionActivationJob{ID: "job", TargetID: "control-panel", TargetType: "control_panel", Operation: "software_update", TargetVersion: "v2.0.7", CurrentVersion: "v2.0.6", Status: "installing", AgentID: "agent", HostID: "host", Transport: "pull_v2", Epoch: 7}
	if !nodeSelectionActivationInstallingJob(valid, "v2.0.7") {
		t.Fatal("valid installing update rejected")
	}
	for name, change := range map[string]func(*nodeSelectionActivationJob){
		"wrong target":            func(j *nodeSelectionActivationJob) { j.TargetID = "worker" },
		"wrong type":              func(j *nodeSelectionActivationJob) { j.TargetType = "worker" },
		"port operation":          func(j *nodeSelectionActivationJob) { j.Operation = "port_reconfigure" },
		"other version":           func(j *nodeSelectionActivationJob) { j.TargetVersion = "v2.0.8" },
		"missing host":            func(j *nodeSelectionActivationJob) { j.HostID = "" },
		"missing updater":         func(j *nodeSelectionActivationJob) { j.AgentID = "" },
		"zero epoch":              func(j *nodeSelectionActivationJob) { j.Epoch = 0 },
		"legacy transport":        func(j *nodeSelectionActivationJob) { j.Transport = "ssh_v1" },
		"queued":                  func(j *nodeSelectionActivationJob) { j.Status = "queued" },
		"failed":                  func(j *nodeSelectionActivationJob) { j.Status = "failed" },
		"rolled back":             func(j *nodeSelectionActivationJob) { j.Status = "rolled_back" },
		"succeeded is not active": func(j *nodeSelectionActivationJob) { j.Status = "succeeded" },
	} {
		t.Run(name, func(t *testing.T) {
			j := valid
			change(&j)
			if nodeSelectionActivationInstallingJob(j, "v2.0.7") {
				t.Fatal("unsafe installing update accepted")
			}
		})
	}
	binding := nodeSelectionActivationBinding{JobID: "job", TargetVersion: "v2.0.7", AgentID: "agent", HostID: "host", Epoch: 7, CurrentVersion: "v2.0.6"}
	if !nodeSelectionActivationJobMatches(valid, binding) {
		t.Fatal("original binding rejected")
	}
	for name, change := range map[string]func(*nodeSelectionActivationJob){
		"replacement job":    func(j *nodeSelectionActivationJob) { j.ID = "replacement" },
		"different updater":  func(j *nodeSelectionActivationJob) { j.AgentID = "another" },
		"different host":     func(j *nodeSelectionActivationJob) { j.HostID = "another" },
		"different epoch":    func(j *nodeSelectionActivationJob) { j.Epoch++ },
		"different baseline": func(j *nodeSelectionActivationJob) { j.CurrentVersion = "v2.0.5" },
	} {
		t.Run(name, func(t *testing.T) {
			j := valid
			change(&j)
			if nodeSelectionActivationJobMatches(j, binding) {
				t.Fatal("changed update binding accepted")
			}
		})
	}
}

func TestNodeSelectionActivationRequiresDurableTerminalProof(t *testing.T) {
	valid := nodeSelectionActivationJob{Status: "succeeded", CompletedAt: sql.NullTime{Time: time.Now(), Valid: true}, ArtifactDigest: "sha256:" + strings.Repeat("a", 64), PreviousDigest: strings.Repeat("b", 64)}
	if !nodeSelectionActivationTerminalProof(valid) {
		t.Fatal("complete terminal proof rejected")
	}
	for name, change := range map[string]func(*nodeSelectionActivationJob){
		"missing completion":  func(j *nodeSelectionActivationJob) { j.CompletedAt = sql.NullTime{} },
		"zero completion":     func(j *nodeSelectionActivationJob) { j.CompletedAt.Time = time.Time{} },
		"missing artifact":    func(j *nodeSelectionActivationJob) { j.ArtifactDigest = "" },
		"invalid artifact":    func(j *nodeSelectionActivationJob) { j.ArtifactDigest = strings.Repeat("z", 64) },
		"nonterminal outcome": func(j *nodeSelectionActivationJob) { j.Status = "installing" },
	} {
		t.Run(name, func(t *testing.T) {
			j := valid
			change(&j)
			if nodeSelectionActivationTerminalProof(j) {
				t.Fatal("incomplete terminal proof accepted")
			}
		})
	}
}
