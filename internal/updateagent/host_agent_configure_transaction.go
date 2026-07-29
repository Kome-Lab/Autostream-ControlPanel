package updateagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	DefaultLocalExecutorPolicyPath      = "/etc/autostream-local-executor/policy.json"
	defaultSystemdPortSidecarDirectory  = "/opt/autostream/local-executor/ports"
	systemdPortSidecarConfigureMaxBytes = 1 << 10
)

// PreparedHostAgentConfiguration preflights both root-owned destinations
// before a one-time Configure Token is read. Commit initializes only missing
// canonical systemd sidecars, then installs the exact canonical policy and the
// inactive staged identity. If the identity rename has definitely not
// happened, the policy and newly created sidecars are rolled back.
type PreparedHostAgentConfiguration struct {
	identity *PreparedUpdaterConfig
	policy   *preparedLocalExecutorPolicy
	sidecars *preparedSystemdPortSidecars
}

func PrepareHostAgentConfiguration(
	identityPath, policyPath, installGroup string,
) (*PreparedHostAgentConfiguration, error) {
	identity, err := PrepareManagedIdentityConfig(identityPath, installGroup)
	if err != nil {
		return nil, err
	}
	policy, err := prepareLocalExecutorPolicy(policyPath)
	if err != nil {
		identity.Abort()
		return nil, err
	}
	sidecars, err := prepareSystemdPortSidecars(defaultSystemdPortSidecarDirectory)
	if err != nil {
		policy.Abort()
		identity.Abort()
		return nil, err
	}
	return &PreparedHostAgentConfiguration{
		identity: identity,
		policy:   policy,
		sidecars: sidecars,
	}, nil
}

func (p *PreparedHostAgentConfiguration) Commit(
	identity UpdaterConfigureIdentity,
	projection ConfigurePolicyProjection,
) error {
	if p == nil || p.identity == nil || p.policy == nil || p.sidecars == nil {
		return errors.New("Host Agent configuration transaction is not prepared")
	}
	canonicalPolicy, err := configurePolicyProjectionPolicy(projection)
	if err != nil {
		return err
	}
	if err := p.sidecars.Commit(canonicalPolicy); err != nil {
		return err
	}
	if err := p.policy.Commit(projection); err != nil {
		policyRollbackErr := p.policy.Rollback()
		var sidecarRollbackErr error
		if !p.policy.committed {
			sidecarRollbackErr = p.sidecars.Rollback()
		}
		if policyRollbackErr != nil || sidecarRollbackErr != nil {
			return fmt.Errorf(
				"install Local Executor policy: %v; rollback configuration: %w",
				err,
				errors.Join(policyRollbackErr, sidecarRollbackErr),
			)
		}
		return err
	}
	if err := p.identity.Commit(identity); err != nil {
		if !p.identity.committed {
			policyRollbackErr := p.policy.Rollback()
			var sidecarRollbackErr error
			if !p.policy.committed {
				sidecarRollbackErr = p.sidecars.Rollback()
			}
			if policyRollbackErr != nil || sidecarRollbackErr != nil {
				return fmt.Errorf(
					"install Host Agent identity: %v; rollback configuration: %w",
					err,
					errors.Join(policyRollbackErr, sidecarRollbackErr),
				)
			}
		}
		return err
	}
	return nil
}

func (p *PreparedHostAgentConfiguration) Abort() {
	if p == nil {
		return
	}
	if p.identity != nil {
		p.identity.Abort()
	}
	if p.policy != nil {
		p.policy.Abort()
	}
	if p.sidecars != nil {
		p.sidecars.Abort()
	}
}

func ValidateInstalledHostAgentConfiguration(
	identityPath, policyPath string,
	staged UpdaterStagedConfiguration,
) error {
	if err := ValidateInstalledUpdaterIdentity(identityPath, staged.Config); err != nil {
		return err
	}
	if staged.LocalExecutorPolicy == nil {
		return errors.New("staged Local Executor policy is missing")
	}
	payload, err := readRootPolicySnapshot(policyPath)
	if err != nil {
		return err
	}
	projection := staged.LocalExecutorPolicy
	if err := ValidateConfigurePolicyActivation(
		payload,
		projection.SHA256,
		projection.SourcePolicyRevision,
		projection.ProjectionRevision,
		projection.PolicyRevision,
	); err != nil {
		return fmt.Errorf("validate installed Local Executor policy: %w", err)
	}
	if !bytes.Equal(payload, projection.Policy) {
		return errors.New("installed Local Executor policy bytes do not match the staged projection")
	}
	policy, err := configurePolicyProjectionPolicy(*projection)
	if err != nil {
		return err
	}
	if err := validateInstalledSystemdPortSidecars(
		policy,
		defaultSystemdPortSidecarDirectory,
	); err != nil {
		return err
	}
	return nil
}

func configurePolicyProjectionPolicy(
	projection ConfigurePolicyProjection,
) (LocalExecutorPolicy, error) {
	if err := ValidateConfigurePolicyActivation(
		projection.Policy,
		projection.SHA256,
		projection.SourcePolicyRevision,
		projection.ProjectionRevision,
		projection.PolicyRevision,
	); err != nil {
		return LocalExecutorPolicy{}, err
	}
	var policy LocalExecutorPolicy
	if err := json.Unmarshal(projection.Policy, &policy); err != nil {
		return LocalExecutorPolicy{}, errors.New("decode canonical Local Executor policy")
	}
	return policy, nil
}

type initialSystemdPortSidecarPlan struct {
	ServiceID string
	Path      string
	Body      []byte
	SHA256    string
}

type initialSystemdPortSidecarSnapshot struct {
	Existed bool
	Body    []byte
}

func initialSystemdPortSidecarPlans(
	policy LocalExecutorPolicy,
	parent string,
) ([]initialSystemdPortSidecarPlan, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if policy.SchemaVersion != LocalExecutorMutationPolicySchemaVersion ||
		policy.ProtocolVersion != LocalExecutorMutationProtocolVersion {
		return nil, errors.New("initial systemd port sidecars require the mutation policy protocol")
	}
	if !cleanAbsoluteSystemdSidecarDirectory(parent) {
		return nil, errors.New("systemd port sidecar directory must be a clean absolute path")
	}
	plans := make([]initialSystemdPortSidecarPlan, 0, len(policy.Targets))
	seen := make(map[string]struct{}, len(policy.Targets))
	for _, target := range policy.Targets {
		if target.DeploymentMode != ModeSystemd {
			continue
		}
		if target.Systemd == nil {
			return nil, errors.New("systemd target is missing its fixed service definition")
		}
		adapter, err := hostAgentConfigureSystemdPortAdapterFor(
			target.ServiceType,
			target.Systemd.Unit,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"derive initial systemd port sidecar for %s: %w",
				target.ServiceID,
				err,
			)
		}
		if path.Dir(adapter.SidecarPath) != defaultSystemdPortSidecarDirectory {
			return nil, errors.New("fixed systemd port sidecar escaped its canonical directory")
		}
		sidecarPath := joinSystemdSidecarPath(
			parent,
			path.Base(adapter.SidecarPath),
		)
		if _, exists := seen[sidecarPath]; exists {
			return nil, errors.New("duplicate fixed systemd port sidecar path")
		}
		seen[sidecarPath] = struct{}{}
		body := systemdPortSidecarBytes(
			adapter.BindVariable,
			target.LocalListen.Host,
			target.LocalListen.Port,
			target.ConfigRevision,
		)
		digest := systemdPortSidecarSHA256(body)
		if target.ConfigSHA256 != digest {
			return nil, fmt.Errorf(
				"canonical systemd port sidecar digest does not match target %s",
				target.ServiceID,
			)
		}
		if bytes.Count(body, []byte{'\n'}) != 2 ||
			len(body) == 0 ||
			len(body) > systemdPortSidecarConfigureMaxBytes ||
			body[len(body)-1] != '\n' {
			return nil, errors.New("canonical systemd port sidecar is not exactly two bounded lines")
		}
		plans = append(plans, initialSystemdPortSidecarPlan{
			ServiceID: target.ServiceID,
			Path:      sidecarPath,
			Body:      append([]byte(nil), body...),
			SHA256:    digest,
		})
	}
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].Path < plans[j].Path
	})
	return plans, nil
}

type preparedSystemdPortSidecar struct {
	path          string
	existed       bool
	existing      []byte
	existingInfo  os.FileInfo
	tempPath      string
	temp          *os.File
	tempInfo      os.FileInfo
	created       bool
	createdInfo   os.FileInfo
	installedBody []byte
}

type preparedSystemdPortSidecars struct {
	parent    string
	entries   map[string]*preparedSystemdPortSidecar
	committed bool
}

func canonicalSystemdPortSidecarPaths(parent string) ([]string, error) {
	fixed := []struct {
		serviceType string
		unit        string
	}{
		{serviceType: "control_panel", unit: "autostream-control-panel.service"},
		{serviceType: "worker", unit: "autostream-worker.service"},
		{serviceType: "encoder_recorder", unit: "autostream-encoder-recorder.service"},
		{serviceType: "discord_bot", unit: "autostream-discord-bot.service"},
		{serviceType: "observability", unit: "autostream-observability.service"},
	}
	paths := make([]string, 0, len(fixed))
	for _, item := range fixed {
		adapter, err := hostAgentConfigureSystemdPortAdapterFor(
			item.serviceType,
			item.unit,
		)
		if err != nil {
			return nil, err
		}
		if path.Dir(adapter.SidecarPath) != defaultSystemdPortSidecarDirectory {
			return nil, errors.New("fixed systemd port sidecar escaped its canonical directory")
		}
		paths = append(
			paths,
			joinSystemdSidecarPath(parent, path.Base(adapter.SidecarPath)),
		)
	}
	sort.Strings(paths)
	return paths, nil
}

func cleanAbsoluteSystemdSidecarDirectory(value string) bool {
	if strings.HasPrefix(value, "/") {
		return path.IsAbs(value) && path.Clean(value) == value
	}
	return filepath.IsAbs(value) && filepath.Clean(value) == value
}

func joinSystemdSidecarPath(parent, name string) string {
	if strings.HasPrefix(parent, "/") {
		return path.Join(parent, name)
	}
	return filepath.Join(parent, name)
}

func prepareSystemdPortSidecars(
	parent string,
) (*preparedSystemdPortSidecars, error) {
	if err := validateSystemdPortSidecarDirectory(parent); err != nil {
		return nil, err
	}
	paths, err := canonicalSystemdPortSidecarPaths(parent)
	if err != nil {
		return nil, err
	}
	prepared := &preparedSystemdPortSidecars{
		parent:  parent,
		entries: make(map[string]*preparedSystemdPortSidecar, len(paths)),
	}
	failed := true
	defer func() {
		if failed {
			prepared.Abort()
		}
	}()
	for _, path := range paths {
		body, info, existed, err := readRootSystemdPortSidecarOptional(path)
		if err != nil {
			return nil, err
		}
		entry := &preparedSystemdPortSidecar{
			path:         path,
			existed:      existed,
			existing:     body,
			existingInfo: info,
		}
		prepared.entries[path] = entry
		if !existed {
			temp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".configure-*")
			if err != nil {
				return nil, errors.New("create initial systemd port sidecar temporary file")
			}
			entry.temp = temp
			entry.tempPath = temp.Name()
			if err := temp.Chown(0, 0); err != nil {
				return nil, errors.New("set initial systemd port sidecar temporary file ownership")
			}
			if err := temp.Chmod(0o600); err != nil {
				return nil, errors.New("set initial systemd port sidecar temporary file mode")
			}
			if err := temp.Sync(); err != nil {
				return nil, errors.New("sync initial systemd port sidecar temporary file")
			}
			entry.tempInfo, err = temp.Stat()
			if err != nil ||
				!entry.tempInfo.Mode().IsRegular() ||
				entry.tempInfo.Mode().Perm() != 0o600 ||
				!updaterConfigHasInstallOwner(entry.tempInfo, 0) {
				return nil, errors.New("initial systemd port sidecar temporary file is unsafe")
			}
		}
	}
	if err := prepared.verifyDestinations(); err != nil {
		return nil, err
	}
	if err := syncDirectory(parent); err != nil {
		return nil, errors.New("sync systemd port sidecar directory during preflight")
	}
	failed = false
	return prepared, nil
}

func (p *preparedSystemdPortSidecars) Commit(
	policy LocalExecutorPolicy,
) error {
	if p == nil || p.committed {
		return errors.New("initial systemd port sidecar update is not prepared")
	}
	plans, err := initialSystemdPortSidecarPlans(policy, p.parent)
	if err != nil {
		return err
	}
	if err := p.verifyDestinations(); err != nil {
		return err
	}
	snapshots := make(map[string]initialSystemdPortSidecarSnapshot, len(p.entries))
	for path, entry := range p.entries {
		snapshots[path] = initialSystemdPortSidecarSnapshot{
			Existed: entry.existed,
			Body:    append([]byte(nil), entry.existing...),
		}
	}
	if err := validateInitialSystemdPortSidecarSnapshots(plans, snapshots); err != nil {
		return err
	}
	for _, plan := range plans {
		entry, ok := p.entries[plan.Path]
		if !ok {
			return errors.New("canonical systemd port sidecar was not preflighted")
		}
		if entry.existed {
			continue
		}
		if err := entry.prepareBody(plan.Body); err != nil {
			return err
		}
	}
	if err := p.verifyDestinations(); err != nil {
		return err
	}
	for _, plan := range plans {
		entry := p.entries[plan.Path]
		if entry.existed {
			continue
		}
		if err := entry.installNoReplace(); err != nil {
			rollbackErr := p.Rollback()
			if rollbackErr != nil {
				return fmt.Errorf(
					"install initial systemd port sidecar: %v; rollback sidecars: %w",
					err,
					rollbackErr,
				)
			}
			return err
		}
	}
	if err := syncDirectory(p.parent); err != nil {
		rollbackErr := p.Rollback()
		if rollbackErr != nil {
			return fmt.Errorf(
				"sync initial systemd port sidecars: %v; rollback sidecars: %w",
				err,
				rollbackErr,
			)
		}
		return errors.New("sync initial systemd port sidecars")
	}
	if err := p.verifyDestinations(); err != nil {
		rollbackErr := p.Rollback()
		if rollbackErr != nil {
			return fmt.Errorf(
				"verify installed initial systemd port sidecars: %v; rollback sidecars: %w",
				err,
				rollbackErr,
			)
		}
		return err
	}
	p.committed = true
	return nil
}

func validateInitialSystemdPortSidecarSnapshots(
	plans []initialSystemdPortSidecarPlan,
	snapshots map[string]initialSystemdPortSidecarSnapshot,
) error {
	for _, plan := range plans {
		snapshot, ok := snapshots[plan.Path]
		if !ok {
			return errors.New("canonical systemd port sidecar was not preflighted")
		}
		if snapshot.Existed && !bytes.Equal(snapshot.Body, plan.Body) {
			return fmt.Errorf(
				"existing systemd port sidecar for %s differs from the active policy target",
				plan.ServiceID,
			)
		}
	}
	return nil
}

func (e *preparedSystemdPortSidecar) prepareBody(body []byte) error {
	if e == nil || e.temp == nil || e.tempInfo == nil || e.created {
		return errors.New("initial systemd port sidecar temporary file is unavailable")
	}
	if err := e.verifyTemporaryFile(); err != nil {
		return err
	}
	if err := e.temp.Truncate(0); err != nil {
		return errors.New("truncate initial systemd port sidecar temporary file")
	}
	if _, err := e.temp.Seek(0, io.SeekStart); err != nil {
		return errors.New("rewind initial systemd port sidecar temporary file")
	}
	if _, err := e.temp.Write(body); err != nil {
		return errors.New("write initial systemd port sidecar temporary file")
	}
	if err := e.temp.Chown(0, 0); err != nil {
		return errors.New("restore initial systemd port sidecar temporary file ownership")
	}
	if err := e.temp.Chmod(0o600); err != nil {
		return errors.New("restore initial systemd port sidecar temporary file mode")
	}
	if err := e.temp.Sync(); err != nil {
		return errors.New("sync initial systemd port sidecar temporary file")
	}
	e.installedBody = append([]byte(nil), body...)
	return e.verifyTemporaryFile()
}

func (e *preparedSystemdPortSidecar) installNoReplace() error {
	if e == nil || e.existed || e.created || len(e.installedBody) == 0 {
		return errors.New("initial systemd port sidecar is not ready for installation")
	}
	if err := e.verifyTemporaryFile(); err != nil {
		return err
	}
	if _, _, existed, err := readRootSystemdPortSidecarOptional(e.path); err != nil {
		return err
	} else if existed {
		return errors.New("systemd port sidecar destination appeared after preflight")
	}
	linkErr := os.Link(e.tempPath, e.path)
	pathInfo, pathErr := os.Lstat(e.path)
	if pathErr == nil && e.tempInfo != nil && os.SameFile(pathInfo, e.tempInfo) {
		e.created = true
		e.createdInfo = pathInfo
	} else if linkErr == nil {
		return errors.New("initial systemd port sidecar installed with an unsafe identity")
	}
	if linkErr != nil {
		if e.created {
			return errors.New("initial systemd port sidecar install result was uncertain")
		}
		return errors.New("install initial systemd port sidecar without replacing an existing file")
	}
	if !e.created ||
		pathInfo.Mode()&os.ModeSymlink != 0 ||
		!pathInfo.Mode().IsRegular() ||
		pathInfo.Mode().Perm() != 0o600 ||
		!updaterConfigHasInstallOwner(pathInfo, 0) {
		return errors.New("initial systemd port sidecar installed with unsafe ownership or mode")
	}
	if err := os.Remove(e.tempPath); err != nil {
		return errors.New("initial systemd port sidecar installed but temporary link cleanup failed")
	}
	e.tempPath = ""
	return nil
}

func (p *preparedSystemdPortSidecars) Rollback() error {
	if p == nil {
		return nil
	}
	var rollbackErr error
	removed := false
	paths := make([]string, 0, len(p.entries))
	for path := range p.entries {
		paths = append(paths, path)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, path := range paths {
		entry := p.entries[path]
		if !entry.created {
			continue
		}
		body, info, existed, err := readRootSystemdPortSidecarOptional(entry.path)
		if err != nil ||
			!existed ||
			entry.createdInfo == nil ||
			!os.SameFile(info, entry.createdInfo) ||
			!bytes.Equal(body, entry.installedBody) {
			rollbackErr = errors.Join(
				rollbackErr,
				fmt.Errorf(
					"new systemd port sidecar %s changed before rollback",
					filepath.Base(entry.path),
				),
			)
			continue
		}
		if err := os.Remove(entry.path); err != nil {
			rollbackErr = errors.Join(
				rollbackErr,
				fmt.Errorf(
					"remove new systemd port sidecar %s during rollback",
					filepath.Base(entry.path),
				),
			)
			continue
		}
		entry.created = false
		entry.createdInfo = nil
		removed = true
	}
	if removed {
		if err := syncDirectory(p.parent); err != nil {
			rollbackErr = errors.Join(
				rollbackErr,
				errors.New("sync systemd port sidecar directory during rollback"),
			)
		}
	}
	if rollbackErr == nil {
		p.committed = false
	}
	return rollbackErr
}

func (p *preparedSystemdPortSidecars) Abort() {
	if p == nil {
		return
	}
	for _, entry := range p.entries {
		if entry.temp != nil {
			_ = entry.temp.Close()
			entry.temp = nil
		}
		if entry.tempPath != "" {
			_ = os.Remove(entry.tempPath)
			entry.tempPath = ""
		}
	}
}

func (p *preparedSystemdPortSidecars) verifyDestinations() error {
	if p == nil {
		return errors.New("initial systemd port sidecar update is not prepared")
	}
	if err := validateSystemdPortSidecarDirectory(p.parent); err != nil {
		return err
	}
	for _, entry := range p.entries {
		body, info, existed, err := readRootSystemdPortSidecarOptional(entry.path)
		if err != nil {
			return err
		}
		if entry.created {
			if !existed ||
				entry.createdInfo == nil ||
				!os.SameFile(info, entry.createdInfo) ||
				!bytes.Equal(body, entry.installedBody) {
				return errors.New("new systemd port sidecar changed during configuration")
			}
			continue
		}
		if !entry.existed {
			if existed {
				return errors.New("systemd port sidecar destination appeared after preflight")
			}
			if err := entry.verifyTemporaryFile(); err != nil {
				return err
			}
			continue
		}
		if !existed ||
			!os.SameFile(info, entry.existingInfo) ||
			!bytes.Equal(body, entry.existing) {
			return errors.New("existing systemd port sidecar changed after preflight")
		}
	}
	return nil
}

func (e *preparedSystemdPortSidecar) verifyTemporaryFile() error {
	if e == nil || e.temp == nil || e.tempPath == "" || e.tempInfo == nil {
		return errors.New("initial systemd port sidecar temporary file is unavailable")
	}
	pathInfo, err := os.Lstat(e.tempPath)
	if err != nil ||
		pathInfo.Mode()&os.ModeSymlink != 0 ||
		!pathInfo.Mode().IsRegular() {
		return errors.New("initial systemd port sidecar temporary file changed after preflight")
	}
	openedInfo, err := e.temp.Stat()
	if err != nil ||
		!os.SameFile(pathInfo, openedInfo) ||
		!os.SameFile(e.tempInfo, openedInfo) ||
		openedInfo.Mode().Perm() != 0o600 ||
		!updaterConfigHasInstallOwner(openedInfo, 0) {
		return errors.New("initial systemd port sidecar temporary file changed after preflight")
	}
	return nil
}

func validateInstalledSystemdPortSidecars(
	policy LocalExecutorPolicy,
	parent string,
) error {
	if err := validateSystemdPortSidecarDirectory(parent); err != nil {
		return err
	}
	plans, err := initialSystemdPortSidecarPlans(policy, parent)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		body, _, existed, err := readRootSystemdPortSidecarOptional(plan.Path)
		if err != nil {
			return err
		}
		if !existed || !bytes.Equal(body, plan.Body) {
			return fmt.Errorf(
				"installed systemd port sidecar for %s does not match the staged policy",
				plan.ServiceID,
			)
		}
	}
	return nil
}

func validateSystemdPortSidecarDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("systemd port sidecar directory must be a clean absolute path")
	}
	if err := validateSecureRootPath(path, true); err != nil {
		return fmt.Errorf("systemd port sidecar directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil ||
		info.Mode()&os.ModeSymlink != 0 ||
		!info.IsDir() ||
		info.Mode().Perm() != 0o700 ||
		!updaterConfigHasInstallOwner(info, 0) {
		return errors.New("systemd port sidecar directory must be root:root 0700")
	}
	return nil
}

func readRootSystemdPortSidecarOptional(
	path string,
) ([]byte, os.FileInfo, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, errors.New("stat systemd port sidecar")
	}
	if info.Mode()&os.ModeSymlink != 0 ||
		!info.Mode().IsRegular() ||
		info.Size() <= 0 ||
		info.Size() > systemdPortSidecarConfigureMaxBytes ||
		info.Mode().Perm() != 0o600 {
		return nil, nil, false, errors.New("systemd port sidecar must be a bounded root:root 0600 regular non-symlink file")
	}
	file, openedInfo, err := openVerifiedConfig(path, info)
	if err != nil {
		return nil, nil, false, errors.New("open systemd port sidecar")
	}
	defer file.Close()
	if !updaterConfigHasInstallOwner(openedInfo, 0) {
		return nil, nil, false, errors.New("systemd port sidecar must be owned by root")
	}
	if err := validateRootOwnedFileAndParents(
		path,
		openedInfo,
		"systemd port sidecar",
	); err != nil {
		return nil, nil, false, err
	}
	data, err := io.ReadAll(io.LimitReader(
		file,
		systemdPortSidecarConfigureMaxBytes+1,
	))
	if err != nil ||
		len(data) == 0 ||
		len(data) > systemdPortSidecarConfigureMaxBytes {
		return nil, nil, false, errors.New("read systemd port sidecar")
	}
	return data, openedInfo, true, nil
}

type preparedLocalExecutorPolicy struct {
	path          string
	parent        string
	tempPath      string
	temp          *os.File
	tempInfo      os.FileInfo
	existing      []byte
	existingInfo  os.FileInfo
	existed       bool
	renamePath    func(string, string) error
	committed     bool
	committedInfo os.FileInfo
}

func prepareLocalExecutorPolicy(path string) (*preparedLocalExecutorPolicy, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("Local Executor policy path must be a clean absolute path")
	}
	if _, err := updaterConfigInstallGID("root"); err != nil {
		return nil, err
	}
	parent := filepath.Dir(path)
	if err := validateSecureRootPath(parent, true); err != nil {
		return nil, fmt.Errorf("Local Executor policy parent: %w", err)
	}
	existing, existingInfo, existed, err := readRootPolicySnapshotOptional(path)
	if err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(parent, ".policy.json.configure-*")
	if err != nil {
		return nil, errors.New("create Local Executor policy temporary file")
	}
	prepared := &preparedLocalExecutorPolicy{
		path: path, parent: parent, tempPath: temp.Name(), temp: temp,
		existing: existing, existingInfo: existingInfo, existed: existed,
		renamePath: os.Rename,
	}
	failed := true
	defer func() {
		if failed {
			prepared.Abort()
		}
	}()
	if err := temp.Chown(0, 0); err != nil {
		return nil, errors.New("set Local Executor policy temporary file ownership")
	}
	if err := temp.Chmod(0o600); err != nil {
		return nil, errors.New("set Local Executor policy temporary file mode")
	}
	if _, err := temp.Write([]byte("{}")); err != nil {
		return nil, errors.New("write Local Executor policy preflight file")
	}
	if err := temp.Sync(); err != nil {
		return nil, errors.New("sync Local Executor policy preflight file")
	}
	prepared.tempInfo, err = temp.Stat()
	if err != nil || !prepared.tempInfo.Mode().IsRegular() ||
		prepared.tempInfo.Mode().Perm() != 0o600 ||
		!updaterConfigHasInstallOwner(prepared.tempInfo, 0) {
		return nil, errors.New("Local Executor policy temporary file ownership or mode is unsafe")
	}
	if err := prepared.verifyDestination(); err != nil {
		return nil, err
	}
	if err := syncDirectory(parent); err != nil {
		return nil, errors.New("sync Local Executor policy directory during preflight")
	}
	failed = false
	return prepared, nil
}

func (p *preparedLocalExecutorPolicy) Commit(projection ConfigurePolicyProjection) error {
	if p == nil || p.temp == nil || p.committed {
		return errors.New("Local Executor policy update is not prepared")
	}
	if err := ValidateConfigurePolicyActivation(
		projection.Policy,
		projection.SHA256,
		projection.SourcePolicyRevision,
		projection.ProjectionRevision,
		projection.PolicyRevision,
	); err != nil {
		return err
	}
	if err := p.verifyTemporaryFile(); err != nil {
		return err
	}
	if err := p.verifyDestination(); err != nil {
		return err
	}
	if err := p.temp.Truncate(0); err != nil {
		return errors.New("truncate Local Executor policy temporary file")
	}
	if _, err := p.temp.Seek(0, io.SeekStart); err != nil {
		return errors.New("rewind Local Executor policy temporary file")
	}
	if _, err := io.Copy(p.temp, bytes.NewReader(projection.Policy)); err != nil {
		return errors.New("write canonical Local Executor policy")
	}
	if err := p.temp.Chown(0, 0); err != nil {
		return errors.New("restore Local Executor policy temporary file ownership")
	}
	if err := p.temp.Chmod(0o600); err != nil {
		return errors.New("restore Local Executor policy temporary file mode")
	}
	if err := p.temp.Sync(); err != nil {
		return errors.New("sync canonical Local Executor policy")
	}
	if err := p.verifyTemporaryFile(); err != nil {
		return err
	}
	if err := p.verifyDestination(); err != nil {
		return err
	}
	if p.renamePath == nil {
		return errors.New("Local Executor policy update is not prepared")
	}
	if err := p.renamePath(p.tempPath, p.path); err != nil {
		switch inspectPreparedRenameOutcome(p.tempPath, p.path, p.tempInfo) {
		case preparedRenameNotInstalled:
			return fmt.Errorf("install canonical Local Executor policy: %w", err)
		case preparedRenameInstalled:
			return p.finishCommittedInstall(
				p.tempInfo,
				fmt.Errorf(
					"canonical Local Executor policy was installed but rename reported an error: %w",
					err,
				),
			)
		default:
			return p.finishCommittedInstall(
				nil,
				fmt.Errorf(
					"canonical Local Executor policy install result is uncertain; inspect %s and %s before retrying: %w",
					p.path,
					p.tempPath,
					err,
				),
			)
		}
	}
	return p.finishCommittedInstall(p.tempInfo, nil)
}

func (p *preparedLocalExecutorPolicy) finishCommittedInstall(
	committedInfo os.FileInfo,
	finalErr error,
) error {
	p.committed = true
	p.committedInfo = committedInfo
	closeErr := p.temp.Close()
	p.temp = nil
	if closeErr != nil {
		finalErr = errors.Join(
			finalErr,
			errors.New("Local Executor policy installed but close failed"),
		)
	}
	if err := syncDirectory(p.parent); err != nil {
		finalErr = errors.Join(
			finalErr,
			errors.New("Local Executor policy installed but directory sync failed"),
		)
	}
	return finalErr
}

func (p *preparedLocalExecutorPolicy) Rollback() error {
	if p == nil || !p.committed {
		return nil
	}
	current, currentInfo, existed, err := readRootPolicySnapshotOptional(p.path)
	if err != nil || !existed || p.committedInfo == nil ||
		!os.SameFile(currentInfo, p.committedInfo) {
		return errors.New("installed Local Executor policy changed before rollback")
	}
	if !p.existed {
		if err := os.Remove(p.path); err != nil {
			return errors.New("remove newly installed Local Executor policy during rollback")
		}
		p.committed = false
		return syncDirectory(p.parent)
	}
	_ = current
	temp, err := os.CreateTemp(p.parent, ".policy.json.rollback-*")
	if err != nil {
		return errors.New("create Local Executor policy rollback file")
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chown(0, 0); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(p.existing); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, p.path); err != nil {
		return errors.New("restore previous Local Executor policy")
	}
	p.committed = false
	return syncDirectory(p.parent)
}

func (p *preparedLocalExecutorPolicy) Abort() {
	if p == nil || p.committed {
		return
	}
	if p.temp != nil {
		_ = p.temp.Close()
		p.temp = nil
	}
	if p.tempPath != "" {
		_ = os.Remove(p.tempPath)
	}
}

func (p *preparedLocalExecutorPolicy) verifyDestination() error {
	if err := validateSecureRootPath(p.parent, true); err != nil {
		return fmt.Errorf("Local Executor policy parent changed after preflight: %w", err)
	}
	current, currentInfo, existed, err := readRootPolicySnapshotOptional(p.path)
	if err != nil {
		return err
	}
	if !p.existed {
		if existed {
			return errors.New("Local Executor policy destination appeared after preflight")
		}
		return nil
	}
	if !existed || !os.SameFile(currentInfo, p.existingInfo) ||
		!bytes.Equal(current, p.existing) {
		return errors.New("Local Executor policy changed after preflight")
	}
	return nil
}

func (p *preparedLocalExecutorPolicy) verifyTemporaryFile() error {
	pathInfo, err := os.Lstat(p.tempPath)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!pathInfo.Mode().IsRegular() {
		return errors.New("Local Executor policy temporary file changed after preflight")
	}
	openedInfo, err := p.temp.Stat()
	if err != nil || !os.SameFile(pathInfo, openedInfo) ||
		!os.SameFile(p.tempInfo, openedInfo) ||
		openedInfo.Mode().Perm() != 0o600 ||
		!updaterConfigHasInstallOwner(openedInfo, 0) {
		return errors.New("Local Executor policy temporary file changed after preflight")
	}
	return nil
}

func readRootPolicySnapshot(path string) ([]byte, error) {
	data, _, existed, err := readRootPolicySnapshotOptional(path)
	if err != nil {
		return nil, err
	}
	if !existed {
		return nil, errors.New("installed Local Executor policy is missing")
	}
	return data, nil
}

func readRootPolicySnapshotOptional(path string) ([]byte, os.FileInfo, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("stat Local Executor policy: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Size() <= 0 || info.Size() > localExecutorPolicyMaxBytes ||
		info.Mode().Perm() != 0o600 {
		return nil, nil, false, errors.New("Local Executor policy must be a bounded root:root 0600 regular non-symlink file")
	}
	file, openedInfo, err := openVerifiedConfig(path, info)
	if err != nil {
		return nil, nil, false, err
	}
	defer file.Close()
	if !updaterConfigHasInstallOwner(openedInfo, 0) {
		return nil, nil, false, errors.New("Local Executor policy must be owned by root")
	}
	if err := validateRootOwnedFileAndParents(path, openedInfo, "Local Executor policy"); err != nil {
		return nil, nil, false, err
	}
	data, err := io.ReadAll(io.LimitReader(file, localExecutorPolicyMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > localExecutorPolicyMaxBytes {
		return nil, nil, false, errors.New("read Local Executor policy")
	}
	return data, openedInfo, true, nil
}
