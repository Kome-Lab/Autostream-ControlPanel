package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"github.com/example/autostream-control-panel/internal/store"
)

func productionEnvironment() bool {
	for _, key := range []string{"AUTOSTREAM_ENV", "APP_ENV", "GO_ENV"} {
		if strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "production") {
			return true
		}
	}
	return false
}

func runtimeSecretTransportAllowed(r *http.Request) bool {
	if !productionEnvironment() {
		return true
	}
	if r.TLS != nil {
		return true
	}
	return trustedForwardedProto(r) == "https"
}

func trustedForwardedProto(r *http.Request) string {
	if !trustedProxy(remoteHost(r.RemoteAddr)) {
		return ""
	}
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if forwarded == "" {
		return ""
	}
	if idx := strings.Index(forwarded, ","); idx >= 0 {
		forwarded = forwarded[:idx]
	}
	return strings.ToLower(strings.TrimSpace(forwarded))
}

func productionMFASettingsAllowed(settings store.SecuritySettings) bool {
	if strings.TrimSpace(settings.MFAMode) == "disabled" {
		return false
	}
	requiredRoles := map[string]bool{}
	for _, role := range settings.MFARequiredRoles {
		role = strings.TrimSpace(role)
		if role != "" {
			requiredRoles[role] = true
		}
	}
	if len(requiredRoles) == 0 {
		return true
	}
	return requiredRoles["super_admin"]
}

func parseLimit(r *http.Request, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func requestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "request-id-unavailable"
	}
	return hex.EncodeToString(b[:])
}

func clientIP(r *http.Request) string {
	remote := remoteHost(r.RemoteAddr)
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwarded != "" && trustedProxy(remote) {
		current, err := netip.ParseAddr(remote)
		if err != nil {
			return remote
		}
		current = current.Unmap()
		hops := strings.Split(forwarded, ",")
		for i := len(hops) - 1; i >= 0; i-- {
			hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			if err != nil {
				return remote
			}
			if !trustedProxy(current.String()) {
				return current.String()
			}
			current = hop.Unmap()
		}
		return current.String()
	}
	if remote != "" {
		return remote
	}
	if raw := strings.TrimSpace(r.RemoteAddr); raw != "" {
		return raw
	}
	return "unknown"
}

func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	if idx := strings.LastIndex(remoteAddr, ":"); idx > 0 && !strings.Contains(remoteAddr[idx+1:], ":") {
		return remoteAddr[:idx]
	}
	return strings.Trim(remoteAddr, "[]")
}

func trustedProxy(host string) bool {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	raw := strings.TrimSpace(os.Getenv("AUTOSTREAM_TRUSTED_PROXIES"))
	if raw == "" {
		return false
	}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(item); err == nil {
			if prefix.Contains(addr) {
				return true
			}
			continue
		}
		if trustedAddr, err := netip.ParseAddr(item); err == nil && trustedAddr.Unmap() == addr {
			return true
		}
	}
	return false
}
