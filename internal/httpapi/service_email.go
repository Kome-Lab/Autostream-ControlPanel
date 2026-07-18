package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxServiceEmailRecipients   = 20
	maxServiceEmailSubjectRunes = 200
	maxServiceEmailTextBytes    = 16 * 1024
	serviceEmailRateLimit       = 60
	serviceEmailRateWindow      = time.Minute
)

type serviceEmailRateEntry struct {
	count       int
	windowStart time.Time
}

type serviceEmailRateLimiter struct {
	mu      sync.Mutex
	entries map[string]serviceEmailRateEntry
	now     func() time.Time
	limit   int
	window  time.Duration
}

func newServiceEmailRateLimiter(limit int, window time.Duration) *serviceEmailRateLimiter {
	return &serviceEmailRateLimiter{
		entries: map[string]serviceEmailRateEntry{},
		now:     time.Now,
		limit:   limit,
		window:  window,
	}
}

func (l *serviceEmailRateLimiter) allow(key string) bool {
	if l == nil {
		return true
	}
	key = strings.TrimSpace(key)
	if key == "" || l.limit < 1 || l.window <= 0 {
		return false
	}
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || now.Sub(entry.windowStart) >= l.window {
		l.entries[key] = serviceEmailRateEntry{count: 1, windowStart: now}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}

type serviceEmailNotificationRequest struct {
	Recipients []string `json:"recipients"`
	Subject    string   `json:"subject"`
	Text       string   `json:"text"`
}

func (s *Server) serviceEmailNotification(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "notifications.email.send")
	if !ok {
		return
	}
	if token.ServiceType != "observability" {
		s.writeServiceAudit(r, token, "notifications.email.send", "service", "", "failure", map[string]any{"reason": "service_type_not_allowed", "recipient_count": 0})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	service, registered, err := s.registeredServiceForToken(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	if !registered {
		s.writeServiceAudit(r, token, "notifications.email.send", "service", "", "failure", map[string]any{"reason": "service_token_not_registered", "recipient_count": 0})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_token_not_registered"})
		return
	}
	if !s.serviceEmailLimiter.allow(service.ServiceID) {
		s.writeServiceAudit(r, token, "notifications.email.send", "service", service.ServiceID, "failure", map[string]any{"reason": "rate_limited", "recipient_count": 0})
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "rate_limited"})
		return
	}

	var body serviceEmailNotificationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		s.writeServiceAudit(r, token, "notifications.email.send", "service", service.ServiceID, "failure", map[string]any{"reason": "bad_request", "recipient_count": 0})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		s.writeServiceAudit(r, token, "notifications.email.send", "service", service.ServiceID, "failure", map[string]any{"reason": "bad_request", "recipient_count": 0})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	recipients, subject, text, valid := normalizeServiceEmailNotification(body)
	if !valid {
		s.writeServiceAudit(r, token, "notifications.email.send", "service", service.ServiceID, "failure", map[string]any{"reason": "invalid_email_notification", "recipient_count": len(body.Recipients)})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_email_notification"})
		return
	}

	settings, password, status, code := s.mailSettingsForRequest(r.Context())
	if code != "" {
		s.writeServiceAudit(r, token, "notifications.email.send", "service", service.ServiceID, "failure", map[string]any{"reason": code, "recipient_count": len(recipients)})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	for _, recipient := range recipients {
		if err := s.mailer.Send(r.Context(), settings, password, MailMessage{To: recipient, Subject: subject, Text: text}); err != nil {
			code := safeErrorCode(err)
			s.writeServiceAudit(r, token, "notifications.email.send", "service", service.ServiceID, "failure", map[string]any{"reason": code, "recipient_count": len(recipients)})
			writeJSON(w, smtpTestStatus(code), map[string]string{"code": code})
			return
		}
	}
	s.writeServiceAudit(r, token, "notifications.email.send", "service", service.ServiceID, "success", map[string]any{"recipient_count": len(recipients)})
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "sent", "recipient_count": len(recipients)})
}

func normalizeServiceEmailNotification(body serviceEmailNotificationRequest) ([]string, string, string, bool) {
	if len(body.Recipients) == 0 || len(body.Recipients) > maxServiceEmailRecipients {
		return nil, "", "", false
	}
	recipients := make([]string, 0, len(body.Recipients))
	seen := make(map[string]struct{}, len(body.Recipients))
	for _, value := range body.Recipients {
		if len(value) > 320 || strings.ContainsRune(value, '\x00') {
			return nil, "", "", false
		}
		recipient, ok := normalizeSMTPTestRecipient(value)
		if !ok {
			return nil, "", "", false
		}
		key := strings.ToLower(recipient)
		if _, duplicate := seen[key]; duplicate {
			return nil, "", "", false
		}
		seen[key] = struct{}{}
		recipients = append(recipients, recipient)
	}

	subject := strings.TrimSpace(body.Subject)
	if subject == "" || len([]rune(subject)) > maxServiceEmailSubjectRunes || strings.ContainsAny(body.Subject, "\r\n\x00") {
		return nil, "", "", false
	}
	if strings.TrimSpace(body.Text) == "" || len(body.Text) > maxServiceEmailTextBytes || strings.ContainsRune(body.Text, '\x00') {
		return nil, "", "", false
	}
	return recipients, subject, body.Text, true
}
