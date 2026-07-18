package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/example/autostream-control-panel/internal/updateagent"
	"github.com/example/autostream-control-panel/internal/version"
)

const defaultConfigPath = "/etc/autostream/update-host.json"

var loadRemoteSystemdBootstrapPaths = updateagent.LoadRemoteSystemdBootstrapPaths

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "autostream-update-host: %s\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Fprintf(stdout, "autostream-update-host %s\ncommit: %s\nbuild_date: %s\n", version.Current(), version.Commit, version.BuildDate)
		return nil
	}
	if len(args) == 0 {
		return errors.New("usage: validate-config, rpc, bootstrap-docker-target, or installer-systemd-paths")
	}
	switch args[0] {
	case "validate-config":
		flags := newFlagSet("validate-config", stderr)
		configPath := flags.String("config", defaultConfigPath, "root-owned helper configuration")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
			return errors.New("validate-config requires only --config PATH")
		}
		if updateagent.RequireRemoteHelperRoot() != nil {
			return errors.New("validate-config requires root")
		}
		if _, err := updateagent.ValidateRemoteHelperConfig(*configPath); err != nil {
			return errors.New("configuration rejected")
		}
		fmt.Fprintln(stdout, "configuration valid")
		return nil

	case "rpc":
		flags := newFlagSet("rpc", stderr)
		configPath := flags.String("config", defaultConfigPath, "root-owned helper configuration")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
			return errors.New("rpc requires only --config PATH")
		}
		if updateagent.RequireRemoteHelperRoot() != nil {
			return errors.New("rpc requires root")
		}
		if err := updateagent.RunRemoteHelperRPC(ctx, *configPath, getenv("SSH_ORIGINAL_COMMAND"), stdin, stdout); err != nil {
			return errors.New("rpc rejected")
		}
		return nil

	case "bootstrap-docker-target":
		flags := newFlagSet("bootstrap-docker-target", stderr)
		configPath := flags.String("config", defaultConfigPath, "root-owned draft helper configuration")
		targetID := flags.String("target", "", "configured Docker target ID")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *targetID == "" {
			return errors.New("bootstrap-docker-target requires --config PATH and --target ID")
		}
		if updateagent.RequireRemoteHelperRoot() != nil {
			return errors.New("bootstrap-docker-target requires root")
		}
		cfg, err := updateagent.LoadBootstrapRemoteHelperConfig(*configPath, *targetID, true)
		if err != nil {
			return errors.New("bootstrap configuration rejected")
		}
		token, err := updateagent.ReadRemoteBootstrapToken(stdin)
		if err != nil {
			return errors.New("bootstrap token rejected")
		}
		digest, err := updateagent.BootstrapRemoteDockerTarget(ctx, cfg, *targetID, token, nil)
		token = ""
		if err != nil {
			return errors.New("Docker bootstrap failed")
		}
		fmt.Fprintln(stdout, digest)
		return nil

	case "installer-systemd-paths":
		// This root-only local command is consumed by the verified installer. It
		// is not present in the forced sudo rule and cannot be selected over SSH.
		flags := newFlagSet("installer-systemd-paths", stderr)
		configPath := flags.String("config", defaultConfigPath, "root-owned helper configuration")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
			return errors.New("installer-systemd-paths requires only --config PATH")
		}
		if updateagent.RequireRemoteHelperRoot() != nil {
			return errors.New("installer-systemd-paths requires root")
		}
		paths, err := loadRemoteSystemdBootstrapPaths(*configPath)
		if err != nil {
			return errors.New("installer systemd path policy rejected")
		}
		for _, directory := range paths {
			if _, err := fmt.Fprintln(stdout, directory); err != nil {
				return errors.New("write installer systemd paths")
			}
		}
		return nil

	case "worker":
		// worker is an internal systemd transient-unit entrypoint. It is not
		// accepted over SSH and all of its argv values are non-secret paths.
		flags := newFlagSet("worker", stderr)
		configPath := flags.String("config", "", "root-owned helper configuration")
		requestPath := flags.String("request", "", "one-time root-owned request")
		resultPath := flags.String("result", "", "root-owned result path")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *configPath == "" || *requestPath == "" || *resultPath == "" {
			return errors.New("worker arguments rejected")
		}
		if updateagent.RequireRemoteHelperRoot() != nil {
			return errors.New("worker requires root")
		}
		if err := updateagent.RunRemoteHelperWorker(ctx, *configPath, *requestPath, *resultPath); err != nil {
			return errors.New("worker failed")
		}
		return nil
	default:
		return errors.New("usage: validate-config, rpc, bootstrap-docker-target, or installer-systemd-paths")
	}
}

func newFlagSet(name string, output io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(output)
	return flags
}
