package netpolicy

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServiceURLPolicyRejectsUnsafeTargetsByDefault(t *testing.T) {
	policy := ServiceURLPolicy{}
	for _, raw := range []string{
		"http://127.0.0.1:8080",
		"http://localhost:8080",
		"http://10.0.0.10:8080",
		"http://169.254.169.254/latest/meta-data",
		"http://user:password@example.com",
		"file:///etc/passwd",
	} {
		if err := policy.ValidateURL(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
	if err := policy.ValidateURL("https://service.example.com"); err != nil {
		t.Fatalf("expected public HTTPS URL to be accepted: %v", err)
	}
}

func TestServiceURLPolicyFromEnvRequiresPublicAllowlistByDefault(t *testing.T) {
	policy := ServiceURLPolicyFromEnv()
	if !policy.RequirePublicAllowlist {
		t.Fatal("expected public service host allowlist to be required by default")
	}
	if err := policy.ValidateURL("https://service.example.com"); !errors.Is(err, ErrBlockedServiceURL) {
		t.Fatalf("expected public HTTPS URL to be blocked without allowlist, got %v", err)
	}
}

func TestServiceURLPolicyFromEnvCanDisablePublicAllowlistForDevelopment(t *testing.T) {
	t.Setenv("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", "false")
	policy := ServiceURLPolicyFromEnv()
	if policy.RequirePublicAllowlist {
		t.Fatal("expected explicit false to disable public service host allowlist requirement")
	}
	if err := policy.ValidateURL("https://service.example.com"); err != nil {
		t.Fatalf("expected public HTTPS URL to be accepted when allowlist is explicitly disabled: %v", err)
	}
}

func TestServiceURLPolicyAllowsExplicitHostAndCIDR(t *testing.T) {
	_, privateNetwork, err := net.ParseCIDR("10.10.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	policy := ServiceURLPolicy{
		AllowedHosts: map[string]struct{}{"host.docker.internal": {}},
		AllowedCIDRs: []*net.IPNet{privateNetwork},
	}
	if err := policy.ValidateURL("http://host.docker.internal:8081"); err != nil {
		t.Fatalf("explicit host was rejected: %v", err)
	}
	if err := policy.ValidateURL("http://10.10.2.4:8081"); err != nil {
		t.Fatalf("explicit CIDR was rejected: %v", err)
	}
	if err := policy.ValidateURL("http://10.11.2.4:8081"); !errors.Is(err, ErrBlockedServiceURL) {
		t.Fatalf("unexpected error for untrusted private IP: %v", err)
	}
}

func TestServiceURLPolicyCanRestrictPublicHosts(t *testing.T) {
	policy := ServiceURLPolicy{
		PublicAllowedHosts: map[string]struct{}{
			"encoder.example.com":    {},
			"*.services.example.com": {},
			"192.0.2.10":             {},
			"host.docker.internal":   {},
		},
		AllowedHosts: map[string]struct{}{"host.docker.internal": {}},
	}
	for _, raw := range []string{
		"https://encoder.example.com",
		"https://worker.services.example.com",
		"http://host.docker.internal:8080",
	} {
		if err := policy.ValidateURL(raw); err != nil {
			t.Fatalf("expected %q to be accepted: %v", raw, err)
		}
	}
	if err := policy.ValidateURL("http://192.0.2.10:8080"); !errors.Is(err, ErrBlockedServiceURL) {
		t.Fatalf("expected public HTTP service URL to be blocked, got %v", err)
	}
	if err := policy.ValidateURL("https://attacker.example.net"); !errors.Is(err, ErrBlockedServiceURL) {
		t.Fatalf("expected disallowed public host to be blocked, got %v", err)
	}
}

func TestServiceURLPolicyCanRequirePublicHostAllowlist(t *testing.T) {
	policy := ServiceURLPolicy{RequirePublicAllowlist: true}
	if err := policy.ValidateURL("https://encoder.example.com"); !errors.Is(err, ErrBlockedServiceURL) {
		t.Fatalf("expected public HTTPS URL to require an allowlist, got %v", err)
	}

	policy.PublicAllowedHosts = map[string]struct{}{"encoder.example.com": {}}
	if err := policy.ValidateURL("https://encoder.example.com"); err != nil {
		t.Fatalf("expected explicitly allowed public host to pass: %v", err)
	}
}

func TestServiceURLHTTPClientAllowsOnlyExplicitLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	blocked := ServiceURLPolicy{}.HTTPClient(time.Second)
	if _, err := blocked.Get(server.URL); !errors.Is(err, ErrBlockedServiceURL) {
		t.Fatalf("expected loopback dial to be blocked, got %v", err)
	}

	allowed := ServiceURLPolicy{AllowedHosts: map[string]struct{}{"127.0.0.1": {}}}.HTTPClient(time.Second)
	response, err := allowed.Get(server.URL)
	if err != nil {
		t.Fatalf("explicit loopback host was rejected: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}
}

func TestServiceURLPolicyFromEnvIgnoresInvalidCIDR(t *testing.T) {
	t.Setenv("AUTOSTREAM_SERVICE_ALLOWED_HOSTS", "host.docker.internal, Encoder.Internal.")
	t.Setenv("AUTOSTREAM_SERVICE_ALLOWED_CIDRS", "10.0.0.0/8,not-a-cidr")
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "encoder.example.com,*.services.example.com")
	policy := ServiceURLPolicyFromEnv()
	if !policy.hostAllowed("encoder.internal") {
		t.Fatal("normalized host allowlist was not loaded")
	}
	if !policy.publicHostAllowed("worker.services.example.com") {
		t.Fatal("public host allowlist was not loaded")
	}
	if len(policy.AllowedCIDRs) != 1 {
		t.Fatalf("unexpected CIDR count: %d", len(policy.AllowedCIDRs))
	}
}
