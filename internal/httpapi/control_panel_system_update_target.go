package httpapi

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"strconv"

	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/updateagent"
	"github.com/example/autostream-control-panel/internal/version"
)

const (
	controlPanelSystemUpdateServiceID   = "control-panel"
	controlPanelSystemUpdateServiceType = "control_panel"
	defaultControlPanelBindAddress      = "127.0.0.1:8080"
)

// controlPanelSystemUpdateService projects the running Control Panel into the
// same server-owned service shape used by pull_v2 targets. Control Panel cannot
// self-register, so this is intentionally limited to its exact fixed identity
// and to the local runtime endpoint already owned by this process.
func controlPanelSystemUpdateService() (store.RegisteredService, error) {
	bindAddress := os.Getenv("AUTOSTREAM_BIND_ADDR")
	if bindAddress == "" {
		bindAddress = defaultControlPanelBindAddress
	}
	host, rawPort, err := net.SplitHostPort(bindAddress)
	if err != nil {
		return store.RegisteredService{}, errors.New("Control Panel bind address is invalid")
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return store.RegisteredService{}, errors.New("Control Panel bind host must be an IP address")
	}
	if address != netip.MustParseAddr("127.0.0.1") {
		return store.RegisteredService{}, errors.New("Control Panel bind host must be 127.0.0.1")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1024 || port > 65535 {
		return store.RegisteredService{}, errors.New("Control Panel bind port must be unprivileged")
	}
	configRevision, err := controlPanelConfigRevision()
	if err != nil {
		return store.RegisteredService{}, err
	}
	configSHA256, err := updateagent.SystemdConfigurePortSidecarSHA256(
		controlPanelSystemUpdateServiceType,
		port,
		configRevision,
	)
	if err != nil {
		return store.RegisteredService{}, err
	}
	host = address.String()
	publicURL := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	desiredEndpoint := &store.ServiceEndpoint{
		Host:      host,
		Port:      port,
		PublicURL: publicURL,
	}
	appliedEndpoint := *desiredEndpoint
	currentVersion := version.Current()
	return store.RegisteredService{
		ServiceID:             controlPanelSystemUpdateServiceID,
		ServiceType:           controlPanelSystemUpdateServiceType,
		ServiceName:           "Control Panel",
		Host:                  host,
		Port:                  port,
		PublicURL:             publicURL,
		DesiredEndpoint:       desiredEndpoint,
		AppliedEndpoint:       &appliedEndpoint,
		EndpointRevision:      configRevision,
		EndpointStatus:        "applied",
		AppliedConfigRevision: configRevision,
		AppliedConfigSHA256:   configSHA256,
		Version:               currentVersion,
		ReportedVersion:       currentVersion,
		Status:                "online",
	}, nil
}

func addControlPanelSystemUpdateService(
	servicesByID map[string]store.RegisteredService,
) error {
	if servicesByID == nil {
		return errors.New("Control Panel service map is unavailable")
	}
	service, err := controlPanelSystemUpdateService()
	if err != nil {
		return err
	}
	servicesByID[controlPanelSystemUpdateServiceID] = service
	return nil
}

func updaterPolicyUsesControlPanelSystemUpdateTarget(policy store.UpdaterPolicy) bool {
	if policy.TransportMode != store.SystemUpdateTransportPullV2 {
		return false
	}
	for _, target := range policy.Targets {
		if target.TargetID == controlPanelSystemUpdateServiceID &&
			target.ServiceID == controlPanelSystemUpdateServiceID &&
			target.ServiceType == controlPanelSystemUpdateServiceType &&
			target.DeploymentMode == updateagent.ModeSystemd {
			return true
		}
	}
	return false
}

func addControlPanelSystemUpdateServiceForPolicy(
	servicesByID map[string]store.RegisteredService,
	policy store.UpdaterPolicy,
) error {
	if !updaterPolicyUsesControlPanelSystemUpdateTarget(policy) {
		return nil
	}
	return addControlPanelSystemUpdateService(servicesByID)
}

func controlPanelPullUpdaterActivationTarget(
	policy store.UpdaterPolicy,
) (*store.PullUpdaterControlPanelTarget, error) {
	if !updaterPolicyUsesControlPanelSystemUpdateTarget(policy) {
		return nil, nil
	}
	return controlPanelPullUpdaterRuntimeTarget()
}

func controlPanelPullUpdaterRuntimeTarget() (*store.PullUpdaterControlPanelTarget, error) {
	service, err := controlPanelSystemUpdateService()
	if err != nil {
		return nil, err
	}
	if service.AppliedEndpoint == nil {
		return nil, errors.New("Control Panel applied endpoint is unavailable")
	}
	endpoint := *service.AppliedEndpoint
	return &store.PullUpdaterControlPanelTarget{
		ServiceID:             service.ServiceID,
		ServiceType:           service.ServiceType,
		EndpointRevision:      service.EndpointRevision,
		AppliedConfigRevision: service.AppliedConfigRevision,
		AppliedConfigSHA256:   service.AppliedConfigSHA256,
		AppliedEndpoint:       endpoint,
	}, nil
}
