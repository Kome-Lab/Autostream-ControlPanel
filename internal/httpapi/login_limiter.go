package httpapi

import (
	"strings"
	"sync"
	"time"
)

const defaultLoginFailureWindow = 5 * time.Minute

type loginFailureEntry struct {
	count       int
	windowStart time.Time
}

type loginFailureLimiter struct {
	mu      sync.Mutex
	entries map[string]loginFailureEntry
	now     func() time.Time
	window  time.Duration
}

func newLoginFailureLimiter() *loginFailureLimiter {
	return &loginFailureLimiter{
		entries: map[string]loginFailureEntry{},
		now:     time.Now,
		window:  defaultLoginFailureWindow,
	}
}

func loginFailureKey(username, ip string) string {
	return strings.ToLower(strings.TrimSpace(username)) + "\x00" + strings.TrimSpace(ip)
}

func (l *loginFailureLimiter) allow(key string, lockoutThreshold int) bool {
	if l == nil {
		return true
	}
	limit := lockoutThreshold - 1
	if limit < 1 {
		limit = 1
	}
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || now.Sub(entry.windowStart) >= l.window {
		if ok {
			delete(l.entries, key)
		}
		return true
	}
	return entry.count < limit
}

func (l *loginFailureLimiter) record(key string) {
	if l == nil {
		return
	}
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || now.Sub(entry.windowStart) >= l.window {
		entry = loginFailureEntry{windowStart: now}
	}
	entry.count++
	l.entries[key] = entry
}

func (l *loginFailureLimiter) clear(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}
