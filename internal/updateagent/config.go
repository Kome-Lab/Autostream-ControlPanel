package updateagent

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	ServiceTypeUpdateAgent = "update_agent"
	ModeSystemd            = "systemd"
	ModeDocker             = "docker"
	configMaxBytes         = 1 << 20
)

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	unitPattern       = regexp.MustCompile(`^[A-Za-z0-9_.@-]+\.service$`)
	imagePattern      = regexp.MustCompile(`^ghcr\.io/[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+$`)
	envNamePattern    = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// Config is local-only. Jobs select a target ID and version; privileged paths,
// units, images and commands always come from this root-owned file.
type Config struct {
	PanelURL     string    `json:"panel_url"`
	NodeID       string    `json:"node_id"`
	RuntimeToken string    `json:"runtime_token"`
	ServiceName  string    `json:"service_name,omitempty"`
	GitHubToken  string    `json:"github_token,omitempty"`
	API          APIConfig `json:"api"`
	StateDir     string    `json:"state_dir"`
	// HelperArgv is retained only for the legacy per-host updater mode. A
	// central updater always invokes the fixed remote RPC command over SSH and
	// must not retain a local privileged-helper command line.
	HelperArgv               []string  `json:"helper_argv,omitempty"`
	PollIntervalSeconds      int       `json:"poll_interval_seconds,omitempty"`
	HeartbeatIntervalSeconds int       `json:"heartbeat_interval_seconds,omitempty"`
	Hosts                    []SSHHost `json:"hosts,omitempty"`
	Targets                  []Target  `json:"targets"`
	hostsSpecified           bool
}

// UnmarshalJSON distinguishes an absent hosts field (legacy local mode) from
// an explicitly present [] or null field (central mode, which must then fail
// closed unless valid hosts are supplied).
func (c *Config) UnmarshalJSON(data []byte) error {
	type configWire Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded configWire
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("config contains trailing data")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*c = Config(decoded)
	_, c.hostsSpecified = fields["hosts"]
	return nil
}

type APIConfig struct {
	BindHost    string `json:"bind_host,omitempty"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	SSLEnabled  bool   `json:"ssl_enabled"`
	TLSCertFile string `json:"tls_cert_file,omitempty"`
	TLSKeyFile  string `json:"tls_key_file,omitempty"`
}

type Target struct {
	TargetID       string         `json:"target_id"`
	HostID         string         `json:"host_id,omitempty"`
	ServiceType    string         `json:"service_type"`
	DeploymentMode string         `json:"deployment_mode"`
	HealthURL      string         `json:"health_url,omitempty"`
	VersionURL     string         `json:"version_url,omitempty"`
	BackupArgv     []string       `json:"backup_argv,omitempty"`
	Systemd        *SystemdTarget `json:"systemd,omitempty"`
	Docker         *DockerTarget  `json:"docker,omitempty"`
	presentFields  map[string]bool
}

// UnmarshalJSON keeps the exact field-presence information needed to enforce
// the central coordinator's identity-only target schema. Its own strict
// decoder is necessary because implementing UnmarshalJSON would otherwise
// bypass the parent decoder's DisallowUnknownFields setting for nested values.
func (t *Target) UnmarshalJSON(data []byte) error {
	type targetWire Target
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded targetWire
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("target contains trailing data")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*t = Target(decoded)
	t.presentFields = make(map[string]bool, len(fields))
	for name := range fields {
		t.presentFields[name] = true
	}
	return nil
}

type SystemdTarget struct {
	SystemctlPath string   `json:"systemctl_path"`
	RunuserPath   string   `json:"runuser_path"`
	SmokeUser     string   `json:"smoke_user"`
	Unit          string   `json:"unit"`
	ReleaseRoot   string   `json:"release_root"`
	CurrentLink   string   `json:"current_link"`
	BinaryPath    string   `json:"binary_path"`
	RequiredPaths []string `json:"required_paths,omitempty"`
}

type DockerTarget struct {
	DockerPath          string   `json:"docker_path"`
	ComposeProject      string   `json:"compose_project"`
	ProjectDir          string   `json:"project_dir"`
	ComposeFiles        []string `json:"compose_files"`
	Service             string   `json:"service"`
	ImageRepo           string   `json:"image_repo"`
	ImageVariable       string   `json:"image_variable"`
	VersionEnvFile      string   `json:"version_env_file"`
	ComposeConfigSHA256 string   `json:"compose_config_sha256"`
	CurrentVersion      string   `json:"current_version,omitempty"`
	Channel             string   `json:"channel,omitempty"`
}

func LoadConfig(path string, requireRootOwned bool) (Config, error) {
	return loadConfig(path, requireRootOwned, false)
}

// LoadBootstrapConfig accepts the explicit all-zero compose approval digest
// only for the one-time Docker bootstrap command. Runtime and privileged helper
// entrypoints always use LoadConfig and therefore fail closed on the sentinel.
func LoadBootstrapConfig(path string, requireRootOwned bool) (Config, error) {
	return loadConfig(path, requireRootOwned, true)
}

func loadConfig(path string, requireRootOwned, allowBootstrapSentinel bool) (Config, error) {
	if !filepath.IsAbs(path) {
		return Config{}, errors.New("config path must be absolute")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return Config{}, fmt.Errorf("stat config: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || pathInfo.Size() <= 0 || pathInfo.Size() > configMaxBytes {
		return Config{}, errors.New("config must be a bounded regular non-symlink file")
	}
	f, openedInfo, err := openVerifiedConfig(path, pathInfo)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	if requireRootOwned {
		if openedInfo.Mode().Perm()&0o007 != 0 || openedInfo.Mode().Perm()&0o022 != 0 {
			return Config{}, errors.New("config must be root-owned, not writable by group, and inaccessible to other users")
		}
		if err := validateRootOwnedFileAndParents(path, openedInfo, "config"); err != nil {
			return Config{}, err
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, configMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > configMaxBytes {
		return Config{}, errors.New("read config")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("config contains trailing data")
	}
	if err := cfg.validate(allowBootstrapSentinel); err != nil {
		return Config{}, err
	}
	if requireRootOwned {
		for i := range cfg.Hosts {
			if err := cfg.Hosts[i].validateRootOwnedFiles(); err != nil {
				return Config{}, fmt.Errorf("hosts[%d]: %w", i, err)
			}
		}
	}
	return cfg, nil
}

func openVerifiedConfig(path string, expected os.FileInfo) (*os.File, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.New("open config")
	}
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(expected, openedInfo) || expected.Size() != openedInfo.Size() || expected.Mode() != openedInfo.Mode() || !expected.ModTime().Equal(openedInfo.ModTime()) || openedInfo.Size() <= 0 || openedInfo.Size() > configMaxBytes {
		_ = file.Close()
		return nil, nil, errors.New("config changed during secure open")
	}
	return file, openedInfo, nil
}

func (c Config) Validate() error {
	return c.validate(false)
}

func (c Config) validate(allowBootstrapSentinel bool) error {
	if err := validatePanelURL(strings.TrimSpace(c.PanelURL)); err != nil {
		return err
	}
	if !identifierPattern.MatchString(strings.TrimSpace(c.NodeID)) {
		return errors.New("node_id is invalid")
	}
	if strings.TrimSpace(c.RuntimeToken) == "" {
		return errors.New("runtime_token is required")
	}
	if strings.TrimSpace(c.GitHubToken) == "" {
		return errors.New("github_token is required for private release artifacts")
	}
	if err := c.API.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(c.StateDir) || filepath.Clean(c.StateDir) == string(filepath.Separator) {
		return errors.New("state_dir must be a non-root absolute path")
	}
	centralMode := c.Hosts != nil || c.hostsSpecified
	if centralMode {
		if len(c.HelperArgv) != 0 {
			return errors.New("helper_argv is forbidden when hosts are configured")
		}
	} else {
		if len(c.HelperArgv) == 0 || !filepath.IsAbs(c.HelperArgv[0]) {
			return errors.New("helper_argv must begin with an absolute executable path")
		}
		for _, arg := range c.HelperArgv {
			if strings.TrimSpace(arg) == "" || strings.ContainsRune(arg, '\x00') {
				return errors.New("helper_argv contains an invalid argument")
			}
		}
	}
	if len(c.Targets) == 0 {
		return errors.New("at least one target is required")
	}
	hosts := make(map[string]SSHHost, len(c.Hosts))
	identityFiles := make(map[string]string, len(c.Hosts))
	identityFingerprints := make(map[string]string, len(c.Hosts))
	for i := range c.Hosts {
		host := c.Hosts[i]
		if err := host.Validate(); err != nil {
			return fmt.Errorf("hosts[%d]: %w", i, err)
		}
		if _, exists := hosts[host.HostID]; exists {
			return fmt.Errorf("duplicate host_id %q", host.HostID)
		}
		identityPath := filepath.Clean(host.IdentityFile)
		if runtime.GOOS == "windows" {
			identityPath = strings.ToLower(identityPath)
		}
		if previous, exists := identityFiles[identityPath]; exists {
			return fmt.Errorf("hosts[%d]: identity_file must be unique per host (already used by %s)", i, previous)
		}
		fingerprint, err := host.identityFingerprint()
		if err != nil {
			return fmt.Errorf("hosts[%d]: identity_file is not a usable SSH private key", i)
		}
		if previous, exists := identityFingerprints[fingerprint]; exists {
			return fmt.Errorf("hosts[%d]: SSH identity must be unique per host (already used by %s)", i, previous)
		}
		hosts[host.HostID] = host
		identityFiles[identityPath] = host.HostID
		identityFingerprints[fingerprint] = host.HostID
	}
	seen := map[string]bool{}
	versionFiles := map[string]bool{}
	for i := range c.Targets {
		var targetErr error
		if centralMode {
			targetErr = c.Targets[i].ValidateCentralIdentity()
		} else {
			targetErr = c.Targets[i].Validate()
		}
		if targetErr != nil {
			return fmt.Errorf("targets[%d]: %w", i, targetErr)
		}
		hostID := strings.TrimSpace(c.Targets[i].HostID)
		if centralMode {
			if hostID == "" {
				return fmt.Errorf("targets[%d]: host_id is required when hosts are configured", i)
			}
			if _, exists := hosts[hostID]; !exists {
				return fmt.Errorf("targets[%d]: host_id %q is not configured", i, hostID)
			}
		} else if hostID != "" {
			return fmt.Errorf("targets[%d]: host_id requires central hosts configuration", i)
		}
		if seen[c.Targets[i].TargetID] {
			return fmt.Errorf("duplicate target_id %q", c.Targets[i].TargetID)
		}
		seen[c.Targets[i].TargetID] = true
		if !centralMode {
			if docker := c.Targets[i].Docker; docker != nil {
				if docker.ComposeConfigSHA256 == strings.Repeat("0", 64) && !allowBootstrapSentinel {
					return fmt.Errorf("targets[%d]: compose_config_sha256 bootstrap sentinel is not allowed at runtime", i)
				}
				path := filepath.Clean(docker.VersionEnvFile)
				if versionFiles[path] {
					return fmt.Errorf("targets[%d]: version_env_file must be unique per Docker target", i)
				}
				versionFiles[path] = true
			}
		}
	}
	if c.PollIntervalSeconds < 0 || c.PollIntervalSeconds > 3600 {
		return errors.New("poll_interval_seconds must be between 0 and 3600")
	}
	if c.HeartbeatIntervalSeconds < 0 || c.HeartbeatIntervalSeconds > 3600 {
		return errors.New("heartbeat_interval_seconds must be between 0 and 3600")
	}
	return nil
}

func (a APIConfig) Validate() error {
	host := strings.Trim(strings.TrimSpace(a.Host), "[]")
	if host == "" || strings.ContainsAny(host, "/@?#") || a.Port < 1 || a.Port > 65535 {
		return errors.New("api.host and api.port are invalid")
	}
	bindHost := strings.Trim(strings.TrimSpace(a.BindHost), "[]")
	if bindHost != "" && strings.ContainsAny(bindHost, "/@?#") {
		return errors.New("api.bind_host is invalid")
	}
	if a.SSLEnabled && (!filepath.IsAbs(a.TLSCertFile) || !filepath.IsAbs(a.TLSKeyFile)) {
		return errors.New("TLS-enabled API requires absolute tls_cert_file and tls_key_file")
	}
	if !a.SSLEnabled && (!isLoopbackHost(host) || (bindHost != "" && !isLoopbackHost(bindHost))) {
		return errors.New("non-loopback updater API host or bind_host requires TLS")
	}
	return nil
}

func (a APIConfig) BindAddress() string {
	host := strings.Trim(strings.TrimSpace(a.BindHost), "[]")
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", a.Port))
}

func (a APIConfig) PublicURL() string {
	scheme := "http"
	if a.SSLEnabled {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(strings.Trim(strings.TrimSpace(a.Host), "[]"), fmt.Sprintf("%d", a.Port))
}

func (c Config) PollInterval() time.Duration {
	if c.PollIntervalSeconds <= 0 {
		return 15 * time.Second
	}
	return time.Duration(c.PollIntervalSeconds) * time.Second
}

func (c Config) HeartbeatInterval() time.Duration {
	if c.HeartbeatIntervalSeconds <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.HeartbeatIntervalSeconds) * time.Second
}

func (c Config) Target(id string) (Target, bool) {
	for _, target := range c.Targets {
		if target.TargetID == id {
			return target, true
		}
	}
	return Target{}, false
}

// Host returns one configured central-updater SSH host. Legacy configurations
// have no hosts and therefore always return false.
func (c Config) Host(id string) (SSHHost, bool) {
	for _, host := range c.Hosts {
		if host.HostID == id {
			return host, true
		}
	}
	return SSHHost{}, false
}

func (c Config) TargetsForHost(hostID string) []Target {
	var targets []Target
	for _, target := range c.Targets {
		if target.HostID == hostID {
			targets = append(targets, target)
		}
	}
	return targets
}

func (t Target) Validate() error {
	if !identifierPattern.MatchString(t.TargetID) {
		return errors.New("target_id is invalid")
	}
	switch t.ServiceType {
	case "control_panel", "worker", "encoder_recorder", "discord_bot", "observability":
	default:
		return fmt.Errorf("unsupported service_type %q", t.ServiceType)
	}
	if err := validateLoopbackEndpoint(t.HealthURL, "health_url"); err != nil {
		return err
	}
	if err := validateLoopbackEndpoint(t.VersionURL, "version_url"); err != nil {
		return err
	}
	if (t.ServiceType == "control_panel" || t.ServiceType == "observability") && len(t.BackupArgv) == 0 {
		return errors.New("backup_argv is required for database-owning services")
	}
	if len(t.BackupArgv) > 0 {
		if !filepath.IsAbs(t.BackupArgv[0]) {
			return errors.New("backup_argv must begin with an absolute executable path")
		}
		for _, arg := range t.BackupArgv {
			if strings.TrimSpace(arg) == "" || strings.ContainsRune(arg, '\x00') {
				return errors.New("backup_argv contains an invalid argument")
			}
		}
	}
	switch t.DeploymentMode {
	case ModeSystemd:
		if t.Systemd == nil || t.Docker != nil {
			return errors.New("systemd target must define only systemd configuration")
		}
		return t.Systemd.Validate()
	case ModeDocker:
		if t.Docker == nil || t.Systemd != nil {
			return errors.New("docker target must define only docker configuration")
		}
		return t.Docker.Validate()
	default:
		return fmt.Errorf("unsupported deployment_mode %q", t.DeploymentMode)
	}
}

// ValidateCentralIdentity deliberately accepts only the non-privileged target
// identity stored by a central updater. All host paths, commands, endpoints and
// images must exist solely in the destination's root-owned HelperConfig.
func (t Target) ValidateCentralIdentity() error {
	if !identifierPattern.MatchString(t.TargetID) || !identifierPattern.MatchString(t.HostID) {
		return errors.New("target_id and host_id are invalid")
	}
	switch t.ServiceType {
	case "control_panel", "worker", "encoder_recorder", "discord_bot", "observability":
	default:
		return fmt.Errorf("unsupported service_type %q", t.ServiceType)
	}
	if t.DeploymentMode != ModeSystemd && t.DeploymentMode != ModeDocker {
		return fmt.Errorf("unsupported deployment_mode %q", t.DeploymentMode)
	}
	for _, field := range []string{"health_url", "version_url", "backup_argv", "systemd", "docker"} {
		if t.presentFields[field] {
			return errors.New("central target must contain identity fields only")
		}
	}
	if t.HealthURL != "" || t.VersionURL != "" || len(t.BackupArgv) != 0 || t.Systemd != nil || t.Docker != nil {
		return errors.New("central target must contain identity fields only")
	}
	return nil
}

func (t SystemdTarget) Validate() error {
	if !filepath.IsAbs(t.SystemctlPath) {
		return errors.New("systemctl_path must be absolute")
	}
	if !filepath.IsAbs(t.RunuserPath) || !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(t.SmokeUser) || t.SmokeUser == "root" {
		return errors.New("runuser_path and smoke_user are required for unprivileged binary verification")
	}
	if !unitPattern.MatchString(t.Unit) {
		return errors.New("unit must be a fixed .service name")
	}
	for name, value := range map[string]string{"release_root": t.ReleaseRoot, "current_link": t.CurrentLink} {
		if !filepath.IsAbs(value) || filepath.Clean(value) == string(filepath.Separator) {
			return fmt.Errorf("%s must be a non-root absolute path", name)
		}
	}
	if filepath.Clean(t.ReleaseRoot) == filepath.Clean(t.CurrentLink) {
		return errors.New("release_root and current_link must differ")
	}
	paths := append([]string{t.BinaryPath}, t.RequiredPaths...)
	for _, path := range paths {
		if !safeRelativePath(path) {
			return fmt.Errorf("artifact path %q is unsafe", path)
		}
	}
	return nil
}

func (t DockerTarget) Validate() error {
	if !filepath.IsAbs(t.DockerPath) {
		return errors.New("docker_path must be absolute")
	}
	if !identifierPattern.MatchString(t.ComposeProject) || !identifierPattern.MatchString(t.Service) {
		return errors.New("compose_project and service must be fixed identifiers")
	}
	if !filepath.IsAbs(t.ProjectDir) || len(t.ComposeFiles) == 0 {
		return errors.New("project_dir and compose_files must be absolute")
	}
	for _, file := range t.ComposeFiles {
		if !filepath.IsAbs(file) {
			return errors.New("compose_files entries must be absolute")
		}
	}
	if !imagePattern.MatchString(t.ImageRepo) {
		return errors.New("image_repo must be a fixed ghcr.io repository without a tag")
	}
	if !envNamePattern.MatchString(t.ImageVariable) {
		return errors.New("image_variable is invalid")
	}
	if t.ImageVariable != "AUTOSTREAM_DOCKER_VERSION" {
		return errors.New("image_variable must be AUTOSTREAM_DOCKER_VERSION")
	}
	if !filepath.IsAbs(t.VersionEnvFile) || filepath.Clean(t.VersionEnvFile) == string(filepath.Separator) {
		return errors.New("version_env_file must be a non-root absolute path")
	}
	if t.Channel != "" && t.Channel != "docker" {
		return errors.New("docker channel must be docker")
	}
	if !versionPattern.MatchString(strings.TrimSpace(t.CurrentVersion)) {
		return errors.New("current_version must identify the currently deployed Docker bundle")
	}
	if len(t.ComposeConfigSHA256) != 64 || strings.ToLower(t.ComposeConfigSHA256) != t.ComposeConfigSHA256 {
		return errors.New("compose_config_sha256 must be a lowercase SHA256 digest")
	}
	if _, err := hex.DecodeString(t.ComposeConfigSHA256); err != nil {
		return errors.New("compose_config_sha256 must be a lowercase SHA256 digest")
	}
	return nil
}

func validatePanelURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("panel_url must be an absolute HTTP(S) URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("panel_url must not contain credentials, query, or fragment")
	}
	if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
		return errors.New("remote panel_url must use HTTPS")
	}
	return nil
}

func validateLoopbackEndpoint(raw, name string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute HTTP(S) URL", name)
	}
	if u.User != nil || !isLoopbackHost(u.Hostname()) {
		return fmt.Errorf("%s must use a loopback host", name)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func safeRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") || strings.ContainsRune(path, '\x00') {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func validateConfigFileSecurity(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > configMaxBytes {
		return errors.New("config must be a bounded regular non-symlink file")
	}
	if info.Mode().Perm()&0o007 != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("config must be root-owned, not writable by group, and inaccessible to other users")
	}
	if err := validateRootOwnedFileAndParents(path, info, "config"); err != nil {
		return err
	}
	return nil
}
