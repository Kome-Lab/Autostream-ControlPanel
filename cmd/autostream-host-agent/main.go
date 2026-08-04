package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/autostream-control-panel/internal/updateagent"
	"github.com/example/autostream-control-panel/internal/version"
)

const defaultHostAgentConfigPath = updateagent.HostAgentIdentityPath
const hostAgentUsage = "usage: autostream-host-agent run --config PATH | configure --panel-url URL --node ID [--config PATH] [--adopt-live-systemd-sidecar] | validate-config --config PATH | --version"

type hostAgentCLIDependencies struct {
	LoadIdentity func(string, bool) (updateagent.Config, error)
	Start        func(context.Context, updateagent.Config) error
	Configure    func(context.Context, []string) error
	Output       io.Writer
}

func main() {
	if err := run(os.Args[1:], defaultHostAgentCLIDependencies()); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("autostream-host-agent: %v", err)
		os.Exit(1)
	}
}

func defaultHostAgentCLIDependencies() hostAgentCLIDependencies {
	return hostAgentCLIDependencies{
		LoadIdentity: updateagent.LoadHostAgentIdentity,
		Start: func(ctx context.Context, identity updateagent.Config) error {
			agent, err := updateagent.NewHostPullAgent(identity, updateagent.HostPullAgentOptions{
				ObserveTargets: updateagent.NewLocalExecutorTargetObserver(updateagent.LocalExecutorClient{
					SocketPath: updateagent.LocalExecutorSocketPath,
				}),
			})
			if err != nil {
				return err
			}
			return agent.Run(ctx)
		},
		Configure: func(ctx context.Context, args []string) error {
			return runHostAgentConfigure(ctx, args, defaultHostAgentConfigureDependencies())
		},
		Output: os.Stdout,
	}
}

func run(args []string, dependencies hostAgentCLIDependencies) error {
	if dependencies.LoadIdentity == nil || dependencies.Start == nil || dependencies.Configure == nil || dependencies.Output == nil {
		return errors.New("host agent CLI dependencies are incomplete")
	}
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Fprintf(dependencies.Output, "autostream-host-agent %s\ncommit: %s\nbuild_date: %s\n", version.Current(), version.Commit, version.BuildDate)
		return nil
	}
	if len(args) == 0 {
		return errors.New(hostAgentUsage)
	}

	switch args[0] {
	case "configure":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return dependencies.Configure(ctx, args[1:])
	case "validate-config":
		configPath, err := parseHostAgentConfigFlag("validate-config", args[1:])
		if err != nil {
			return err
		}
		if _, err := dependencies.LoadIdentity(configPath, true); err != nil {
			return err
		}
		fmt.Fprintln(dependencies.Output, "host agent identity configuration valid")
		return nil
	case "run":
		configPath, err := parseHostAgentConfigFlag("run", args[1:])
		if err != nil {
			return err
		}
		identity, err := dependencies.LoadIdentity(configPath, true)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return dependencies.Start(ctx, identity)
	default:
		return errors.New(hostAgentUsage)
	}
}

func parseHostAgentConfigFlag(command string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", defaultHostAgentConfigPath, "root-owned host agent identity configuration")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("%s accepts only --config PATH", command)
	}
	return *configPath, nil
}
