package updateagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type acceptingTransientRunner struct{}

func (acceptingTransientRunner) Run(context.Context, string, []string, string, ...string) (string, error) {
	return "", nil
}

type linkingErrorTransientRunner struct {
	capturePath string
}

func (r linkingErrorTransientRunner) Run(_ context.Context, _ string, _ []string, _ string, args ...string) (string, error) {
	for index := range args {
		if args[index] == "--request" && index+1 < len(args) {
			if err := os.Link(args[index+1], r.capturePath); err != nil {
				return "", err
			}
			return "", errors.New("systemd-run result unknown")
		}
	}
	return "", errors.New("request argument missing")
}

func TestTerminalLedgerReplayRequiresAuthorizedApplyOrReconcile(t *testing.T) {
	plan := validRemotePlan()
	plan.ConfigSHA256 = "sha256:" + strings.Repeat("c", 64)
	plan.LeaseGeneration = 4
	plan.SessionID = "session-terminal-original-04"
	plan.PlanSHA256, _ = plan.ComputePlanSHA256()
	result := ApplyResult{Status: "succeeded", ArtifactDigest: normalizeDigest(plan.ResultArtifactDigest())}
	ledger := remoteMutationLedger{
		SchemaVersion: remoteLedgerSchemaVersion, JobID: plan.JobID, TargetID: plan.TargetID,
		PlanSHA256: plan.PlanSHA256, SessionID: plan.SessionID, LeaseGeneration: plan.LeaseGeneration,
		Intent: newRemoteMutationIntent(plan), Operation: "apply", State: remoteLedgerTerminal,
		Stage:  &remoteStage{RootDir: filepath.Join(t.TempDir(), "stage"), ArtifactDigest: plan.ArtifactDigest},
		Result: &result,
	}

	exactApply := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "apply", Plan: &plan, MutationGrant: NewRemoteSecret("grant")}
	if response, ok := remoteTerminalLedgerOutcome(ledger, exactApply); !ok || response.Result == nil {
		t.Fatalf("exact apply did not replay terminal result: response=%#v ok=%v", response, ok)
	}

	cases := []struct {
		name      string
		operation string
		plan      RemotePlan
		want      bool
	}{
		{name: "stage operation", operation: "stage", plan: plan},
		{name: "apply different session", operation: "apply", plan: func() RemotePlan { p := plan; p.SessionID = "session-terminal-other-04"; return p }()},
		{name: "apply different generation", operation: "apply", plan: func() RemotePlan { p := plan; p.LeaseGeneration++; return p }()},
		{name: "stale reconcile", operation: "reconcile", plan: func() RemotePlan {
			p := plan
			p.LeaseGeneration--
			p.SessionID = "session-terminal-stale-03"
			p.PlanSHA256, _ = p.ComputePlanSHA256()
			return p
		}()},
		{name: "different job reconcile", operation: "reconcile", plan: func() RemotePlan {
			p := plan
			p.JobID = "job-terminal-other"
			p.PlanSHA256, _ = p.ComputePlanSHA256()
			return p
		}()},
		{name: "fresh reconcile", operation: "reconcile", plan: func() RemotePlan {
			p := plan
			p.LeaseGeneration++
			p.SessionID = "session-terminal-fresh-05"
			p.PlanSHA256, _ = p.ComputePlanSHA256()
			return p
		}(), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: tc.operation, Plan: &tc.plan}
			response, ok := remoteTerminalLedgerOutcome(ledger, request)
			if ok != tc.want {
				t.Fatalf("terminal replay authorization = %v, response=%#v", ok, response)
			}
			if tc.want && (response.Result == nil || response.SessionID != tc.plan.SessionID || response.PlanSHA256 != tc.plan.PlanSHA256) {
				t.Fatalf("fresh reconcile result is not request-bound: %#v", response)
			}
		})
	}
}

func TestTransientReturnRemovesUnconsumedMutationGrantFile(t *testing.T) {
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned transient request policy")
	}
	cfg := validHelperTestConfig(t)
	for _, dir := range []string{cfg.StateDir, filepath.Join(cfg.StateDir, "requests"), filepath.Join(cfg.StateDir, "results"), filepath.Join(cfg.StateDir, "ledger")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	plan := validRemotePlan()
	plan.ConfigSHA256 = "sha256:" + strings.Repeat("c", 64)
	plan.PlanSHA256, _ = plan.ComputePlanSHA256()
	secret := "mutation-grant-must-not-survive-return"
	request := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "apply", Plan: &plan, MutationGrant: NewRemoteSecret(secret)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := launchTransientRemoteHelperRequest(ctx, cfg, "/etc/autostream/update-host.json", request, remoteHelperRuntime{runner: acceptingTransientRunner{}}, "/usr/local/libexec/autostream-update-host", "/usr/bin/systemd-run", remoteWorkerResultPath(cfg, plan.SessionID))
	if response.Error == nil || response.Error.Code != "operation_continues" {
		if response.Error == nil {
			t.Fatalf("canceled accepted launch had no failure: %#v", response)
		}
		t.Fatalf("canceled accepted launch code = %q", response.Error.Code)
	}
	entries, err := os.ReadDir(filepath.Join(cfg.StateDir, "requests"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unconsumed request survived transient return: %#v", entries)
	}
	assertStateTreeDoesNotContain(t, cfg.StateDir, secret)
}

func TestLauncherErrorWipesMutationGrantBeforeUnlink(t *testing.T) {
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned transient request policy")
	}
	cfg := validHelperTestConfig(t)
	for _, dir := range []string{cfg.StateDir, filepath.Join(cfg.StateDir, "requests"), filepath.Join(cfg.StateDir, "results"), filepath.Join(cfg.StateDir, "ledger")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	plan := validRemotePlan()
	secret := "launcher-error-grant-must-be-wiped"
	request := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "apply", Plan: &plan, MutationGrant: NewRemoteSecret(secret)}
	result := ApplyResult{Status: "succeeded", ArtifactDigest: normalizeDigest(plan.ResultArtifactDigest())}
	resultPath := remoteWorkerResultPath(cfg, plan.SessionID)
	envelope := remoteWorkerResult{
		SchemaVersion: 1, JobID: plan.JobID, PlanSHA256: plan.PlanSHA256,
		SessionID: plan.SessionID, Operation: request.Operation,
		Response: RemoteRPCResponse{Version: RemoteProtocolVersion, Result: &result, SessionID: plan.SessionID, PlanSHA256: plan.PlanSHA256},
	}
	if err := writeRemoteWorkerResult(cfg, resultPath, envelope); err != nil {
		t.Fatal(err)
	}
	capturePath := filepath.Join(cfg.StateDir, "captured-request-inode")
	response := launchTransientRemoteHelperRequest(context.Background(), cfg, "/etc/autostream/update-host.json", request, remoteHelperRuntime{runner: linkingErrorTransientRunner{capturePath: capturePath}}, "/usr/local/libexec/autostream-update-host", "/usr/bin/systemd-run", resultPath)
	if response.Error == nil || response.Error.Code != "operation_continues" {
		t.Fatalf("launcher ambiguity response = %#v", response)
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(captured, []byte(secret)) {
		t.Fatal("launcher error unlinked the grant request without wiping its inode")
	}
}

func TestWipeRemoteWorkerRequestZerosHardLinkedInode(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request")
	capturePath := filepath.Join(dir, "capture")
	secret := []byte("mutation-grant-must-be-zeroed")
	payload := append([]byte(`{"grant":"`), secret...)
	payload = append(payload, []byte(`","suffix":"keep-length"}`)...)
	if len(payload) == 0 || len(payload) > RemoteProtocolMaxFrameBytes {
		t.Fatal("invalid test payload")
	}
	if err := os.WriteFile(requestPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(requestPath, capturePath); err != nil {
		t.Fatal(err)
	}
	linked, err := os.Lstat(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(expected, linked) {
		t.Fatal("capture is not a hard link to the request inode")
	}

	wipeRemoteWorkerRequest(requestPath, expected)
	if err := os.Remove(requestPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(requestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original request survived unlink: %v", err)
	}
	got, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("wiped inode length = %d, want %d", len(got), len(payload))
	}
	if bytes.Contains(got, secret) {
		t.Fatal("hard-linked inode retained the mutation grant")
	}
	if !bytes.Equal(got, make([]byte, len(payload))) {
		t.Fatal("hard-linked inode was not completely zeroed")
	}
}

func TestWorkerFIFOReadWithoutWriterHasHardDeadlineAndCleansPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix FIFO")
	}
	requestDir := t.TempDir()
	path := filepath.Join(requestDir, "stage-request")
	if err := makeRemoteWorkerFIFO(path, 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := readAndUnlinkRemoteWorkerFIFO(context.Background(), path, requestDir, 150*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("FIFO without writer result = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FIFO reader was not bounded: %v", elapsed)
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("timed-out FIFO survived: %v", statErr)
	}
}

func TestStageFIFOWriteWithStalledReaderHasHardDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix FIFO")
	}
	requestDir := t.TempDir()
	path := filepath.Join(requestDir, "stage-stalled-reader")
	if err := makeRemoteWorkerFIFO(path, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	reader, err := openRemoteWorkerFIFOReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	filler, _, err := openRemoteWorkerFIFOWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer filler.Close()
	fill := make([]byte, 32<<10)
	full := false
	for written := 0; written < 8<<20; {
		n, writeErr := writeRemoteWorkerFIFOChunk(filler, fill)
		written += n
		if retryRemoteWorkerFIFOWrite(writeErr) {
			full = true
			break
		}
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if !full {
		t.Fatal("could not fill the test FIFO without blocking")
	}
	plan := validRemotePlan()
	request := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "stage", Plan: &plan, ReleaseToken: NewRemoteSecret("stalled-reader-release-token")}
	started := time.Now()
	if err := writeRemoteWorkerFIFOWithTimeout(path, request, 150*time.Millisecond); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("stalled FIFO writer result = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stalled FIFO writer was not bounded: %v", elapsed)
	}
}

func TestAgedTransientGCKeepsActiveFreshExcludedAndUnsafePaths(t *testing.T) {
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned transient state policy")
	}
	cfg := validHelperTestConfig(t)
	for _, dir := range []string{cfg.StateDir, filepath.Join(cfg.StateDir, "requests"), filepath.Join(cfg.StateDir, "results"), filepath.Join(cfg.StateDir, "ledger")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	old := now.Add(-remoteMutationWaitLimit - time.Minute)
	makePlan := func(session string) RemotePlan {
		plan := validRemotePlan()
		plan.SessionID = session
		plan.PlanSHA256, _ = plan.ComputePlanSHA256()
		return plan
	}
	writeRequest := func(plan RemotePlan, secret string, mtime time.Time) string {
		t.Helper()
		path, err := writeRemoteWorkerRequest(cfg, RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "apply", Plan: &plan, MutationGrant: NewRemoteSecret(secret)})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return path
	}
	writeResult := func(plan RemotePlan, mtime time.Time) string {
		t.Helper()
		path := remoteWorkerResultPath(cfg, plan.SessionID)
		if err := os.WriteFile(path, []byte("stale-result\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return path
	}

	inactive := makePlan("session-gc-inactive-01")
	inactiveSecret := "aged-inactive-grant-must-be-removed"
	inactiveRequest := writeRequest(inactive, inactiveSecret, old)
	inactiveResult := writeResult(inactive, old)
	active := makePlan("session-gc-active-0002")
	activeRequest := writeRequest(active, "aged-active-grant-must-stay", old)
	activeResult := writeResult(active, old)
	fresh := makePlan("session-gc-fresh-00003")
	freshRequest := writeRequest(fresh, "fresh-grant-must-stay", now)
	excludedPlan := makePlan("session-gc-excluded-04")
	excludedResult := writeResult(excludedPlan, old)

	unknownPath := filepath.Join(cfg.StateDir, "results", "operator-file")
	if err := os.WriteFile(unknownPath, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(unknownPath, old, old); err != nil {
		t.Fatal(err)
	}

	linkTarget := filepath.Join(cfg.StateDir, "link-target")
	linkPath := filepath.Join(cfg.StateDir, "results", "session-"+remoteTransientUnitKey("edge-01", "session-gc-link-000005")+".json")
	linkCreated := false
	if err := os.WriteFile(linkTarget, []byte("keep"), 0o600); err == nil {
		if err := os.Symlink(linkTarget, linkPath); err == nil {
			linkCreated = true
		}
	}

	activeKey := remoteTransientUnitKey(active.HostID, active.SessionID)
	err := cleanupAgedRemoteTransientFilesWithUnitCheck(cfg, now, map[string]struct{}{filepath.Clean(excludedResult): {}}, func(key string) bool {
		return key == activeKey
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{inactiveRequest, inactiveResult} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("aged inactive transient file survived: %s (%v)", removed, err)
		}
	}
	for _, kept := range []string{activeRequest, activeResult, freshRequest, excludedResult, unknownPath} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("protected transient file was removed: %s (%v)", kept, err)
		}
	}
	if linkCreated {
		if info, err := os.Lstat(linkPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("symlink candidate was followed or removed: %v", err)
		}
	}
	assertStateTreeDoesNotContain(t, cfg.StateDir, inactiveSecret)
}

func TestAgedTransientGCDeletionIsBounded(t *testing.T) {
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned transient state policy")
	}
	cfg := validHelperTestConfig(t)
	resultsDir := filepath.Join(cfg.StateDir, "results")
	for _, dir := range []string{cfg.StateDir, resultsDir, filepath.Join(cfg.StateDir, "requests"), filepath.Join(cfg.StateDir, "ledger")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	old := now.Add(-remoteMutationWaitLimit - time.Minute)
	paths := make([]string, 0, remoteTransientGCDeleteLimit+8)
	for i := 0; i < remoteTransientGCDeleteLimit+8; i++ {
		path := remoteWorkerResultPath(cfg, fmt.Sprintf("session-gc-bounded-%04d", i))
		if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if err := cleanupAgedRemoteTransientFilesWithUnitCheck(cfg, now, nil, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, path := range paths {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			removed++
		}
	}
	if removed != remoteTransientGCDeleteLimit {
		t.Fatalf("GC removed %d files, want bounded limit %d", removed, remoteTransientGCDeleteLimit)
	}
}

func TestTransientDirectoryCapPreventsAgedRequestStarvation(t *testing.T) {
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned transient state policy")
	}
	cfg := validHelperTestConfig(t)
	requestsDir := filepath.Join(cfg.StateDir, "requests")
	for _, dir := range []string{cfg.StateDir, requestsDir, filepath.Join(cfg.StateDir, "results"), filepath.Join(cfg.StateDir, "ledger")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < remoteTransientDirectoryLimit; index++ {
		if err := os.WriteFile(filepath.Join(requestsDir, fmt.Sprintf("protected-%03d", index)), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plan := validRemotePlan()
	secret := "aged-request-beyond-protected-page"
	agedPath, err := writeRemoteWorkerRequest(cfg, RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "apply", Plan: &plan, MutationGrant: NewRemoteSecret(secret)})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-remoteMutationWaitLimit - time.Minute)
	if err := os.Chtimes(agedPath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := cleanupAgedRemoteTransientFilesWithUnitCheck(cfg, now, nil, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(agedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aged request beyond protected entries was starved: %v", err)
	}
	if !remoteTransientDirectoriesAtCapacity(cfg) {
		t.Fatal("protected directory population did not fail closed at the bounded limit")
	}
	assertStateTreeDoesNotContain(t, cfg.StateDir, secret)
}

func TestResultGCAndWritersShareOneLockAndPreserveFreshResult(t *testing.T) {
	if RequireRemoteHelperRoot() != nil {
		t.Skip("root-owned transient result lock policy")
	}
	cfg := validHelperTestConfig(t)
	for _, dir := range []string{cfg.StateDir, filepath.Join(cfg.StateDir, "requests"), filepath.Join(cfg.StateDir, "results"), filepath.Join(cfg.StateDir, "ledger")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	plan := validRemotePlan()
	request := RemoteRPCRequest{Version: RemoteProtocolVersion, Operation: "apply", Plan: &plan, MutationGrant: NewRemoteSecret("grant")}
	resultPath := remoteWorkerResultPath(cfg, plan.SessionID)
	if err := os.WriteFile(resultPath, []byte("old-invalid-result\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-remoteMutationWaitLimit - time.Minute)
	if err := os.Chtimes(resultPath, old, old); err != nil {
		t.Fatal(err)
	}
	result := ApplyResult{Status: "succeeded", ArtifactDigest: normalizeDigest(plan.ResultArtifactDigest())}
	envelope := remoteWorkerResult{
		SchemaVersion: 1, JobID: plan.JobID, PlanSHA256: plan.PlanSHA256,
		SessionID: plan.SessionID, Operation: request.Operation,
		Response: RemoteRPCResponse{Version: RemoteProtocolVersion, Result: &result, SessionID: plan.SessionID, PlanSHA256: plan.PlanSHA256},
	}
	gcHasLock := make(chan struct{})
	releaseGC := make(chan struct{})
	gcDone := make(chan error, 1)
	checks := 0
	go func() {
		gcDone <- cleanupAgedRemoteTransientFilesWithUnitCheck(cfg, now, nil, func(string) bool {
			checks++
			if checks == 2 {
				close(gcHasLock)
				<-releaseGC
			}
			return false
		})
	}()
	select {
	case <-gcHasLock:
	case <-time.After(time.Second):
		t.Fatal("GC did not reach its under-lock identity recheck")
	}
	writeDone := make(chan error, 1)
	go func() { writeDone <- writeRemoteWorkerResult(cfg, resultPath, envelope) }()
	select {
	case err := <-writeDone:
		t.Fatalf("result writer bypassed the GC lock: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(releaseGC)
	if err := <-gcDone; err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if response, ok := readRemoteWorkerResult(cfg, resultPath, request, false); !ok || response.Result == nil {
		t.Fatalf("fresh result was deleted after GC race: response=%#v ok=%v", response, ok)
	}

	second := plan
	second.SessionID = "session-result-lock-0002"
	second.PlanSHA256, _ = second.ComputePlanSHA256()
	secondResultPath := remoteWorkerResultPath(cfg, second.SessionID)
	secondEnvelope := envelope
	secondEnvelope.PlanSHA256 = second.PlanSHA256
	secondEnvelope.SessionID = second.SessionID
	secondEnvelope.Response.SessionID = second.SessionID
	secondEnvelope.Response.PlanSHA256 = second.PlanSHA256
	if err := writeRemoteWorkerResult(cfg, secondResultPath, secondEnvelope); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(cfg.StateDir, "ledger"))
	if err != nil {
		t.Fatal(err)
	}
	locks := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".transient-result") && strings.HasSuffix(entry.Name(), ".lock") {
			locks++
		}
	}
	if locks != 1 {
		t.Fatalf("result lock files grew with sessions: %d", locks)
	}
}
