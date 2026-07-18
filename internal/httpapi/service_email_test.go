package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestServiceEmailNotificationRequiresAuthenticatedRegisteredObservability(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()))
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/services/notifications/email", strings.NewReader(validServiceEmailBody())))
		if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), `"code":"missing_service_token"`) {
			t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
		}
	})

	t.Run("missing scope", func(t *testing.T) {
		auth := store.NewMemoryAuthStore()
		token, err := auth.CreateServiceToken(t.Context(), "observability", []string{"observability.ingest"})
		if err != nil {
			t.Fatal(err)
		}
		handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
		res := doServiceEmailRequest(t, handler, token.RawToken, validServiceEmailBody())
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), `"code":"missing_service_scope"`) {
			t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
		}
	})

	t.Run("wrong service type", func(t *testing.T) {
		auth := store.NewMemoryAuthStore()
		token, err := auth.CreateServiceToken(t.Context(), "worker", []string{"notifications.email.send"})
		if err != nil {
			t.Fatal(err)
		}
		registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "worker-01", ServiceType: "worker", ServiceName: "Worker", PublicURL: "https://worker.example.com"})
		handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
		res := doServiceEmailRequest(t, handler, token.RawToken, validServiceEmailBody())
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), `"code":"service_type_not_allowed"`) {
			t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
		}
	})

	t.Run("unregistered token", func(t *testing.T) {
		auth := store.NewMemoryAuthStore()
		token, err := auth.CreateServiceToken(t.Context(), "observability", []string{"notifications.email.send"})
		if err != nil {
			t.Fatal(err)
		}
		handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
		res := doServiceEmailRequest(t, handler, token.RawToken, validServiceEmailBody())
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), `"code":"service_token_not_registered"`) {
			t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
		}
	})
}

func TestServiceEmailNotificationRateLimitsRegisteredService(t *testing.T) {
	handler, auth, token, mailer := newServiceEmailTestServer(t, true)
	handler.serviceEmailLimiter = newServiceEmailRateLimiter(1, serviceEmailRateWindow)
	first := doServiceEmailRequest(t, handler, token.RawToken, validServiceEmailBody())
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d body = %s", first.Code, first.Body.String())
	}
	second := doServiceEmailRequest(t, handler, token.RawToken, validServiceEmailBody())
	if second.Code != http.StatusTooManyRequests || !strings.Contains(second.Body.String(), `"code":"rate_limited"`) {
		t.Fatalf("limited status = %d body = %s", second.Code, second.Body.String())
	}
	if second.Header().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q", second.Header().Get("Retry-After"))
	}
	if len(mailer.messages) != 1 {
		t.Fatalf("rate-limited request invoked mailer: %#v", mailer.messages)
	}
	events := auth.AuditEvents()
	if len(events) != 2 || events[1].Result != "failure" || events[1].Metadata["reason"] != "rate_limited" || events[1].Metadata["recipient_count"] != 0 {
		t.Fatalf("rate-limit audit = %#v", events)
	}
	assertServiceEmailSecretsAbsent(t, second.Body.String()+toJSONForTest(t, []store.AuditEvent{events[1]}))
}

func TestServiceEmailNotificationRejectsUnsafeOrOversizedPayload(t *testing.T) {
	handler, _, token, mailer := newServiceEmailTestServer(t, true)
	tests := map[string]string{
		"malformed":           `{`,
		"unknown field":       `{"recipients":["ops@example.jp"],"subject":"Alert","text":"body","smtp_password":"secret"}`,
		"no recipients":       `{"recipients":[],"subject":"Alert","text":"body"}`,
		"too many recipients": serviceEmailBodyWithRecipients(21),
		"duplicate recipient": `{"recipients":["ops@example.jp","OPS@example.jp"],"subject":"Alert","text":"body"}`,
		"header recipient":    `{"recipients":["ops@example.jp\r\nBcc: bad@example.jp"],"subject":"Alert","text":"body"}`,
		"display recipient":   `{"recipients":["Ops <ops@example.jp>"],"subject":"Alert","text":"body"}`,
		"empty subject":       `{"recipients":["ops@example.jp"],"subject":" ","text":"body"}`,
		"header subject":      `{"recipients":["ops@example.jp"],"subject":"Alert\r\nBcc: bad@example.jp","text":"body"}`,
		"long subject":        `{"recipients":["ops@example.jp"],"subject":"` + strings.Repeat("a", maxServiceEmailSubjectRunes+1) + `","text":"body"}`,
		"empty text":          `{"recipients":["ops@example.jp"],"subject":"Alert","text":" \n"}`,
		"long text":           `{"recipients":["ops@example.jp"],"subject":"Alert","text":"` + strings.Repeat("a", maxServiceEmailTextBytes+1) + `"}`,
		"NUL html":            `{"recipients":["ops@example.jp"],"subject":"Alert","text":"body","html":"<p>unsafe\u0000html</p>"}`,
		"long html":           `{"recipients":["ops@example.jp"],"subject":"Alert","text":"body","html":"` + strings.Repeat("a", maxServiceEmailHTMLBytes+1) + `"}`,
		"trailing json":       validServiceEmailBody() + `{}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			res := doServiceEmailRequest(t, handler, token.RawToken, body)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), `"code":"bad_request"`) && !strings.Contains(res.Body.String(), `"code":"invalid_email_notification"`) {
				t.Fatalf("unsafe error response: %s", res.Body.String())
			}
		})
	}
	if len(mailer.messages) != 0 {
		t.Fatalf("invalid payload invoked mailer: %#v", mailer.messages)
	}
}

func TestServiceEmailNotificationRequiresGlobalSMTPSettings(t *testing.T) {
	handler, auth, token, mailer := newServiceEmailTestServer(t, false)
	res := doServiceEmailRequest(t, handler, token.RawToken, validServiceEmailBody())
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), `"code":"smtp_not_configured"`) {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if len(mailer.messages) != 0 {
		t.Fatalf("unconfigured SMTP invoked mailer: %#v", mailer.messages)
	}
	assertServiceEmailSecretsAbsent(t, res.Body.String()+toJSONForTest(t, auth.AuditEvents()))
}

func TestServiceEmailNotificationUsesGlobalSMTPWithoutLeakingMessage(t *testing.T) {
	handler, auth, token, mailer := newServiceEmailTestServer(t, true)
	body := `{"recipients":["ops@example.jp","oncall@example.jp"],"subject":"Production alert","text":"private incident details","html":"<!doctype html><p>private HTML incident details</p>"}`
	res := doServiceEmailRequest(t, handler, token.RawToken, body)
	if res.Code != http.StatusAccepted || !strings.Contains(res.Body.String(), `"status":"sent"`) || !strings.Contains(res.Body.String(), `"recipient_count":2`) {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if len(mailer.messages) != 2 || mailer.messages[0].To != "ops@example.jp" || mailer.messages[1].To != "oncall@example.jp" {
		t.Fatalf("unexpected messages: %#v", mailer.messages)
	}
	for i := range mailer.messages {
		if mailer.messages[i].Subject != "Production alert" || mailer.messages[i].Text != "private incident details" || mailer.messages[i].HTML != "<!doctype html><p>private HTML incident details</p>" {
			t.Fatalf("message %d was changed: %#v", i, mailer.messages[i])
		}
		if mailer.settings[i].SMTPHost != "smtp.example.jp" || mailer.passwords[i] != "raw-smtp-password" {
			t.Fatalf("global SMTP was not used: settings=%#v passwords=%#v", mailer.settings, mailer.passwords)
		}
	}
	responseAndAudit := res.Body.String() + toJSONForTest(t, auth.AuditEvents())
	assertServiceEmailSecretsAbsent(t, responseAndAudit)
	if !strings.Contains(responseAndAudit, `"recipient_count":2`) {
		t.Fatalf("recipient count missing from audit: %s", responseAndAudit)
	}
	events := auth.AuditEvents()
	if len(events) != 1 || events[0].Action != "notifications.email.send" || events[0].Result != "success" {
		t.Fatalf("unexpected relay audit: %#v", events)
	}
	if len(events[0].Metadata) != 2 || events[0].Metadata["recipient_count"] != 2 || events[0].Metadata["service_type"] != "observability" {
		t.Fatalf("relay audit must contain counts only: %#v", events[0].Metadata)
	}
}

func TestServiceEmailNotificationSanitizesMailerFailure(t *testing.T) {
	handler, auth, token, mailer := newServiceEmailTestServer(t, true)
	mailer.err = fmt.Errorf("delivery failed: %w", errors.New("smtp_auth_failed: raw-smtp-password rejected for ops@example.jp via smtp.example.jp"))
	res := doServiceEmailRequest(t, handler, token.RawToken, validServiceEmailBody())
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), `"code":"smtp_auth_failed"`) {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	assertServiceEmailSecretsAbsent(t, res.Body.String()+toJSONForTest(t, auth.AuditEvents()))
}

func TestNotificationChannelProxyCreatesGlobalSMTPPreservesModeAndSupportsMigration(t *testing.T) {
	type upstreamRequest struct {
		method string
		path   string
		body   map[string]any
	}
	var upstreamRequests []upstreamRequest
	obs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/notification-events" {
			writeJSON(w, http.StatusAccepted, []map[string]any{{"status": "success", "target": "masked"}})
			return
		}
		if r.URL.Path != "/notification-channels" && r.URL.Path != "/notification-channels/legacy-1" && r.URL.Path != "/notification-channels/global-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		upstreamRequests = append(upstreamRequests, upstreamRequest{method: r.Method, path: r.URL.Path, body: body})
		usesGlobalSMTP := body["uses_global_smtp"]
		if r.Method == http.MethodPut {
			if requestedMode, ok := body["uses_global_smtp"].(bool); ok {
				usesGlobalSMTP = requestedMode
			} else {
				usesGlobalSMTP = strings.HasSuffix(r.URL.Path, "/global-1")
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":                       strings.TrimPrefix(r.URL.Path, "/notification-channels/"),
			"name":                     "Ops email",
			"type":                     "email",
			"enabled":                  true,
			"uses_global_smtp":         usesGlobalSMTP,
			"email_recipients":         []string{"ops@example.jp"},
			"smtp_host":                "smtp.upstream.example.jp",
			"smtp_password":            "upstream-smtp-password",
			"masked_email_target":      "o***@example.jp",
			"smtp_password_configured": true,
		})
	}))
	defer obs.Close()

	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"notification_channels.create", "notification_channels.update"}); err != nil {
		t.Fatal(err)
	}
	registerObservabilityNodeForTest(t, auth, "observability-node-token", obs.URL)
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	requests := []struct {
		method         string
		path           string
		requestedMode  bool
		wantPublicMode bool
		migrate        bool
	}{
		{method: http.MethodPost, path: "/observability/notification-channels", requestedMode: false, wantPublicMode: true},
		{method: http.MethodPut, path: "/observability/notification-channels/legacy-1", requestedMode: true, wantPublicMode: false},
		{method: http.MethodPut, path: "/observability/notification-channels/global-1", requestedMode: false, wantPublicMode: true},
		{method: http.MethodPut, path: "/observability/notification-channels/legacy-1", requestedMode: false, wantPublicMode: true, migrate: true},
	}
	for _, request := range requests {
		body := fmt.Sprintf(`{"name":"Ops email","type":"email","enabled":true,"email_recipients":["ops@example.jp"],"uses_global_smtp":%t,"USES_GLOBAL_SMTP":%t,"smtp_host":"smtp.browser.example.jp","smtp_port":587,"smtp_tls":true,"smtp_from":"from@example.jp","smtp_username":"browser-user","smtp_password":"browser-smtp-password","SMTP_SERVER":"bypass.example.jp"}`, request.requestedMode, !request.requestedMode)
		if request.migrate {
			body = fmt.Sprintf(`{"name":"Ops email","type":"email","enabled":true,"email_recipients":["ops@example.jp"],"uses_global_smtp":%t,"USES_GLOBAL_SMTP":%t,"migrate_to_global_smtp":true,"smtp_host":"smtp.browser.example.jp","smtp_password":"browser-smtp-password"}`, request.requestedMode, !request.requestedMode)
		}
		if request.method == http.MethodPost {
			body = fmt.Sprintf(`{"name":"Ops email","type":"email","enabled":true,"email_recipients":["ops@example.jp"],"uses_global_smtp":%t,"smtp_host":"smtp.browser.example.jp","smtp_port":587,"smtp_tls":true,"smtp_from":"from@example.jp","smtp_username":"browser-user","smtp_password":"browser-smtp-password","SMTP_SERVER":"bypass.example.jp"}`, request.requestedMode)
		}
		req := httptest.NewRequest(request.method, request.path, strings.NewReader(body))
		req.AddCookie(cookie)
		req.Header.Set("X-CSRF-Token", csrf)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		wantStatus := http.StatusOK
		if request.method == http.MethodPost {
			wantStatus = http.StatusCreated
		}
		if res.Code != wantStatus || strings.Contains(res.Body.String(), fmt.Sprintf(`"uses_global_smtp":%t`, !request.wantPublicMode)) || !strings.Contains(res.Body.String(), fmt.Sprintf(`"uses_global_smtp":%t`, request.wantPublicMode)) {
			t.Fatalf("%s status = %d body = %s", request.method, res.Code, res.Body.String())
		}
		for _, secret := range []string{"ops@example.jp", "smtp.browser.example.jp", "from@example.jp", "browser-user", "browser-smtp-password", "bypass.example.jp", "smtp.upstream.example.jp", "upstream-smtp-password"} {
			if strings.Contains(res.Body.String(), secret) {
				t.Fatalf("%s response leaked %q: %s", request.method, secret, res.Body.String())
			}
		}
	}

	if len(upstreamRequests) != 4 {
		t.Fatalf("upstream request count = %d", len(upstreamRequests))
	}
	for i, request := range upstreamRequests {
		if request.method == http.MethodPost && request.body["uses_global_smtp"] != true {
			t.Fatalf("create request did not force global SMTP: %#v", request.body)
		}
		if request.method == http.MethodPut {
			_, migrated := request.body["uses_global_smtp"]
			if migrated && request.body["uses_global_smtp"] != true {
				t.Fatalf("update request forwarded an invalid global SMTP migration: %#v", request.body)
			}
			if migrated != (i == 3) {
				t.Fatalf("update request changed existing global SMTP mode: %#v", request.body)
			}
			if _, ok := request.body["USES_GLOBAL_SMTP"]; ok {
				t.Fatalf("update request forwarded a case-variant global SMTP mode: %#v", request.body)
			}
			if _, ok := request.body["migrate_to_global_smtp"]; ok {
				t.Fatalf("update request forwarded the migration control field: %#v", request.body)
			}
		}
		for key := range request.body {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "smtp_") {
				t.Fatalf("request %d forwarded browser SMTP field %q: %#v", i, key, request.body)
			}
		}
	}

	var notificationAudits []store.AuditEvent
	for _, event := range auth.AuditEvents() {
		if event.Action == "notification_channels.create" || event.Action == "notification_channels.update" {
			notificationAudits = append(notificationAudits, event)
		}
	}
	if len(notificationAudits) != 4 {
		t.Fatalf("notification audits = %#v", notificationAudits)
	}
	for _, event := range notificationAudits {
		if event.Metadata["has_smtp_password"] != false {
			t.Fatalf("audit retained browser SMTP password status: %#v", event)
		}
	}
	auditJSON := toJSONForTest(t, notificationAudits)
	for _, secret := range []string{"ops@example.jp", "smtp.browser.example.jp", "from@example.jp", "browser-user", "browser-smtp-password", "bypass.example.jp"} {
		if strings.Contains(auditJSON, secret) {
			t.Fatalf("audit leaked %q: %s", secret, auditJSON)
		}
	}
}

func TestSanitizeNotificationChannelUpdateProxyPayloadOnlyMigratesExplicitTrue(t *testing.T) {
	tests := []struct {
		name         string
		migration    any
		wantMigrated bool
	}{
		{name: "true", migration: true, wantMigrated: true},
		{name: "false", migration: false, wantMigrated: false},
		{name: "string true", migration: "true", wantMigrated: false},
		{name: "number", migration: float64(1), wantMigrated: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := map[string]any{
				"name":                   "Ops email",
				"migrate_to_global_smtp": test.migration,
				"USES_GLOBAL_SMTP":       false,
				"smtp_password":          "browser-secret",
				" SMTP_HOST ":            "smtp.browser.example.jp",
			}
			sanitizeNotificationChannelUpdateProxyPayload(payload)

			mode, migrated := payload["uses_global_smtp"]
			if migrated != test.wantMigrated || (migrated && mode != true) {
				t.Fatalf("payload = %#v, want migrated = %t", payload, test.wantMigrated)
			}
			for key := range payload {
				normalized := strings.ToLower(strings.TrimSpace(key))
				if normalized == "migrate_to_global_smtp" || normalized == "uses_global_smtp" && key != "uses_global_smtp" || strings.HasPrefix(normalized, "smtp_") {
					t.Fatalf("unsafe control or SMTP field survived sanitization: %#v", payload)
				}
			}
		})
	}
}

func newServiceEmailTestServer(t *testing.T, configureSMTP bool) (*Server, *store.MemoryAuthStore, store.ServiceToken, *captureMailer) {
	t.Helper()
	auth := store.NewMemoryAuthStore()
	token, err := auth.CreateServiceToken(t.Context(), "observability", []string{"notifications.email.send"})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceWithTokenForTest(t, auth, token, store.ServiceRegistration{ServiceID: "observability-01", ServiceType: "observability", ServiceName: "Observability", PublicURL: "https://observability.example.com"})
	settings := store.NewMemoryAppSettingsStore()
	secrets := store.NewMemorySecretStore()
	if configureSMTP {
		if _, err := settings.UpdateAppSettings(t.Context(), store.AppSettings{
			AppName:                "AutoStream",
			Timezone:               "Asia/Tokyo",
			SMTPEnabled:            true,
			SMTPHost:               "smtp.example.jp",
			SMTPPort:               587,
			SMTPStartTLS:           true,
			SMTPFrom:               "noreply@example.jp",
			SMTPUsername:           "autostream",
			SMTPPasswordConfigured: true,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := secrets.UpdateSecret(t.Context(), store.AppSMTPPasswordSecretName, "raw-smtp-password"); err != nil {
			t.Fatal(err)
		}
	}
	mailer := &captureMailer{}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithServiceRegistryStore(auth), WithAppSettingsStore(settings), WithSecretStore(secrets), WithMailer(mailer))
	return handler, auth, token, mailer
}

func doServiceEmailRequest(t *testing.T, handler http.Handler, rawToken, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/services/notifications/email", bytes.NewBufferString(body))
	if rawToken != "" {
		req.Header.Set("Authorization", "Bearer "+rawToken)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func validServiceEmailBody() string {
	return `{"recipients":["ops@example.jp"],"subject":"Production alert","text":"private incident details"}`
}

func serviceEmailBodyWithRecipients(count int) string {
	recipients := make([]string, 0, count)
	for i := 0; i < count; i++ {
		recipients = append(recipients, fmt.Sprintf("ops-%d@example.jp", i))
	}
	body, _ := json.Marshal(serviceEmailNotificationRequest{Recipients: recipients, Subject: "Alert", Text: "body"})
	return string(body)
}

func assertServiceEmailSecretsAbsent(t *testing.T, value string) {
	t.Helper()
	for _, secret := range []string{"ops@example.jp", "oncall@example.jp", "Production alert", "private incident details", "private HTML incident details", "raw-smtp-password", "smtp.example.jp", "noreply@example.jp", "autostream"} {
		if strings.Contains(value, secret) {
			t.Fatalf("service email response or audit leaked %q: %s", secret, value)
		}
	}
}
