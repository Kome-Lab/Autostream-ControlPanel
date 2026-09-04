package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNormalizePullUpdaterPolicyRequiresExplicitV2Authority(t *testing.T) {
	policy := validPullUpdaterPolicyForOwnership()
	policy.Targets[0].LocalListenPort = 18081
	normalized, err := NormalizeUpdaterPolicy("host-agent-a", policy)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.TransportMode != SystemUpdateTransportPullV2 ||
		normalized.ExecutionHostID != "host-a" ||
		normalized.Targets[0].HostID != "host-a" {
		t.Fatalf("normalized policy = %#v", normalized)
	}
	for name, mutate := range map[string]func(*UpdaterPolicy){
		"missing transport":     func(p *UpdaterPolicy) { p.TransportMode = "" },
		"unsupported transport": func(p *UpdaterPolicy) { p.TransportMode = "unsupported" },
		"missing host":          func(p *UpdaterPolicy) { p.ExecutionHostID = "" },
		"missing listener":      func(p *UpdaterPolicy) { p.Targets[0].LocalListenPort = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := policy
			candidate.Targets = append([]UpdaterPolicyTarget(nil), policy.Targets...)
			mutate(&candidate)
			if _, err := NormalizeUpdaterPolicy("host-agent-a", candidate); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("error = %v, want ErrInvalidSettings", err)
			}
		})
	}
}

func TestPullUpdaterPolicyTargetLocalListenPortNeverFallsBack(t *testing.T) {
	service := RegisteredService{AppliedEndpoint: &ServiceEndpoint{Port: 18081}}
	target := UpdaterPolicyTarget{
		TargetID: "worker-a", ServiceID: "worker-a", HostID: "host-a",
		ServiceType: "worker", DeploymentMode: "systemd",
	}
	if port, ok := PullUpdaterPolicyTargetLocalListenPort(target, service); ok || port != 0 {
		t.Fatalf("missing v2 listener resolved to (%d,%v)", port, ok)
	}
	target.LocalListenPort = 18082
	if port, ok := PullUpdaterPolicyTargetLocalListenPort(target, service); !ok || port != 18082 {
		t.Fatalf("explicit v2 listener resolved to (%d,%v)", port, ok)
	}
}

func TestPullUpdaterPolicyJSONContainsNoLocalRuntimeFallback(t *testing.T) {
	policy := validPullUpdaterPolicyForOwnership()
	policy.Targets[0].LocalListenPort = 18081
	policy.Targets[0].DatabaseName = ""
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{"local_listen_port", "api", "hosts", "release_token", "github_token"} {
		if strings.Contains(text, `"`+forbidden+`"`) {
			t.Fatalf("policy JSON contains removed field %q: %s", forbidden, text)
		}
	}
}

func TestPullUpdaterPolicyDatabaseBindingsAreExact(t *testing.T) {
	policy := validPullUpdaterPolicyForOwnership()
	policy.Targets[0].LocalListenPort = 18081
	policy.Targets = append(policy.Targets, UpdaterPolicyTarget{
		TargetID: "observability-a", ServiceID: "observability-a",
		ServiceType: "observability", DeploymentMode: "systemd",
		LocalListenPort: 18083, DatabaseName: "autostream_observability",
	})
	if _, err := NormalizeUpdaterPolicy("host-agent-a", policy); err != nil {
		t.Fatal(err)
	}
	policy.Targets[1].DatabaseName = ""
	if _, err := NormalizeUpdaterPolicy("host-agent-a", policy); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("missing database binding error = %v", err)
	}
}
