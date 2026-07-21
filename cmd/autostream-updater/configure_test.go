package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/updateagent"
)

type fakePreparedUpdaterConfig struct {
	committed *updateagent.UpdaterConfigureIdentity
	aborted   bool
}

func (f *fakePreparedUpdaterConfig) Commit(identity updateagent.UpdaterConfigureIdentity) error {
	f.committed = &identity
	return nil
}

func (f *fakePreparedUpdaterConfig) Abort() { f.aborted = true }

func stagedUpdaterConfiguration(panelURL, runtimeToken string) updateagent.UpdaterStagedConfiguration {
	return updateagent.UpdaterStagedConfiguration{
		Config: updateagent.UpdaterConfigureIdentity{
			PanelURL: panelURL, NodeID: "central-updater", RuntimeToken: runtimeToken,
			ServiceName: "Central Updater", ServiceType: "update_agent",
		},
		ConfigurationID:     "configuration-01",
		ActivationToken:     "activation-secret",
		ActivationExpiresAt: time.Now().UTC().Add(time.Hour),
	}
}

func completeUpdaterConfigureDependencies(t *testing.T, prepared *fakePreparedUpdaterConfig, output *strings.Builder) updaterConfigureDependencies {
	t.Helper()
	return updaterConfigureDependencies{
		Prepare:   func(string) (preparedUpdaterConfig, error) { return prepared, nil },
		ReadToken: func(context.Context) (string, error) { return "configure-secret", nil },
		Stage: func(context.Context, string, string, string, time.Duration) (updateagent.UpdaterStagedConfiguration, error) {
			return stagedUpdaterConfiguration("https://panel.example.com", "runtime-secret"), nil
		},
		ValidateInstalled: func(string, updateagent.UpdaterConfigureIdentity) error { return nil },
		Activate: func(context.Context, string, updateagent.UpdaterStagedConfiguration, updateagent.UpdaterRuntimeReport, time.Duration) (updateagent.UpdaterActivationResult, error) {
			return updateagent.UpdaterActivationResult{State: "activated", ConfigurationID: "configuration-01"}, nil
		},
		Hostname: func() (string, error) { return "central-host", nil },
		Output:   output,
	}
}

func TestConfigureRejectsTokenArgumentBeforeLocalMutation(t *testing.T) {
	secret := "must-not-appear-in-errors"
	prepareCalled := false
	dependencies := updaterConfigureDependencies{
		Prepare: func(string) (preparedUpdaterConfig, error) {
			prepareCalled = true
			return nil, nil
		},
	}
	err := runUpdaterConfigure(context.Background(), []string{"--panel-url", "https://panel.example.com", "--node", "central-updater", "--token", secret}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") || strings.Contains(err.Error(), secret) || prepareCalled {
		t.Fatalf("argv token rejection err=%v prepare_called=%v", err, prepareCalled)
	}
}

func TestConfigurePreflightsBeforeReadingOneTimeToken(t *testing.T) {
	readCalled := false
	dependencies := completeUpdaterConfigureDependencies(t, &fakePreparedUpdaterConfig{}, &strings.Builder{})
	dependencies.Prepare = func(string) (preparedUpdaterConfig, error) { return nil, errors.New("unsafe config parent") }
	dependencies.ReadToken = func(context.Context) (string, error) {
		readCalled = true
		return "", nil
	}
	err := runUpdaterConfigure(context.Background(), []string{"--panel-url", "https://panel.example.com", "--node", "central-updater", "--config", "/etc/autostream/updater.json"}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "unsafe config parent") || readCalled {
		t.Fatalf("preflight result err=%v read_called=%v", err, readCalled)
	}
}

func TestConfigureStagesCommitsValidatesThenActivatesWithoutPrintingSecrets(t *testing.T) {
	prepared := &fakePreparedUpdaterConfig{}
	output := &strings.Builder{}
	configureToken := "one-time-configure-secret"
	runtimeToken := "new-runtime-secret"
	activationToken := "activation-secret"
	order := make([]string, 0, 5)
	dependencies := completeUpdaterConfigureDependencies(t, prepared, output)
	dependencies.ReadToken = func(context.Context) (string, error) {
		order = append(order, "read")
		return configureToken, nil
	}
	dependencies.Stage = func(_ context.Context, panelURL, nodeID, token string, timeout time.Duration) (updateagent.UpdaterStagedConfiguration, error) {
		order = append(order, "stage")
		if panelURL != "https://panel.example.com" || nodeID != "central-updater" || token != configureToken || timeout != 30*time.Second {
			t.Fatalf("stage request panel=%q node=%q token=%q timeout=%s", panelURL, nodeID, token, timeout)
		}
		staged := stagedUpdaterConfiguration(panelURL, runtimeToken)
		staged.ActivationToken = activationToken
		return staged, nil
	}
	dependencies.ValidateInstalled = func(path string, identity updateagent.UpdaterConfigureIdentity) error {
		order = append(order, "validate")
		if path != "/etc/autostream/updater.json" || identity.RuntimeToken != runtimeToken {
			t.Fatalf("installed validation path=%q identity=%#v", path, identity)
		}
		return nil
	}
	dependencies.Activate = func(_ context.Context, panelURL string, staged updateagent.UpdaterStagedConfiguration, report updateagent.UpdaterRuntimeReport, timeout time.Duration) (updateagent.UpdaterActivationResult, error) {
		order = append(order, "activate")
		if prepared.committed == nil {
			t.Fatal("activation ran before commit")
		}
		if panelURL != "https://panel.example.com" || staged.ActivationToken != activationToken || report.Hostname != "central-host" || report.OS == "" || report.Arch == "" || timeout != 30*time.Second {
			t.Fatalf("activation request panel=%q staged=%#v report=%#v timeout=%s", panelURL, staged, report, timeout)
		}
		return updateagent.UpdaterActivationResult{State: "activated", ConfigurationID: staged.ConfigurationID}, nil
	}
	originalCommit := prepared.Commit
	dependencies.Prepare = func(path string) (preparedUpdaterConfig, error) {
		if path != "/etc/autostream/updater.json" {
			t.Fatalf("config path = %q", path)
		}
		return preparedUpdaterConfigFunc{
			commit: func(identity updateagent.UpdaterConfigureIdentity) error {
				order = append(order, "commit")
				return originalCommit(identity)
			},
			abort: prepared.Abort,
		}, nil
	}
	if err := runUpdaterConfigure(context.Background(), []string{"--panel-url", "https://panel.example.com", "--node", "central-updater"}, dependencies); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "read,stage,commit,validate,activate" {
		t.Fatalf("configure order = %s", got)
	}
	if prepared.committed == nil || prepared.committed.RuntimeToken != runtimeToken || !prepared.aborted {
		t.Fatalf("prepared update = %#v aborted=%v", prepared.committed, prepared.aborted)
	}
	if strings.Contains(output.String(), configureToken) || strings.Contains(output.String(), runtimeToken) || strings.Contains(output.String(), activationToken) || !strings.Contains(output.String(), "restart") {
		t.Fatalf("configure output = %q", output.String())
	}
}

type preparedUpdaterConfigFunc struct {
	commit func(updateagent.UpdaterConfigureIdentity) error
	abort  func()
}

func (f preparedUpdaterConfigFunc) Commit(identity updateagent.UpdaterConfigureIdentity) error {
	return f.commit(identity)
}

func (f preparedUpdaterConfigFunc) Abort() { f.abort() }

func TestConfigureStageFailureAbortsWithoutLeakingToken(t *testing.T) {
	prepared := &fakePreparedUpdaterConfig{}
	secret := "configure-secret-not-for-errors"
	dependencies := completeUpdaterConfigureDependencies(t, prepared, &strings.Builder{})
	dependencies.ReadToken = func(context.Context) (string, error) { return secret, nil }
	dependencies.Stage = func(context.Context, string, string, string, time.Duration) (updateagent.UpdaterStagedConfiguration, error) {
		return updateagent.UpdaterStagedConfiguration{}, errors.New("control panel rejected staged configuration")
	}
	err := runUpdaterConfigure(context.Background(), []string{"--panel-url", "https://panel.example.com", "--node", "central-updater"}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "existing updater remains active") || !strings.Contains(err.Error(), "new Configure Token") || strings.Contains(err.Error(), secret) || !prepared.aborted || prepared.committed != nil {
		t.Fatalf("stage failure err=%v aborted=%v committed=%#v", err, prepared.aborted, prepared.committed)
	}
}

func TestConfigureCancellationAfterSuccessfulStageAbortsBeforeCommit(t *testing.T) {
	for _, test := range []struct {
		name        string
		cancelStage func(context.CancelFunc, updateagent.UpdaterStagedConfiguration) (updateagent.UpdaterStagedConfiguration, error)
	}{
		{
			name: "before successful return",
			cancelStage: func(cancel context.CancelFunc, staged updateagent.UpdaterStagedConfiguration) (updateagent.UpdaterStagedConfiguration, error) {
				cancel()
				return staged, nil
			},
		},
		{
			name: "as successful return completes",
			cancelStage: func(cancel context.CancelFunc, staged updateagent.UpdaterStagedConfiguration) (result updateagent.UpdaterStagedConfiguration, err error) {
				defer cancel()
				return staged, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared := &fakePreparedUpdaterConfig{}
			configureSecret := "configure-secret-not-for-errors"
			runtimeSecret := "runtime-secret-not-for-errors"
			activationSecret := "activation-secret-not-for-errors"
			dependencies := completeUpdaterConfigureDependencies(t, prepared, &strings.Builder{})
			dependencies.ReadToken = func(context.Context) (string, error) { return configureSecret, nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			dependencies.Stage = func(context.Context, string, string, string, time.Duration) (updateagent.UpdaterStagedConfiguration, error) {
				staged := stagedUpdaterConfiguration("https://panel.example.com", runtimeSecret)
				staged.ActivationToken = activationSecret
				return test.cancelStage(cancel, staged)
			}
			err := runUpdaterConfigure(ctx, []string{"--panel-url", "https://panel.example.com", "--node", "central-updater"}, dependencies)
			if err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "existing updater remains active") || !strings.Contains(err.Error(), "new Configure Token") || strings.Contains(err.Error(), configureSecret) || strings.Contains(err.Error(), runtimeSecret) || strings.Contains(err.Error(), activationSecret) || prepared.committed != nil || !prepared.aborted {
				t.Fatalf("post-stage cancellation err=%v committed=%#v aborted=%v", err, prepared.committed, prepared.aborted)
			}
		})
	}
}

func TestConfigurePostCommitFailureForbidsRestartAndRequiresNewToken(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*updaterConfigureDependencies)
	}{
		{name: "validation", mutate: func(d *updaterConfigureDependencies) {
			d.ValidateInstalled = func(string, updateagent.UpdaterConfigureIdentity) error {
				return errors.New("invalid installed config")
			}
		}},
		{name: "activation", mutate: func(d *updaterConfigureDependencies) {
			d.Activate = func(context.Context, string, updateagent.UpdaterStagedConfiguration, updateagent.UpdaterRuntimeReport, time.Duration) (updateagent.UpdaterActivationResult, error) {
				return updateagent.UpdaterActivationResult{}, errors.New("activation unavailable")
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared := &fakePreparedUpdaterConfig{}
			dependencies := completeUpdaterConfigureDependencies(t, prepared, &strings.Builder{})
			test.mutate(&dependencies)
			err := runUpdaterConfigure(context.Background(), []string{"--panel-url", "https://panel.example.com", "--node", "central-updater"}, dependencies)
			if err == nil || !strings.Contains(err.Error(), "do not restart") || !strings.Contains(err.Error(), "new Configure Token") || strings.Contains(err.Error(), "configure-secret") || strings.Contains(err.Error(), "runtime-secret") || strings.Contains(err.Error(), "activation-secret") {
				t.Fatalf("post-commit failure = %v", err)
			}
		})
	}
}

func TestConfigureCancellationAbortsBeforeCommitAndWarnsAfterCommit(t *testing.T) {
	t.Run("before commit", func(t *testing.T) {
		prepared := &fakePreparedUpdaterConfig{}
		dependencies := completeUpdaterConfigureDependencies(t, prepared, &strings.Builder{})
		ctx, cancel := context.WithCancel(context.Background())
		dependencies.Stage = func(ctx context.Context, _, _, _ string, _ time.Duration) (updateagent.UpdaterStagedConfiguration, error) {
			cancel()
			<-ctx.Done()
			return updateagent.UpdaterStagedConfiguration{}, ctx.Err()
		}
		defer cancel()
		err := runUpdaterConfigure(ctx, []string{"--panel-url", "https://panel.example.com", "--node", "central-updater"}, dependencies)
		if err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "context canceled") || !strings.Contains(err.Error(), "existing updater remains active") || !strings.Contains(err.Error(), "new Configure Token") || !prepared.aborted || prepared.committed != nil {
			t.Fatalf("pre-commit cancellation err=%v aborted=%v committed=%#v", err, prepared.aborted, prepared.committed)
		}
	})
	t.Run("after commit", func(t *testing.T) {
		prepared := &fakePreparedUpdaterConfig{}
		dependencies := completeUpdaterConfigureDependencies(t, prepared, &strings.Builder{})
		dependencies.Activate = func(_ context.Context, _ string, _ updateagent.UpdaterStagedConfiguration, _ updateagent.UpdaterRuntimeReport, _ time.Duration) (updateagent.UpdaterActivationResult, error) {
			return updateagent.UpdaterActivationResult{}, context.Canceled
		}
		err := runUpdaterConfigure(context.Background(), []string{"--panel-url", "https://panel.example.com", "--node", "central-updater"}, dependencies)
		if err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "context canceled") || !strings.Contains(err.Error(), "do not restart") || !strings.Contains(err.Error(), "new Configure Token") || prepared.committed == nil {
			t.Fatalf("post-commit cancellation err=%v committed=%#v", err, prepared.committed)
		}
	})
}

func TestConfigureTokenStandardInputIsBoundedAndTrimmed(t *testing.T) {
	token, err := readBoundedUpdaterConfigureToken(strings.NewReader("  configure-secret  \r\nignored"))
	if err != nil || token != "configure-secret" {
		t.Fatalf("bounded token = %q, %v", token, err)
	}
	if _, err := readBoundedUpdaterConfigureToken(strings.NewReader(strings.Repeat("x", updaterConfigureTokenMaxBytes+1))); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized token error = %v", err)
	}
	if _, err := readBoundedUpdaterConfigureToken(strings.NewReader("\n")); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("empty token error = %v", err)
	}
}
