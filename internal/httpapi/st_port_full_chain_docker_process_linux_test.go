//go:build linux

package httpapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/example/autostream-contracts/pkg/contracts"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateradapter"
)

// These are observations of the already-created isolated Docker fixture, not
// paths, Compose input, policy bytes, credentials, or a replacement authority.
type stPortChainDockerCPConfig struct {
	ComposeConfigSHA256 string `json:"compose_config_sha256"`
	ComposePolicySHA256 string `json:"compose_policy_sha256"`
	ComposeRevision     int64  `json:"compose_revision"`
	PublishedPort       int    `json:"published_port"`
	ContainerPort       int    `json:"container_port"`
	CurrentVersion      string `json:"current_version"`
	ContainerID         string `json:"container_id"`
	ImageID             string `json:"image_id"`
	RepositoryDigest    string `json:"repository_digest"`
	VersionEnvSHA256    string `json:"version_env_sha256"`
}

func (c stPortChainCPConfig) validateRuntime() error {
	if c.Mode == "systemd" && c.Docker == nil {
		return nil
	}
	d := c.Docker
	if c.Mode != "docker" || d == nil || d.PublishedPort != 18081 || d.ContainerPort != 8080 || d.ComposeRevision != 23 ||
		d.CurrentVersion != c.WorkerVersion || !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`).MatchString(d.CurrentVersion) {
		return errors.New("invalid fixed Docker fixture metadata")
	}
	hex64 := func(value string) bool {
		decoded, err := hex.DecodeString(value)
		return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
	}
	if !hex64(d.ComposeConfigSHA256) || !hex64(d.ComposePolicySHA256) || !hex64(d.ContainerID) {
		return errors.New("invalid Docker fixture observation")
	}
	for _, value := range []string{d.ImageID, d.RepositoryDigest, d.VersionEnvSHA256} {
		if !strings.HasPrefix(value, "sha256:") || !hex64(strings.TrimPrefix(value, "sha256:")) {
			return errors.New("invalid Docker fixture observation")
		}
	}
	return nil
}

func (c stPortChainCPConfig) initialLocalListenPort() int {
	if c.Mode == "docker" {
		return c.Docker.PublishedPort
	}
	return 18084
}

func (c stPortChainCPConfig) initialPortConfig() ([]byte, error) {
	if c.Mode == "docker" {
		return contracts.SystemUpdatePortDockerConfig(contracts.SystemUpdateTargetWorker, c.Docker.PublishedPort, c.Docker.ContainerPort, 31)
	}
	return contracts.SystemUpdatePortListenerConfig(contracts.SystemUpdateTargetWorker, contracts.SystemUpdateDeploymentSystemd, c.initialLocalListenPort(), 31)
}

func (d stPortChainDockerCPConfig) snapshot() contracts.SystemUpdatePortDockerSnapshot {
	return contracts.SystemUpdatePortDockerSnapshot{PublishedHostIP: "127.0.0.1", PublishedPort: d.PublishedPort,
		ContainerPort: d.ContainerPort, HealthPort: d.PublishedPort, ComposePolicySHA256: "sha256:" + d.ComposePolicySHA256,
		ComposeRevision: d.ComposeRevision, VersionEnvSHA256: d.VersionEnvSHA256, ImageID: d.ImageID, RepositoryDigest: d.RepositoryDigest}
}

func (f *stPortChainCP) primeDockerBootstrapTarget(ctx context.Context) error {
	if f.config.Mode != "docker" {
		return nil
	}
	body, err := f.config.initialPortConfig()
	if err != nil {
		return errors.New("materialize initial Docker mapping")
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE services SET applied_config_revision=31,applied_config_sha256=? WHERE service_id=?`,
		contracts.ComputeSystemUpdatePortBytesSHA256(body), stPortChainTarget); err != nil {
		return errors.New("align registered Docker fixture config")
	}
	return nil
}

func (f *stPortChainCP) primeDockerBootstrapPolicy(ctx context.Context, policy *store.UpdaterPolicy) error {
	if f.config.Mode != "docker" {
		return nil
	}
	// The existing activation API requires the installed Compose revision to
	// equal X. Bootstrap the fixture's X before activation instead of changing
	// the observed Compose revision or weakening that production constraint.
	policy.LocalExecutorPolicyRevision = f.config.Docker.ComposeRevision
	worker, err := f.auth.GetService(ctx, stPortChainTarget)
	if err != nil {
		return errors.New("read registered Docker fixture")
	}
	projection, err := f.rootProjection(*policy, worker)
	if err != nil {
		return errors.New("materialize Docker activation profile")
	}
	policy.LocalExecutorPolicySHA256 = projection.SHA256
	body, err := json.Marshal(policy)
	if err != nil {
		return errors.New("encode Docker activation policy")
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE update_agent_policies SET local_executor_policy_revision=?,policy_json=? WHERE service_id=?`,
		policy.LocalExecutorPolicyRevision, body, stPortChainAgent); err != nil {
		return errors.New("align Docker activation policy")
	}
	return nil
}

func (f *stPortChainCP) addDockerBootstrapCapabilities(capabilities map[string]any) error {
	if f.config.Mode != "docker" {
		return nil
	}
	body, err := f.config.initialPortConfig()
	if err != nil {
		return errors.New("materialize Docker bootstrap mapping")
	}
	d := f.config.Docker
	values := map[string]any{
		"reported_config_revisions": int64(31), "reported_config_sha256": contracts.ComputeSystemUpdatePortBytesSHA256(body),
		"reported_docker_port_capabilities": "v1", "reported_docker_published_ports": int64(d.PublishedPort),
		"reported_docker_container_ports": int64(d.ContainerPort), "reported_docker_health_ports": int64(d.PublishedPort),
		"reported_docker_compose_sha256": d.ComposeConfigSHA256, "reported_docker_compose_revisions": d.ComposeRevision,
		"reported_docker_version_env_sha256": d.VersionEnvSHA256, "reported_docker_container_ids": d.ContainerID,
		"reported_docker_image_ids": d.ImageID, "reported_docker_repository_digests": d.RepositoryDigest,
	}
	for name, value := range values {
		capabilities[name] = map[string]any{stPortChainTarget: value}
	}
	// This is isolated activation setup. The 2/1 port_policy_baseline is never
	// manufactured here; only the subsequently running Agent/root can send it.
	return nil
}

func (f *stPortChainCP) rootProjectionForInit(ctx context.Context, policy store.UpdaterPolicy, worker store.RegisteredService) (updateradapter.ConfigurePolicyProjection, error) {
	snapshot, err := f.updates.GetSystemUpdatePortPolicyProjection(ctx, stPortChainTarget, policy)
	if errors.Is(err, store.ErrNotFound) {
		if policy.Revision != 11 || policy.ProjectionRevision != 17 || policy.LocalExecutorPolicyRevision != 23 ||
			worker.AppliedEndpointRevision != 3 || worker.AppliedConfigRevision != 31 {
			return updateradapter.ConfigurePolicyProjection{}, errors.New("unbound initial fixture projection")
		}
		return f.rootProjection(policy, worker)
	}
	if err != nil {
		return updateradapter.ConfigurePolicyProjection{}, errors.New("current saved fixture projection unavailable")
	}
	var root updateradapter.LocalExecutorPolicy
	if stPortChainDecode(snapshot.Snapshot.RootPolicy, &root) != nil {
		return updateradapter.ConfigurePolicyProjection{}, errors.New("current saved fixture policy invalid")
	}
	projection, err := updateradapter.BuildConfigurePolicyProjection(root)
	if err != nil || !bytes.Equal(projection.Policy, snapshot.Snapshot.RootPolicy) || projection.SHA256 != policy.LocalExecutorPolicySHA256 ||
		projection.SHA256 != snapshot.Ref.ExecutorPolicySHA256 || projection.SourcePolicyRevision != policy.Revision ||
		projection.ProjectionRevision != policy.ProjectionRevision || projection.PolicyRevision != policy.LocalExecutorPolicyRevision {
		return updateradapter.ConfigurePolicyProjection{}, errors.New("current saved fixture projection mismatch")
	}
	// Init returns CP's exact current policy for observation. The parent never
	// reinstalls these bytes over the existing independent root on restart.
	return projection, nil
}
