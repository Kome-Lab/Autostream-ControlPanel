package updateagent

import (
	"errors"
	"fmt"
	"path"
	"sort"
)

const remoteSystemdInstallRoot = "/opt/autostream"

var remoteSystemdServiceDirectories = map[string]string{
	"control_panel":    "control-panel",
	"worker":           "worker",
	"encoder_recorder": "encoder-recorder",
	"discord_bot":      "discord-bot",
	"observability":    "observability",
}

// LoadRemoteSystemdBootstrapPaths returns the only production directories that
// the host installer may create. Runtime stage/apply/reconcile operations never
// call this function and require these directories to exist already.
func LoadRemoteSystemdBootstrapPaths(configPath string) ([]string, error) {
	cfg, err := LoadHelperConfig(configPath, true)
	if err != nil {
		return nil, err
	}
	return remoteSystemdBootstrapPaths(cfg)
}

func remoteSystemdBootstrapPaths(cfg HelperConfig) ([]string, error) {
	unique := make(map[string]struct{})
	for i, target := range cfg.Targets {
		if target.DeploymentMode != ModeSystemd {
			continue
		}
		if target.Systemd == nil {
			return nil, fmt.Errorf("targets[%d]: systemd policy is missing", i)
		}
		serviceDir, ok := remoteSystemdServiceDirectories[target.ServiceType]
		if !ok {
			return nil, fmt.Errorf("targets[%d]: unsupported systemd service_type", i)
		}
		base := path.Join(remoteSystemdInstallRoot, serviceDir)
		releaseRoot := path.Join(base, "releases")
		currentLink := path.Join(base, "current")
		configuredReleaseRoot := target.Systemd.ReleaseRoot
		configuredCurrentLink := target.Systemd.CurrentLink
		if !path.IsAbs(configuredReleaseRoot) || path.Clean(configuredReleaseRoot) != configuredReleaseRoot || configuredReleaseRoot != releaseRoot {
			return nil, fmt.Errorf("targets[%d]: release_root must be %s", i, releaseRoot)
		}
		if !path.IsAbs(configuredCurrentLink) || path.Clean(configuredCurrentLink) != configuredCurrentLink || configuredCurrentLink != currentLink {
			return nil, fmt.Errorf("targets[%d]: current_link must be %s", i, currentLink)
		}
		unique[base] = struct{}{}
		unique[releaseRoot] = struct{}{}
	}

	paths := make([]string, 0, len(unique))
	for directory := range unique {
		if directory == remoteSystemdInstallRoot || !path.IsAbs(directory) || path.Clean(directory) != directory {
			return nil, errors.New("systemd bootstrap directory policy is invalid")
		}
		paths = append(paths, directory)
	}
	sort.Strings(paths)
	return paths, nil
}
