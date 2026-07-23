package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/updateagent"
	"github.com/example/autostream-control-panel/internal/version"
)

const defaultUpdaterConfigExamplePath = "/opt/autostream/control-panel/current/autostream-updater.json.example"

type preparedUpdaterConfig interface {
	Commit(updateagent.UpdaterConfigureIdentity) error
	Abort()
}

type updaterConfigureDependencies struct {
	Initialize        func(string, string) (bool, error)
	Prepare           func(string) (preparedUpdaterConfig, error)
	ReadToken         func(context.Context) (string, error)
	Stage             func(context.Context, string, string, string, time.Duration) (updateagent.UpdaterStagedConfiguration, error)
	ValidateInstalled func(string, updateagent.UpdaterConfigureIdentity) error
	Activate          func(context.Context, string, updateagent.UpdaterStagedConfiguration, updateagent.UpdaterRuntimeReport, time.Duration) (updateagent.UpdaterActivationResult, error)
	Hostname          func() (string, error)
	Output            io.Writer
}

func defaultUpdaterConfigureDependencies() updaterConfigureDependencies {
	return updaterConfigureDependencies{
		Initialize: updateagent.InitializeUpdaterConfig,
		Prepare: func(path string) (preparedUpdaterConfig, error) {
			return updateagent.PrepareUpdaterConfig(path)
		},
		ReadToken: defaultReadUpdaterConfigureToken,
		Stage: func(ctx context.Context, panelURL, nodeID, token string, timeout time.Duration) (updateagent.UpdaterStagedConfiguration, error) {
			return updateagent.StageUpdaterConfiguration(ctx, http.DefaultClient, panelURL, nodeID, token, timeout)
		},
		ValidateInstalled: updateagent.ValidateInstalledUpdaterIdentity,
		Activate: func(ctx context.Context, panelURL string, staged updateagent.UpdaterStagedConfiguration, report updateagent.UpdaterRuntimeReport, timeout time.Duration) (updateagent.UpdaterActivationResult, error) {
			return updateagent.ActivateUpdaterConfiguration(ctx, http.DefaultClient, panelURL, staged, report, timeout)
		},
		Hostname: os.Hostname,
		Output:   os.Stdout,
	}
}

func runUpdaterConfigure(ctx context.Context, args []string, dependencies updaterConfigureDependencies) error {
	flags := flag.NewFlagSet("configure", flag.ContinueOnError)
	panelURL := flags.String("panel-url", "", "Control Panel base URL")
	nodeID := flags.String("node", "", "registered Update Agent node ID")
	configPath := flags.String("config", "/etc/autostream/updater.json", "root-owned updater configuration")
	initFrom := flags.String("init-from", defaultUpdaterConfigExamplePath, "root-controlled example used only when the updater configuration is missing")
	timeout := flags.Duration("timeout", 30*time.Second, "Control Panel configure request timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("configure accepts only --panel-url, --node, --config, --init-from, and --timeout")
	}
	if strings.TrimSpace(*panelURL) == "" {
		return errors.New("--panel-url is required")
	}
	if strings.TrimSpace(*nodeID) == "" {
		return errors.New("--node is required")
	}
	if *timeout <= 0 || *timeout > 5*time.Minute {
		return errors.New("--timeout must be greater than zero and at most 5m")
	}
	if dependencies.Initialize == nil || dependencies.Prepare == nil || dependencies.ReadToken == nil || dependencies.Stage == nil || dependencies.ValidateInstalled == nil || dependencies.Activate == nil || dependencies.Hostname == nil || dependencies.Output == nil {
		return errors.New("configure dependencies are incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	created, err := dependencies.Initialize(*configPath, *initFrom)
	if err != nil {
		if created {
			return fmt.Errorf("initialize updater config: configuration may have been installed at %s but final verification failed; inspect it before rerunning; Configure Token was not requested or consumed: %w", *configPath, err)
		}
		return fmt.Errorf("initialize updater config; Configure Token was not requested or consumed: %w", err)
	}
	if created {
		return fmt.Errorf("created %s from %s; complete local policy (github_token, api, state_dir, hosts, targets, and SSH files), then rerun the same command; Configure Token was not requested or consumed", *configPath, *initFrom)
	}

	prepared, err := dependencies.Prepare(*configPath)
	if err != nil {
		return err
	}
	defer prepared.Abort()
	hostname, err := dependencies.Hostname()
	if err != nil {
		return errors.New("read updater hostname")
	}
	report := updateagent.UpdaterRuntimeReport{
		Version: version.Current(), BuildDate: version.BuildDate, Commit: version.Commit,
		Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH,
	}
	configureToken, err := dependencies.ReadToken(ctx)
	if err != nil {
		return err
	}
	staged, err := dependencies.Stage(ctx, strings.TrimSpace(*panelURL), strings.TrimSpace(*nodeID), configureToken, *timeout)
	configureToken = ""
	if err != nil {
		// A lost stage response is indistinguishable from a consumed Configure
		// Token. Keep the running updater on its old active credential and require
		// a newly issued token before another attempt.
		return fmt.Errorf("updater configuration stage failed; existing updater remains active; issue a new Configure Token before retrying: %v", err)
	}
	if err := ctx.Err(); err != nil {
		// Stage succeeded and consumed the one-time token, but cancellation won
		// before any local mutation. Keep the old active identity on disk and do
		// not return a directly wrapped context error that main would suppress.
		return fmt.Errorf("updater configuration was staged but canceled before installation; existing updater remains active; issue a new Configure Token before retrying: %v", err)
	}
	if err := prepared.Commit(staged.Config); err != nil {
		return fmt.Errorf("configuration was staged but installation failed; do not restart autostream-updater and issue a new Configure Token: %w", err)
	}
	postCommitError := func(operation string, err error) error {
		// Do not wrap context cancellation here: main treats a direct cancellation
		// as a clean shutdown, while post-commit cancellation requires a visible
		// failure because the inactive staged token is already on disk.
		return fmt.Errorf("updater identity was installed but %s failed; do not restart autostream-updater and issue a new Configure Token: %v", operation, err)
	}
	if err := dependencies.ValidateInstalled(*configPath, staged.Config); err != nil {
		return postCommitError("installed configuration validation", err)
	}
	result, err := dependencies.Activate(ctx, strings.TrimSpace(*panelURL), staged, report, *timeout)
	if err != nil {
		return postCommitError("activation", err)
	}
	if result.ConfigurationID != staged.ConfigurationID || (result.State != "activated" && result.State != "already_activated") {
		return postCommitError("activation", errors.New("Control Panel returned an unexpected activation result"))
	}
	fmt.Fprintf(dependencies.Output, "Updater identity activated in %s. Run validate-config, then restart autostream-updater.\n", *configPath)
	return nil
}
