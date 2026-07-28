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
	"path"
	"strings"
	"syscall"

	"github.com/example/autostream-control-panel/internal/updateagent"
	"github.com/example/autostream-control-panel/internal/version"
)

const defaultLocalExecutorPolicyPath = "/etc/autostream-local-executor/policy.json"
const localExecutorUsage = "usage: autostream-local-executor run [--policy PATH] | recover-self-update --recovery-slot a|b | recover-runtime-credential --rotation-id ID --confirm-emergency-revoked | validate-policy [--policy PATH] | version"

type localExecutorCLIDependencies struct {
	LoadPolicy               func(string, bool) (updateagent.LocalExecutorPolicy, error)
	ServeExecutor            func(context.Context, string) error
	RecoverSelfUpdate        func(context.Context, string) error
	RecoverRuntimeCredential func(string) error
	RequireRoot              func() error
	Output                   io.Writer
}

func main() {
	if err := run(os.Args[1:], defaultLocalExecutorCLIDependencies()); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("autostream-local-executor: %v", err)
		os.Exit(1)
	}
}

func defaultLocalExecutorCLIDependencies() localExecutorCLIDependencies {
	return localExecutorCLIDependencies{
		LoadPolicy:               updateagent.LoadLocalExecutorPolicy,
		ServeExecutor:            updateagent.ServeLocalExecutor,
		RecoverSelfUpdate:        updateagent.RecoverHostSelfUpdate,
		RecoverRuntimeCredential: updateagent.RecoverRuntimeCredentialAfterEmergencyManualReconfigure,
		RequireRoot:              updateagent.RequireRemoteHelperRoot,
		Output:                   os.Stdout,
	}
}

func run(args []string, dependencies localExecutorCLIDependencies) error {
	if dependencies.LoadPolicy == nil || dependencies.ServeExecutor == nil || dependencies.Output == nil {
		return errors.New("local executor CLI dependencies are incomplete")
	}
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Fprintf(
			dependencies.Output,
			"autostream-local-executor %s\ncommit: %s\nbuild_date: %s\nmutation_protocol: %d\nrecovery_protocol: %d\n",
			version.Current(),
			version.Commit,
			version.BuildDate,
			updateagent.LocalExecutorMutationProtocolVersion,
			updateagent.HostSelfUpdateRecoveryProtocolVersion,
		)
		return nil
	}
	if len(args) == 0 {
		return errors.New(localExecutorUsage)
	}

	switch args[0] {
	case "run":
		policyPath, err := parsePolicyFlag("run", args[1:])
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return dependencies.ServeExecutor(ctx, policyPath)
	case "recover-self-update":
		if dependencies.RecoverSelfUpdate == nil {
			return errors.New("local executor recovery dependency is unavailable")
		}
		recoverySlot, err := parseRecoverySlot(args[1:])
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return dependencies.RecoverSelfUpdate(ctx, recoverySlot)
	case "recover-runtime-credential":
		if dependencies.RecoverRuntimeCredential == nil ||
			dependencies.RequireRoot == nil {
			return errors.New(
				"runtime credential recovery dependency is unavailable",
			)
		}
		rotationID, err := parseRuntimeCredentialRecovery(args[1:])
		if err != nil {
			return err
		}
		if err := dependencies.RequireRoot(); err != nil {
			return errors.New(
				"recover-runtime-credential requires root",
			)
		}
		if err := dependencies.RecoverRuntimeCredential(rotationID); err != nil {
			return errors.New(
				"runtime credential emergency recovery rejected",
			)
		}
		fmt.Fprintln(
			dependencies.Output,
			"runtime credential emergency recovery prepared",
		)
		return nil
	case "validate-policy":
		policyPath, err := parsePolicyFlag("validate-policy", args[1:])
		if err != nil {
			return err
		}
		policy, err := dependencies.LoadPolicy(policyPath, true)
		if err != nil {
			return err
		}
		digest, err := policy.SHA256()
		if err != nil {
			return err
		}
		fmt.Fprintf(
			dependencies.Output,
			"local executor policy valid\nhost_id: %s\nagent_uid: %d\nagent_gid: %d\npolicy_revision: %d\npolicy_sha256: %s\n",
			policy.HostID,
			policy.AgentUID,
			policy.AgentGID,
			policy.PolicyRevision,
			digest,
		)
		return nil
	default:
		return errors.New(localExecutorUsage)
	}
}

func parseRuntimeCredentialRecovery(args []string) (string, error) {
	flags := flag.NewFlagSet(
		"recover-runtime-credential",
		flag.ContinueOnError,
	)
	flags.SetOutput(io.Discard)
	rotationID := flags.String(
		"rotation-id",
		"",
		"exact runtime token rotation ID from the local root ledger",
	)
	confirmed := flags.Bool(
		"confirm-emergency-revoked",
		false,
		"confirm both rotation credentials were revoked at the Control Panel",
	)
	if err := flags.Parse(args); err != nil {
		return "", errors.New(
			"recover-runtime-credential arguments are invalid",
		)
	}
	if flags.NArg() != 0 ||
		strings.TrimSpace(*rotationID) == "" ||
		*rotationID != strings.TrimSpace(*rotationID) ||
		!*confirmed {
		return "", errors.New(
			"recover-runtime-credential requires exactly --rotation-id ID --confirm-emergency-revoked",
		)
	}
	return *rotationID, nil
}

func parseRecoverySlot(args []string) (string, error) {
	flags := flag.NewFlagSet("recover-self-update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	recoverySlot := flags.String("recovery-slot", "", "fixed A/B recovery slot")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() != 0 ||
		(*recoverySlot != updateagent.HostSelfUpdateSlotA &&
			*recoverySlot != updateagent.HostSelfUpdateSlotB) {
		return "", errors.New("recover-self-update requires exactly --recovery-slot a|b")
	}
	return *recoverySlot, nil
}

func parsePolicyFlag(command string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	policyPath := flags.String("policy", defaultLocalExecutorPolicyPath, "root-owned local executor policy")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("%s accepts only --policy PATH", command)
	}
	if !path.IsAbs(*policyPath) {
		return "", errors.New("local executor policy path must be absolute")
	}
	return *policyPath, nil
}
