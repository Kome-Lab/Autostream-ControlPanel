package netpolicy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	ErrInvalidServiceURL = errors.New("service public_url is invalid")
	ErrBlockedServiceURL = errors.New("service public_url is blocked by outbound policy")
)

type ServiceURLPolicy struct {
	AllowedHosts           map[string]struct{}
	AllowedCIDRs           []*net.IPNet
	PublicAllowedHosts     map[string]struct{}
	RequirePublicAllowlist bool
}

func ServiceURLPolicyFromEnv() ServiceURLPolicy {
	policy := ServiceURLPolicy{
		AllowedHosts:           map[string]struct{}{},
		PublicAllowedHosts:     map[string]struct{}{},
		RequirePublicAllowlist: envBoolDefault("AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS", true),
	}
	for _, value := range strings.Split(os.Getenv("AUTOSTREAM_SERVICE_ALLOWED_HOSTS"), ",") {
		if host := normalizeHost(value); host != "" {
			policy.AllowedHosts[host] = struct{}{}
		}
	}
	for _, value := range strings.Split(os.Getenv("AUTOSTREAM_SERVICE_ALLOWED_CIDRS"), ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err == nil {
			policy.AllowedCIDRs = append(policy.AllowedCIDRs, network)
		}
	}
	for _, value := range strings.Split(os.Getenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS"), ",") {
		if host := normalizeHost(value); host != "" {
			policy.PublicAllowedHosts[host] = struct{}{}
		}
	}
	return policy
}

func (p ServiceURLPolicy) ValidateURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ErrInvalidServiceURL
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return ErrInvalidServiceURL
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return ErrInvalidServiceURL
	}
	host := normalizeHost(parsed.Hostname())
	if host == "" {
		return ErrInvalidServiceURL
	}
	if p.hostAllowed(host) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && p.explicitCIDRAllowed(ip) {
		return nil
	}
	if parsed.Scheme == "http" {
		return ErrBlockedServiceURL
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ErrBlockedServiceURL
	}
	if ip := net.ParseIP(host); ip != nil && !p.ipAllowed(ip) {
		return ErrBlockedServiceURL
	}
	if !p.publicHostAllowed(host) {
		return ErrBlockedServiceURL
	}
	return nil
}

func (p ServiceURLPolicy) HTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	transport := &http.Transport{
		Proxy:       nil,
		DialContext: p.safeDialContext(),
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (p ServiceURLPolicy) safeDialContext() func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrInvalidServiceURL
		}
		normalizedHost := normalizeHost(host)
		if !p.hostAllowed(normalizedHost) && !p.publicHostAllowed(normalizedHost) {
			return nil, ErrBlockedServiceURL
		}
		resolved, err := net.DefaultResolver.LookupIPAddr(ctx, normalizedHost)
		if err != nil {
			return nil, errors.New("service public_url host resolution failed")
		}
		for _, candidate := range resolved {
			if !p.hostAllowed(normalizedHost) && !p.ipAllowed(candidate.IP) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		}
		return nil, ErrBlockedServiceURL
	}
}

func (p ServiceURLPolicy) hostAllowed(host string) bool {
	if len(p.AllowedHosts) == 0 {
		return false
	}
	_, ok := p.AllowedHosts[normalizeHost(host)]
	return ok
}

func (p ServiceURLPolicy) publicHostAllowed(host string) bool {
	if len(p.PublicAllowedHosts) == 0 {
		return !p.RequirePublicAllowlist
	}
	host = normalizeHost(host)
	if _, ok := p.PublicAllowedHosts[host]; ok {
		return true
	}
	for pattern := range p.PublicAllowedHosts {
		if strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, strings.TrimPrefix(pattern, "*")) {
			return true
		}
	}
	return false
}

func envBool(key string) bool {
	return envBoolDefault(key, false)
}

func envBoolDefault(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func (p ServiceURLPolicy) ipAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, network := range p.AllowedCIDRs {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return !unsafeIP(ip)
}

func (p ServiceURLPolicy) explicitCIDRAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, network := range p.AllowedCIDRs {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func unsafeIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}
