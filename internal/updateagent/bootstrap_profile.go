package updateagent

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

const standardSystemdHelperStateDir = "/var/lib/autostream-update-host"

var databaseNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type standardSystemdProfile struct {
	port             int
	unit             string
	releaseRoot      string
	currentLink      string
	binaryPath       string
	requiredPaths    []string
	backupExecutable string
}

// BuildStandardSystemdHelperConfig expands the identity-only targets retained
// by a central updater into the fixed, privileged policy installed on one
// managed host. Paths, commands and loopback endpoints come only from this
// allowlist; callers may supply only the detected database name needed by the
// two database-owning services.
func BuildStandardSystemdHelperConfig(
	panelURL string,
	hostID string,
	arch string,
	targets []Target,
	databaseNames map[string]string,
) (HelperConfig, error) {
	for serviceType, databaseName := range databaseNames {
		profile, ok := standardSystemdProfileFor(serviceType)
		if !ok || profile.backupExecutable == "" {
			return HelperConfig{}, fmt.Errorf("databaseNames contains unsupported service_type %q", serviceType)
		}
		if !databaseNamePattern.MatchString(databaseName) {
			return HelperConfig{}, fmt.Errorf("database name for %s is invalid", serviceType)
		}
	}

	hostID = strings.TrimSpace(hostID)
	cfg := HelperConfig{
		SchemaVersion: HelperConfigSchemaVersion,
		HostID:        hostID,
		PanelURL:      strings.TrimSpace(panelURL),
		Arch:          strings.TrimSpace(arch),
		StateDir:      standardSystemdHelperStateDir,
		Targets:       make([]Target, 0, len(targets)),
	}
	for i, identity := range targets {
		if err := identity.ValidateCentralIdentity(); err != nil {
			return HelperConfig{}, fmt.Errorf("targets[%d]: %w", i, err)
		}
		if identity.HostID != hostID {
			return HelperConfig{}, fmt.Errorf("targets[%d]: host_id must match helper host_id", i)
		}
		if identity.DeploymentMode != ModeSystemd {
			return HelperConfig{}, fmt.Errorf("targets[%d]: standard helper profile supports systemd targets only", i)
		}
		profile, ok := standardSystemdProfileFor(identity.ServiceType)
		if !ok {
			return HelperConfig{}, fmt.Errorf("targets[%d]: unsupported service_type %q", i, identity.ServiceType)
		}

		target := Target{
			TargetID:       identity.TargetID,
			HostID:         hostID,
			ServiceType:    identity.ServiceType,
			DeploymentMode: ModeSystemd,
			HealthURL:      fmt.Sprintf("http://127.0.0.1:%d/health", profile.port),
			VersionURL:     fmt.Sprintf("http://127.0.0.1:%d/updater/version", profile.port),
			Systemd: &SystemdTarget{
				SystemctlPath: "/usr/bin/systemctl",
				RunuserPath:   "/usr/sbin/runuser",
				SmokeUser:     "autostream",
				Unit:          profile.unit,
				ReleaseRoot:   profile.releaseRoot,
				CurrentLink:   profile.currentLink,
				BinaryPath:    profile.binaryPath,
				RequiredPaths: append([]string(nil), profile.requiredPaths...),
			},
		}
		if profile.backupExecutable != "" {
			databaseName, ok := databaseNames[identity.ServiceType]
			if !ok {
				return HelperConfig{}, fmt.Errorf("targets[%d]: database name for %s is required", i, identity.ServiceType)
			}
			target.BackupArgv = []string{profile.backupExecutable, databaseName}
		}
		cfg.Targets = append(cfg.Targets, target)
	}

	if err := cfg.validateForArchitecture(arch); err != nil {
		return HelperConfig{}, fmt.Errorf("generated helper config: %w", err)
	}
	return cfg, nil
}

func standardSystemdProfileFor(serviceType string) (standardSystemdProfile, bool) {
	switch serviceType {
	case "control_panel":
		return standardSystemdProfile{
			port:             8080,
			unit:             "autostream-control-panel.service",
			releaseRoot:      "/opt/autostream/control-panel/releases",
			currentLink:      "/opt/autostream/control-panel/current",
			binaryPath:       "bin/control-panel",
			requiredPaths:    []string{"share/autostream-control-panel"},
			backupExecutable: "/usr/local/sbin/autostream-backup-control-panel",
		}, true
	case "encoder_recorder":
		return standardSystemdProfile{
			port:        8081,
			unit:        "autostream-encoder-recorder.service",
			releaseRoot: "/opt/autostream/encoder-recorder/releases",
			currentLink: "/opt/autostream/encoder-recorder/current",
			binaryPath:  "bin/autostream-encoder-recorder",
		}, true
	case "observability":
		return standardSystemdProfile{
			port:             8082,
			unit:             "autostream-observability.service",
			releaseRoot:      "/opt/autostream/observability/releases",
			currentLink:      "/opt/autostream/observability/current",
			binaryPath:       "bin/autostream-observability",
			backupExecutable: "/usr/local/sbin/autostream-backup-observability",
		}, true
	case "discord_bot":
		return standardSystemdProfile{
			port:        8083,
			unit:        "autostream-discord-bot.service",
			releaseRoot: "/opt/autostream/discord-bot/releases",
			currentLink: "/opt/autostream/discord-bot/current",
			binaryPath:  "bin/autostream-discord-bot",
		}, true
	case "worker":
		return standardSystemdProfile{
			port:        8084,
			unit:        "autostream-worker.service",
			releaseRoot: "/opt/autostream/worker/releases",
			currentLink: "/opt/autostream/worker/current",
			binaryPath:  "bin/autostream-worker",
		}, true
	default:
		return standardSystemdProfile{}, false
	}
}

// ValidateLegacyStandardSystemdHelperConfig loads a receipt-less helper
// configuration and permits automatic adoption only when every privileged
// field is exactly one of the central updater's fixed systemd profiles.
func ValidateLegacyStandardSystemdHelperConfig(path string) error {
	cfg, err := LoadHelperConfig(path, true)
	if err != nil {
		return err
	}
	return validateLegacyStandardSystemdHelperConfig(cfg)
}

func validateLegacyStandardSystemdHelperConfig(cfg HelperConfig) error {
	identities := make([]Target, 0, len(cfg.Targets))
	databaseNames := make(map[string]string)
	for i, target := range cfg.Targets {
		if target.DeploymentMode != ModeSystemd || target.Systemd == nil || target.Docker != nil {
			return fmt.Errorf("targets[%d]: legacy automatic adoption requires systemd only", i)
		}
		profile, ok := standardSystemdProfileFor(target.ServiceType)
		if !ok {
			return fmt.Errorf("targets[%d]: legacy automatic adoption service is unsupported", i)
		}
		if profile.backupExecutable == "" {
			if len(target.BackupArgv) != 0 {
				return fmt.Errorf("targets[%d]: legacy automatic adoption backup policy differs", i)
			}
		} else {
			if len(target.BackupArgv) != 2 ||
				target.BackupArgv[0] != profile.backupExecutable ||
				!databaseNamePattern.MatchString(target.BackupArgv[1]) {
				return fmt.Errorf("targets[%d]: legacy automatic adoption backup policy differs", i)
			}
			if existing, duplicate := databaseNames[target.ServiceType]; duplicate && existing != target.BackupArgv[1] {
				return fmt.Errorf("targets[%d]: legacy automatic adoption database policy differs", i)
			}
			databaseNames[target.ServiceType] = target.BackupArgv[1]
		}
		identities = append(identities, Target{
			TargetID:       target.TargetID,
			HostID:         target.HostID,
			ServiceType:    target.ServiceType,
			DeploymentMode: target.DeploymentMode,
		})
	}

	expected, err := BuildStandardSystemdHelperConfig(
		cfg.PanelURL,
		cfg.HostID,
		cfg.Arch,
		identities,
		databaseNames,
	)
	if err != nil {
		return err
	}
	for i := range cfg.Targets {
		cfg.Targets[i].presentFields = nil
	}
	if !reflect.DeepEqual(cfg, expected) {
		return errors.New("legacy helper configuration is not an exact standard systemd profile")
	}
	return nil
}
