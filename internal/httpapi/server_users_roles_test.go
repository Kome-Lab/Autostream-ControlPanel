package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
)

func TestCreateOAuthProviderDefaultRolesRequireRoleAssignmentPermission(t *testing.T) {
	t.Setenv("AUTOSTREAM_PUBLIC_URL", "https://control.example.com")
	auth := store.NewMemoryAuthStore()
	role, err := auth.CreateRole(t.Context(), "viewer", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"integrations.create"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithIntegrationStore(store.NewMemoryIntegrationStore()))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	body := fmt.Sprintf(`{"provider_type":"google","name":"Google Login","enabled":true,"client_id":"client-id","client_secret":"client-secret","allowed_domains":["example.com"],"auto_provision":true,"default_role_ids":[%q],"redirect_uri":"https://control.example.com/auth/oauth/callback"}`, role.ID)
	req := httptest.NewRequest(http.MethodPost, "/integrations/oauth-providers", bytes.NewBufferString(body))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected roles.assign requirement, status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestSecuritySettingsProductionRequiresSuperAdminRoleWhenScoped(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin"}, "correct horse battery", []string{"system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	missingSuperAdminReq := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":14,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"totp","mfa_required_roles":["admin"]}`))
	missingSuperAdminReq.AddCookie(cookie)
	missingSuperAdminReq.Header.Set("X-CSRF-Token", csrf)
	missingSuperAdminRes := httptest.NewRecorder()
	handler.ServeHTTP(missingSuperAdminRes, missingSuperAdminReq)
	if missingSuperAdminRes.Code != http.StatusBadRequest || !strings.Contains(missingSuperAdminRes.Body.String(), "production_mfa_required") {
		t.Fatalf("production scoped MFA missing super_admin status = %d body = %s", missingSuperAdminRes.Code, missingSuperAdminRes.Body.String())
	}

	okReq := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":14,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"passkey","mfa_required_roles":["super_admin"]}`))
	okReq.AddCookie(cookie)
	okReq.Header.Set("X-CSRF-Token", csrf)
	okRes := httptest.NewRecorder()
	handler.ServeHTTP(okRes, okReq)
	if okRes.Code != http.StatusOK || !strings.Contains(okRes.Body.String(), `"mfa_mode":"passkey"`) {
		t.Fatalf("production scoped MFA ok status = %d body = %s", okRes.Code, okRes.Body.String())
	}

	allUsersReq := httptest.NewRequest(http.MethodPut, "/security/settings", bytes.NewBufferString(`{"password_min_length":14,"password_hash":"argon2id","login_lockout_threshold":6,"session_idle_timeout_min":20,"session_absolute_lifetime_h":8,"remember_me_enabled":false,"mfa_mode":"totp","mfa_required_roles":[]}`))
	allUsersReq.AddCookie(cookie)
	allUsersReq.Header.Set("X-CSRF-Token", csrf)
	allUsersRes := httptest.NewRecorder()
	handler.ServeHTTP(allUsersRes, allUsersReq)
	if allUsersRes.Code != http.StatusOK || !strings.Contains(allUsersRes.Body.String(), `"mfa_mode":"totp"`) {
		t.Fatalf("production all-user MFA status = %d body = %s", allUsersRes.Code, allUsersRes.Body.String())
	}
}

func TestMFARequiredRolesLimitEnforcementToConfiguredRoles(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-01", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "viewer-01", Username: "viewer", Roles: []string{"viewer"}}, "correct horse battery", nil); err != nil {
		t.Fatal(err)
	}
	settings := store.NewMemorySecuritySettingsStore()
	if _, err := settings.UpdateSecuritySettings(t.Context(), store.SecuritySettings{
		PasswordMinLength:        12,
		PasswordHash:             "argon2id",
		LoginLockoutThreshold:    5,
		SessionIdleTimeoutMin:    30,
		SessionAbsoluteLifetimeH: 12,
		MFAMode:                  "totp",
		MFARequiredRoles:         []string{"super_admin", "admin"},
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithSecuritySettingsStore(settings), WithMFAStore(auth))

	viewerReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"viewer","password":"correct horse battery"}`))
	viewerRes := httptest.NewRecorder()
	handler.ServeHTTP(viewerRes, viewerReq)
	if viewerRes.Code != http.StatusOK || strings.Contains(viewerRes.Body.String(), "mfa_required") {
		t.Fatalf("viewer should not require MFA under admin-only policy: %d %s", viewerRes.Code, viewerRes.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"}`))
	adminRes := httptest.NewRecorder()
	handler.ServeHTTP(adminRes, adminReq)
	if adminRes.Code != http.StatusForbidden || !strings.Contains(adminRes.Body.String(), "mfa_enrollment_required") {
		t.Fatalf("admin should require MFA enrollment under admin-only policy: %d %s", adminRes.Code, adminRes.Body.String())
	}
}

func TestUsersAndRolesAPI(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.read", "users.create", "roles.read", "roles.create", "roles.assign", "streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	roleReq := httptest.NewRequest(http.MethodPost, "/roles", bytes.NewBufferString(`{"name":"viewer","permissions":["streams.read"]}`))
	roleReq.AddCookie(cookie)
	roleReq.Header.Set("X-CSRF-Token", csrf)
	roleRes := httptest.NewRecorder()
	handler.ServeHTTP(roleRes, roleReq)
	if roleRes.Code != http.StatusCreated {
		t.Fatalf("create role status = %d body = %s", roleRes.Code, roleRes.Body.String())
	}
	var role store.Role
	if err := json.NewDecoder(roleRes.Body).Decode(&role); err != nil {
		t.Fatal(err)
	}

	userReq := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"viewer","email":"viewer@example.jp","temporary_password":"correct horse battery","role_ids":["`+role.ID+`"]}`))
	userReq.AddCookie(cookie)
	userReq.Header.Set("X-CSRF-Token", csrf)
	userRes := httptest.NewRecorder()
	handler.ServeHTTP(userRes, userReq)
	if userRes.Code != http.StatusCreated {
		t.Fatalf("create user status = %d body = %s", userRes.Code, userRes.Body.String())
	}
	var user map[string]any
	if err := json.NewDecoder(userRes.Body).Decode(&user); err != nil {
		t.Fatal(err)
	}
	if _, hasHash := user["PasswordHash"]; hasHash {
		t.Fatal("password hash leaked")
	}
}

func TestDeleteUserRemovesUserSessionsAndBlocksSelf(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-id", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.read", "users.delete"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "target-id", Username: "target"}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	targetSession, err := auth.CreateSession(t.Context(), "target-id", time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/users/target-id", nil)
	deleteReq.AddCookie(cookie)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	deleteRes := httptest.NewRecorder()
	handler.ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete user status = %d body = %s", deleteRes.Code, deleteRes.Body.String())
	}
	if _, err := auth.GetUser(t.Context(), "target-id"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted user should not be retrievable, got %v", err)
	}
	if _, err := auth.GetSession(t.Context(), targetSession.Token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted user sessions should be removed, got %v", err)
	}
	events, err := auth.ListAudit(t.Context(), store.AuditFilter{Actions: []string{"users.delete"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ResourceID != "target-id" || events[0].Result != "success" {
		t.Fatalf("delete audit missing: %#v", events)
	}

	selfReq := httptest.NewRequest(http.MethodDelete, "/users/admin-id", nil)
	selfReq.AddCookie(cookie)
	selfReq.Header.Set("X-CSRF-Token", csrf)
	selfRes := httptest.NewRecorder()
	handler.ServeHTTP(selfRes, selfReq)
	if selfRes.Code != http.StatusConflict || !strings.Contains(selfRes.Body.String(), "cannot_delete_self") {
		t.Fatalf("self delete status = %d body = %s", selfRes.Code, selfRes.Body.String())
	}
	if _, err := auth.GetUser(t.Context(), "admin-id"); err != nil {
		t.Fatalf("self delete should not remove admin: %v", err)
	}
}

func TestDeleteUserRejectsSuperAdminAndPermissionEscalation(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "operator-id", Username: "operator", Roles: []string{"operator"}}, "correct horse battery", []string{"users.delete", "streams.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "super-id", Username: "super", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.delete"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "elevated-id", Username: "elevated"}, "correct horse battery", []string{"streams.read", "system_settings.update"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	superReq := httptest.NewRequest(http.MethodDelete, "/users/super-id", nil)
	superReq.AddCookie(cookie)
	superReq.Header.Set("X-CSRF-Token", csrf)
	superRes := httptest.NewRecorder()
	handler.ServeHTTP(superRes, superReq)
	if superRes.Code != http.StatusForbidden || !strings.Contains(superRes.Body.String(), "cannot_delete_super_admin") {
		t.Fatalf("super_admin delete status = %d body = %s", superRes.Code, superRes.Body.String())
	}

	elevatedReq := httptest.NewRequest(http.MethodDelete, "/users/elevated-id", nil)
	elevatedReq.AddCookie(cookie)
	elevatedReq.Header.Set("X-CSRF-Token", csrf)
	elevatedRes := httptest.NewRecorder()
	handler.ServeHTTP(elevatedRes, elevatedReq)
	if elevatedRes.Code != http.StatusForbidden || !strings.Contains(elevatedRes.Body.String(), "permission_escalation") {
		t.Fatalf("permission escalation delete status = %d body = %s", elevatedRes.Code, elevatedRes.Body.String())
	}
	if _, err := auth.GetUser(t.Context(), "super-id"); err != nil {
		t.Fatalf("super_admin should remain: %v", err)
	}
	if _, err := auth.GetUser(t.Context(), "elevated-id"); err != nil {
		t.Fatalf("elevated user should remain: %v", err)
	}
}

func TestCreateUserCanSendWelcomeEmailWithoutLeakingTemporaryPassword(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.create"}); err != nil {
		t.Fatal(err)
	}
	appSettings := store.NewMemoryAppSettingsStore()
	if _, err := appSettings.UpdateAppSettings(t.Context(), store.AppSettings{
		AppName:                "Kome Panel",
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
	secrets := store.NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), store.AppSMTPPasswordSecretName, "raw-smtp-password"); err != nil {
		t.Fatal(err)
	}
	mailer := &captureMailer{}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithAppSettingsStore(appSettings), WithSecretStore(secrets), WithMailer(mailer))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	req := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"operator","email":"operator@example.jp","temporary_password":"correct horse battery","send_welcome_email":true}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create user status = %d body = %s", res.Code, res.Body.String())
	}
	var user map[string]any
	if err := json.NewDecoder(res.Body).Decode(&user); err != nil {
		t.Fatal(err)
	}
	if user["email"] != "operator@example.jp" {
		t.Fatalf("created user email not returned: %#v", user)
	}
	if len(mailer.messages) != 1 {
		t.Fatalf("expected one welcome email, got %#v", mailer.messages)
	}
	if mailer.messages[0].To != "operator@example.jp" || !strings.Contains(mailer.messages[0].Subject, "Kome Panel") {
		t.Fatalf("unexpected welcome email: %#v", mailer.messages[0])
	}
	if !strings.Contains(mailer.messages[0].Subject, "アカウント作成のお知らせ") ||
		!strings.Contains(mailer.messages[0].Text, "アカウントを作成しました") ||
		!strings.Contains(mailer.messages[0].Text, "ログインURL: ") ||
		!strings.Contains(mailer.messages[0].Text, "初期パスワードはこのメールには記載していません") ||
		strings.Contains(mailer.messages[0].Text, "Welcome") ||
		strings.Contains(mailer.messages[0].Text, "temporary password") {
		t.Fatalf("welcome email is not localized: %#v", mailer.messages[0])
	}
	if mailer.passwords[0] != "raw-smtp-password" {
		t.Fatalf("SMTP password was not resolved for mailer")
	}
	if strings.Contains(mailer.messages[0].Text, "correct horse battery") || strings.Contains(res.Body.String(), "correct horse battery") {
		t.Fatalf("temporary password leaked in welcome flow: body=%s mail=%s", res.Body.String(), mailer.messages[0].Text)
	}
}

func TestUserRoleAssignmentRequiresRolesAssign(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "limited", Roles: []string{"admin"}}, "correct horse battery", []string{"users.create", "users.update", "roles.read"}); err != nil {
		t.Fatal(err)
	}
	role, err := auth.CreateRole(t.Context(), "operator", []string{"streams.start"})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := auth.CreateUser(t.Context(), "existing", "", "correct horse battery", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "limited", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"created","email":"created@example.jp","temporary_password":"correct horse battery","role_ids":["`+role.ID+`"]}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusForbidden {
		t.Fatalf("create with role assignment should be forbidden, got %d body = %s", createRes.Code, createRes.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/users/"+existing.ID, bytes.NewBufferString(`{"role_ids":["`+role.ID+`"]}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusForbidden {
		t.Fatalf("update with role assignment should be forbidden, got %d body = %s", updateRes.Code, updateRes.Body.String())
	}
}

func TestUsersExposeRoleIDsForEditingAndPreserveUntouchedRoles(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-id", Username: "admin", Roles: []string{"admin"}}, "correct horse battery", []string{"users.read", "users.update"}); err != nil {
		t.Fatal(err)
	}
	role, err := auth.CreateRole(t.Context(), "operator", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := auth.CreateUser(t.Context(), "target", "target@example.jp", "correct horse battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")

	listReq := httptest.NewRequest(http.MethodGet, "/users", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK || !strings.Contains(listRes.Body.String(), `"role_ids":["`+role.ID+`"]`) {
		t.Fatalf("user list did not expose editable role ids: status=%d body=%s", listRes.Code, listRes.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/users/"+target.ID, bytes.NewBufferString(`{"username":"target-updated","email":"updated@example.jp"}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK || !strings.Contains(updateRes.Body.String(), `"role_ids":["`+role.ID+`"]`) {
		t.Fatalf("user update lost untouched roles: status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}
	updated, err := auth.GetUser(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Username != "target-updated" || updated.Email != "updated@example.jp" || !slices.Equal(updated.RoleIDs, []string{role.ID}) || !slices.Equal(updated.Roles, []string{"operator"}) {
		t.Fatalf("unexpected updated user: %#v", updated)
	}
}

func TestNonSuperAdminCannotAssignSuperAdminRole(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "super-id", Username: "super", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{ID: "operator-id", Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"users.create", "users.update", "roles.assign"}); err != nil {
		t.Fatal(err)
	}
	target, err := auth.CreateUser(t.Context(), "target", "", "correct horse battery", nil)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var superAdminRole store.Role
	for _, role := range roles {
		if role.Name == "super_admin" {
			superAdminRole = role
			break
		}
	}
	if superAdminRole.ID == "" {
		t.Fatal("expected super_admin role")
	}

	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"blocked","email":"blocked@example.jp","temporary_password":"correct horse battery","role_ids":["`+superAdminRole.ID+`"]}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusForbidden || !strings.Contains(createRes.Body.String(), "cannot_assign_super_admin") {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}
	if _, err := auth.FindUserByUsername(t.Context(), "blocked"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("blocked user should not be created, err = %v", err)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/users/"+target.ID, bytes.NewBufferString(`{"role_ids":["`+superAdminRole.ID+`"]}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusForbidden || !strings.Contains(updateRes.Body.String(), "cannot_assign_super_admin") {
		t.Fatalf("update status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}
	gotTarget, err := auth.GetUser(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if userHasRoleName(gotTarget, "super_admin") {
		t.Fatal("target received super_admin role")
	}
}

func TestRolePermissionsCannotExceedActorPermissions(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	actorPermissions := []string{"roles.create", "roles.update", "streams.read"}
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"admin"}}, "correct horse battery", actorPermissions); err != nil {
		t.Fatal(err)
	}
	viewerRole, err := auth.CreateRole(t.Context(), "viewer", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")

	createReq := httptest.NewRequest(http.MethodPost, "/roles", bytes.NewBufferString(`{"name":"escalated","permissions":["streams.start"]}`))
	createReq.AddCookie(cookie)
	createReq.Header.Set("X-CSRF-Token", csrf)
	createRes := httptest.NewRecorder()
	handler.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusForbidden || !strings.Contains(createRes.Body.String(), "permission_escalation") {
		t.Fatalf("create status = %d body = %s", createRes.Code, createRes.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/roles/"+viewerRole.ID, bytes.NewBufferString(`{"name":"viewer","permissions":["streams.read","streams.start"]}`))
	updateReq.AddCookie(cookie)
	updateReq.Header.Set("X-CSRF-Token", csrf)
	updateRes := httptest.NewRecorder()
	handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusForbidden || !strings.Contains(updateRes.Body.String(), "permission_escalation") {
		t.Fatalf("update status = %d body = %s", updateRes.Code, updateRes.Body.String())
	}
	gotRole, err := auth.GetRole(t.Context(), viewerRole.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotRole.Permissions) != 1 || gotRole.Permissions[0] != "streams.read" {
		t.Fatalf("role permissions changed after denied update: %#v", gotRole.Permissions)
	}
}

func TestOnlySuperAdminCanResetSuperAdminPassword(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "target-id", Username: "target", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"users.reset_password"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "super", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.reset_password"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))

	operatorCookie, operatorCSRF := loginForTest(t, handler, "operator", "correct horse battery")
	deniedReq := httptest.NewRequest(http.MethodPost, "/users/target-id/reset-password", bytes.NewBufferString(`{"temporary_password":"replacement passphrase"}`))
	deniedReq.AddCookie(operatorCookie)
	deniedReq.Header.Set("X-CSRF-Token", operatorCSRF)
	deniedRes := httptest.NewRecorder()
	handler.ServeHTTP(deniedRes, deniedReq)
	if deniedRes.Code != http.StatusForbidden || !strings.Contains(deniedRes.Body.String(), "cannot_reset_super_admin_password") {
		t.Fatalf("denied status = %d body = %s", deniedRes.Code, deniedRes.Body.String())
	}
	target, err := auth.GetUser(t.Context(), "target-id")
	if err != nil {
		t.Fatal(err)
	}
	if !security.VerifyPassword("correct horse battery", target.PasswordHash) {
		t.Fatal("password changed after denied reset")
	}

	superCookie, superCSRF := loginForTest(t, handler, "super", "correct horse battery")
	allowedReq := httptest.NewRequest(http.MethodPost, "/users/target-id/reset-password", bytes.NewBufferString(`{"temporary_password":"replacement passphrase"}`))
	allowedReq.AddCookie(superCookie)
	allowedReq.Header.Set("X-CSRF-Token", superCSRF)
	allowedRes := httptest.NewRecorder()
	handler.ServeHTTP(allowedRes, allowedReq)
	if allowedRes.Code != http.StatusOK {
		t.Fatalf("allowed status = %d body = %s", allowedRes.Code, allowedRes.Body.String())
	}
	target, err = auth.GetUser(t.Context(), "target-id")
	if err != nil {
		t.Fatal(err)
	}
	if !security.VerifyPassword("replacement passphrase", target.PasswordHash) {
		t.Fatal("super_admin reset did not update password")
	}
}

func TestViewerCannotManageUsers(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "viewer", Roles: []string{"viewer"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "viewer", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/users", bytes.NewBufferString(`{"username":"blocked","temporary_password":"correct horse battery"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestUsersUpdateCannotCreateManualOAuthLink(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "target-id", Username: "target", Roles: []string{"super_admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"users.update"}); err != nil {
		t.Fatal(err)
	}
	oauthStore := store.NewMemoryOAuthLoginStore()
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth), WithOAuthLoginStore(oauthStore), WithIntegrationStore(store.NewMemoryIntegrationStore()))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/users/target-id/oauth-links", bytes.NewBufferString(`{"provider_id":"provider-01","provider_type":"google","subject":"attacker-subject","email":"attacker@example.com"}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCannotDisableLastSuperAdmin(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-id", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"users.disable"}); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPost, "/users/admin-id/disable", nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCannotUpdateOwnUserRoleAssignments(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "operator-id", Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"users.update", "roles.assign"}); err != nil {
		t.Fatal(err)
	}
	viewerRole, err := auth.CreateRole(t.Context(), "viewer", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/users/operator-id", bytes.NewBufferString(`{"role_ids":["`+viewerRole.ID+`"]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "cannot_update_own_roles") {
		t.Fatalf("expected cannot_update_own_roles, body = %s", res.Body.String())
	}
}

func TestCannotRemoveLastSuperAdminRoleAssignment(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{ID: "admin-id", Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"streams.read"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"admin"}}, "correct horse battery", []string{"users.update", "roles.assign", "streams.read"}); err != nil {
		t.Fatal(err)
	}
	viewerRole, err := auth.CreateRole(t.Context(), "viewer", []string{"streams.read"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/users/admin-id", bytes.NewBufferString(`{"role_ids":["`+viewerRole.ID+`"]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "last_super_admin") {
		t.Fatalf("expected last_super_admin, body = %s", res.Body.String())
	}
}

func TestCannotDeleteSuperAdminRole(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"roles.read", "roles.delete"}); err != nil {
		t.Fatal(err)
	}
	roles, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) == 0 {
		t.Fatal("expected super_admin role")
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodDelete, "/roles/"+roles[0].ID, nil)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCannotUpdateSuperAdminRole(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "admin", Roles: []string{"super_admin"}}, "correct horse battery", []string{"roles.read", "roles.update"}); err != nil {
		t.Fatal(err)
	}
	roles, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) == 0 {
		t.Fatal("expected super_admin role")
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "admin", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/roles/"+roles[0].ID, bytes.NewBufferString(`{"name":"super_admin","permissions":["roles.read"]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestCannotUpdateOwnNonSuperAdminRole(t *testing.T) {
	auth := store.NewMemoryAuthStore()
	if err := auth.AddUser(store.User{Username: "operator", Roles: []string{"operator"}}, "correct horse battery", []string{"roles.read", "roles.update"}); err != nil {
		t.Fatal(err)
	}
	roles, err := auth.ListRoles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var operatorRole store.Role
	for _, role := range roles {
		if role.Name == "operator" {
			operatorRole = role
			break
		}
	}
	if operatorRole.ID == "" {
		t.Fatal("expected operator role")
	}
	handler := NewServer(store.NewMemoryStreamStore(), WithAuthStore(auth), WithAuditStore(auth))
	cookie, csrf := loginForTest(t, handler, "operator", "correct horse battery")
	req := httptest.NewRequest(http.MethodPut, "/roles/"+operatorRole.ID, bytes.NewBufferString(`{"name":"operator","permissions":["roles.read","roles.update","users.create"]}`))
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", csrf)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}
