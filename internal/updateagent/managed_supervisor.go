package updateagent

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

type ManagedPolicySource interface {
	FetchManagedPolicy(context.Context, string, int64) (*ManagedPolicy, bool, error)
}

type ManagedCoordinatorRuntime interface {
	StartManaged(context.Context) (<-chan error, error)
	SetPolicyState(int64, int64, string, string, map[string]string, map[string]string)
	ActivatePolicy()
	BeginPolicyReplacement(context.Context) (func(bool), bool, error)
	AbortPolicyReplacement()
}

type ManagedSupervisor struct {
	Bootstrap          Config
	Policy             ManagedPolicySource
	PollInterval       time.Duration
	Materialize        func(ManagedPolicy, Config) (Config, error)
	NewCoordinator     func(Config) (ManagedCoordinatorRuntime, error)
	Snapshot           ManagedPolicySnapshotStore
	PruneSSHIdentities func(Config) error
	ReportStatus       func(context.Context, Config) error
	Logf               func(string, ...any)
}

func NewManagedSupervisor(bootstrap Config) *ManagedSupervisor {
	panel := PanelClient{BaseURL: bootstrap.PanelURL, Token: bootstrap.RuntimeToken}
	return &ManagedSupervisor{
		Bootstrap:    bootstrap,
		Policy:       panel,
		PollInterval: 5 * time.Second,
		Materialize: func(policy ManagedPolicy, bootstrap Config) (Config, error) {
			return policy.Materialize(bootstrap)
		},
		NewCoordinator: func(cfg Config) (ManagedCoordinatorRuntime, error) {
			return NewCentralCoordinator(cfg)
		},
		Snapshot: FileManagedPolicySnapshotStore{StateDir: ManagedUpdaterStateDir},
		PruneSSHIdentities: func(cfg Config) error {
			return pruneManagedSSHIdentitiesForConfig(ManagedUpdaterStateDir, cfg)
		},
		ReportStatus: func(ctx context.Context, cfg Config) error {
			return panel.HeartbeatWithHosts(ctx, cfg, "online", nil, nil)
		},
		Logf: log.Printf,
	}
}

func (s ManagedSupervisor) Run(ctx context.Context) error {
	if !s.Bootstrap.IsManagedBootstrap() {
		return errors.New("managed supervisor requires an identity-only updater bootstrap")
	}
	if s.Policy == nil || s.Materialize == nil || s.NewCoordinator == nil {
		return errors.New("managed supervisor dependencies are incomplete")
	}
	if s.PollInterval <= 0 {
		s.PollInterval = 5 * time.Second
	}
	if s.Logf == nil {
		s.Logf = func(string, ...any) {}
	}
	if s.ReportStatus == nil {
		panel := PanelClient{BaseURL: s.Bootstrap.PanelURL, Token: s.Bootstrap.RuntimeToken}
		s.ReportStatus = func(ctx context.Context, cfg Config) error {
			return panel.HeartbeatWithHosts(ctx, cfg, "online", nil, nil)
		}
	}

	var current ManagedCoordinatorRuntime
	var cancelCurrent context.CancelFunc
	var currentDone <-chan error
	var currentRevision int64
	var desiredRevision int64
	var appliedConfig Config
	var appliedPolicy *ManagedPolicy
	var lastErrorCode string
	var snapshotNeedsSync bool
	var identityPruneNeeded bool
	pruningEnabled := s.Snapshot != nil && s.PruneSSHIdentities != nil

	reportWithoutCoordinator := func(appliedRevision, desired int64, status, errorCode string, publicKeys, fingerprints map[string]string) {
		cfg := managedRuntimeStatusConfig(s.Bootstrap, appliedRevision, desired, status, errorCode, publicKeys, fingerprints)
		if err := s.ReportStatus(ctx, cfg); err != nil && ctx.Err() == nil {
			s.Logf("managed updater status heartbeat failed")
		}
	}
	setCurrentStatus := func(appliedRevision, desired int64, status, errorCode string, publicKeys, fingerprints map[string]string) {
		if current != nil {
			current.SetPolicyState(appliedRevision, desired, status, errorCode, publicKeys, fingerprints)
			return
		}
		reportWithoutCoordinator(appliedRevision, desired, status, errorCode, publicKeys, fingerprints)
	}
	pruneAppliedIdentities := func() bool {
		if !identityPruneNeeded {
			return true
		}
		if appliedPolicy == nil || !pruningEnabled {
			lastErrorCode = PolicyErrorSSHIdentity
			setCurrentStatus(currentRevision, desiredRevision, PolicyStatusFailed, PolicyErrorSSHIdentity, appliedConfig.SSHClientPublicKeys, appliedConfig.SSHClientKeyFingerprints)
			return false
		}
		if err := s.PruneSSHIdentities(appliedConfig); err != nil {
			lastErrorCode = PolicyErrorSSHIdentity
			setCurrentStatus(currentRevision, currentRevision, PolicyStatusFailed, PolicyErrorSSHIdentity, appliedConfig.SSHClientPublicKeys, appliedConfig.SSHClientKeyFingerprints)
			s.Logf("managed updater SSH identity prune failed (%s)", PolicyErrorSSHIdentity)
			return false
		}
		identityPruneNeeded = false
		return true
	}
	activateApplied := func() {
		if current == nil || snapshotNeedsSync || identityPruneNeeded {
			return
		}
		lastErrorCode = ""
		current.SetPolicyState(currentRevision, currentRevision, PolicyStatusApplied, "", appliedConfig.SSHClientPublicKeys, appliedConfig.SSHClientKeyFingerprints)
		current.ActivatePolicy()
	}
	startRuntime := func(runtime ManagedCoordinatorRuntime, activate bool) error {
		runCtx, cancel := context.WithCancel(ctx)
		done, err := runtime.StartManaged(runCtx)
		if err != nil {
			cancel()
			return err
		}
		current = runtime
		cancelCurrent = cancel
		currentDone = done
		if activate {
			current.ActivatePolicy()
		}
		return nil
	}
	stopRuntime := func() error {
		if current == nil {
			return nil
		}
		cancelCurrent()
		err, ok := <-currentDone
		current = nil
		cancelCurrent = nil
		currentDone = nil
		if !ok || err == nil || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	restartApplied := func(errorCode string, publicKeys, fingerprints map[string]string, holdActivation bool) bool {
		if appliedPolicy == nil || currentRevision <= 0 {
			reportWithoutCoordinator(currentRevision, desiredRevision, PolicyStatusFailed, errorCode, publicKeys, fingerprints)
			return false
		}
		startStatus := PolicyStatusFailed
		if holdActivation {
			startStatus = PolicyStatusPending
		}
		cfg := managedRuntimeStatusConfig(appliedConfig, currentRevision, desiredRevision, startStatus, errorCode, publicKeys, fingerprints)
		runtime, err := s.NewCoordinator(cfg)
		if err == nil {
			err = startRuntime(runtime, !holdActivation)
		}
		if err != nil {
			reportWithoutCoordinator(currentRevision, desiredRevision, PolicyStatusFailed, PolicyErrorCoordinator, publicKeys, fingerprints)
			s.Logf("managed updater rollback coordinator failed (%s)", PolicyErrorCoordinator)
			return false
		}
		current.SetPolicyState(currentRevision, desiredRevision, PolicyStatusFailed, errorCode, publicKeys, fingerprints)
		return true
	}
	defer func() {
		if current != nil {
			_ = stopRuntime()
		}
	}()

	if s.Snapshot != nil {
		policy, exists, err := s.Snapshot.Load()
		if err != nil {
			lastErrorCode = PolicyErrorSnapshot
			reportWithoutCoordinator(0, 0, PolicyStatusFailed, PolicyErrorSnapshot, nil, nil)
			s.Logf("managed updater last-applied snapshot unavailable (%s)", PolicyErrorSnapshot)
			return errors.New("managed updater last-applied snapshot unavailable")
		} else if exists {
			desiredRevision = policy.Revision
			cfg, materializeErr := s.Materialize(*policy, s.Bootstrap)
			if materializeErr != nil {
				lastErrorCode = materializePolicyErrorCode(materializeErr)
				reportWithoutCoordinator(0, policy.Revision, PolicyStatusFailed, lastErrorCode, nil, nil)
				s.Logf("managed updater last-applied snapshot materialization failed (%s)", lastErrorCode)
				return errors.New("managed updater last-applied snapshot materialization failed")
			} else {
				appliedConfig = cfg
				appliedCopy := *policy
				appliedPolicy = &appliedCopy
				currentRevision = policy.Revision
				identityPruneNeeded = pruningEnabled
				if pruningEnabled {
					if durabilityErr := s.Snapshot.Save(*policy); durabilityErr != nil {
						snapshotNeedsSync = true
						lastErrorCode = PolicyErrorSnapshot
						s.Logf("managed updater loaded snapshot durability confirmation failed (%s)", PolicyErrorSnapshot)
					}
				}
				runtimeStatus := PolicyStatusApplied
				holdActivation := snapshotNeedsSync || identityPruneNeeded
				if holdActivation {
					runtimeStatus = PolicyStatusPending
				}
				runtimeConfig := managedRuntimeStatusConfig(cfg, policy.Revision, policy.Revision, runtimeStatus, "", cfg.SSHClientPublicKeys, cfg.SSHClientKeyFingerprints)
				runtime, openErr := s.NewCoordinator(runtimeConfig)
				if openErr == nil {
					openErr = startRuntime(runtime, !holdActivation)
				}
				if openErr != nil {
					lastErrorCode = PolicyErrorCoordinator
					reportWithoutCoordinator(policy.Revision, policy.Revision, PolicyStatusFailed, PolicyErrorCoordinator, cfg.SSHClientPublicKeys, cfg.SSHClientKeyFingerprints)
					s.Logf("managed updater last-applied coordinator failed (%s)", PolicyErrorCoordinator)
				} else if snapshotNeedsSync {
					current.SetPolicyState(policy.Revision, policy.Revision, PolicyStatusFailed, PolicyErrorSnapshot, cfg.SSHClientPublicKeys, cfg.SSHClientKeyFingerprints)
				} else if identityPruneNeeded {
					if pruneAppliedIdentities() {
						activateApplied()
					}
				} else {
					current.SetPolicyState(policy.Revision, policy.Revision, PolicyStatusApplied, "", cfg.SSHClientPublicKeys, cfg.SSHClientKeyFingerprints)
					lastErrorCode = ""
				}
			}
		}
	}

	initialPollDelay := time.Duration(0)
	if identityPruneNeeded || snapshotNeedsSync {
		initialPollDelay = s.PollInterval
	}
	timer := time.NewTimer(initialPollDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := stopRuntime(); err != nil {
				s.Logf("managed updater shutdown failed")
			}
			return ctx.Err()
		case <-currentDone:
			current = nil
			cancelCurrent = nil
			currentDone = nil
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErrorCode = PolicyErrorCoordinator
			if snapshotNeedsSync {
				lastErrorCode = PolicyErrorSnapshot
			} else if identityPruneNeeded {
				lastErrorCode = PolicyErrorSSHIdentity
			}
			reportWithoutCoordinator(currentRevision, maxPolicyRevision(desiredRevision, currentRevision), PolicyStatusFailed, lastErrorCode, appliedConfig.SSHClientPublicKeys, appliedConfig.SSHClientKeyFingerprints)
			s.Logf("managed updater coordinator stopped unexpectedly (%s)", lastErrorCode)
			timer.Reset(s.PollInterval)
			continue
		case <-timer.C:
		}

		// Never evaluate or construct a newer desired policy while the durable
		// last-applied runtime is absent. Its journals are the only authoritative
		// recovery cursors for targets that the desired revision may remove.
		// Restarting first forces interrupted work through StartManaged recovery
		// and BeginPolicyReplacement drain before replacement can proceed.
		if current == nil && appliedPolicy != nil && currentRevision > 0 {
			recoveryError := lastErrorCode
			if recoveryError == "" {
				recoveryError = PolicyErrorCoordinator
			}
			if !restartApplied(recoveryError, appliedConfig.SSHClientPublicKeys, appliedConfig.SSHClientKeyFingerprints, snapshotNeedsSync || identityPruneNeeded) {
				timer.Reset(s.PollInterval)
				continue
			}
		}

		if snapshotNeedsSync && s.Snapshot != nil && appliedPolicy != nil {
			err := s.Snapshot.Save(*appliedPolicy)
			if err != nil {
				lastErrorCode = PolicyErrorSnapshot
				setCurrentStatus(currentRevision, currentRevision, PolicyStatusFailed, PolicyErrorSnapshot, appliedConfig.SSHClientPublicKeys, appliedConfig.SSHClientKeyFingerprints)
				s.Logf("managed updater policy snapshot durability retry failed (%s)", PolicyErrorSnapshot)
				timer.Reset(s.PollInterval)
				continue
			}
			snapshotNeedsSync = false
			if !identityPruneNeeded {
				activateApplied()
			}
		}

		if identityPruneNeeded {
			if !pruneAppliedIdentities() {
				timer.Reset(s.PollInterval)
				continue
			}
			activateApplied()
		}

		policy, changed, err := s.Policy.FetchManagedPolicy(ctx, s.Bootstrap.NodeID, currentRevision)
		if err != nil {
			if currentRevision > 0 || desiredRevision > 0 {
				lastErrorCode = PolicyErrorFetch
				setCurrentStatus(currentRevision, maxPolicyRevision(desiredRevision, currentRevision), PolicyStatusFailed, PolicyErrorFetch, nil, nil)
			} else if lastErrorCode != "" {
				reportWithoutCoordinator(0, 0, PolicyStatusFailed, lastErrorCode, nil, nil)
			} else {
				reportWithoutCoordinator(0, 0, "", "", nil, nil)
			}
			s.Logf("managed updater policy fetch failed (%s)", PolicyErrorFetch)
			timer.Reset(s.PollInterval)
			continue
		}
		if !changed || policy == nil {
			if current == nil && appliedPolicy != nil {
				status := PolicyStatusApplied
				errorCode := ""
				if desiredRevision > currentRevision {
					status = PolicyStatusFailed
					errorCode = safePolicyErrorCode(lastErrorCode)
				}
				cfg := managedRuntimeStatusConfig(appliedConfig, currentRevision, desiredRevision, status, errorCode, appliedConfig.SSHClientPublicKeys, appliedConfig.SSHClientKeyFingerprints)
				runtime, startErr := s.NewCoordinator(cfg)
				if startErr == nil {
					startErr = startRuntime(runtime, true)
				}
				if startErr != nil {
					lastErrorCode = PolicyErrorCoordinator
					reportWithoutCoordinator(currentRevision, desiredRevision, PolicyStatusFailed, PolicyErrorCoordinator, appliedConfig.SSHClientPublicKeys, appliedConfig.SSHClientKeyFingerprints)
				} else {
					current.SetPolicyState(currentRevision, desiredRevision, status, errorCode, appliedConfig.SSHClientPublicKeys, appliedConfig.SSHClientKeyFingerprints)
				}
			} else if current != nil && desiredRevision <= currentRevision {
				current.AbortPolicyReplacement()
				current.SetPolicyState(currentRevision, currentRevision, PolicyStatusApplied, "", nil, nil)
				lastErrorCode = ""
			} else if current == nil {
				if desiredRevision > 0 || lastErrorCode != "" {
					reportWithoutCoordinator(currentRevision, desiredRevision, PolicyStatusFailed, safePolicyErrorCode(lastErrorCode), nil, nil)
				} else {
					reportWithoutCoordinator(0, 0, "", "", nil, nil)
				}
			}
			timer.Reset(s.PollInterval)
			continue
		}

		desiredRevision = policy.Revision
		if policy.Revision <= currentRevision {
			lastErrorCode = PolicyErrorInvalid
			setCurrentStatus(currentRevision, currentRevision, PolicyStatusFailed, PolicyErrorInvalid, nil, nil)
			timer.Reset(s.PollInterval)
			continue
		}
		if current != nil {
			setCurrentStatus(currentRevision, policy.Revision, PolicyStatusPending, "", nil, nil)
		}
		candidateConfig, err := s.Materialize(*policy, s.Bootstrap)
		if err != nil {
			lastErrorCode = materializePolicyErrorCode(err)
			if current != nil {
				current.AbortPolicyReplacement()
			}
			setCurrentStatus(currentRevision, policy.Revision, PolicyStatusFailed, lastErrorCode, nil, nil)
			s.Logf("managed updater policy materialization failed (%s)", lastErrorCode)
			timer.Reset(s.PollInterval)
			continue
		}
		candidateRuntimeConfig := managedRuntimeStatusConfig(
			candidateConfig,
			currentRevision,
			policy.Revision,
			PolicyStatusPending,
			"",
			candidateConfig.SSHClientPublicKeys,
			candidateConfig.SSHClientKeyFingerprints,
		)
		var candidate ManagedCoordinatorRuntime
		if current != nil {
			release, ready, drainErr := current.BeginPolicyReplacement(ctx)
			if drainErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				current.AbortPolicyReplacement()
				lastErrorCode = PolicyErrorCoordinator
				setCurrentStatus(currentRevision, policy.Revision, PolicyStatusFailed, PolicyErrorCoordinator, candidateConfig.SSHClientPublicKeys, candidateConfig.SSHClientKeyFingerprints)
				s.Logf("managed updater policy drain failed (%s)", PolicyErrorCoordinator)
				timer.Reset(s.PollInterval)
				continue
			}
			if !ready {
				lastErrorCode = PolicyErrorActiveJob
				setCurrentStatus(currentRevision, policy.Revision, PolicyStatusPending, PolicyErrorActiveJob, candidateConfig.SSHClientPublicKeys, candidateConfig.SSHClientKeyFingerprints)
				timer.Reset(s.PollInterval)
				continue
			}
			candidate, err = s.NewCoordinator(candidateRuntimeConfig)
			if err != nil {
				release(false)
				lastErrorCode = PolicyErrorCoordinator
				setCurrentStatus(currentRevision, policy.Revision, PolicyStatusFailed, PolicyErrorCoordinator, candidateConfig.SSHClientPublicKeys, candidateConfig.SSHClientKeyFingerprints)
				s.Logf("managed updater coordinator preparation failed (%s)", PolicyErrorCoordinator)
				timer.Reset(s.PollInterval)
				continue
			}
			cancelCurrent()
			release(true)
			oldErr, _ := <-currentDone
			current = nil
			cancelCurrent = nil
			currentDone = nil
			if oldErr != nil && !errors.Is(oldErr, context.Canceled) {
				s.Logf("managed updater previous coordinator stopped unexpectedly")
			}
		} else {
			candidate, err = s.NewCoordinator(candidateRuntimeConfig)
			if err != nil {
				lastErrorCode = PolicyErrorCoordinator
				setCurrentStatus(currentRevision, policy.Revision, PolicyStatusFailed, PolicyErrorCoordinator, candidateConfig.SSHClientPublicKeys, candidateConfig.SSHClientKeyFingerprints)
				s.Logf("managed updater coordinator preparation failed (%s)", PolicyErrorCoordinator)
				timer.Reset(s.PollInterval)
				continue
			}
		}

		if err := startRuntime(candidate, false); err != nil {
			lastErrorCode = PolicyErrorCoordinator
			s.Logf("managed updater candidate readiness failed (%s)", PolicyErrorCoordinator)
			restartApplied(PolicyErrorCoordinator, candidateConfig.SSHClientPublicKeys, candidateConfig.SSHClientKeyFingerprints, false)
			timer.Reset(s.PollInterval)
			continue
		}
		if err := ctx.Err(); err != nil {
			_ = stopRuntime()
			return err
		}

		snapshotErr := error(nil)
		if s.Snapshot != nil {
			snapshotErr = s.Snapshot.Save(*policy)
		}
		if snapshotErr != nil && !ManagedPolicySnapshotWasCommitted(snapshotErr) {
			_ = stopRuntime()
			lastErrorCode = PolicyErrorSnapshot
			s.Logf("managed updater policy snapshot failed before commit (%s)", PolicyErrorSnapshot)
			restartApplied(PolicyErrorSnapshot, candidateConfig.SSHClientPublicKeys, candidateConfig.SSHClientKeyFingerprints, false)
			timer.Reset(s.PollInterval)
			continue
		}

		candidateCopy := *policy
		appliedPolicy = &candidateCopy
		appliedConfig = candidateConfig
		currentRevision = policy.Revision
		desiredRevision = policy.Revision
		identityPruneNeeded = pruningEnabled
		if snapshotErr != nil {
			snapshotNeedsSync = true
			lastErrorCode = PolicyErrorSnapshot
			current.SetPolicyState(policy.Revision, policy.Revision, PolicyStatusFailed, PolicyErrorSnapshot, candidateConfig.SSHClientPublicKeys, candidateConfig.SSHClientKeyFingerprints)
			s.Logf("managed updater policy snapshot committed with durability warning (%s)", PolicyErrorSnapshot)
		} else if identityPruneNeeded {
			if !pruneAppliedIdentities() {
				timer.Reset(s.PollInterval)
				continue
			}
			activateApplied()
		} else {
			activateApplied()
		}
		timer.Reset(s.PollInterval)
	}
}

func managedRuntimeStatusConfig(base Config, appliedRevision, desiredRevision int64, status, errorCode string, publicKeys, fingerprints map[string]string) Config {
	cfg := base
	cfg.PolicyRevision = appliedRevision
	cfg.PolicyDesiredRevision = desiredRevision
	cfg.PolicyStatus = status
	cfg.PolicyErrorCode = ""
	if status == PolicyStatusFailed || (status == PolicyStatusPending && errorCode == PolicyErrorActiveJob) {
		cfg.PolicyErrorCode = safePolicyErrorCode(errorCode)
	}
	cfg.SSHClientPublicKeys = cloneCapabilityStringMap(publicKeys)
	cfg.SSHClientKeyFingerprints = cloneCapabilityStringMap(fingerprints)
	return cfg
}

func materializePolicyErrorCode(err error) string {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "ssh identit") {
		return PolicyErrorSSHIdentity
	}
	return PolicyErrorInvalid
}

func maxPolicyRevision(value, minimum int64) int64 {
	if value < minimum {
		return minimum
	}
	return value
}
