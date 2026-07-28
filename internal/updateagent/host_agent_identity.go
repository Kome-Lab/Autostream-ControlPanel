package updateagent

import (
	"errors"
	"os"
)

const (
	HostAgentIdentityDir        = "/etc/autostream-host-agent"
	HostAgentIdentityPath       = "/etc/autostream-host-agent/identity.json"
	HostAgentStagedIdentityPath = "/etc/autostream-host-agent/identity.staged.json"
	HostAgentWipingIdentityPath = "/etc/autostream-host-agent/.identity.staged.wipe"
	LegacyHostAgentIdentityPath = "/etc/autostream/host-agent.json"
)

// LoadHostAgentIdentity keeps one read-only bridge for hosts installed before
// the dedicated identity directory existed. New writes and runtime rotations
// always target HostAgentIdentityPath.
func LoadHostAgentIdentity(path string, requireRootOwned bool) (Config, error) {
	if path != HostAgentIdentityPath {
		return LoadManagedBootstrapConfig(path, requireRootOwned)
	}
	_, currentErr := os.Lstat(HostAgentIdentityPath)
	_, legacyErr := os.Lstat(LegacyHostAgentIdentityPath)
	currentExists := currentErr == nil
	legacyExists := legacyErr == nil
	if currentExists && legacyExists {
		return Config{}, errors.New(
			"both current and legacy Host Agent identities exist; remove the legacy secret through the managed migration",
		)
	}
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		return Config{}, errors.New("stat current Host Agent identity")
	}
	if legacyErr != nil && !errors.Is(legacyErr, os.ErrNotExist) {
		return Config{}, errors.New("stat legacy Host Agent identity")
	}
	if currentExists {
		return LoadManagedBootstrapConfig(
			HostAgentIdentityPath, requireRootOwned,
		)
	}
	if legacyExists {
		return LoadManagedBootstrapConfig(
			LegacyHostAgentIdentityPath, requireRootOwned,
		)
	}
	return Config{}, &os.PathError{
		Op: "stat", Path: HostAgentIdentityPath, Err: os.ErrNotExist,
	}
}
