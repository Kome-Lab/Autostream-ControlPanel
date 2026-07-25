package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/autostream-control-panel/internal/updateagent"
	"github.com/example/autostream-control-panel/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("autostream-updater: %v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Printf("autostream-updater %s\ncommit: %s\nbuild_date: %s\n", version.Current(), version.Commit, version.BuildDate)
		return nil
	}
	if len(args) > 0 && args[0] == "validate-config" {
		flags := flag.NewFlagSet("validate-config", flag.ContinueOnError)
		configPath := flags.String("config", "/etc/autostream/updater.json", "root-owned updater configuration")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("validate-config requires only --config PATH")
		}
		cfg, err := loadUpdaterConfig(*configPath)
		if err != nil {
			return err
		}
		if err := requireCentralConfig(cfg); err != nil {
			return err
		}
		if cfg.IsManagedBootstrap() {
			if managedValidationRunsAsRoot() {
				return errors.New("managed validate-config must run as the autostream-updater service user; use sudo -u autostream-updater autostream-updater validate-config --config /etc/autostream/updater.json")
			}
			policy, changed, err := (updateagent.PanelClient{BaseURL: cfg.PanelURL, Token: cfg.RuntimeToken}).FetchManagedPolicy(context.Background(), cfg.NodeID, 0)
			if err != nil {
				return err
			}
			if !changed || policy == nil {
				return errors.New("Control Panel did not return a managed updater policy")
			}
			cfg, err = policy.Materialize(cfg)
			if err != nil {
				return err
			}
		}
		results, err := updateagent.ValidateCentralHosts(context.Background(), cfg, updateagent.SSHRemoteExecutor{})
		if err != nil {
			return err
		}
		for _, result := range results {
			fmt.Println(result)
		}
		fmt.Println("configuration and runtime targets valid")
		return nil
	}
	if len(args) > 0 && args[0] == "configure" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runUpdaterConfigure(ctx, args[1:], defaultUpdaterConfigureDependencies())
	}
	if len(args) == 0 || args[0] == "run" {
		if len(args) > 0 {
			args = args[1:]
		}
		flags := flag.NewFlagSet("run", flag.ContinueOnError)
		configPath := flags.String("config", "/etc/autostream/updater.json", "root-owned updater configuration")
		if err := flags.Parse(args); err != nil {
			return err
		}
		cfg, err := loadUpdaterConfig(*configPath)
		if err != nil {
			return err
		}
		if err := requireCentralConfig(cfg); err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if cfg.IsManagedBootstrap() {
			return updateagent.NewManagedSupervisor(cfg).Run(ctx)
		}
		coordinator, err := updateagent.NewCentralCoordinator(cfg)
		if err != nil {
			return err
		}
		return coordinator.Run(ctx)
	}
	return errors.New("usage: autostream-updater configure --panel-url URL --node ID [--config PATH] | run --config PATH | validate-config --config PATH | --version")
}

func loadUpdaterConfig(path string) (updateagent.Config, error) {
	cfg, err := updateagent.LoadConfig(path, true)
	if err != nil {
		return updateagent.Config{}, err
	}
	if cfg.IsManagedBootstrap() {
		return updateagent.LoadManagedBootstrapConfig(path, true)
	}
	return cfg, nil
}

func requireCentralConfig(cfg updateagent.Config) error {
	if cfg.IsManagedBootstrap() {
		return nil
	}
	if len(cfg.Hosts) == 0 {
		return errors.New("central updater configuration requires at least one hosts entry")
	}
	return nil
}
