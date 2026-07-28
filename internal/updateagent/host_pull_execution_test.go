package updateagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type hostPullExecutionTestPanel struct {
	job          *UpdateJob
	claimHostIDs []string
	reports      []JobReport
	grants       []MutationGrantRequest
	grantErrors  []error
}

func (*hostPullExecutionTestPanel) RegisterHostAgent(context.Context, Config, map[string]any) (HostAgentBinding, error) {
	return HostAgentBinding{}, errors.New("not used")
}
func (*hostPullExecutionTestPanel) HeartbeatHostAgent(context.Context, Config, string, map[string]any) error {
	return errors.New("not used")
}
func (*hostPullExecutionTestPanel) FetchHostAgentPolicy(context.Context, string, int64) (*HostAgentPolicy, bool, error) {
	return nil, false, errors.New("not used")
}
func (p *hostPullExecutionTestPanel) ClaimHost(_ context.Context, _, hostID, _ string) (*UpdateJob, bool, error) {
	p.claimHostIDs = append(p.claimHostIDs, hostID)
	if p.job == nil {
		return nil, false, nil
	}
	copy := *p.job
	return &copy, false, nil
}
func (p *hostPullExecutionTestPanel) Report(_ context.Context, _ string, report JobReport) error {
	p.reports = append(p.reports, report)
	return nil
}
func (p *hostPullExecutionTestPanel) IssueMutationGrant(_ context.Context, _ string, request MutationGrantRequest) (MutationGrant, error) {
	p.grants = append(p.grants, request)
	if len(p.grantErrors) > 0 {
		err := p.grantErrors[0]
		p.grantErrors = p.grantErrors[1:]
		if err != nil {
			return MutationGrant{}, err
		}
	}
	return MutationGrant{Token: "ast_mutation_" + strings.Repeat("a", 43), ExpiresAt: "2099-01-01T00:00:00Z"}, nil
}

type hostPullExecutionTestDownloader struct{}

func (hostPullExecutionTestDownloader) Download(context.Context, string, string, string, string) (DownloadedArtifact, error) {
	return DownloadedArtifact{SHA256: strings.Repeat("a", 64)}, nil
}

type hostPullFailingDownloader struct{}

func (hostPullFailingDownloader) Download(context.Context, string, string, string, string) (DownloadedArtifact, error) {
	return DownloadedArtifact{}, errors.New("release provider unavailable")
}
func (hostPullFailingDownloader) ResolveDockerReleaseForArch(context.Context, string, string, string, string, string, string) (ResolvedDockerRelease, error) {
	return ResolvedDockerRelease{}, errors.New("release provider unavailable")
}
func (hostPullExecutionTestDownloader) ResolveDockerReleaseForArch(context.Context, string, string, string, string, string, string) (ResolvedDockerRelease, error) {
	return ResolvedDockerRelease{}, errors.New("not used")
}

type hostPullExecutionTestExecutor struct {
	stageCalls      int
	applyCalls      int
	reconcileCalls  int
	portApplyCalls  int
	portReconCalls  int
	reconcilePlans  []RemotePlan
	portApplyPlans  []SystemdPortReconfigurePlan
	portReconPlans  []SystemdPortReconfigurePlan
	applyFences     []LocalExecutorMutationFence
	reconcileFences []LocalExecutorMutationFence
	portFences      []LocalExecutorMutationFence
	applyErr        error
	portApplyErr    error
	portReconResult *SystemdPortReconfigureResult
}

func (e *hostPullExecutionTestExecutor) Stage(_ context.Context, plan RemotePlan, _ LocalExecutorMutationFence) (RemoteStageResult, error) {
	e.stageCalls++
	return RemoteStageResult{Status: "staged", SessionID: plan.SessionID, PlanSHA256: plan.PlanSHA256, ArtifactDigest: plan.ArtifactDigest}, nil
}
func (e *hostPullExecutionTestExecutor) Apply(_ context.Context, plan RemotePlan, fence LocalExecutorMutationFence, _ RemoteSecret) (ApplyResult, error) {
	e.applyCalls++
	e.applyFences = append(e.applyFences, fence)
	if e.applyErr != nil {
		return ApplyResult{}, e.applyErr
	}
	return ApplyResult{Status: "succeeded", ArtifactDigest: plan.ResultArtifactDigest()}, nil
}
func (e *hostPullExecutionTestExecutor) Reconcile(_ context.Context, plan RemotePlan, fence LocalExecutorMutationFence, _ RemoteSecret) (ApplyResult, error) {
	e.reconcileCalls++
	e.reconcilePlans = append(e.reconcilePlans, plan)
	e.reconcileFences = append(e.reconcileFences, fence)
	return ApplyResult{Status: "succeeded", ArtifactDigest: plan.ResultArtifactDigest()}, nil
}

func (e *hostPullExecutionTestExecutor) PortReconfigure(_ context.Context, plan SystemdPortReconfigurePlan, fence LocalExecutorMutationFence, _ RemoteSecret) (SystemdPortReconfigureResult, error) {
	e.portApplyCalls++
	e.portApplyPlans = append(e.portApplyPlans, plan)
	e.portFences = append(e.portFences, fence)
	if e.portApplyErr != nil {
		return SystemdPortReconfigureResult{}, e.portApplyErr
	}
	return appliedPortExecutionResult(plan), nil
}

func (e *hostPullExecutionTestExecutor) PortReconfigureReconcile(_ context.Context, plan SystemdPortReconfigurePlan, fence LocalExecutorMutationFence, _ RemoteSecret) (SystemdPortReconfigureResult, error) {
	e.portReconCalls++
	e.portReconPlans = append(e.portReconPlans, plan)
	e.portFences = append(e.portFences, fence)
	if e.portReconResult != nil {
		return *e.portReconResult, nil
	}
	return appliedPortExecutionResult(plan), nil
}

func appliedPortExecutionResult(plan SystemdPortReconfigurePlan) SystemdPortReconfigureResult {
	return SystemdPortReconfigureResult{
		Status: "succeeded", Result: systemdPortResultApplied, StateKnown: true,
		OldPort: plan.OldPort, NewPort: plan.NewPort, AppliedPort: plan.NewPort,
		EndpointRevision: plan.TargetEndpointRevision,
		ConfigRevision:   plan.TargetConfigRevision,
		ConfigSHA256:     plan.TargetConfigSHA256,
		Message:          "requested systemd port is running and verified",
	}
}

func unchangedPortExecutionResult(plan SystemdPortReconfigurePlan) SystemdPortReconfigureResult {
	return SystemdPortReconfigureResult{
		Status: "succeeded", Result: systemdPortResultUnchanged, StateKnown: true,
		OldPort: plan.OldPort, NewPort: plan.NewPort, AppliedPort: plan.OldPort,
		EndpointRevision: plan.TargetEndpointRevision + 1,
		ConfigRevision:   plan.ExpectedConfigRevision,
		ConfigSHA256:     plan.ExpectedConfigSHA256,
		Message:          "systemd port mutation had not changed the verified previous state",
	}
}

func dockerPortExecutionResultForTest(
	plan SystemdPortReconfigurePlan,
	resultKind string,
) SystemdPortReconfigureResult {
	result := SystemdPortReconfigureResult{
		DeploymentMode: ModeDocker,
		OldPort:        plan.OldPort,
		NewPort:        plan.NewPort,
	}
	var publishedPort, containerPort, healthPort int
	switch resultKind {
	case systemdPortResultApplied:
		result.Status = "succeeded"
		result.Result = systemdPortResultApplied
		result.StateKnown = true
		result.AppliedPort = plan.NewPort
		result.EndpointRevision = plan.TargetEndpointRevision
		result.ConfigRevision = plan.TargetConfigRevision
		result.ConfigSHA256 = plan.TargetConfigSHA256
		result.Message = "requested Docker port mapping is running and verified"
		publishedPort = plan.Docker.NewPublishedPort
		containerPort = plan.Docker.NewContainerPort
		healthPort = plan.Docker.NewHealthPort
	case systemdPortResultRolledBack:
		result.Status = "rolled_back"
		result.Result = systemdPortResultRolledBack
		result.StateKnown = true
		result.AppliedPort = plan.OldPort
		result.EndpointRevision = plan.TargetEndpointRevision + 1
		result.ConfigRevision = plan.ExpectedConfigRevision
		result.ConfigSHA256 = plan.ExpectedConfigSHA256
		result.Message = "previous Docker port mapping was restored and verified"
		publishedPort = plan.Docker.OldPublishedPort
		containerPort = plan.Docker.OldContainerPort
		healthPort = plan.Docker.OldHealthPort
	case systemdPortResultUnchanged:
		result.Status = "succeeded"
		result.Result = systemdPortResultUnchanged
		result.StateKnown = true
		result.AppliedPort = plan.OldPort
		result.EndpointRevision = plan.TargetEndpointRevision + 1
		result.ConfigRevision = plan.ExpectedConfigRevision
		result.ConfigSHA256 = plan.ExpectedConfigSHA256
		result.Message = "Docker port mutation did not change the verified mapping"
		publishedPort = plan.Docker.OldPublishedPort
		containerPort = plan.Docker.OldContainerPort
		healthPort = plan.Docker.OldHealthPort
	default:
		panic("unsupported Docker port result kind")
	}
	result.Docker = &DockerPortReconfigureResultState{
		AppliedPublishedPort: publishedPort,
		AppliedContainerPort: containerPort,
		AppliedHealthPort:    healthPort,
		ComposeConfigSHA256:  strings.Repeat("c", 64),
	}
	return result
}

func TestValidatePortExecutionResultBindsDockerMappingToImmutablePlan(t *testing.T) {
	plan := newDockerPortHarness(t).plan
	t.Run("deployment_mode_spoof", func(t *testing.T) {
		result := appliedPortExecutionResult(plan)
		if err := result.Validate(); err != nil {
			t.Fatalf("systemd-mode spoof fixture must remain structurally valid: %v", err)
		}
		if err := validatePortExecutionResult(plan, result); err == nil {
			t.Fatal("systemd-mode response for an immutable Docker plan was accepted")
		}
	})
	for _, resultKind := range []string{
		systemdPortResultApplied,
		systemdPortResultRolledBack,
		systemdPortResultUnchanged,
	} {
		t.Run(resultKind+"_exact", func(t *testing.T) {
			result := dockerPortExecutionResultForTest(plan, resultKind)
			if err := validatePortExecutionResult(plan, result); err != nil {
				t.Fatalf("exact Docker result rejected: %v", err)
			}
		})
		t.Run(resultKind+"_container_spoof", func(t *testing.T) {
			result := dockerPortExecutionResultForTest(plan, resultKind)
			result.Docker.AppliedContainerPort++
			if err := result.Validate(); err != nil {
				t.Fatalf("spoof fixture must remain structurally valid: %v", err)
			}
			if err := validatePortExecutionResult(plan, result); err == nil {
				t.Fatal("structurally valid Docker container-port spoof was accepted")
			}
		})
		t.Run(resultKind+"_published_spoof", func(t *testing.T) {
			result := dockerPortExecutionResultForTest(plan, resultKind)
			result.Docker.AppliedPublishedPort++
			result.Docker.AppliedHealthPort++
			if err := result.Validate(); err != nil {
				t.Fatalf("spoof fixture must remain structurally valid: %v", err)
			}
			if err := validatePortExecutionResult(plan, result); err == nil {
				t.Fatal("structurally valid Docker published-port spoof was accepted")
			}
		})
	}
}

func TestHostPullExecutionClaimsServerOwnedHostAndCompletesThroughLocalExecutor(t *testing.T) {
	agent, panel, executor, binding, policy := newHostPullExecutionHarness(t, false)
	if err := agent.executeOnce(context.Background(), binding, policy); err != nil {
		t.Fatalf("executeOnce: %v", err)
	}
	if len(panel.claimHostIDs) != 1 || panel.claimHostIDs[0] != "" {
		t.Fatalf("claim host ids=%v", panel.claimHostIDs)
	}
	if executor.stageCalls != 1 || executor.applyCalls != 1 || executor.reconcileCalls != 0 {
		t.Fatalf("executor calls stage=%d apply=%d reconcile=%d", executor.stageCalls, executor.applyCalls, executor.reconcileCalls)
	}
	if len(executor.applyFences) != 1 || executor.applyFences[0].SourcePolicyRevision != policy.SourcePolicyRevision {
		t.Fatalf("apply fences=%+v policy source revision=%d", executor.applyFences, policy.SourcePolicyRevision)
	}
	if len(panel.grants) != 1 {
		t.Fatalf("grants=%d", len(panel.grants))
	}
	grant := panel.grants[0]
	if grant.TransportMode != HostTransportPullV2 ||
		grant.OwnershipEpoch != binding.OwnershipEpoch ||
		grant.PolicyRevision != policy.Revision ||
		grant.HostID != binding.ExecutionHostID ||
		grant.ServiceType != panel.job.EffectiveType() ||
		grant.Operation != "apply" {
		t.Fatalf("grant=%+v", grant)
	}
	if len(panel.reports) == 0 || panel.reports[len(panel.reports)-1].Status != "succeeded" {
		t.Fatalf("reports=%+v", panel.reports)
	}
	if active := agent.Journal.Active(); active != nil {
		t.Fatalf("terminal report left active job: %+v", active)
	}
	payload, err := os.ReadFile(filepath.Join(agent.StateDir, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "ast_mutation_") ||
		strings.Contains(string(payload), strings.Repeat("l", 48)) {
		t.Fatalf("journal persisted a bearer secret: %s", payload)
	}
}

func TestHostPullJournalRejectsTamperedDurablePlan(t *testing.T) {
	agent, panel, _, _, policy := newHostPullExecutionHarness(t, false)
	job := *panel.job
	if err := agent.Journal.SetActive(&job); err != nil {
		t.Fatal(err)
	}
	plan, err := agent.prepareExecutionPlan(context.Background(), policy, job)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Journal.SetActivePlan(plan); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(agent.StateDir, "journal.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data journalData
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatal(err)
	}
	data.ActivePlan.TargetID = "different-target"
	tampered, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJournal(agent.StateDir); err == nil ||
		!strings.Contains(err.Error(), "active plan binding") {
		t.Fatalf("tampered journal error=%v", err)
	}
}

func TestHostPullExecutionUncertainApplyReconcilesWithoutReapplying(t *testing.T) {
	agent, panel, executor, binding, policy := newHostPullExecutionHarness(t, false)
	executor.applyErr = errors.New("UDS result lost")
	if err := agent.executeOnce(context.Background(), binding, policy); err != nil {
		t.Fatalf("executeOnce: %v", err)
	}
	if executor.applyCalls != 1 || executor.reconcileCalls != 1 {
		t.Fatalf("apply=%d reconcile=%d", executor.applyCalls, executor.reconcileCalls)
	}
	if len(executor.reconcileFences) != 1 || executor.reconcileFences[0].SourcePolicyRevision != policy.SourcePolicyRevision {
		t.Fatalf("reconcile fences=%+v policy source revision=%d", executor.reconcileFences, policy.SourcePolicyRevision)
	}
	if len(panel.grants) != 2 ||
		panel.grants[0].Operation != "apply" ||
		panel.grants[1].Operation != "reconcile" {
		t.Fatalf("grants=%+v", panel.grants)
	}
	foundReconciling := false
	for _, report := range panel.reports {
		if report.Status == "reconciling" {
			foundReconciling = true
		}
	}
	if !foundReconciling {
		t.Fatalf("reports=%+v", panel.reports)
	}
}

func TestHostPullClaimRejectsMalformedLeaseCredential(t *testing.T) {
	_, panel, _, binding, policy := newHostPullExecutionHarness(t, false)
	for name, token := range map[string]string{
		"control":  "valid-prefix\nsecret",
		"oversize": strings.Repeat("x", (16<<10)+1),
	} {
		t.Run(name, func(t *testing.T) {
			job := *panel.job
			job.LeaseToken = token
			if err := validateHostPullClaim(job, panel.job.AgentServiceID, binding, policy); err == nil {
				t.Fatal("malformed lease credential was accepted")
			}
		})
	}
}

func TestHostPullRecoveryOnlyReconcilesDurableExecutorState(t *testing.T) {
	agent, panel, executor, binding, policy := newHostPullExecutionHarness(t, true)
	interrupted := *panel.job
	interrupted.RecoveryRequired = false
	if err := agent.Journal.SetActive(&interrupted); err != nil {
		t.Fatal(err)
	}
	plan, err := agent.prepareExecutionPlan(context.Background(), policy, interrupted)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Journal.SetActivePlan(plan); err != nil {
		t.Fatal(err)
	}
	panel.job.LeaseGeneration = interrupted.LeaseGeneration + 1
	agent.Downloader = hostPullFailingDownloader{}
	if err := agent.executeOnce(context.Background(), binding, policy); err != nil {
		t.Fatalf("executeOnce: %v", err)
	}
	if executor.stageCalls != 0 || executor.applyCalls != 0 || executor.reconcileCalls != 1 {
		t.Fatalf("executor calls stage=%d apply=%d reconcile=%d", executor.stageCalls, executor.applyCalls, executor.reconcileCalls)
	}
	if len(executor.reconcileFences) != 1 || executor.reconcileFences[0].SourcePolicyRevision != policy.SourcePolicyRevision {
		t.Fatalf("recovery reconcile fences=%+v policy source revision=%d", executor.reconcileFences, policy.SourcePolicyRevision)
	}
	if len(panel.grants) != 1 || panel.grants[0].Operation != "reconcile" {
		t.Fatalf("grants=%+v", panel.grants)
	}
	if len(executor.reconcilePlans) != 1 ||
		executor.reconcilePlans[0].LeaseGeneration != panel.job.LeaseGeneration ||
		panel.grants[0].LeaseGeneration != panel.job.LeaseGeneration {
		t.Fatalf("plan=%+v grant=%+v", executor.reconcilePlans, panel.grants)
	}
}

func TestHostPullPortReconfigureSkipsReleaseAndStagesNoSoftware(t *testing.T) {
	agent, panel, executor, binding, policy := newHostPullPortExecutionHarness(t, false)
	agent.Downloader = hostPullFailingDownloader{}

	if err := agent.executeOnce(context.Background(), binding, policy); err != nil {
		t.Fatalf("executeOnce: %v", err)
	}
	if executor.stageCalls != 0 || executor.applyCalls != 0 || executor.reconcileCalls != 0 {
		t.Fatalf("software executor calls stage=%d apply=%d reconcile=%d", executor.stageCalls, executor.applyCalls, executor.reconcileCalls)
	}
	if executor.portApplyCalls != 1 || executor.portReconCalls != 0 {
		t.Fatalf("port executor calls apply=%d reconcile=%d", executor.portApplyCalls, executor.portReconCalls)
	}
	if len(panel.grants) != 1 {
		t.Fatalf("grants=%+v", panel.grants)
	}
	grant := panel.grants[0]
	intentHash := panel.job.PortReconfigure.PortPlanSHA256
	if grant.JobOperation != updateJobOperationPortReconfigure ||
		grant.Operation != "port_reconfigure" ||
		grant.ServiceType != panel.job.EffectiveType() ||
		grant.PortReconfigure == nil ||
		grant.PlanSHA256 == intentHash ||
		grant.PortReconfigure.PortPlanSHA256 != grant.PlanSHA256 ||
		grant.PlanSHA256 != executor.portApplyPlans[0].PortPlanSHA256 {
		t.Fatalf("grant=%+v intent_hash=%q plan=%+v", grant, intentHash, executor.portApplyPlans)
	}
	if len(executor.portFences) != 1 ||
		executor.portFences[0].SourcePolicyRevision != policy.SourcePolicyRevision ||
		executor.portFences[0].OwnershipPolicyRevision != policy.Revision ||
		executor.portFences[0].ExecutorPolicyRevision != policy.LocalExecutorPolicyRevision {
		t.Fatalf("port fences=%+v", executor.portFences)
	}
	for index, report := range panel.reports {
		terminal := index == len(panel.reports)-1
		if terminal {
			if report.Status != "succeeded" ||
				report.PortReconfigure == nil ||
				report.PortReconfigure.Result != systemdPortResultApplied {
				t.Fatalf("terminal report=%+v", report)
			}
		} else if report.PortReconfigure != nil {
			t.Fatalf("non-terminal report leaked a result: %+v", report)
		}
	}
	if active := agent.Journal.Active(); active != nil {
		t.Fatalf("terminal port report left active job: %+v", active)
	}
	payload, err := os.ReadFile(filepath.Join(agent.StateDir, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "ast_mutation_") ||
		strings.Contains(string(payload), strings.Repeat("l", 48)) {
		t.Fatalf("journal persisted a bearer secret: %s", payload)
	}
}

func TestHostPullPortReconfigureUncertainResultReconcilesWithoutReapply(t *testing.T) {
	agent, panel, executor, binding, policy := newHostPullPortExecutionHarness(t, false)
	executor.portApplyErr = errors.New("UDS result lost")

	if err := agent.executeOnce(context.Background(), binding, policy); err != nil {
		t.Fatalf("executeOnce: %v", err)
	}
	if executor.portApplyCalls != 1 || executor.portReconCalls != 1 {
		t.Fatalf("port calls apply=%d reconcile=%d", executor.portApplyCalls, executor.portReconCalls)
	}
	if len(panel.grants) != 2 ||
		panel.grants[0].Operation != "port_reconfigure" ||
		panel.grants[1].Operation != "port_reconfigure_reconcile" {
		t.Fatalf("grants=%+v", panel.grants)
	}
	if panel.grants[0].PlanSHA256 != panel.grants[1].PlanSHA256 ||
		panel.grants[0].SessionID != panel.grants[1].SessionID {
		t.Fatalf("apply and reconcile grants changed runtime intent: %+v", panel.grants)
	}
	foundReconciling := false
	for _, report := range panel.reports {
		if report.Status == "reconciling" {
			foundReconciling = true
		}
	}
	if !foundReconciling {
		t.Fatalf("reports=%+v", panel.reports)
	}
}

func TestHostPullPortGrantResponseFailureReconcilesUnstartedMutationAsUnchanged(t *testing.T) {
	agent, panel, executor, binding, policy := newHostPullPortExecutionHarness(t, false)
	panel.grantErrors = []error{errors.New("grant response lost")}
	plan, err := agent.preparePortExecutionPlan(policy, *panel.job)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := unchangedPortExecutionResult(plan)
	executor.portReconResult = &unchanged

	if err := agent.executeOnce(context.Background(), binding, policy); err != nil {
		t.Fatalf("executeOnce: %v", err)
	}
	if executor.portApplyCalls != 0 || executor.portReconCalls != 1 {
		t.Fatalf(
			"grant failure reached apply or skipped reconcile: apply=%d reconcile=%d",
			executor.portApplyCalls,
			executor.portReconCalls,
		)
	}
	if len(panel.grants) != 2 ||
		panel.grants[0].Operation != "port_reconfigure" ||
		panel.grants[1].Operation != "port_reconfigure_reconcile" ||
		panel.grants[0].PlanSHA256 != panel.grants[1].PlanSHA256 ||
		panel.grants[0].SessionID != panel.grants[1].SessionID {
		t.Fatalf("grants=%+v", panel.grants)
	}
	terminal := panel.reports[len(panel.reports)-1]
	if terminal.Status != "succeeded" ||
		terminal.PortReconfigure == nil ||
		terminal.PortReconfigure.Result != systemdPortResultUnchanged {
		t.Fatalf("terminal report=%+v", terminal)
	}
}

func TestHostPullPortRecoveryRebindsLeaseAndPreservesSessionAndIntent(t *testing.T) {
	agent, panel, executor, binding, policy := newHostPullPortExecutionHarness(t, true)
	interrupted := *panel.job
	interrupted.RecoveryRequired = false
	if err := agent.Journal.SetActive(&interrupted); err != nil {
		t.Fatal(err)
	}
	original, err := agent.preparePortExecutionPlan(policy, interrupted)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Journal.SetActivePortPlan(original); err != nil {
		t.Fatal(err)
	}
	panel.job.LeaseGeneration++
	agent.Downloader = hostPullFailingDownloader{}

	if err := agent.executeOnce(context.Background(), binding, policy); err != nil {
		t.Fatalf("executeOnce: %v", err)
	}
	if executor.portApplyCalls != 0 || executor.portReconCalls != 1 {
		t.Fatalf("port calls apply=%d reconcile=%d", executor.portApplyCalls, executor.portReconCalls)
	}
	rebound := executor.portReconPlans[0]
	if rebound.LeaseGeneration != panel.job.LeaseGeneration ||
		rebound.SessionID != original.SessionID ||
		rebound.PortPlanSHA256 == original.PortPlanSHA256 ||
		panel.job.PortReconfigure.PortPlanSHA256 != interrupted.PortReconfigure.PortPlanSHA256 {
		t.Fatalf("original=%+v rebound=%+v recovered_job=%+v", original, rebound, panel.job)
	}
	if len(panel.grants) != 1 ||
		panel.grants[0].Operation != "port_reconfigure_reconcile" ||
		panel.grants[0].PlanSHA256 != rebound.PortPlanSHA256 {
		t.Fatalf("grants=%+v", panel.grants)
	}
}

func TestHostPullPortRecoveryReconstructsMissingPreMutationPlanAndReconciles(t *testing.T) {
	agent, panel, executor, binding, policy := newHostPullPortExecutionHarness(t, true)
	interrupted := *panel.job
	interrupted.RecoveryRequired = false
	if err := agent.Journal.SetActive(&interrupted); err != nil {
		t.Fatal(err)
	}
	reconstructed, err := agent.preparePortExecutionPlan(policy, *panel.job)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := unchangedPortExecutionResult(reconstructed)
	executor.portReconResult = &unchanged

	if err := agent.executeOnce(context.Background(), binding, policy); err != nil {
		t.Fatalf("executeOnce: %v", err)
	}
	if executor.portApplyCalls != 0 || executor.portReconCalls != 1 ||
		len(panel.grants) != 1 ||
		panel.grants[0].Operation != "port_reconfigure_reconcile" {
		t.Fatalf(
			"recovery calls apply=%d reconcile=%d grants=%+v",
			executor.portApplyCalls,
			executor.portReconCalls,
			panel.grants,
		)
	}
	terminal := panel.reports[len(panel.reports)-1]
	if terminal.Status != "succeeded" ||
		terminal.PortReconfigure == nil ||
		terminal.PortReconfigure.Result != systemdPortResultUnchanged {
		t.Fatalf("terminal=%+v", terminal)
	}
}

func TestHostPullPortPlanGenerationFailureNeverSendsInvalidTerminalResult(t *testing.T) {
	agent, panel, executor, binding, policy := newHostPullPortExecutionHarness(t, false)
	agent.NewSessionID = func() (string, error) {
		return "", errors.New("entropy unavailable")
	}
	if err := agent.executeOnce(context.Background(), binding, policy); err == nil {
		t.Fatal("plan generation failure was hidden")
	}
	for _, report := range panel.reports {
		if isTerminalUpdateStatus(report.Status) {
			t.Fatalf("unverified terminal report was emitted: %+v", report)
		}
	}
	if agent.Journal.Active() == nil || agent.Journal.ActivePortPlan() != nil ||
		executor.portApplyCalls != 0 || executor.portReconCalls != 0 {
		t.Fatalf(
			"failed plan state active=%+v plan=%+v apply=%d reconcile=%d",
			agent.Journal.Active(),
			agent.Journal.ActivePortPlan(),
			executor.portApplyCalls,
			executor.portReconCalls,
		)
	}

	agent.NewSessionID = func() (string, error) {
		return "recovered-session-0123456789", nil
	}
	panel.job.RecoveryRequired = true
	panel.job.LeaseGeneration++
	reconstructed, err := agent.preparePortExecutionPlan(policy, *panel.job)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := unchangedPortExecutionResult(reconstructed)
	executor.portReconResult = &unchanged
	if err := agent.executeOnce(context.Background(), binding, policy); err != nil {
		t.Fatalf("recovery executeOnce: %v", err)
	}
	terminal := panel.reports[len(panel.reports)-1]
	if terminal.PortReconfigure == nil ||
		terminal.PortReconfigure.Result != systemdPortResultUnchanged {
		t.Fatalf("recovery terminal=%+v", terminal)
	}
}

func TestHostPullPortClaimRejectsUnionAndPolicyDrift(t *testing.T) {
	_, panel, _, binding, policy := newHostPullPortExecutionHarness(t, false)
	for name, mutate := range map[string]func(*UpdateJob){
		"software with port plan": func(job *UpdateJob) {
			job.Operation = updateJobOperationSoftwareUpdate
		},
		"port without nested plan": func(job *UpdateJob) {
			job.PortReconfigure = nil
		},
		"intent hash missing": func(job *UpdateJob) {
			job.PortReconfigure.PortPlanSHA256 = ""
		},
		"source policy stale": func(job *UpdateJob) {
			job.PortReconfigure.ExpectedSourcePolicyRevision--
		},
		"executor digest stale": func(job *UpdateJob) {
			job.PortReconfigure.ExpectedExecutorPolicySHA256 = "sha256:" + strings.Repeat("f", 64)
		},
		"old port mismatch": func(job *UpdateJob) {
			job.PortReconfigure.OldPort++
		},
		"new port mismatch": func(job *UpdateJob) {
			job.PortReconfigure.NewPort++
		},
		"software version mixed in": func(job *UpdateJob) {
			job.TargetVersion = "v1.1.0"
		},
	} {
		t.Run(name, func(t *testing.T) {
			job := *panel.job
			nested := *panel.job.PortReconfigure
			job.PortReconfigure = &nested
			mutate(&job)
			if err := validateHostPullClaim(job, panel.job.AgentServiceID, binding, policy); err == nil {
				t.Fatalf("invalid port claim was accepted: %+v", job)
			}
		})
	}
}

func TestHostPullPortJournalRejectsTamperedRuntimePlan(t *testing.T) {
	agent, panel, _, _, policy := newHostPullPortExecutionHarness(t, false)
	job := *panel.job
	if err := agent.Journal.SetActive(&job); err != nil {
		t.Fatal(err)
	}
	plan, err := agent.preparePortExecutionPlan(policy, job)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Journal.SetActivePortPlan(plan); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(agent.StateDir, "journal.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data journalData
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatal(err)
	}
	data.ActivePortPlan.NewPort++
	tampered, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJournal(agent.StateDir); err == nil ||
		!strings.Contains(err.Error(), "active plan binding") {
		t.Fatalf("tampered port journal error=%v", err)
	}
}

func newHostPullExecutionHarness(t *testing.T, recovery bool) (*HostPullAgent, *hostPullExecutionTestPanel, *hostPullExecutionTestExecutor, HostAgentBinding, HostAgentPolicy) {
	t.Helper()
	bootstrap := managedHostAgentBootstrap("https://panel.example.com")
	binding := HostAgentBinding{
		ServiceID: bootstrap.NodeID, ServiceType: ServiceTypeUpdateAgent,
		TransportMode: HostTransportPullV2, ExecutionHostID: "host-a", OwnershipEpoch: 4,
	}
	policy := HostAgentPolicy{
		ServiceID: bootstrap.NodeID, TransportMode: HostTransportPullV2,
		ExecutionHostID: binding.ExecutionHostID, OwnershipEpoch: binding.OwnershipEpoch,
		Revision: 11, SourcePolicyRevision: 7, LocalExecutorPolicyRevision: 9,
		ObserveOnly:               false,
		LocalExecutorPolicySHA256: "sha256:" + strings.Repeat("b", 64),
		Targets: []HostAgentPolicyTarget{{
			ServiceID: "worker-01", ServiceType: "worker",
			DeploymentMode: ModeSystemd, AppliedConfigRevision: 1,
		}},
	}
	panel := &hostPullExecutionTestPanel{job: &UpdateJob{
		ID: "job-one", AgentServiceID: bootstrap.NodeID,
		HostID: binding.ExecutionHostID, TransportMode: HostTransportPullV2,
		OwnershipEpoch: binding.OwnershipEpoch, PolicyRevision: policy.Revision,
		TargetID: "worker-01", TargetType: "worker", ServiceType: "worker",
		DeploymentMode: ModeSystemd, CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0",
		LeaseToken: strings.Repeat("l", 48), LeaseGeneration: 2, ReportSequence: 1,
		RecoveryRequired: recovery,
	}}
	executor := &hostPullExecutionTestExecutor{}
	agent, err := NewObserveOnlyHostAgent(bootstrap, HostPullAgentOptions{
		StateDir: t.TempDir(), ControlPlane: panel,
		Executor: executor, Downloader: hostPullExecutionTestDownloader{},
		NewSessionID: func() (string, error) { return "session-0123456789abcdef", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := OpenJournal(agent.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	agent.Journal = journal
	return agent, panel, executor, binding, policy
}

func newHostPullPortExecutionHarness(t *testing.T, recovery bool) (*HostPullAgent, *hostPullExecutionTestPanel, *hostPullExecutionTestExecutor, HostAgentBinding, HostAgentPolicy) {
	t.Helper()
	agent, panel, executor, binding, policy := newHostPullExecutionHarness(t, recovery)
	expectedConfig := "sha256:" + strings.Repeat("d", 64)
	targetConfig := "sha256:" + strings.Repeat("e", 64)
	policy.Targets[0].AppliedConfigRevision = 3
	policy.Targets[0].AppliedConfigSHA256 = expectedConfig
	policy.Targets[0].LocalListenEndpoint = &HostAgentEndpoint{
		Host: "127.0.0.1", Port: 8084, PublicURL: "http://127.0.0.1:8084",
	}
	policy.Targets[0].DesiredEndpoint = &HostAgentEndpoint{
		Host: "127.0.0.1", Port: 9084, PublicURL: "http://127.0.0.1:9084",
	}
	panel.job.Operation = updateJobOperationPortReconfigure
	panel.job.CurrentVersion = "v1.0.0"
	panel.job.TargetVersion = "v1.0.0"
	panel.job.PortReconfigure = &SystemdPortMutationGrantBinding{
		NetworkNamespace:               systemdPortNetworkNamespaceHost,
		Protocol:                       systemdPortProtocolTCP,
		OldPort:                        8084,
		NewPort:                        9084,
		ExpectedEndpointRevision:       5,
		TargetEndpointRevision:         6,
		ExpectedConfigRevision:         3,
		TargetConfigRevision:           4,
		ExpectedConfigSHA256:           expectedConfig,
		TargetConfigSHA256:             targetConfig,
		ExpectedSourcePolicyRevision:   policy.SourcePolicyRevision,
		ExpectedUpdaterPolicyRevision:  policy.Revision,
		ExpectedExecutorPolicyRevision: policy.LocalExecutorPolicyRevision,
		ExpectedExecutorPolicySHA256:   policy.LocalExecutorPolicySHA256,
		PortPlanSHA256:                 strings.Repeat("c", 64),
	}
	agent.PortExecutor = executor
	return agent, panel, executor, binding, policy
}
