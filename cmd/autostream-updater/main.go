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
		cfg, err := updateagent.LoadConfig(*configPath, true)
		if err != nil {
			return err
		}
		if err := requireCentralConfig(cfg); err != nil {
			return err
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
		cfg, err := updateagent.LoadConfig(*configPath, true)
		if err != nil {
			return err
		}
		if err := requireCentralConfig(cfg); err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		coordinator, err := updateagent.NewCentralCoordinator(cfg)
		if err != nil {
			return err
		}
		return coordinator.Run(ctx)
	}
	return errors.New("usage: autostream-updater configure --panel-url URL --node ID [--config PATH] [--init-from PATH] | run --config PATH | validate-config --config PATH | --version")
}

func requireCentralConfig(cfg updateagent.Config) error {
	if len(cfg.Hosts) == 0 {
		return errors.New("central updater configuration requires at least one hosts entry")
	}
	return nil
}
