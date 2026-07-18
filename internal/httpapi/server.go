package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/ingesttoken"
	"github.com/example/autostream-control-panel/internal/netpolicy"
	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/observability"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/version"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
)

const (
	sessionCookieName            = "autostream_session"
	oauthStateCookieName         = "autostream_oauth_state"
	maxControlRequestBytes       = 1 << 20
	defaultNodeConfigureTokenTTL = 24 * time.Hour
	minStreamIngestSigningKeyLen = 32
	minSecretEncryptionKeyLen    = 32
)

var requiredStartServiceTypes = []string{"discord_bot", "worker", "encoder_recorder"}
var requiredStopServiceTypes = []string{"discord_bot", "worker", "encoder_recorder"}
var requiredRetryUploadServiceTypes = []string{"encoder_recorder"}
var requiredWorkerEventServiceTypes = []string{"worker"}

const serviceHeartbeatWarningDefault = 60 * time.Second
const serviceHeartbeatOfflineDefault = 180 * time.Second
const runtimeSecretLeaseTTL = 60 * time.Second
const youtubeCompleteRetryDefaultInterval = 60 * time.Second
const sensitiveActionAttemptThreshold = 6
const emailChangeChallengeTTL = 30 * time.Minute
const defaultArchiveRetentionDays = 30
const maxArchiveRetentionDays = 3650
const streamPreviewLinkTTL = 12 * time.Hour
const adminAuditNotificationTimeout = 30 * time.Second

type Server struct {
	mux                 *http.ServeMux
	handler             http.Handler
	streams             store.StreamStore
	auth                store.AuthStore
	audit               store.AuditStore
	users               store.UserAdminStore
	roles               store.RoleStore
	services            store.ServiceRegistryStore
	profiles            store.ProfileStore
	integrations        store.IntegrationStore
	settings            store.SecuritySettingsStore
	appSettings         store.AppSettingsStore
	secrets             store.SecretStore
	runtimeLeases       store.RuntimeSecretLeaseStore
	remediation         store.RemediationExecutionStore
	mfa                 store.MFAStore
	emailChanges        store.EmailChangeStore
	passkeys            store.PasskeyStore
	avatars             store.UserAvatarStore
	oauthLogin          store.OAuthLoginStore
	oauthVerifier       oauthlogin.Verifier
	oauthConnector      oauthlogin.Connector
	mailer              Mailer
	turnstile           TurnstileVerifier
	obs                 observability.Client
	dispatcher          serviceDispatcher
	youtubeLive         ytlive.LiveClient
	setupToken          string
	previewSigningKey   string
	loginFailures       *loginFailureLimiter
	serviceEmailLimiter *serviceEmailRateLimiter
}

type serviceDispatcher interface {
	Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult
	Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult
	RetryArchiveUpload(ctx context.Context, stream store.Stream, services []store.RegisteredService, archiveConfig map[string]any) []servicecall.DispatchResult
	AudioStatus(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.AudioStatusResult
	WorkerEvents(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.WorkerEventsResult
	EncoderPreflight(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.ServicePreflightResult
	SendWorkerEvent(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.WorkerEventRequest) servicecall.DispatchResult
	DownloadArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) servicecall.ArchiveArtifactDownloadResult
	DeleteArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) servicecall.DispatchResult
	RenameArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, name string) servicecall.DispatchResult
}

type startReadinessChecker interface {
	StartReadinessIssues(services []store.RegisteredService, req servicecall.StartRequest, now time.Time) []servicecall.ReadinessIssue
}

type previewServiceDispatcher interface {
	PreviewAsset(ctx context.Context, stream store.Stream, services []store.RegisteredService, name string) servicecall.PreviewAssetResult
}

type discordLiveNotificationDispatcher interface {
	NotifyDiscordYouTubeLive(ctx context.Context, stream store.Stream, services []store.RegisteredService, eventID, watchURL string) servicecall.DispatchResult
}

type ServerOption func(*Server)

func WithAuthStore(auth store.AuthStore) ServerOption {
	return func(s *Server) { s.auth = auth }
}

func WithAuditStore(audit store.AuditStore) ServerOption {
	return func(s *Server) { s.audit = audit }
}

func WithUserAdminStore(users store.UserAdminStore) ServerOption {
	return func(s *Server) { s.users = users }
}

func WithRoleStore(roles store.RoleStore) ServerOption {
	return func(s *Server) { s.roles = roles }
}

func WithServiceRegistryStore(services store.ServiceRegistryStore) ServerOption {
	return func(s *Server) { s.services = services }
}

func WithProfileStore(profiles store.ProfileStore) ServerOption {
	return func(s *Server) { s.profiles = profiles }
}

func WithIntegrationStore(integrations store.IntegrationStore) ServerOption {
	return func(s *Server) { s.integrations = integrations }
}

func WithSecuritySettingsStore(settings store.SecuritySettingsStore) ServerOption {
	return func(s *Server) { s.settings = settings }
}

func WithAppSettingsStore(settings store.AppSettingsStore) ServerOption {
	return func(s *Server) { s.appSettings = settings }
}

func WithSecretStore(secrets store.SecretStore) ServerOption {
	return func(s *Server) { s.secrets = secrets }
}

func WithRuntimeSecretLeaseStore(runtimeLeases store.RuntimeSecretLeaseStore) ServerOption {
	return func(s *Server) { s.runtimeLeases = runtimeLeases }
}

func WithRemediationExecutionStore(remediation store.RemediationExecutionStore) ServerOption {
	return func(s *Server) { s.remediation = remediation }
}

func WithMFAStore(mfa store.MFAStore) ServerOption {
	return func(s *Server) { s.mfa = mfa }
}

func WithEmailChangeStore(emailChanges store.EmailChangeStore) ServerOption {
	return func(s *Server) { s.emailChanges = emailChanges }
}

func WithPasskeyStore(passkeys store.PasskeyStore) ServerOption {
	return func(s *Server) { s.passkeys = passkeys }
}

func WithUserAvatarStore(avatars store.UserAvatarStore) ServerOption {
	return func(s *Server) { s.avatars = avatars }
}

func WithOAuthLoginStore(oauthLogin store.OAuthLoginStore) ServerOption {
	return func(s *Server) { s.oauthLogin = oauthLogin }
}

func WithOAuthVerifier(verifier oauthlogin.Verifier) ServerOption {
	return func(s *Server) { s.oauthVerifier = verifier }
}

func WithOAuthConnector(connector oauthlogin.Connector) ServerOption {
	return func(s *Server) { s.oauthConnector = connector }
}

func WithMailer(mailer Mailer) ServerOption {
	return func(s *Server) { s.mailer = mailer }
}

func WithTurnstileVerifier(verifier TurnstileVerifier) ServerOption {
	return func(s *Server) { s.turnstile = verifier }
}

func WithObservabilityClient(client observability.Client) ServerOption {
	return func(s *Server) { s.obs = client }
}

func WithServiceDispatcher(dispatcher serviceDispatcher) ServerOption {
	return func(s *Server) { s.dispatcher = dispatcher }
}

func WithYouTubeLiveClient(client ytlive.LiveClient) ServerOption {
	return func(s *Server) { s.youtubeLive = client }
}

func WithSetupToken(token string) ServerOption {
	return func(s *Server) { s.setupToken = token }
}

func WithPreviewSigningKey(key string) ServerOption {
	return func(s *Server) { s.previewSigningKey = strings.TrimSpace(key) }
}

func NewServer(streams store.StreamStore, opts ...ServerOption) *Server {
	defaultOAuth := oauthlogin.HTTPVerifier{}
	s := &Server{mux: http.NewServeMux(), streams: streams, obs: observability.FromEnv(), dispatcher: servicecall.FromEnv(), youtubeLive: ytlive.LiveAPIClient{}, oauthVerifier: defaultOAuth, oauthConnector: defaultOAuth, turnstile: HTTPSTurnstileVerifier{}, setupToken: os.Getenv("AUTOSTREAM_SETUP_TOKEN"), previewSigningKey: strings.TrimSpace(os.Getenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY")), loginFailures: newLoginFailureLimiter(), serviceEmailLimiter: newServiceEmailRateLimiter(serviceEmailRateLimit, serviceEmailRateWindow)}
	for _, opt := range opts {
		opt(s)
	}
	if s.users == nil {
		if users, ok := s.auth.(store.UserAdminStore); ok {
			s.users = users
		}
	}
	if s.audit == nil {
		if audit, ok := s.auth.(store.AuditStore); ok {
			s.audit = audit
		}
	}
	if s.roles == nil {
		if roles, ok := s.auth.(store.RoleStore); ok {
			s.roles = roles
		}
	}
	if s.services == nil {
		if services, ok := s.auth.(store.ServiceRegistryStore); ok {
			s.services = services
		}
	}
	if s.profiles == nil {
		s.profiles = store.NewMemoryProfileStore()
	}
	if s.integrations == nil {
		s.integrations = store.NewMemoryIntegrationStore()
	}
	if s.settings == nil {
		s.settings = store.NewMemorySecuritySettingsStore()
	}
	if s.appSettings == nil {
		s.appSettings = store.NewMemoryAppSettingsStore()
	}
	if s.secrets == nil {
		s.secrets = store.NewMemorySecretStore()
	}
	if s.mailer == nil {
		s.mailer = SMTPMailer{}
	}
	if s.runtimeLeases == nil {
		s.runtimeLeases = store.NewMemoryRuntimeSecretLeaseStore()
	}
	if s.remediation == nil {
		if remediation, ok := s.auth.(store.RemediationExecutionStore); ok {
			s.remediation = remediation
		} else {
			s.remediation = store.NewMemoryRemediationExecutionStore()
		}
	}
	if s.mfa == nil {
		if mfa, ok := s.auth.(store.MFAStore); ok {
			s.mfa = mfa
		}
	}
	if s.emailChanges == nil {
		if emailChanges, ok := s.auth.(store.EmailChangeStore); ok {
			s.emailChanges = emailChanges
		}
	}
	if s.passkeys == nil {
		if passkeys, ok := s.auth.(store.PasskeyStore); ok {
			s.passkeys = passkeys
		}
	}
	if s.avatars == nil {
		if avatars, ok := s.auth.(store.UserAvatarStore); ok {
			s.avatars = avatars
		}
	}
	if s.oauthLogin == nil {
		s.oauthLogin = store.NewMemoryOAuthLoginStore()
	}
	if s.oauthConnector == nil {
		if connector, ok := s.oauthVerifier.(oauthlogin.Connector); ok {
			s.oauthConnector = connector
		} else {
			s.oauthConnector = oauthlogin.HTTPVerifier{}
		}
	}
	s.routes()
	s.handler = secureHeaders(limitRequestBody(s.mux, maxControlRequestBytes))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.rootRedirect)
	s.mux.HandleFunc("GET /health", s.health)
	s.mux.HandleFunc("GET /setup/status", s.setupStatus)
	s.mux.HandleFunc("POST /setup/first-admin", s.setupFirstAdmin)
	s.mux.HandleFunc("POST /auth/login", s.login)
	s.mux.HandleFunc("GET /auth/oauth/providers", s.listOAuthLoginProviders)
	s.mux.HandleFunc("POST /auth/oauth/{id}/start", s.startOAuthLogin)
	s.mux.HandleFunc("GET /auth/oauth/callback", s.oauthLoginRedirectCallback)
	s.mux.HandleFunc("POST /auth/oauth/callback", s.oauthLoginCallback)
	s.mux.HandleFunc("POST /services/register", s.serviceRegister)
	s.mux.HandleFunc("GET /services/runtime-config", s.serviceRuntimeConfig)
	s.mux.HandleFunc("POST /services/runtime-secrets/resolve", s.serviceRuntimeSecretResolve)
	s.mux.HandleFunc("POST /services/heartbeat", s.serviceHeartbeat)
	s.mux.HandleFunc("POST /services/streams/{id}/start", s.serviceStartStream)
	s.mux.HandleFunc("POST /services/observability/signals", s.serviceObservabilitySignal)
	s.mux.HandleFunc("POST /services/stream-events", s.serviceStreamEvent)
	s.mux.HandleFunc("POST /services/stream-artifacts", s.serviceStreamArtifacts)
	s.mux.HandleFunc("POST /services/notifications/email", s.serviceEmailNotification)
	s.mux.HandleFunc("POST /services/remediation-actions/execute", s.serviceRemediationExecute)
	s.mux.HandleFunc("POST /api/node-agent/configure", s.nodeAgentConfigure)
	s.mux.HandleFunc("POST /api/node-agent/heartbeat", s.nodeAgentHeartbeat)
	s.mux.HandleFunc("POST /api/node-agent/report", s.nodeAgentReport)
	s.mux.HandleFunc("POST /api/node-agent/events", s.serviceStreamEvent)
	s.mux.HandleFunc("POST /auth/logout", s.requirePermission("", s.logout))
	s.mux.HandleFunc("POST /auth/session/refresh", s.requirePermission("", s.refreshSession))
	s.mux.HandleFunc("GET /auth/me", s.requirePermission("", s.me))
	s.mux.HandleFunc("GET /auth/avatar", s.requirePermission("", s.getCurrentUserAvatar))
	s.mux.HandleFunc("PUT /auth/avatar", s.requirePermission("", s.updateCurrentUserAvatar))
	s.mux.HandleFunc("DELETE /auth/avatar", s.requirePermission("", s.deleteCurrentUserAvatar))
	s.mux.HandleFunc("PUT /auth/email", s.requirePermission("", s.updateCurrentUserEmail))
	s.mux.HandleFunc("POST /auth/email/confirm", s.confirmCurrentUserEmail)
	s.mux.HandleFunc("POST /auth/change-password", s.requirePermission("", s.changePassword))
	s.mux.HandleFunc("GET /auth/mfa/status", s.requirePermission("", s.mfaStatus))
	s.mux.HandleFunc("POST /auth/mfa/enroll", s.requirePermission("", s.mfaEnroll))
	s.mux.HandleFunc("POST /auth/mfa/verify", s.mfaVerify)
	s.mux.HandleFunc("POST /auth/mfa/disable", s.requirePermission("", s.mfaDisable))
	s.mux.HandleFunc("POST /auth/recovery-codes/regenerate", s.requirePermission("", s.mfaRegenerateRecoveryCodes))
	s.mux.HandleFunc("POST /auth/passkeys/register/start", s.requirePermission("", s.startPasskeyRegistration))
	s.mux.HandleFunc("POST /auth/passkeys/register/finish", s.requirePermission("", s.finishPasskeyRegistration))
	s.mux.HandleFunc("POST /auth/passkeys/login/start", s.startPasskeyLogin)
	s.mux.HandleFunc("POST /auth/passkeys/login/finish", s.finishPasskeyLogin)
	s.mux.HandleFunc("GET /auth/passkeys", s.requirePermission("", s.listPasskeys))
	s.mux.HandleFunc("DELETE /auth/passkeys/{id}", s.requirePermission("", s.deletePasskey))
	s.mux.HandleFunc("GET /auth/oauth-links", s.requirePermission("", s.listCurrentUserOAuthLinks))
	s.mux.HandleFunc("POST /auth/oauth-links/{id}/start", s.requirePermission("", s.startCurrentUserOAuthLink))
	s.mux.HandleFunc("DELETE /auth/oauth-links/{id}", s.requirePermission("", s.deleteCurrentUserOAuthLink))
	s.mux.HandleFunc("GET /permissions", s.requirePermission("roles.read", s.listPermissions))
	s.mux.HandleFunc("GET /users", s.requirePermission("users.read", s.listUsers))
	s.mux.HandleFunc("POST /users", s.requirePermission("users.create", s.createUser))
	s.mux.HandleFunc("GET /users/{id}", s.requirePermission("users.read", s.getUser))
	s.mux.HandleFunc("PUT /users/{id}", s.requirePermission("users.update", s.updateUser))
	s.mux.HandleFunc("POST /users/{id}/disable", s.requirePermission("users.disable", s.disableUser))
	s.mux.HandleFunc("POST /users/{id}/lock", s.requirePermission("users.disable", s.lockUser))
	s.mux.HandleFunc("POST /users/{id}/unlock", s.requirePermission("users.update", s.unlockUser))
	s.mux.HandleFunc("POST /users/{id}/reset-password", s.requirePermission("users.reset_password", s.resetPassword))
	s.mux.HandleFunc("POST /users/{id}/force-password-change", s.requirePermission("users.reset_password", s.forcePasswordChange))
	s.mux.HandleFunc("DELETE /users/{id}", s.requirePermission("users.delete", s.deleteUser))
	s.mux.HandleFunc("GET /users/{id}/oauth-links", s.requirePermission("users.read", s.listUserOAuthLinks))
	s.mux.HandleFunc("POST /users/{id}/oauth-links", s.requirePermission("users.manage_mfa", s.createUserOAuthLink))
	s.mux.HandleFunc("DELETE /users/{id}/oauth-links/{link_id}", s.requirePermission("users.manage_mfa", s.deleteUserOAuthLink))
	s.mux.HandleFunc("GET /roles", s.requirePermission("roles.read", s.listRoles))
	s.mux.HandleFunc("POST /roles", s.requirePermission("roles.create", s.createRole))
	s.mux.HandleFunc("GET /roles/{id}", s.requirePermission("roles.read", s.getRole))
	s.mux.HandleFunc("PUT /roles/{id}", s.requirePermission("roles.update", s.updateRole))
	s.mux.HandleFunc("DELETE /roles/{id}", s.requirePermission("roles.delete", s.deleteRole))
	s.registerProfileRoutes("/profiles/encoder", store.ProfileEncoder, "encoder_profiles")
	s.registerProfileRoutes("/profiles/archive", store.ProfileArchive, "archive_profiles")
	s.registerProfileRoutes("/profiles/caption", store.ProfileCaption, "caption_profiles")
	s.registerProfileRoutes("/profiles/overlay", store.ProfileOverlay, "overlay_profiles")
	s.mux.HandleFunc("GET /discord/configs", s.requirePermission("discord_configs.read", s.listDiscordConfigs))
	s.mux.HandleFunc("POST /discord/configs", s.requirePermission("discord_configs.create", s.createDiscordConfig))
	s.mux.HandleFunc("GET /discord/configs/{id}", s.requirePermission("discord_configs.read", s.getDiscordConfig))
	s.mux.HandleFunc("PUT /discord/configs/{id}", s.requirePermission("discord_configs.update", s.updateDiscordConfig))
	s.mux.HandleFunc("DELETE /discord/configs/{id}", s.requirePermission("discord_configs.delete", s.deleteDiscordConfig))
	s.mux.HandleFunc("GET /youtube/outputs", s.requirePermission("youtube_outputs.read", s.listYouTubeOutputs))
	s.mux.HandleFunc("POST /youtube/outputs", s.requirePermission("youtube_outputs.create", s.createYouTubeOutput))
	s.mux.HandleFunc("GET /youtube/outputs/{id}", s.requirePermission("youtube_outputs.read", s.getYouTubeOutput))
	s.mux.HandleFunc("PUT /youtube/outputs/{id}", s.requirePermission("youtube_outputs.update", s.updateYouTubeOutput))
	s.mux.HandleFunc("DELETE /youtube/outputs/{id}", s.requirePermission("youtube_outputs.delete", s.deleteYouTubeOutput))
	s.mux.HandleFunc("GET /integrations/oauth-providers", s.requirePermission("integrations.read", s.listOAuthProviders))
	s.mux.HandleFunc("POST /integrations/oauth-providers", s.requirePermission("integrations.create", s.createOAuthProvider))
	s.mux.HandleFunc("GET /integrations/oauth-providers/{id}", s.requirePermission("integrations.read", s.getOAuthProvider))
	s.mux.HandleFunc("PUT /integrations/oauth-providers/{id}", s.requirePermission("integrations.update", s.updateOAuthProvider))
	s.mux.HandleFunc("DELETE /integrations/oauth-providers/{id}", s.requirePermission("integrations.delete", s.deleteOAuthProvider))
	s.mux.HandleFunc("GET /integrations/oauth-accounts", s.requirePermission("integrations.read", s.listOAuthAccounts))
	s.mux.HandleFunc("POST /integrations/oauth-accounts/start", s.requirePermission("integrations.create", s.startOAuthAccountConnection))
	s.mux.HandleFunc("GET /integrations/oauth-accounts/callback", s.requirePermission("integrations.create", s.oauthAccountRedirectCallback))
	s.mux.HandleFunc("POST /integrations/oauth-accounts/callback", s.requirePermission("integrations.create", s.oauthAccountCallback))
	s.mux.HandleFunc("POST /integrations/oauth-accounts", s.requirePermission("integrations.create", s.createOAuthAccount))
	s.mux.HandleFunc("GET /integrations/oauth-accounts/{id}", s.requirePermission("integrations.read", s.getOAuthAccount))
	s.mux.HandleFunc("PUT /integrations/oauth-accounts/{id}", s.requirePermission("integrations.update", s.updateOAuthAccount))
	s.mux.HandleFunc("DELETE /integrations/oauth-accounts/{id}", s.requirePermission("integrations.delete", s.deleteOAuthAccount))
	s.mux.HandleFunc("GET /archive/destinations", s.requirePermission("integrations.read", s.listDriveDestinations))
	s.mux.HandleFunc("POST /archive/destinations", s.requirePermission("integrations.create", s.createDriveDestination))
	s.mux.HandleFunc("GET /archive/destinations/{id}", s.requirePermission("integrations.read", s.getDriveDestination))
	s.mux.HandleFunc("PUT /archive/destinations/{id}", s.requirePermission("integrations.update", s.updateDriveDestination))
	s.mux.HandleFunc("DELETE /archive/destinations/{id}", s.requirePermission("integrations.delete", s.deleteDriveDestination))
	s.mux.HandleFunc("GET /api-tokens", s.requirePermission("api_tokens.read", s.listServiceTokens))
	s.mux.HandleFunc("POST /api-tokens", s.requirePermission("api_tokens.create", s.createServiceToken))
	s.mux.HandleFunc("POST /api-tokens/{id}/rotate", s.requirePermission("api_tokens.create", s.rotateServiceToken))
	s.mux.HandleFunc("DELETE /api-tokens/{id}", s.requirePermission("api_tokens.revoke", s.revokeServiceToken))
	s.mux.HandleFunc("GET /nodes", s.requirePermission("api_tokens.create", s.listNodes))
	s.mux.HandleFunc("POST /nodes/registration-tokens", s.requirePermission("api_tokens.create", s.createNodeRegistrationToken))
	s.mux.HandleFunc("PUT /nodes/{id}", s.requirePermission("api_tokens.create", s.updateNode))
	s.mux.HandleFunc("GET /nodes/{id}/configuration", s.requireAnyPermission([]string{"service_health.read", "api_tokens.create"}, s.nodeConfiguration))
	s.mux.HandleFunc("POST /nodes/{id}/configure-token", s.requirePermission("api_tokens.create", s.regenerateNodeConfigureToken))
	s.mux.HandleFunc("POST /nodes/{id}/rotate-token", s.requirePermission("api_tokens.create", s.rotateNodeRuntimeToken))
	s.mux.HandleFunc("GET /service-health", s.requirePermission("service_health.read", s.listServices))
	s.mux.HandleFunc("GET /service-health/{id}/runtime-config", s.requirePermission("service_health.read", s.adminServiceRuntimeConfig))
	s.mux.HandleFunc("POST /services/{id}/assign", s.requirePermission("services.assign", s.assignService))
	s.mux.HandleFunc("DELETE /services/{id}/assignment", s.requirePermission("services.unassign", s.unassignService))
	s.mux.HandleFunc("DELETE /services/{id}", s.requirePermission("services.disable", s.deleteService))
	s.mux.HandleFunc("GET /workers", s.requirePermission("workers.read", s.listWorkers))
	s.mux.HandleFunc("GET /workers/{id}", s.requirePermission("workers.read", s.getWorker))
	s.mux.HandleFunc("POST /workers/{id}/assign", s.requirePermission("workers.assign", s.assignWorker))
	s.mux.HandleFunc("DELETE /workers/{id}/assignment", s.requirePermission("workers.unassign", s.unassignWorker))
	s.mux.HandleFunc("POST /workers/{id}/restart", s.requirePermission("workers.restart", s.restartWorker))
	s.mux.HandleFunc("GET /streams", s.requirePermission("streams.read", s.listStreams))
	s.mux.HandleFunc("POST /streams", s.requirePermission("streams.create", s.createStream))
	s.mux.HandleFunc("GET /streams/{id}", s.requirePermission("streams.read", s.getStream))
	s.mux.HandleFunc("GET /streams/{id}/external-e2e-config", s.requirePermission("streams.read", s.externalE2EConfig))
	s.mux.HandleFunc("PUT /streams/{id}/settings", s.requirePermission("streams.update", s.updateStreamSettings))
	s.mux.HandleFunc("POST /streams/{id}/start-readiness", s.requirePermission("streams.start", s.startReadiness))
	s.mux.HandleFunc("POST /streams/{id}/start", s.requirePermission("streams.start", s.startStream))
	s.mux.HandleFunc("POST /streams/{id}/stop", s.requirePermission("streams.stop", s.stopStream))
	s.mux.HandleFunc("POST /streams/{id}/youtube/complete", s.requirePermission("streams.stop", s.completeYouTubeStream))
	s.mux.HandleFunc("POST /streams/{id}/mark-failed", s.requirePermission("streams.update", s.markStreamFailed))
	s.mux.HandleFunc("POST /streams/{id}/retry-upload", s.requirePermission("streams.retry_upload", s.retryUpload))
	s.mux.HandleFunc("GET /streams/{id}/encoder-preflight", s.requirePermission("streams.read", s.streamEncoderPreflight))
	s.mux.HandleFunc("GET /streams/{id}/preview/{name}", s.requirePermission("streams.read", s.streamPreviewAsset))
	s.mux.HandleFunc("POST /streams/{id}/preview-links", s.requirePermission("streams.read", s.createStreamPreviewLink))
	s.mux.HandleFunc("GET /streams/{id}/audio-status", s.requirePermission("streams.read", s.streamAudioStatus))
	s.mux.HandleFunc("GET /streams/{id}/worker-events", s.requirePermission("streams.read", s.streamWorkerEvents))
	s.mux.HandleFunc("POST /streams/{id}/worker-events/test", s.requirePermission("streams.update", s.sendWorkerTestEvent))
	s.mux.HandleFunc("GET /streams/{id}/logs", s.requirePermission("logs.read", s.streamLogs))
	s.mux.HandleFunc("GET /streams/{id}/artifacts", s.requirePermission("archives.read", s.streamArtifacts))
	s.mux.HandleFunc("GET /streams/{id}/artifacts/{artifact_id}/download", s.requirePermission("archives.download", s.downloadStreamArtifact))
	s.mux.HandleFunc("GET /streams/{id}/artifacts/{artifact_id}/shares", s.requirePermission("archives.read", s.listStreamArtifactShares))
	s.mux.HandleFunc("POST /streams/{id}/artifacts/{artifact_id}/shares", s.requirePermission("archives.download", s.createStreamArtifactShare))
	s.mux.HandleFunc("DELETE /streams/{id}/artifacts/{artifact_id}/shares/{share_id}", s.requirePermission("archives.delete", s.revokeStreamArtifactShare))
	s.mux.HandleFunc("DELETE /streams/{id}/artifacts/{artifact_id}", s.requirePermission("archives.delete", s.deleteStreamArtifact))
	s.mux.HandleFunc("PUT /streams/{id}/artifacts/{artifact_id}", s.requirePermission("archives.delete", s.renameStreamArtifact))
	s.mux.HandleFunc("GET /archive-shares/{token}", s.publicArchiveShare)
	s.mux.HandleFunc("GET /archive-shares/{token}/download", s.downloadPublicArchiveShare)
	s.mux.HandleFunc("GET /stream-previews/{token}/{name}", s.publicStreamPreviewAsset)
	s.mux.HandleFunc("GET /audit-logs", s.requirePermission("audit_logs.read", s.listAuditLogs))
	s.mux.HandleFunc("GET /audit-logs/export", s.requirePermission("audit_logs.export", s.exportAuditLogs))
	s.mux.HandleFunc("GET /security/settings", s.requirePermission("system_settings.read", s.securitySettings))
	s.mux.HandleFunc("PUT /security/settings", s.requirePermission("system_settings.update", s.updateSecuritySettings))
	s.mux.HandleFunc("GET /settings/app", s.appSettingsView)
	s.mux.HandleFunc("GET /settings/app/manage", s.requirePermission("system_settings.update", s.managedAppSettingsView))
	s.mux.HandleFunc("PUT /settings/app", s.requirePermission("system_settings.update", s.updateAppSettings))
	s.mux.HandleFunc("POST /settings/app/test-email", s.requirePermission("system_settings.update", s.sendAppSettingsTestEmail))
	s.mux.HandleFunc("GET /version", s.requirePermission("", s.versionInfo))
	s.mux.HandleFunc("GET /secrets/status", s.requirePermission("secrets.read_status", s.secretStatus))
	s.mux.HandleFunc("PUT /secrets/{name}", s.requirePermission("secrets.update", s.updateSecret))
	s.mux.HandleFunc("GET /observability/incidents", s.requirePermission("incidents.read", s.observabilityGet("/incidents")))
	s.mux.HandleFunc("GET /observability/diagnostics", s.requirePermission("diagnostics.read", s.observabilityGet("/diagnostics")))
	s.mux.HandleFunc("GET /observability/metrics", s.requirePermission("metrics.read", s.observabilityMetrics))
	s.mux.HandleFunc("GET /observability/remediation-actions", s.requirePermission("remediation.read", s.observabilityGet("/remediation-actions")))
	s.mux.HandleFunc("POST /observability/remediation-actions/{id}/approve", s.requirePermission("remediation.approve", s.observabilityPostAction("/remediation-actions/{id}/approve")))
	s.mux.HandleFunc("POST /observability/remediation-actions/{id}/execute", s.requirePermission("remediation.execute", s.observabilityPostAction("/remediation-actions/{id}/execute")))
	s.mux.HandleFunc("POST /observability/incidents/{id}/acknowledge", s.requirePermission("incidents.acknowledge", s.observabilityPostActionWithAudit("/incidents/{id}/acknowledge", "incidents.acknowledge", "incident")))
	s.mux.HandleFunc("POST /observability/incidents/{id}/resolve", s.requirePermission("incidents.resolve", s.observabilityPostActionWithAudit("/incidents/{id}/resolve", "incidents.resolve", "incident")))
	s.mux.HandleFunc("GET /observability/notification-deliveries", s.requirePermission("notification_channels.read", s.observabilityGet("/notification-deliveries")))
	s.mux.HandleFunc("GET /observability/notification-channels", s.requirePermission("notification_channels.read", s.observabilityGet("/notification-channels")))
	s.mux.HandleFunc("POST /observability/notification-channels", s.requirePermission("notification_channels.create", s.observabilityNotificationChannelPostProxyStatus("/notification-channels", "notification_channels.create", "notification_channel", http.StatusCreated)))
	s.mux.HandleFunc("GET /observability/notification-channels/{id}", s.requirePermission("notification_channels.read", s.observabilityGetAction("/notification-channels/{id}")))
	s.mux.HandleFunc("PUT /observability/notification-channels/{id}", s.requirePermission("notification_channels.update", s.observabilityNotificationChannelPutProxy("/notification-channels/{id}", "notification_channels.update", "notification_channel")))
	s.mux.HandleFunc("DELETE /observability/notification-channels/{id}", s.requirePermission("notification_channels.delete", s.observabilityDeleteProxy("/notification-channels/{id}", "notification_channels.delete", "notification_channel")))
	s.mux.HandleFunc("POST /observability/notification-channels/{id}/test", s.requirePermission("notification_channels.test", s.observabilityPostActionWithAuditStatus("/notification-channels/{id}/test", "notification_channels.test", "notification_channel", http.StatusAccepted)))
}

func (s *Server) registerProfileRoutes(base string, kind store.ProfileKind, permissionPrefix string) {
	s.mux.HandleFunc("GET "+base, s.requirePermission(permissionPrefix+".read", s.listProfiles(kind)))
	s.mux.HandleFunc("POST "+base, s.requirePermission(permissionPrefix+".create", s.createProfile(kind, permissionPrefix+".create")))
	s.mux.HandleFunc("GET "+base+"/{id}", s.requirePermission(permissionPrefix+".read", s.getProfile(kind)))
	s.mux.HandleFunc("PUT "+base+"/{id}", s.requirePermission(permissionPrefix+".update", s.updateProfile(kind, permissionPrefix+".update")))
	s.mux.HandleFunc("DELETE "+base+"/{id}", s.requirePermission(permissionPrefix+".delete", s.deleteProfile(kind, permissionPrefix+".delete")))
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func limitRequestBody(next http.Handler, maximum int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && isUnsafeMethod(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, maximum)
		}
		next.ServeHTTP(w, r)
	})
}

func sessionCookieSecure() bool {
	override := strings.ToLower(strings.TrimSpace(os.Getenv("AUTOSTREAM_COOKIE_SECURE")))
	publicHTTPS := strings.HasPrefix(strings.ToLower(strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL"))), "https://")
	if override == "true" || override == "1" || override == "yes" {
		return true
	}
	if override == "false" || override == "0" || override == "no" {
		return !productionEnvironment() && publicHTTPS
	}
	if productionEnvironment() {
		return true
	}
	return publicHTTPS
}

func setOAuthStateCookie(w http.ResponseWriter, state store.OAuthLoginState) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    state.StateHash,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   sessionCookieSecure(),
		Expires:  state.ExpiresAt,
	})
}

func clearOAuthStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   sessionCookieSecure(),
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func oauthStateCookieMatches(r *http.Request, state store.OAuthLoginState) bool {
	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil {
		return false
	}
	return strings.TrimSpace(cookie.Value) != "" && strings.TrimSpace(cookie.Value) == state.StateHash
}

func oauthStateTokenCookieMatches(r *http.Request, stateToken string) bool {
	stateToken = strings.TrimSpace(stateToken)
	if stateToken == "" {
		return false
	}
	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil {
		return false
	}
	return strings.TrimSpace(cookie.Value) != "" && strings.TrimSpace(cookie.Value) == security.HashToken(stateToken)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "control-panel"})
}

func (s *Server) rootRedirect(w http.ResponseWriter, r *http.Request) {
	target := "/login"
	if s.auth != nil {
		count, err := s.auth.CountUsers(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "count_users_failed"})
			return
		}
		if s.setupToken != "" && count == 0 {
			target = "/setup"
		} else if _, ok := s.authenticate(r); ok {
			target = "/admin"
		}
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	setupEnabled := s.auth != nil && s.setupToken != ""
	if s.auth == nil {
		writeJSON(w, http.StatusOK, map[string]any{"setup_enabled": false, "setup_required": false})
		return
	}
	count, err := s.auth.CountUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "count_users_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_enabled":  setupEnabled,
		"setup_required": setupEnabled && count == 0,
	})
}

func (s *Server) setupFirstAdmin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil || s.setupToken == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "setup_disabled"})
		return
	}
	var body struct {
		SetupToken string `json:"setup_token"`
		Username   string `json:"username"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	setupAttemptKey := loginFailureKey("setup:first-admin", clientIP(r))
	if !s.loginFailures.allow(setupAttemptKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "setup_rate_limited"})
		return
	}
	if !security.VerifyTokenHash(body.SetupToken, security.HashToken(s.setupToken)) {
		s.loginFailures.record(setupAttemptKey)
		s.writeAudit(r, store.AuditEvent{Action: "setup.first_admin", ResourceType: "user", Result: "failure", Metadata: map[string]any{"reason": "invalid_setup_token"}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "invalid_setup_token"})
		return
	}
	count, err := s.auth.CountUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "count_users_failed"})
		return
	}
	if count > 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "users_already_exist"})
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "username_required"})
		return
	}
	if !s.passwordMeetsConfiguredPolicy(w, r, body.Password) {
		return
	}
	user, err := s.auth.CreateFirstAdmin(r.Context(), username, body.Password, security.DefaultPermissions)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_first_admin_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "setup.first_admin", ResourceType: "user", ResourceID: user.ID, Result: "success"})
	s.loginFailures.clear(setupAttemptKey)
	writeJSON(w, http.StatusCreated, map[string]any{"user": publicUser(user)})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "auth_store_not_configured"})
		return
	}
	var body struct {
		Username       string `json:"username"`
		Password       string `json:"password"`
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if status, code := s.turnstileFailure(r.Context(), r, body.TurnstileToken, "login"); code != "" {
		s.writeAudit(r, store.AuditEvent{Action: "auth.login", ResourceType: "user", ResourceID: body.Username, Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	failureKey := loginFailureKey(body.Username, clientIP(r))
	if !s.loginFailures.allow(failureKey, settings.LoginLockoutThreshold) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.login", ResourceType: "user", ResourceID: body.Username, Result: "failure", Metadata: map[string]any{"reason": "rate_limited"}})
		w.Header().Set("Retry-After", strconv.Itoa(int(defaultLoginFailureWindow/time.Second)))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "login_rate_limited"})
		return
	}
	user, err := s.auth.FindUserByUsername(r.Context(), body.Username)
	if err != nil || user.Status == "disabled" || user.Status == "locked" || !security.VerifyPassword(body.Password, user.PasswordHash) {
		s.loginFailures.record(failureKey)
		if s.auth != nil {
			_ = s.auth.RecordLoginFailure(r.Context(), body.Username, settings.LoginLockoutThreshold)
		}
		s.writeAudit(r, store.AuditEvent{Action: "auth.login", ResourceType: "user", ResourceID: body.Username, Result: "failure", Metadata: map[string]any{"reason": "invalid_credentials"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_credentials"})
		return
	}
	if passkeyRequiredForUser(settings, user) {
		s.rejectPasskeyRequiredLogin(w, r, user, "auth.login", nil)
		return
	}
	mfaConfig, mfaRequired, err := s.loginMFARequirement(r.Context(), settings, user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_state_unavailable"})
		return
	}
	if mfaRequired {
		if s.mfa == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "mfa_not_configured"})
			return
		}
		if mfaConfig.Enabled {
			challenge, err := s.mfa.CreateMFAChallenge(r.Context(), user.ID, 10*time.Minute)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_challenge_failed"})
				return
			}
			s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.login", ResourceType: "user", ResourceID: user.ID, Result: "mfa_required"})
			writeJSON(w, http.StatusAccepted, map[string]any{"mfa_required": true, "challenge_token": challenge.Token, "expires_at": challenge.ExpiresAt})
			return
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.login", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "mfa_enrollment_required"}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "mfa_enrollment_required"})
		return
	}
	s.completeLogin(w, r, user, settings)
}

func (s *Server) completeLogin(w http.ResponseWriter, r *http.Request, user store.User, settings store.SecuritySettings) {
	session, err := s.auth.CreateSession(r.Context(), user.ID, time.Duration(settings.SessionIdleTimeoutMin)*time.Minute, time.Duration(settings.SessionAbsoluteLifetimeH)*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_session_failed"})
		return
	}
	_ = s.auth.RecordLoginSuccess(r.Context(), user.ID, clientIP(r))
	s.loginFailures.clear(loginFailureKey(user.Username, clientIP(r)))
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: session.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: sessionCookieSecure(), Expires: session.AbsoluteExpiresAt,
	})
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.login", ResourceType: "user", ResourceID: user.ID, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]any{"csrf_token": session.CSRFToken, "user": publicUser(user)})
}

type oauthStartRequest struct {
	RedirectAfter  string `json:"redirect_after"`
	TurnstileToken string `json:"turnstile_token"`
}

type oauthCallbackRequest struct {
	ProviderID string `json:"provider_id"`
	State      string `json:"state"`
	Code       string `json:"code"`
}

type oauthUserLinkRequest struct {
	ProviderID   string `json:"provider_id"`
	ProviderType string `json:"provider_type"`
	Subject      string `json:"subject"`
	Email        string `json:"email"`
}

func (s *Server) listOAuthLoginProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.integrations.ListOAuthProviders(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_oauth_login_providers_failed"})
		return
	}
	out := []map[string]any{}
	for _, provider := range providers {
		if !provider.Enabled || !supportedLoginOAuthProvider(provider.ProviderType) {
			continue
		}
		out = append(out, publicOAuthLoginProvider(provider))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) startOAuthLogin(w http.ResponseWriter, r *http.Request) {
	var body oauthStartRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if status, code := s.turnstileFailure(r.Context(), r, body.TurnstileToken, "login"); code != "" {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.start", ResourceType: "oauth_provider", ResourceID: r.PathValue("id"), Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	provider, err := s.integrations.GetOAuthProvider(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_provider_failed"})
		return
	}
	if !provider.Enabled || !supportedLoginOAuthProvider(provider.ProviderType) || strings.TrimSpace(provider.ClientID) == "" || strings.TrimSpace(provider.RedirectURI) == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_login"})
		return
	}
	if !validOAuthRedirectURI(provider.RedirectURI, "/auth/oauth/callback") {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_redirect_uri_invalid"})
		return
	}
	oauthStartKey := loginFailureKey("oauth:start:"+provider.ID, clientIP(r))
	if !s.loginFailures.allow(oauthStartKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "oauth_start_rate_limited"})
		return
	}
	state, err := s.oauthLogin.CreateOAuthLoginState(r.Context(), store.OAuthLoginState{
		ProviderID:    provider.ID,
		ProviderType:  provider.ProviderType,
		Purpose:       "login",
		RedirectAfter: safeRedirectAfter(body.RedirectAfter),
	}, 10*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_state_create_failed"})
		return
	}
	s.loginFailures.record(oauthStartKey)
	authorizationURL, err := oauthAuthorizationURL(provider, state)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_login"})
		return
	}
	setOAuthStateCookie(w, state)
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"provider":          publicOAuthLoginProvider(provider),
		"authorization_url": authorizationURL,
		"state":             state.StateToken,
		"nonce":             state.Nonce,
		"expires_at":        state.ExpiresAt,
	})
}

func (s *Server) oauthLoginCallback(w http.ResponseWriter, r *http.Request) {
	setOAuthCallbackNoStoreHeaders(w)
	var body oauthCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	s.finishOAuthLogin(w, r, body, false)
}

func (s *Server) oauthLoginRedirectCallback(w http.ResponseWriter, r *http.Request) {
	setOAuthCallbackNoStoreHeaders(w)
	body := oauthCallbackRequest{
		ProviderID: r.URL.Query().Get("provider_id"),
		State:      r.URL.Query().Get("state"),
		Code:       r.URL.Query().Get("code"),
	}
	if s.isConnectedAccountOAuthRedirect(r, body.State) {
		s.requirePermission("integrations.create", s.oauthAccountRedirectCallback)(w, r)
		return
	}
	s.finishOAuthLogin(w, r, body, true)
}

func (s *Server) isConnectedAccountOAuthRedirect(r *http.Request, stateToken string) bool {
	if s.oauthLogin == nil || !oauthStateTokenCookieMatches(r, stateToken) {
		return false
	}
	state, err := s.oauthLogin.GetOAuthLoginState(r.Context(), stateToken)
	return err == nil && state.Purpose == "connected_account"
}

func setOAuthCallbackNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (s *Server) finishOAuthLogin(w http.ResponseWriter, r *http.Request, body oauthCallbackRequest, redirectOnSuccess bool) {
	if !oauthStateTokenCookieMatches(r, body.State) {
		clearOAuthStateCookie(w)
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_cookie_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	state, err := s.oauthLogin.ConsumeOAuthLoginState(r.Context(), body.State)
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "invalid_or_expired_state"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_state_consume_failed"})
		return
	}
	if !oauthStateCookieMatches(r, state) {
		clearOAuthStateCookie(w)
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_cookie_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	clearOAuthStateCookie(w)
	if strings.TrimSpace(body.ProviderID) != "" && strings.TrimSpace(body.ProviderID) != state.ProviderID {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_provider", ResourceID: body.ProviderID, Result: "failure", Metadata: map[string]any{"reason": "provider_state_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	if state.Purpose != "login" && state.Purpose != "account_link" {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_purpose_mismatch", "purpose": state.Purpose}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	provider, err := s.integrations.GetOAuthProviderForDispatch(r.Context(), state.ProviderID)
	if errors.Is(err, store.ErrNotFound) || !provider.Enabled || strings.TrimSpace(provider.ClientSecret) == "" {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_provider", ResourceID: state.ProviderID, Result: "failure", Metadata: map[string]any{"reason": "provider_unavailable"}})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_unavailable"})
		return
	}
	if err != nil {
		code := "oauth_provider_unavailable"
		if errors.Is(err, store.ErrSecretKeyRequired) {
			code = "secret_encryption_key_required"
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": code})
		return
	}
	identity, err := s.oauthVerifier.Verify(r.Context(), oauthlogin.VerifyRequest{Provider: provider, Code: body.Code, Nonce: state.Nonce})
	if err != nil {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": "identity_verification_failed", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "oauth_identity_verification_failed"})
		return
	}
	if identity.ProviderID != provider.ID || identity.Subject == "" || !identityAllowedForProvider(provider, identity) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": "identity_not_allowed", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "oauth_identity_not_allowed"})
		return
	}
	if state.Purpose == "account_link" {
		s.finishCurrentUserOAuthLink(w, r, provider, state, identity, redirectOnSuccess)
		return
	}
	link, err := s.oauthLogin.FindOAuthUserLink(r.Context(), provider.ID, identity.Subject)
	if errors.Is(err, store.ErrNotFound) {
		user, provisioned, provisionErr := s.autoProvisionOAuthUser(r.Context(), provider, identity)
		if provisionErr != nil {
			reason := "account_not_linked"
			if provider.AutoProvision {
				reason = "auto_provision_failed"
			}
			s.writeAudit(r, store.AuditEvent{Action: "auth.oauth.login", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": reason, "provider_type": provider.ProviderType}})
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "oauth_account_not_linked"})
			return
		}
		if provisioned {
			s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.oauth.provision_user", ResourceType: "user", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"provider_type": provider.ProviderType, "default_role_count": len(provider.DefaultRoleIDs), "email_present": identity.Email != ""}})
		}
		s.continueOAuthLogin(w, r, user, provider, state, redirectOnSuccess)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_link_lookup_failed"})
		return
	}
	user, err := s.auth.GetUser(r.Context(), link.UserID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "oauth_account_not_linked"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_user_lookup_failed"})
		return
	}
	if user.Status == "disabled" || user.Status == "locked" {
		s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.oauth.login", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "account_unavailable", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "account_unavailable"})
		return
	}
	s.continueOAuthLogin(w, r, user, provider, state, redirectOnSuccess)
}

func (s *Server) finishCurrentUserOAuthLink(w http.ResponseWriter, r *http.Request, provider store.OAuthProvider, state store.OAuthLoginState, identity oauthlogin.Identity, redirectOnSuccess bool) {
	current, ok := s.authenticate(r)
	if !ok {
		s.writeAudit(r, store.AuditEvent{Action: "auth.oauth_link.create", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": "unauthorized", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
		return
	}
	if current.User.Status == "disabled" || current.User.Status == "locked" {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.oauth_link.create", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "account_unavailable", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "account_unavailable"})
		return
	}
	existing, err := s.oauthLogin.FindOAuthUserLink(r.Context(), provider.ID, identity.Subject)
	if err == nil {
		if existing.UserID != current.User.ID {
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.oauth_link.create", ResourceType: "oauth_link", ResourceID: existing.ID, Result: "failure", Metadata: map[string]any{"reason": "identity_already_linked", "provider_type": provider.ProviderType}})
			writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_identity_already_linked"})
			return
		}
		s.finishOAuthLinkResponse(w, r, state, existing, redirectOnSuccess, http.StatusOK)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_link_lookup_failed"})
		return
	}
	link, err := s.oauthLogin.LinkOAuthUser(r.Context(), store.OAuthUserLink{
		UserID:       current.User.ID,
		ProviderID:   provider.ID,
		ProviderType: provider.ProviderType,
		Subject:      identity.Subject,
		Email:        identity.Email,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_oauth_user_link_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.oauth_link.create", ResourceType: "oauth_link", ResourceID: link.ID, Result: "success", Metadata: map[string]any{"provider_type": provider.ProviderType, "email_present": identity.Email != ""}})
	s.finishOAuthLinkResponse(w, r, state, link, redirectOnSuccess, http.StatusCreated)
}

func (s *Server) finishOAuthLinkResponse(w http.ResponseWriter, r *http.Request, state store.OAuthLoginState, link store.OAuthUserLink, redirectOnSuccess bool, status int) {
	if redirectOnSuccess {
		target := safeRedirectAfter(state.RedirectAfter)
		if target == "" {
			target = "/admin/account/"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	writeJSON(w, status, link)
}

func (s *Server) continueOAuthLogin(w http.ResponseWriter, r *http.Request, user store.User, provider store.OAuthProvider, state store.OAuthLoginState, redirectOnSuccess bool) {
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	if passkeyRequiredForUser(settings, user) {
		s.rejectPasskeyRequiredLogin(w, r, user, "auth.oauth.login", map[string]any{"provider_type": provider.ProviderType})
		return
	}
	mfaConfig, mfaRequired, err := s.loginMFARequirement(r.Context(), settings, user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_state_unavailable"})
		return
	}
	if mfaRequired {
		if s.mfa == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "mfa_not_configured"})
			return
		}
		if mfaConfig.Enabled {
			challenge, err := s.mfa.CreateMFAChallenge(r.Context(), user.ID, 10*time.Minute)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_challenge_failed"})
				return
			}
			s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.oauth.login", ResourceType: "user", ResourceID: user.ID, Result: "mfa_required", Metadata: map[string]any{"provider_type": provider.ProviderType}})
			if redirectOnSuccess {
				redirectOAuthMFAChallenge(w, r, challenge, state.RedirectAfter)
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"mfa_required": true, "challenge_token": challenge.Token, "expires_at": challenge.ExpiresAt})
			return
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.oauth.login", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "mfa_enrollment_required", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "mfa_enrollment_required"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.oauth.login", ResourceType: "user", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"provider_type": provider.ProviderType}})
	if redirectOnSuccess {
		s.completeLoginRedirect(w, r, user, settings, state.RedirectAfter)
		return
	}
	s.completeLogin(w, r, user, settings)
}

func redirectOAuthMFAChallenge(w http.ResponseWriter, r *http.Request, challenge store.MFAChallenge, redirectAfter string) {
	fragment := url.Values{}
	fragment.Set("oauth_mfa_challenge", challenge.Token)
	fragment.Set("expires_at", challenge.ExpiresAt.UTC().Format(time.RFC3339Nano))
	target := "/login"
	if redirectAfter = safeRedirectAfter(redirectAfter); redirectAfter != "" {
		target += "?" + url.Values{"redirect_after": []string{redirectAfter}}.Encode()
	}
	http.Redirect(w, r, target+"#"+fragment.Encode(), http.StatusSeeOther)
}

func mfaRequiredForUser(settings store.SecuritySettings, user store.User) bool {
	return settings.MFAMode == "totp" && mfaPolicyAppliesToUser(settings, user)
}

func (s *Server) loginMFARequirement(ctx context.Context, settings store.SecuritySettings, user store.User) (store.MFAConfig, bool, error) {
	policyRequired := mfaRequiredForUser(settings, user)
	if s.mfa == nil {
		return store.MFAConfig{}, policyRequired, nil
	}
	cfg, err := s.mfa.GetMFAConfig(ctx, user.ID)
	if err != nil {
		return store.MFAConfig{}, false, err
	}
	return cfg, cfg.Enabled || policyRequired, nil
}

func passkeyRequiredForUser(settings store.SecuritySettings, user store.User) bool {
	return settings.MFAMode == "passkey" && mfaPolicyAppliesToUser(settings, user)
}

func mfaPolicyAppliesToUser(settings store.SecuritySettings, user store.User) bool {
	if settings.MFAMode == "disabled" {
		return false
	}
	requiredRoles := settings.MFARequiredRoles
	if len(requiredRoles) == 0 {
		return true
	}
	granted := map[string]bool{}
	for _, role := range user.Roles {
		granted[strings.TrimSpace(role)] = true
	}
	for _, role := range requiredRoles {
		if granted[strings.TrimSpace(role)] {
			return true
		}
	}
	return false
}

func (s *Server) rejectPasskeyRequiredLogin(w http.ResponseWriter, r *http.Request, user store.User, action string, metadata map[string]any) {
	if s.passkeys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "passkeys_not_configured"})
		return
	}
	credentials, err := s.passkeys.ListPasskeyCredentialsForVerification(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_state_unavailable"})
		return
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	result := "passkey_required"
	code := "passkey_required"
	if len(credentials) == 0 {
		result = "failure"
		code = "passkey_enrollment_required"
		metadata["reason"] = code
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: action, ResourceType: "user", ResourceID: user.ID, Result: result, Metadata: metadata})
	writeJSON(w, http.StatusForbidden, map[string]string{"code": code})
}

func (s *Server) autoProvisionOAuthUser(ctx context.Context, provider store.OAuthProvider, identity oauthlogin.Identity) (store.User, bool, error) {
	if !provider.AutoProvision || len(provider.DefaultRoleIDs) == 0 || s.users == nil {
		return store.User{}, false, store.ErrNotFound
	}
	base := oauthProvisionUsername(provider.ProviderType, identity)
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		username := base
		if attempt > 0 {
			username = base + "-" + strconv.Itoa(attempt+1)
		}
		user, err := s.users.CreateOAuthUser(ctx, username, identity.Email, provider.DefaultRoleIDs)
		if err != nil {
			lastErr = err
			continue
		}
		if _, err := s.oauthLogin.LinkOAuthUser(ctx, store.OAuthUserLink{
			UserID:       user.ID,
			ProviderID:   provider.ID,
			ProviderType: provider.ProviderType,
			Subject:      identity.Subject,
			Email:        identity.Email,
		}); err != nil {
			return store.User{}, false, err
		}
		return user, true, nil
	}
	if lastErr == nil {
		lastErr = store.ErrNotFound
	}
	return store.User{}, false, lastErr
}

func oauthProvisionUsername(providerType string, identity oauthlogin.Identity) string {
	if email := strings.TrimSpace(strings.ToLower(identity.Email)); email != "" {
		return safeOAuthUsername(email)
	}
	return safeOAuthUsername(providerType + "-" + identity.Subject)
}

func safeOAuthUsername(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '@' || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), ".-_@")
	if out == "" {
		return "oauth-user"
	}
	if len(out) > 96 {
		out = out[:96]
	}
	return out
}

func (s *Server) completeLoginRedirect(w http.ResponseWriter, r *http.Request, user store.User, settings store.SecuritySettings, redirectAfter string) {
	session, err := s.auth.CreateSession(r.Context(), user.ID, time.Duration(settings.SessionIdleTimeoutMin)*time.Minute, time.Duration(settings.SessionAbsoluteLifetimeH)*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_session_failed"})
		return
	}
	_ = s.auth.RecordLoginSuccess(r.Context(), user.ID, clientIP(r))
	s.loginFailures.clear(loginFailureKey(user.Username, clientIP(r)))
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: session.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: sessionCookieSecure(), Expires: session.AbsoluteExpiresAt,
	})
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.login", ResourceType: "user", ResourceID: user.ID, Result: "success"})
	target := safeRedirectAfter(redirectAfter)
	if target == "" {
		target = "/"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) startCurrentUserOAuthLink(w http.ResponseWriter, r *http.Request) {
	var body oauthStartRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	provider, err := s.integrations.GetOAuthProvider(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_provider_failed"})
		return
	}
	if !provider.Enabled || !supportedLoginOAuthProvider(provider.ProviderType) || strings.TrimSpace(provider.ClientID) == "" || strings.TrimSpace(provider.RedirectURI) == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_login"})
		return
	}
	if !validOAuthRedirectURI(provider.RedirectURI, "/auth/oauth/callback") {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_redirect_uri_invalid"})
		return
	}
	state, err := s.oauthLogin.CreateOAuthLoginState(r.Context(), store.OAuthLoginState{
		ProviderID:    provider.ID,
		ProviderType:  provider.ProviderType,
		Purpose:       "account_link",
		RedirectAfter: safeRedirectAfter(body.RedirectAfter),
	}, 10*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_state_create_failed"})
		return
	}
	authorizationURL, err := oauthAuthorizationURL(provider, state)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_login"})
		return
	}
	setOAuthStateCookie(w, state)
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"provider":          publicOAuthLoginProvider(provider),
		"authorization_url": authorizationURL,
		"state":             state.StateToken,
		"nonce":             state.Nonce,
		"expires_at":        state.ExpiresAt,
	})
}

func (s *Server) listUserOAuthLinks(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if _, err := s.auth.GetUser(r.Context(), userID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	links, err := s.oauthLogin.ListOAuthUserLinks(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_oauth_user_links_failed"})
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *Server) createUserOAuthLink(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	user, err := s.auth.GetUser(r.Context(), userID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.oauth_link.create", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "manual_oauth_link_disabled"}})
	writeJSON(w, http.StatusForbidden, map[string]string{"code": "manual_oauth_link_disabled"})
	return
}

func (s *Server) deleteUserOAuthLink(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	if err := s.oauthLogin.DeleteOAuthUserLink(r.Context(), r.PathValue("link_id"), userID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_user_link_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.oauth_link.delete", ResourceType: "user", ResourceID: userID, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) listCurrentUserOAuthLinks(w http.ResponseWriter, r *http.Request) {
	if s.oauthLogin == nil {
		writeJSON(w, http.StatusOK, []store.OAuthUserLink{})
		return
	}
	current := currentFromContext(r.Context())
	links, err := s.oauthLogin.ListOAuthUserLinks(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_oauth_user_links_failed"})
		return
	}
	writeJSON(w, http.StatusOK, links)
}

func (s *Server) deleteCurrentUserOAuthLink(w http.ResponseWriter, r *http.Request) {
	if s.oauthLogin == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	current := currentFromContext(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "oauth_link_id_required"})
		return
	}
	if err := s.oauthLogin.DeleteOAuthUserLink(r.Context(), id, current.User.ID); errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.oauth_link.delete", ResourceType: "oauth_link", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "not_found"}})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_user_link_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.oauth_link.delete", ResourceType: "oauth_link", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.auth.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: sessionCookieSecure(), Expires: time.Unix(0, 0), MaxAge: -1})
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.logout", ResourceType: "session", Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) refreshSession(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	session, err := s.auth.RefreshSession(r.Context(), current.Session.Token, time.Duration(settings.SessionIdleTimeoutMin)*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":              "refreshed",
		"idle_expires_at":     session.IdleExpiresAt,
		"absolute_expires_at": session.AbsoluteExpiresAt,
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	user := publicUser(current.User)
	if s.avatars != nil {
		if info, err := s.avatars.GetUserAvatarInfo(r.Context(), current.User.ID); err == nil {
			user["avatar_url"] = userAvatarURL(info)
			user["avatar_updated_at"] = info.UpdatedAt
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user, "permissions": current.Permissions})
}

func (s *Server) updateCurrentUserEmail(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "users_not_configured"})
		return
	}
	if s.emailChanges == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "email_change_not_configured"})
		return
	}
	email, ok := normalizeSMTPTestRecipient(body.Email)
	if !ok {
		code := "invalid_email"
		if strings.TrimSpace(body.Email) == "" {
			code = "email_required"
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	maskedEmail := maskEmailAddress(email)
	if strings.EqualFold(email, strings.TrimSpace(current.User.Email)) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"unchanged": true, "target": maskedEmail}})
		writeJSON(w, http.StatusOK, map[string]string{"status": "unchanged", "target": maskedEmail})
		return
	}
	settings, password, status, code := s.mailSettingsForRequest(r.Context())
	if code != "" {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": code, "target": maskedEmail}})
		writeJSON(w, status, map[string]string{"code": code, "target": maskedEmail})
		return
	}
	challenge, err := s.emailChanges.CreateEmailChangeChallenge(r.Context(), current.User.ID, email, emailChangeChallengeTTL)
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "not_found", "target": maskedEmail}})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "target": maskedEmail})
		return
	}
	if err != nil {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "email_change_create_failed", "target": maskedEmail}})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "email_change_create_failed", "target": maskedEmail})
		return
	}
	if err := s.sendEmailChangeConfirmation(r, settings, password, current.User, challenge); err != nil {
		_, _ = s.emailChanges.ConsumeEmailChangeChallenge(r.Context(), challenge.Token)
		code := safeErrorCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": code, "target": maskedEmail}})
		writeJSON(w, smtpTestStatus(code), map[string]string{"code": code, "target": maskedEmail})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.email.change_request", ResourceType: "user", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"target": maskedEmail}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmation_sent", "target": maskedEmail})
}

func (s *Server) confirmCurrentUserEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token          string `json:"token"`
		TurnstileToken string `json:"turnstile_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if status, code := s.turnstileFailure(r.Context(), r, body.TurnstileToken, "email_confirm"); code != "" {
		s.writeAudit(r, store.AuditEvent{Action: "auth.email.confirm", ResourceType: "email_change", Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if s.emailChanges == nil || s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "email_change_not_configured"})
		return
	}
	challenge, err := s.emailChanges.ConsumeEmailChangeChallenge(r.Context(), body.Token)
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.email.confirm", ResourceType: "email_change", Result: "failure", Metadata: map[string]any{"reason": "invalid_or_expired_token"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_email_change_token"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "email_change_confirm_failed"})
		return
	}
	user, err := s.users.UpdateUser(r.Context(), challenge.UserID, store.UserPatch{Email: &challenge.Email})
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{Action: "auth.email.confirm", ResourceType: "user", ResourceID: challenge.UserID, Result: "failure", Metadata: map[string]any{"reason": "not_found"}})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		s.writeAudit(r, store.AuditEvent{Action: "auth.email.confirm", ResourceType: "user", ResourceID: challenge.UserID, Result: "failure", Metadata: map[string]any{"reason": "invalid_email"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_email"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "auth.email.confirm", ResourceType: "user", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"target": maskEmailAddress(user.Email)}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "email_changed", "target": maskEmailAddress(user.Email)})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if !security.VerifyPassword(body.CurrentPassword, current.User.PasswordHash) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.change_password", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "current_password_invalid"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "current_password_invalid"})
		return
	}
	if !s.passwordMeetsConfiguredPolicy(w, r, body.NewPassword) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.change_password", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "password_policy"}})
		return
	}
	if err := s.auth.ChangePassword(r.Context(), current.User.ID, body.NewPassword); err != nil {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.change_password", ResourceType: "user", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "password_policy"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "password_change_failed"})
		return
	}
	_ = s.auth.DeleteUserSessions(r.Context(), current.User.ID)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: sessionCookieSecure(), Expires: time.Unix(0, 0), MaxAge: -1})
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "auth.change_password", ResourceType: "user", ResourceID: current.User.ID, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "password_changed"})
}

func (s *Server) mfaStatus(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	if s.mfa == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available":          false,
			"enabled":            false,
			"pending_enrollment": false,
			"policy_mode":        settings.MFAMode,
			"required":           mfaRequiredForUser(settings, current.User),
		})
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_state_unavailable"})
		return
	}
	method := ""
	if cfg.Enabled || strings.TrimSpace(cfg.PendingTOTPSecret) != "" {
		method = "totp"
	}
	response := map[string]any{
		"available":           true,
		"enabled":             cfg.Enabled,
		"method":              method,
		"pending_enrollment":  strings.TrimSpace(cfg.PendingTOTPSecret) != "",
		"recovery_code_count": len(cfg.RecoveryCodeHashes),
		"policy_mode":         settings.MFAMode,
		"required":            cfg.Enabled || mfaRequiredForUser(settings, current.User),
	}
	if !cfg.UpdatedAt.IsZero() {
		response["updated_at"] = cfg.UpdatedAt
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) mfaEnroll(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !s.mfaAvailable(w, r, "mfa.enroll") {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	if settings.MFAMode == "passkey" {
		code := "totp_mfa_unavailable"
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.enroll", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": code, "mfa_mode": settings.MFAMode}})
		writeJSON(w, http.StatusConflict, map[string]string{"code": code})
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_state_unavailable"})
		return
	}
	if cfg.Enabled {
		attemptKey := loginFailureKey("mfa:enroll:"+current.User.ID, clientIP(r))
		if !s.loginFailures.allow(attemptKey, sensitiveActionAttemptThreshold) {
			w.Header().Set("Retry-After", "300")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "mfa_rate_limited"})
			return
		}
		if _, ok := s.verifyMFAConfigCode(r.Context(), current.User.ID, cfg, body.Code); !ok {
			s.loginFailures.record(attemptKey)
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.enroll", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_current_code"}})
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_code"})
			return
		}
		s.loginFailures.clear(attemptKey)
	}
	secret, err := security.GenerateTOTPSecret()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_secret_failed"})
		return
	}
	recoveryCodes, hashes, err := generateRecoveryCodesAndHashes()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "recovery_codes_failed"})
		return
	}
	if err := s.mfa.StartTOTPEnrollment(r.Context(), current.User.ID, secret, hashes); err != nil {
		code := "mfa_enroll_failed"
		if errors.Is(err, store.ErrSecretKeyRequired) {
			code = "secret_encryption_key_required"
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.enroll", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": code})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.enroll", ResourceType: "mfa", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"method": "totp", "recovery_codes_issued": len(recoveryCodes)}})
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"method":           "totp",
		"secret":           secret,
		"provisioning_uri": security.ProvisioningURI("AutoStream", current.User.Username, secret),
		"recovery_codes":   recoveryCodes,
		"message":          "Verify a TOTP code to enable MFA. Recovery codes are shown only once.",
	})
}

func (s *Server) mfaVerify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChallengeToken string `json:"challenge_token"`
		Code           string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if current, ok := s.authenticate(r); ok {
		if isUnsafeMethod(r.Method) && !security.VerifyTokenHash(r.Header.Get("X-CSRF-Token"), current.Session.CSRFTokenHash) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "csrf_failed"})
			return
		}
		s.mfaVerifyEnrollment(w, r, current, body.Code)
		return
	}
	s.mfaVerifyLoginChallenge(w, r, body.ChallengeToken, body.Code)
}

func (s *Server) mfaVerifyEnrollment(w http.ResponseWriter, r *http.Request, current currentUser, code string) {
	if !s.mfaAvailable(w, r, "mfa.verify") {
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_state_unavailable"})
		return
	}
	if cfg.PendingTOTPSecret == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "mfa_enrollment_not_pending"})
		return
	}
	attemptKey := loginFailureKey("mfa:verify:"+current.User.ID, clientIP(r))
	if !s.loginFailures.allow(attemptKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "mfa_rate_limited"})
		return
	}
	if !security.VerifyTOTP(cfg.PendingTOTPSecret, code, time.Now()) {
		s.loginFailures.record(attemptKey)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.verify", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_code"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_code"})
		return
	}
	if err := s.mfa.ConfirmTOTPEnrollment(r.Context(), current.User.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_verify_failed"})
		return
	}
	s.loginFailures.clear(attemptKey)
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.verify", ResourceType: "mfa", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"method": "totp"}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "mfa_enabled", "method": "totp"})
}

func (s *Server) mfaVerifyLoginChallenge(w http.ResponseWriter, r *http.Request, challengeToken, code string) {
	if s.mfa == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "mfa_not_configured"})
		return
	}
	challenge, err := s.mfa.GetMFAChallenge(r.Context(), strings.TrimSpace(challengeToken))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_challenge"})
		return
	}
	user, err := s.auth.GetUser(r.Context(), challenge.UserID)
	if err != nil || user.Status == "disabled" || user.Status == "locked" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_challenge"})
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), user.ID)
	if err != nil || !cfg.Enabled {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "mfa_not_enabled"})
		return
	}
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return
	}
	usedRecovery, ok := s.verifyMFAConfigCode(r.Context(), user.ID, cfg, code)
	if !ok {
		_ = s.mfa.DeleteMFAChallenge(r.Context(), challengeToken)
		_ = s.auth.RecordLoginFailure(r.Context(), user.Username, settings.LoginLockoutThreshold)
		s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "mfa.verify", ResourceType: "mfa", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_code"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_code"})
		return
	}
	_ = s.mfa.DeleteMFAChallenge(r.Context(), challengeToken)
	s.writeAudit(r, store.AuditEvent{ActorUserID: user.ID, ActorUsername: user.Username, Action: "mfa.verify", ResourceType: "mfa", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"method": map[bool]string{true: "recovery_code", false: "totp"}[usedRecovery]}})
	s.completeLogin(w, r, user, settings)
}

func (s *Server) mfaDisable(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !s.mfaAvailable(w, r, "mfa.disable") {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), current.User.ID)
	if err != nil || !cfg.Enabled {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "mfa_not_enabled"})
		return
	}
	attemptKey := loginFailureKey("mfa:disable:"+current.User.ID, clientIP(r))
	if !s.loginFailures.allow(attemptKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "mfa_rate_limited"})
		return
	}
	if _, ok := s.verifyMFAConfigCode(r.Context(), current.User.ID, cfg, body.Code); !ok {
		s.loginFailures.record(attemptKey)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.disable", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_code"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_code"})
		return
	}
	if err := s.mfa.DisableMFA(r.Context(), current.User.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "mfa_disable_failed"})
		return
	}
	s.loginFailures.clear(attemptKey)
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.disable", ResourceType: "mfa", ResourceID: current.User.ID, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "mfa_disabled"})
}

func (s *Server) mfaRegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !s.mfaAvailable(w, r, "mfa.recovery_codes.regenerate") {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	cfg, err := s.mfa.GetMFAConfig(r.Context(), current.User.ID)
	if err != nil || !cfg.Enabled {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "mfa_not_enabled"})
		return
	}
	attemptKey := loginFailureKey("mfa:recovery-regenerate:"+current.User.ID, clientIP(r))
	if !s.loginFailures.allow(attemptKey, sensitiveActionAttemptThreshold) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": "mfa_rate_limited"})
		return
	}
	if _, ok := s.verifyMFAConfigCode(r.Context(), current.User.ID, cfg, body.Code); !ok {
		s.loginFailures.record(attemptKey)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.recovery_codes.regenerate", ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "invalid_code"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_mfa_code"})
		return
	}
	codes, hashes, err := generateRecoveryCodesAndHashes()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "recovery_codes_failed"})
		return
	}
	if err := s.mfa.RegenerateRecoveryCodes(r.Context(), current.User.ID, hashes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "recovery_codes_failed"})
		return
	}
	s.loginFailures.clear(attemptKey)
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "mfa.recovery_codes.regenerate", ResourceType: "mfa", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"recovery_codes_issued": len(codes)}})
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes, "message": "Recovery codes are shown only once."})
}

func (s *Server) verifyMFAConfigCode(ctx context.Context, userID string, cfg store.MFAConfig, code string) (bool, bool) {
	if security.VerifyTOTP(cfg.TOTPSecret, code, time.Now()) {
		return false, true
	}
	hash := security.HashRecoveryCode(code)
	for _, candidate := range cfg.RecoveryCodeHashes {
		if candidate == hash {
			if err := s.mfa.ConsumeRecoveryCode(ctx, userID, hash); err != nil {
				return false, false
			}
			return true, true
		}
	}
	return false, false
}

func (s *Server) mfaAvailable(w http.ResponseWriter, r *http.Request, action string) bool {
	if s.mfa != nil {
		return true
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: "mfa", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "mfa_not_configured"}})
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "mfa_not_configured"})
	return false
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w, r, "passkeys.list") {
		return
	}
	current := currentFromContext(r.Context())
	credentials, err := s.passkeys.ListPasskeyCredentials(r.Context(), current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkeys_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, credentials)
}

func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w, r, "passkeys.delete") {
		return
	}
	current := currentFromContext(r.Context())
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "passkey_id_required"})
		return
	}
	if err := s.passkeys.DeletePasskeyCredential(r.Context(), current.User.ID, id); errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.delete", ResourceType: "passkey", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "not_found"}})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_delete_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.delete", ResourceType: "passkey", ResourceID: id, Result: "success"})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) passkeysAvailable(w http.ResponseWriter, r *http.Request, action string) bool {
	if s.passkeys != nil {
		return true
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: "passkey", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "passkeys_not_configured"}})
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "passkeys_not_configured"})
	return false
}

type passkeyRegistrationStartRequest struct {
	DisplayName string `json:"display_name"`
}

type passkeyRegistrationStartResponse struct {
	RegistrationToken string    `json:"registration_token"`
	ExpiresAt         time.Time `json:"expires_at"`
	PublicKey         any       `json:"public_key"`
}

type passkeyCreationOptionsJSON struct {
	Challenge              string                        `json:"challenge"`
	RP                     passkeyRelyingPartyJSON       `json:"rp"`
	User                   passkeyUserJSON               `json:"user"`
	PubKeyCredParams       []passkeyCredentialParamJSON  `json:"pubKeyCredParams"`
	Timeout                int                           `json:"timeout"`
	Attestation            string                        `json:"attestation"`
	AuthenticatorSelection passkeyAuthenticatorSelection `json:"authenticatorSelection"`
}

type passkeyRelyingPartyJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type passkeyUserJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type passkeyCredentialParamJSON struct {
	Type string `json:"type"`
	Alg  int    `json:"alg"`
}

type passkeyAuthenticatorSelection struct {
	ResidentKey      string `json:"residentKey"`
	UserVerification string `json:"userVerification"`
}

func (s *Server) startPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.passkeysAvailable(w, r, "passkeys.registration.start") {
		return
	}
	current := currentFromContext(r.Context())
	var input passkeyRegistrationStartRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
	}
	rpID, rpName := passkeyRelyingParty(r)
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = current.User.Username
	}
	response, err := s.beginPasskeyRegistration(r, current.User, displayName, rpID, rpName)
	if err != nil {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.registration.start", ResourceType: "passkey", ResourceID: current.User.ID, Result: "failure", Metadata: map[string]any{"reason": "challenge_create_failed"}})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "passkey_registration_challenge_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "passkeys.registration.start", ResourceType: "passkey", ResourceID: current.User.ID, Result: "success", Metadata: map[string]any{"rp_id": rpID}})
	writeOneTimeSecretJSON(w, http.StatusCreated, response)
}

func passkeyRelyingParty(r *http.Request) (string, string) {
	rpName := strings.TrimSpace(os.Getenv("AUTOSTREAM_WEBAUTHN_RP_NAME"))
	if rpName == "" {
		rpName = "AutoStream Control Panel"
	}
	if rpID := strings.TrimSpace(os.Getenv("AUTOSTREAM_WEBAUTHN_RP_ID")); rpID != "" {
		return rpID, rpName
	}
	if publicURL := strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL")); publicURL != "" {
		if parsed, err := url.Parse(publicURL); err == nil && parsed.Hostname() != "" {
			return parsed.Hostname(), rpName
		}
	}
	if host := strings.TrimSpace(r.Host); host != "" {
		if parsedHost, _, err := net.SplitHostPort(host); err == nil && parsedHost != "" {
			return parsedHost, rpName
		}
		return strings.TrimPrefix(host, "["), rpName
	}
	return "localhost", rpName
}

func generateRecoveryCodesAndHashes() ([]string, []string, error) {
	codes, err := security.GenerateRecoveryCodes(10)
	if err != nil {
		return nil, nil, err
	}
	hashes := make([]string, 0, len(codes))
	for _, code := range codes {
		hashes = append(hashes, security.HashRecoveryCode(code))
	}
	return codes, hashes, nil
}

func (s *Server) listPermissions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, security.DefaultPermissions)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_users_failed"})
		return
	}
	writeJSON(w, http.StatusOK, publicUsers(users))
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username          string   `json:"username"`
		Email             string   `json:"email"`
		TemporaryPassword string   `json:"temporary_password"`
		RoleIDs           []string `json:"role_ids"`
		SendWelcomeEmail  bool     `json:"send_welcome_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "username_required"})
		return
	}
	email, ok := normalizeSMTPTestRecipient(body.Email)
	if !ok {
		code := "invalid_email"
		if strings.TrimSpace(body.Email) == "" {
			code = "email_required"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if !s.passwordMeetsConfiguredPolicy(w, r, body.TemporaryPassword) {
		return
	}
	if len(body.RoleIDs) > 0 && !security.HasPermission(currentFromContext(r.Context()).Permissions, "roles.assign") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	if len(body.RoleIDs) > 0 {
		if err := s.validateRoleAssignments(r.Context(), body.RoleIDs); err != nil {
			writeRoleAssignmentError(w, err)
			return
		}
	}
	user, err := s.users.CreateUser(r.Context(), username, email, body.TemporaryPassword, body.RoleIDs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	emailSent := false
	if body.SendWelcomeEmail && user.Email != "" {
		if err := s.sendUserWelcomeEmail(r, user); err != nil {
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.email_welcome", ResourceType: "user", ResourceID: user.ID, Result: "failure", Metadata: map[string]any{"reason": safeErrorCode(err)}})
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "welcome_email_failed"})
			return
		}
		emailSent = true
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.create", ResourceType: "user", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"username": user.Username, "email_present": user.Email != "", "welcome_email_sent": emailSent}})
	writeJSON(w, http.StatusCreated, publicUser(user))
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	user, err := s.users.GetUser(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	writeJSON(w, http.StatusOK, publicUser(user))
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string   `json:"username"`
		Email    *string  `json:"email"`
		RoleIDs  []string `json:"role_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if body.RoleIDs != nil && !security.HasPermission(currentFromContext(r.Context()).Permissions, "roles.assign") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	if body.RoleIDs != nil {
		if err := s.validateUserRoleUpdate(r.Context(), r.PathValue("id"), body.RoleIDs); err != nil {
			status := http.StatusInternalServerError
			code := "validate_user_roles_failed"
			if errors.Is(err, store.ErrNotFound) {
				status = http.StatusNotFound
				code = "not_found"
			}
			if errors.Is(err, errCannotUpdateOwnRoles) {
				status = http.StatusForbidden
				code = "cannot_update_own_roles"
			}
			if errors.Is(err, store.ErrLastSuperAdmin) {
				status = http.StatusConflict
				code = "last_super_admin"
			}
			if errors.Is(err, store.ErrSuperAdminAssignmentForbidden) {
				status = http.StatusForbidden
				code = "cannot_assign_super_admin"
			}
			if errors.Is(err, store.ErrPermissionEscalation) {
				status = http.StatusForbidden
				code = "permission_escalation"
			}
			if errors.Is(err, store.ErrUnknownPermission) {
				status = http.StatusBadRequest
				code = "invalid_permissions"
			}
			if errors.Is(err, errInvalidRoleAssignment) {
				status = http.StatusBadRequest
				code = "invalid_role_assignment"
			}
			writeJSON(w, status, map[string]string{"code": code})
			return
		}
	}
	user, err := s.users.UpdateUser(r.Context(), r.PathValue("id"), store.UserPatch{Username: strings.TrimSpace(body.Username), Email: body.Email, RoleIDs: body.RoleIDs})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.update", ResourceType: "user", ResourceID: user.ID, Result: "success"})
	writeJSON(w, http.StatusOK, publicUser(user))
}

var (
	errCannotUpdateOwnRoles  = errors.New("cannot update own role assignments")
	errInvalidRoleAssignment = errors.New("invalid role assignment")
)

func (s *Server) validateUserRoleUpdate(ctx context.Context, targetUserID string, roleIDs []string) error {
	current := currentFromContext(ctx)
	if targetUserID == current.User.ID {
		return errCannotUpdateOwnRoles
	}
	target, err := s.users.GetUser(ctx, targetUserID)
	if err != nil {
		return err
	}
	if err := s.validateRoleAssignments(ctx, roleIDs); err != nil {
		return err
	}
	if !userHasRoleName(target, "super_admin") || target.Status != "active" {
		return nil
	}
	includesSuperAdmin, err := s.roleIDsIncludeRoleName(ctx, roleIDs, "super_admin")
	if err != nil {
		return err
	}
	if includesSuperAdmin {
		return nil
	}
	count, err := s.users.CountActiveSuperAdmins(ctx)
	if err != nil {
		return err
	}
	if count <= 1 {
		return store.ErrLastSuperAdmin
	}
	return nil
}

func (s *Server) validateRoleAssignments(ctx context.Context, roleIDs []string) error {
	roles := make([]store.Role, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		role, err := s.roles.GetRole(ctx, roleID)
		if errors.Is(err, store.ErrNotFound) {
			return errInvalidRoleAssignment
		}
		if err != nil {
			return err
		}
		roles = append(roles, role)
	}
	current := currentFromContext(ctx)
	if err := store.ValidateRoleAssignment(current.User, roles); err != nil {
		return err
	}
	if userHasRoleName(current.User, "super_admin") {
		return nil
	}
	for _, role := range roles {
		if err := store.ValidateRolePermissions(current.Permissions, role.Permissions); err != nil {
			return err
		}
	}
	return nil
}

func writeRoleAssignmentError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "validate_user_roles_failed"
	if errors.Is(err, store.ErrSuperAdminAssignmentForbidden) {
		status = http.StatusForbidden
		code = "cannot_assign_super_admin"
	} else if errors.Is(err, store.ErrPermissionEscalation) {
		status = http.StatusForbidden
		code = "permission_escalation"
	} else if errors.Is(err, store.ErrUnknownPermission) {
		status = http.StatusBadRequest
		code = "invalid_permissions"
	} else if errors.Is(err, errInvalidRoleAssignment) {
		status = http.StatusBadRequest
		code = "invalid_role_assignment"
	}
	writeJSON(w, status, map[string]string{"code": code})
}

func (s *Server) roleIDsIncludeRoleName(ctx context.Context, roleIDs []string, roleName string) (bool, error) {
	for _, roleID := range roleIDs {
		role, err := s.roles.GetRole(ctx, roleID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if role.Name == roleName {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) disableUser(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, "disabled", "users.disable")
}

func (s *Server) lockUser(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, "locked", "users.lock")
}

func (s *Server) unlockUser(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, "active", "users.unlock")
}

func (s *Server) forcePasswordChange(w http.ResponseWriter, r *http.Request) {
	s.setUserStatus(w, r, "pending_password_change", "users.force_password_change")
}

func (s *Server) setUserStatus(w http.ResponseWriter, r *http.Request, status, action string) {
	target, err := s.users.GetUser(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if err := store.ValidateUserStatusActor(current.User, target); errors.Is(err, store.ErrSuperAdminStatusForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "cannot_change_super_admin_status"})
		return
	}
	user, err := s.users.SetUserStatus(r.Context(), target.ID, status)
	if errors.Is(err, store.ErrLastSuperAdmin) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "last_super_admin"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "set_user_status_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: "user", ResourceID: user.ID, Result: "success", Metadata: map[string]any{"status": status}})
	writeJSON(w, http.StatusOK, publicUser(user))
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	target, err := s.users.GetUser(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if target.ID == current.User.ID {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "cannot_delete_self"})
		return
	}
	if err := store.ValidateUserStatusActor(current.User, target); errors.Is(err, store.ErrSuperAdminStatusForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "cannot_delete_super_admin"})
		return
	}
	if !userHasRoleName(current.User, "super_admin") {
		targetPermissions, err := s.auth.GetUserPermissions(r.Context(), target.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_permissions_failed"})
			return
		}
		if err := store.ValidateRolePermissions(current.Permissions, targetPermissions); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
			return
		}
	}
	if err := s.users.DeleteUser(r.Context(), target.ID); errors.Is(err, store.ErrLastSuperAdmin) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "last_super_admin"})
		return
	} else if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_user_failed"})
		return
	}
	_ = s.auth.DeleteUserSessions(r.Context(), target.ID)
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.delete", ResourceType: "user", ResourceID: target.ID, Result: "success", Metadata: map[string]any{"username": target.Username, "roles": target.Roles}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TemporaryPassword string `json:"temporary_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	target, err := s.users.GetUser(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if err := store.ValidatePasswordResetActor(current.User, target); err != nil {
		code := "permission_escalation"
		if errors.Is(err, store.ErrSuperAdminPasswordResetForbidden) {
			code = "cannot_reset_super_admin_password"
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"code": code})
		return
	}
	if !userHasRoleName(current.User, "super_admin") {
		targetPermissions, err := s.auth.GetUserPermissions(r.Context(), target.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_user_permissions_failed"})
			return
		}
		if err := store.ValidateRolePermissions(current.Permissions, targetPermissions); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
			return
		}
	}
	if !s.passwordMeetsConfiguredPolicy(w, r, body.TemporaryPassword) {
		return
	}
	if err := s.users.ResetPassword(r.Context(), target.ID, body.TemporaryPassword); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "reset_password_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "users.reset_password", ResourceType: "user", ResourceID: target.ID, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := s.roles.ListRoles(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_roles_failed"})
		return
	}
	writeJSON(w, http.StatusOK, roles)
}

func (s *Server) createRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	if err := store.ValidateRolePermissions(current.Permissions, body.Permissions); err != nil {
		status := http.StatusBadRequest
		code := "invalid_permissions"
		if errors.Is(err, store.ErrPermissionEscalation) {
			status = http.StatusForbidden
			code = "permission_escalation"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	role, err := s.roles.CreateRole(r.Context(), strings.TrimSpace(body.Name), body.Permissions)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_role_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "roles.create", ResourceType: "role", ResourceID: role.ID, Result: "success", Metadata: map[string]any{"name": role.Name}})
	writeJSON(w, http.StatusCreated, role)
}

func (s *Server) getRole(w http.ResponseWriter, r *http.Request) {
	role, err := s.roles.GetRole(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_role_failed"})
		return
	}
	writeJSON(w, http.StatusOK, role)
}

func (s *Server) updateRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	id := r.PathValue("id")
	existing, err := s.roles.GetRole(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_role_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if existing.Name == "super_admin" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "protected_super_admin_role"})
		return
	}
	if !userHasRoleName(current.User, "super_admin") && userHasRoleName(current.User, existing.Name) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "cannot_update_own_role"})
		return
	}
	if err := store.ValidateRolePermissions(current.Permissions, body.Permissions); err != nil {
		status := http.StatusBadRequest
		code := "invalid_permissions"
		if errors.Is(err, store.ErrPermissionEscalation) {
			status = http.StatusForbidden
			code = "permission_escalation"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	role, err := s.roles.UpdateRole(r.Context(), id, strings.TrimSpace(body.Name), body.Permissions)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_role_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "roles.update", ResourceType: "role", ResourceID: role.ID, Result: "success"})
	writeJSON(w, http.StatusOK, role)
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := s.roles.GetRole(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_role_failed"})
		return
	}
	current := currentFromContext(r.Context())
	if existing.Name == "super_admin" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "protected_super_admin_role"})
		return
	}
	if !userHasRoleName(current.User, "super_admin") && userHasRoleName(current.User, existing.Name) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "cannot_delete_own_role"})
		return
	}
	if err := store.ValidateRolePermissions(current.Permissions, existing.Permissions); err != nil {
		status := http.StatusBadRequest
		code := "invalid_permissions"
		if errors.Is(err, store.ErrPermissionEscalation) {
			status = http.StatusForbidden
			code = "permission_escalation"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	err = s.roles.DeleteRole(r.Context(), id)
	if errors.Is(err, store.ErrLastSuperAdmin) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "last_super_admin"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_role_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "roles.delete", ResourceType: "role", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listProfiles(kind store.ProfileKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.profiles.ListProfiles(r.Context(), kind)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_profiles_failed"})
			return
		}
		if kind == store.ProfileCaption {
			for i := range items {
				items[i].Config = normalizeProfileConfig(kind, items[i].Config)
			}
		}
		writeJSON(w, http.StatusOK, items)
	}
}

func (s *Server) createProfile(kind store.ProfileKind, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		body.Config = normalizeProfileConfig(kind, body.Config)
		if !s.validateProfileSecretReferences(w, kind, body.Config) {
			return
		}
		profile, err := s.profiles.CreateProfile(r.Context(), kind, body.Name, body.Config)
		if errors.Is(err, store.ErrProfileRawSecretConfig) {
			writeProfileSecretReferenceRequired(w, kind)
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_profile_failed"})
			return
		}
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: string(kind), ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"name": profile.Name}})
		writeJSON(w, http.StatusCreated, profile)
	}
}

func (s *Server) getProfile(kind store.ProfileKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile, err := s.profiles.GetProfile(r.Context(), kind, r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_profile_failed"})
			return
		}
		if kind == store.ProfileCaption {
			profile.Config = normalizeProfileConfig(kind, profile.Config)
		}
		writeJSON(w, http.StatusOK, profile)
	}
}

func (s *Server) updateProfile(kind store.ProfileKind, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
		body.Config = normalizeProfileConfig(kind, body.Config)
		if !s.validateProfileSecretReferences(w, kind, body.Config) {
			return
		}
		profile, err := s.profiles.UpdateProfile(r.Context(), kind, r.PathValue("id"), body.Name, body.Config)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if errors.Is(err, store.ErrProfileRawSecretConfig) {
			writeProfileSecretReferenceRequired(w, kind)
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_profile_failed"})
			return
		}
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: string(kind), ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"name": profile.Name}})
		writeJSON(w, http.StatusOK, profile)
	}
}

func (s *Server) deleteProfile(kind store.ProfileKind, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		profile, err := s.profiles.GetProfile(r.Context(), kind, id)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_profile_failed"})
			return
		}
		if s.writeProfileDeleteBlockedIfInUse(w, r, kind, id) {
			return
		}
		if err := s.profiles.DeleteProfile(r.Context(), kind, id); errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
			return
		} else if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_profile_failed"})
			return
		}
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: string(kind), ResourceID: id, Result: "success", Metadata: map[string]any{"name": profile.Name}})
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func (s *Server) writeProfileDeleteBlockedIfInUse(w http.ResponseWriter, r *http.Request, kind store.ProfileKind, id string) bool {
	stream, inUse, err := s.streamProfileReferenceInUse(r.Context(), kind, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "profile_reference_check_failed"})
		return true
	}
	if !inUse {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]string{
		"code":      "profile_in_use",
		"stream_id": stream.ID,
	})
	return true
}

func (s *Server) streamProfileReferenceInUse(ctx context.Context, kind store.ProfileKind, id string) (store.Stream, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" || s.streams == nil {
		return store.Stream{}, false, nil
	}
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return store.Stream{}, false, err
	}
	for _, stream := range streams {
		if streamReferencesProfile(stream, kind, id) {
			return stream, true, nil
		}
	}
	return store.Stream{}, false, nil
}

func streamReferencesProfile(stream store.Stream, kind store.ProfileKind, id string) bool {
	switch kind {
	case store.ProfileEncoder:
		return stream.EncoderProfileID == id
	case store.ProfileArchive:
		return stream.ArchiveProfileID == id
	case store.ProfileCaption:
		return stream.CaptionProfileID == id
	case store.ProfileOverlay:
		return stream.OverlayProfileID == id
	case store.ProfileDiscordConfig:
		return stream.DiscordConfigID == id
	case store.ProfileYouTubeOutput:
		return stream.YouTubeOutputID == id
	default:
		return false
	}
}

func normalizeProfileConfig(kind store.ProfileKind, config map[string]any) map[string]any {
	if kind == store.ProfileCaption {
		endpointingMS := configInt(config, "endpointing_ms")
		if endpointingMS < 10 || endpointingMS > 5000 {
			endpointingMS = 300
		}
		delayMS := configInt(config, "delay_ms")
		if _, ok := config["delay_ms"]; !ok {
			delayMS = 800
		} else if delayMS < 0 || delayMS > 10000 {
			delayMS = 800
		}
		return map[string]any{
			"provider":            "deepgram",
			"model":               "nova-3",
			"language":            normalizeDeepgramLanguage(configString(config, "language")),
			"api_key_secret_name": "deepgram_api_key",
			"endpointing_ms":      endpointingMS,
			"interim_results":     configBoolDefault(config, "interim_results", true),
			"smart_format":        configBoolDefault(config, "smart_format", true),
			"delay_ms":            delayMS,
		}
	}
	if kind != store.ProfileOverlay || config == nil {
		return config
	}
	out := make(map[string]any, len(config)+3)
	for key, value := range config {
		out[key] = value
	}
	delete(out, "watermark_position")
	delete(out, "watermark_opacity")
	delete(out, "watermark_width_percent")
	out["watermark_canvas_width"] = 1920
	out["watermark_canvas_height"] = 1080
	out["watermark_fit_mode"] = "scale_to_output"
	return out
}

func normalizeDeepgramLanguage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "en", "en-us", "en-gb":
		return "en"
	default:
		return "ja"
	}
}

func (s *Server) validateProfileSecretReferences(w http.ResponseWriter, kind store.ProfileKind, config map[string]any) bool {
	invalid := invalidProfileSecretReferences(kind, config)
	if len(invalid) == 0 {
		return true
	}
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"code":                      "profile_secret_reference_not_allowed",
		"message":                   "Profile config contains a secret reference that is not allowed for this profile kind.",
		"invalid_secret_references": invalid,
		"allowed_secret_references": allowedProfileSecretReferences(kind),
	})
	return false
}

func writeProfileSecretReferenceRequired(w http.ResponseWriter, kind store.ProfileKind) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"code":                      "profile_secret_reference_required",
		"message":                   "Profile config must use *_secret_name references; raw secret-like keys and values are not accepted.",
		"allowed_secret_references": allowedProfileSecretReferences(kind),
	})
}

func invalidProfileSecretReferences(kind store.ProfileKind, config map[string]any) []string {
	seen := map[string]bool{}
	for _, name := range profileSecretReferences(config) {
		if !runtimeProfileKindAllowsSecret(kind, name) {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func profileSecretReferences(value any) []string {
	var refs []string
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
			if profileSecretReferenceKey(normalizedKey) {
				if name, ok := nested.(string); ok && strings.TrimSpace(name) != "" {
					refs = append(refs, strings.TrimSpace(name))
				}
			}
			refs = append(refs, profileSecretReferences(nested)...)
		}
	case []any:
		for _, nested := range typed {
			refs = append(refs, profileSecretReferences(nested)...)
		}
	}
	return refs
}

func profileSecretReferenceKey(key string) bool {
	for _, suffix := range []string{"_secret_name", "_secret_ref", "_secret_id"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	canonical := canonicalProfileSecretKey(key)
	for _, suffix := range []string{"secretname", "secretref", "secretid"} {
		if strings.HasSuffix(canonical, suffix) {
			return true
		}
	}
	return false
}

func canonicalProfileSecretKey(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func allowedProfileSecretReferences(kind store.ProfileKind) []string {
	switch kind {
	case store.ProfileDiscordConfig:
		return []string{"discord_bot_token", "discord_bot_token_<config_id>"}
	case store.ProfileYouTubeOutput:
		return []string{"youtube_stream_key", "youtube_stream_key_<output_id>"}
	case store.ProfileArchive:
		return []string{"google_drive_folder_id", "google_drive_folder_id_<destination_id>", "google_oauth_refresh_token_<account_id>", "drive_destination:<id>:folder_id", "oauth_provider:<id>:client_secret", "oauth_account:<id>:refresh_token"}
	case store.ProfileEncoder:
		return []string{"encoder_runtime_secret_<name>"}
	case store.ProfileCaption:
		return []string{"deepgram_api_key", "deepgram_api_key_<profile_id>"}
	default:
		return []string{}
	}
}

type discordConfigRequest struct {
	Name                 string `json:"name"`
	ServiceID            string `json:"service_id"`
	GuildID              string `json:"guild_id"`
	VoiceChannelID       string `json:"voice_channel_id"`
	TextChannelID        string `json:"text_channel_id"`
	BotToken             string `json:"bot_token"`
	CaptionEnabled       *bool  `json:"caption_enabled"`
	STTProfileID         string `json:"stt_profile_id"`
	ReconnectEnabled     *bool  `json:"reconnect_enabled"`
	ReconnectMaxAttempts int    `json:"reconnect_max_attempts"`
	ReconnectBaseDelay   string `json:"reconnect_base_delay"`
	ReconnectMaxDelay    string `json:"reconnect_max_delay"`
	AudioForwardEnabled  *bool  `json:"audio_forward_enabled"`
	Config               any    `json:"config"`
}

type discordConfigResponse struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	ServiceID            string    `json:"service_id,omitempty"`
	GuildID              string    `json:"guild_id,omitempty"`
	VoiceChannelID       string    `json:"voice_channel_id,omitempty"`
	TextChannelID        string    `json:"text_channel_id,omitempty"`
	BotTokenConfigured   bool      `json:"bot_token_configured,omitempty"`
	BotTokenFingerprint  string    `json:"bot_token_fingerprint,omitempty"`
	CaptionEnabled       bool      `json:"caption_enabled,omitempty"`
	STTProfileID         string    `json:"stt_profile_id,omitempty"`
	ReconnectEnabled     bool      `json:"reconnect_enabled,omitempty"`
	ReconnectMaxAttempts int       `json:"reconnect_max_attempts,omitempty"`
	ReconnectBaseDelay   string    `json:"reconnect_base_delay,omitempty"`
	ReconnectMaxDelay    string    `json:"reconnect_max_delay,omitempty"`
	AudioForwardEnabled  bool      `json:"audio_forward_enabled,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func (s *Server) listDiscordConfigs(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.profiles.ListProfiles(r.Context(), store.ProfileDiscordConfig)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_discord_configs_failed"})
		return
	}
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	out := make([]discordConfigResponse, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, discordConfigFromProfile(profile, statuses))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createDiscordConfig(w http.ResponseWriter, r *http.Request) {
	var body discordConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	config, err := discordConfigFromRequest(body, "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_discord_config"})
		return
	}
	if err := s.validateDiscordConfigService(r.Context(), config); err != nil {
		writeJSON(w, discordConfigStatus(err), map[string]string{"code": discordConfigCode(err)})
		return
	}
	profile, err := s.profiles.CreateProfile(r.Context(), store.ProfileDiscordConfig, body.Name, config)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_discord_config_failed"})
		return
	}
	if strings.TrimSpace(body.BotToken) != "" {
		secretName := discordBotTokenSecretName(profile.ID)
		status, err := s.secrets.UpdateSecret(r.Context(), secretName, body.BotToken)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileDiscordConfig, profile.ID)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileDiscordConfig, profile.ID)
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "store_discord_bot_token_failed"})
			return
		}
		config["bot_token_secret_name"] = secretName
		config["bot_token_fingerprint"] = status.Fingerprint
		profile, err = s.profiles.UpdateProfile(r.Context(), store.ProfileDiscordConfig, profile.ID, profile.Name, config)
		if err != nil {
			_, _ = s.secrets.UpdateSecret(r.Context(), secretName, "")
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileDiscordConfig, profile.ID)
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_discord_config_failed"})
			return
		}
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "discord_configs.create", ResourceType: "discord_config", ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"service_id": configString(profile.Config, "service_id"), "bot_token_configured": strings.TrimSpace(body.BotToken) != ""}})
	writeJSON(w, http.StatusCreated, discordConfigFromProfile(profile, secretStatusListForConfig(profile, "bot_token_secret_name", "bot_token_fingerprint")))
}

func (s *Server) getDiscordConfig(w http.ResponseWriter, r *http.Request) {
	profile, err := s.profiles.GetProfile(r.Context(), store.ProfileDiscordConfig, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_discord_config_failed"})
		return
	}
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	writeJSON(w, http.StatusOK, discordConfigFromProfile(profile, statuses))
}

func (s *Server) updateDiscordConfig(w http.ResponseWriter, r *http.Request) {
	var body discordConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	id := r.PathValue("id")
	existing, err := s.profiles.GetProfile(r.Context(), store.ProfileDiscordConfig, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_discord_config_failed"})
		return
	}
	config, err := discordConfigFromRequest(body, id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_discord_config"})
		return
	}
	if err := s.validateDiscordConfigService(r.Context(), config); err != nil {
		writeJSON(w, discordConfigStatus(err), map[string]string{"code": discordConfigCode(err)})
		return
	}
	if existingSecret := strings.TrimSpace(configString(existing.Config, "bot_token_secret_name")); existingSecret != "" && strings.TrimSpace(body.BotToken) == "" {
		config["bot_token_secret_name"] = existingSecret
		config["bot_token_fingerprint"] = configString(existing.Config, "bot_token_fingerprint")
	}
	if strings.TrimSpace(body.BotToken) != "" {
		secretName := discordBotTokenSecretName(id)
		status, err := s.secrets.UpdateSecret(r.Context(), secretName, body.BotToken)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "store_discord_bot_token_failed"})
			return
		}
		config["bot_token_secret_name"] = secretName
		config["bot_token_fingerprint"] = status.Fingerprint
	}
	profile, err := s.profiles.UpdateProfile(r.Context(), store.ProfileDiscordConfig, id, body.Name, config)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_discord_config_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "discord_configs.update", ResourceType: "discord_config", ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"service_id": configString(profile.Config, "service_id"), "bot_token_updated": strings.TrimSpace(body.BotToken) != ""}})
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	writeJSON(w, http.StatusOK, discordConfigFromProfile(profile, statuses))
}

func (s *Server) deleteDiscordConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	profile, err := s.profiles.GetProfile(r.Context(), store.ProfileDiscordConfig, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_discord_config_failed"})
		return
	}
	if s.writeProfileDeleteBlockedIfInUse(w, r, store.ProfileDiscordConfig, id) {
		return
	}
	if err := s.profiles.DeleteProfile(r.Context(), store.ProfileDiscordConfig, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_discord_config_failed"})
		return
	}
	if secretName := strings.TrimSpace(configString(profile.Config, "bot_token_secret_name")); secretName != "" {
		_, _ = s.secrets.UpdateSecret(r.Context(), secretName, "")
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "discord_configs.delete", ResourceType: "discord_config", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func discordConfigFromRequest(body discordConfigRequest, id string) (map[string]any, error) {
	if body.Config != nil {
		return nil, errors.New("discord config must use structured fields")
	}
	if strings.TrimSpace(body.Name) == "" {
		return nil, errors.New("discord config name is required")
	}
	config := map[string]any{}
	if value := strings.TrimSpace(body.ServiceID); value != "" {
		config["service_id"] = value
	}
	if value := strings.TrimSpace(body.STTProfileID); value != "" {
		config["stt_profile_id"] = value
	}
	if body.CaptionEnabled != nil {
		config["caption_enabled"] = *body.CaptionEnabled
	}
	config["reconnect_enabled"] = true
	if body.ReconnectMaxAttempts < 0 {
		return nil, errors.New("reconnect max attempts must be positive")
	}
	if body.ReconnectMaxAttempts > 0 {
		config["reconnect_max_attempts"] = body.ReconnectMaxAttempts
	}
	if value := strings.TrimSpace(body.ReconnectBaseDelay); value != "" {
		if _, err := time.ParseDuration(value); err != nil {
			return nil, errors.New("reconnect base delay must be a duration")
		}
		config["reconnect_base_delay"] = value
	}
	if value := strings.TrimSpace(body.ReconnectMaxDelay); value != "" {
		if _, err := time.ParseDuration(value); err != nil {
			return nil, errors.New("reconnect max delay must be a duration")
		}
		config["reconnect_max_delay"] = value
	}
	config["audio_forward_enabled"] = true
	if id != "" {
		config["bot_token_secret_name"] = discordBotTokenSecretName(id)
	}
	return config, nil
}

func (s *Server) validateDiscordConfigService(ctx context.Context, config map[string]any) error {
	serviceID := strings.TrimSpace(configString(config, "service_id"))
	if serviceID == "" {
		return nil
	}
	service, err := s.services.GetService(ctx, serviceID)
	if errors.Is(err, store.ErrNotFound) {
		return errDiscordConfigServiceMismatch
	}
	if err != nil {
		return err
	}
	if service.ServiceType != "discord_bot" {
		return errDiscordConfigServiceMismatch
	}
	return nil
}

func discordBotTokenSecretName(id string) string {
	return "discord_bot_token_" + strings.ToLower(strings.TrimSpace(id))
}

func discordConfigFromProfile(profile store.Profile, statuses []store.SecretStatus) discordConfigResponse {
	secretName := strings.TrimSpace(configString(profile.Config, "bot_token_secret_name"))
	status := secretStatusByName(statuses, secretName)
	return discordConfigResponse{
		ID:                   profile.ID,
		Name:                 profile.Name,
		ServiceID:            configString(profile.Config, "service_id"),
		GuildID:              configString(profile.Config, "guild_id"),
		VoiceChannelID:       configString(profile.Config, "voice_channel_id"),
		TextChannelID:        configString(profile.Config, "text_channel_id"),
		BotTokenConfigured:   status.Configured,
		BotTokenFingerprint:  status.Fingerprint,
		CaptionEnabled:       configBool(profile.Config, "caption_enabled"),
		STTProfileID:         configString(profile.Config, "stt_profile_id"),
		ReconnectEnabled:     true,
		ReconnectMaxAttempts: configInt(profile.Config, "reconnect_max_attempts"),
		ReconnectBaseDelay:   configString(profile.Config, "reconnect_base_delay"),
		ReconnectMaxDelay:    configString(profile.Config, "reconnect_max_delay"),
		AudioForwardEnabled:  true,
		CreatedAt:            profile.CreatedAt,
		UpdatedAt:            profile.UpdatedAt,
	}
}

type youtubeOutputRequest struct {
	Name                   string `json:"name"`
	Mode                   string `json:"mode"`
	RTMPURL                string `json:"rtmp_url"`
	StreamKey              string `json:"stream_key"`
	WatchURL               string `json:"watch_url"`
	OAuthAccountID         string `json:"oauth_account_id"`
	BroadcastTitleTemplate string `json:"broadcast_title_template"`
	BroadcastDescription   string `json:"broadcast_description"`
	PrivacyStatus          string `json:"privacy_status"`
	LatencyPreference      string `json:"latency_preference"`
	EnableAutoStart        *bool  `json:"enable_auto_start"`
	EnableAutoStop         *bool  `json:"enable_auto_stop"`
	CompleteOnStop         *bool  `json:"complete_on_stop"`
	Config                 any    `json:"config"`
}

type youtubeOutputResponse struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	Mode                   string    `json:"mode"`
	RTMPURL                string    `json:"rtmp_url,omitempty"`
	StreamKeyConfigured    bool      `json:"stream_key_configured,omitempty"`
	StreamKeyFingerprint   string    `json:"stream_key_fingerprint,omitempty"`
	WatchURL               string    `json:"watch_url,omitempty"`
	OAuthAccountID         string    `json:"oauth_account_id,omitempty"`
	BroadcastTitleTemplate string    `json:"broadcast_title_template,omitempty"`
	BroadcastDescription   string    `json:"broadcast_description,omitempty"`
	PrivacyStatus          string    `json:"privacy_status,omitempty"`
	LatencyPreference      string    `json:"latency_preference,omitempty"`
	EnableAutoStart        bool      `json:"enable_auto_start,omitempty"`
	EnableAutoStop         bool      `json:"enable_auto_stop,omitempty"`
	CompleteOnStop         bool      `json:"complete_on_stop"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func (s *Server) listYouTubeOutputs(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.profiles.ListProfiles(r.Context(), store.ProfileYouTubeOutput)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_youtube_outputs_failed"})
		return
	}
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	out := make([]youtubeOutputResponse, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, youtubeOutputFromProfile(profile, statuses))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createYouTubeOutput(w http.ResponseWriter, r *http.Request) {
	var body youtubeOutputRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	config, err := youtubeOutputConfigFromRequest(body, "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_youtube_output"})
		return
	}
	if code, status := s.validateYouTubeOutputOAuthAccount(r.Context(), config); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	profile, err := s.profiles.CreateProfile(r.Context(), store.ProfileYouTubeOutput, body.Name, config)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_youtube_output_failed"})
		return
	}
	if strings.TrimSpace(body.StreamKey) != "" {
		secretName := youtubeOutputSecretName(profile.ID)
		status, err := s.secrets.UpdateSecret(r.Context(), secretName, body.StreamKey)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileYouTubeOutput, profile.ID)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileYouTubeOutput, profile.ID)
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "store_youtube_stream_key_failed"})
			return
		}
		config["stream_key_secret_name"] = secretName
		profile, err = s.profiles.UpdateProfile(r.Context(), store.ProfileYouTubeOutput, profile.ID, profile.Name, config)
		if err != nil {
			_, _ = s.secrets.UpdateSecret(r.Context(), secretName, "")
			_ = s.profiles.DeleteProfile(r.Context(), store.ProfileYouTubeOutput, profile.ID)
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_youtube_output_failed"})
			return
		}
		profile.Config["stream_key_fingerprint"] = status.Fingerprint
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube_outputs.create", ResourceType: "youtube_output", ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"mode": normalizedYouTubeOutputMode(body.Mode), "stream_key_configured": strings.TrimSpace(body.StreamKey) != ""}})
	writeJSON(w, http.StatusCreated, youtubeOutputFromProfile(profile, secretStatusListForConfig(profile, "stream_key_secret_name", "stream_key_fingerprint")))
}

func (s *Server) getYouTubeOutput(w http.ResponseWriter, r *http.Request) {
	profile, err := s.profiles.GetProfile(r.Context(), store.ProfileYouTubeOutput, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_youtube_output_failed"})
		return
	}
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	writeJSON(w, http.StatusOK, youtubeOutputFromProfile(profile, statuses))
}

func (s *Server) updateYouTubeOutput(w http.ResponseWriter, r *http.Request) {
	var body youtubeOutputRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	id := r.PathValue("id")
	existing, err := s.profiles.GetProfile(r.Context(), store.ProfileYouTubeOutput, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_youtube_output_failed"})
		return
	}
	config, err := youtubeOutputConfigFromRequest(body, id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_youtube_output"})
		return
	}
	if code, status := s.validateYouTubeOutputOAuthAccount(r.Context(), config); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if existingSecret := strings.TrimSpace(configString(existing.Config, "stream_key_secret_name")); existingSecret != "" && strings.TrimSpace(body.StreamKey) == "" {
		config["stream_key_secret_name"] = existingSecret
	}
	if strings.TrimSpace(body.StreamKey) != "" {
		secretName := youtubeOutputSecretName(id)
		status, err := s.secrets.UpdateSecret(r.Context(), secretName, body.StreamKey)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "store_youtube_stream_key_failed"})
			return
		}
		config["stream_key_secret_name"] = secretName
		config["stream_key_fingerprint"] = status.Fingerprint
	}
	profile, err := s.profiles.UpdateProfile(r.Context(), store.ProfileYouTubeOutput, id, body.Name, config)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_youtube_output_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube_outputs.update", ResourceType: "youtube_output", ResourceID: profile.ID, Result: "success", Metadata: map[string]any{"mode": normalizedYouTubeOutputMode(body.Mode), "stream_key_updated": strings.TrimSpace(body.StreamKey) != ""}})
	statuses, _ := s.secrets.ListSecretStatus(r.Context())
	writeJSON(w, http.StatusOK, youtubeOutputFromProfile(profile, statuses))
}

func (s *Server) deleteYouTubeOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	profile, err := s.profiles.GetProfile(r.Context(), store.ProfileYouTubeOutput, id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_youtube_output_failed"})
		return
	}
	if s.writeProfileDeleteBlockedIfInUse(w, r, store.ProfileYouTubeOutput, id) {
		return
	}
	if err := s.profiles.DeleteProfile(r.Context(), store.ProfileYouTubeOutput, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_youtube_output_failed"})
		return
	}
	if secretName := strings.TrimSpace(configString(profile.Config, "stream_key_secret_name")); secretName != "" {
		_, _ = s.secrets.UpdateSecret(r.Context(), secretName, "")
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube_outputs.delete", ResourceType: "youtube_output", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func youtubeOutputConfigFromRequest(body youtubeOutputRequest, id string) (map[string]any, error) {
	if body.Config != nil {
		return nil, errors.New("youtube output config must use structured fields")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return nil, errors.New("youtube output name is required")
	}
	mode := normalizedYouTubeOutputMode(body.Mode)
	if mode == "" {
		return nil, errors.New("invalid youtube output mode")
	}
	rtmpURL := strings.TrimSpace(body.RTMPURL)
	if mode == "stream_key" && rtmpURL == "" {
		rtmpURL = "rtmps://a.rtmps.youtube.com/live2"
	}
	if rtmpURL != "" {
		parsed, err := url.Parse(rtmpURL)
		if err != nil || parsed.User != nil || parsed.Scheme != "rtmps" {
			return nil, errors.New("invalid rtmp_url")
		}
	}
	config := map[string]any{
		"mode": mode,
	}
	if rtmpURL != "" {
		config["rtmp_url"] = rtmpURL
	}
	if mode == "stream_key" && id != "" {
		config["stream_key_secret_name"] = youtubeOutputSecretName(id)
	}
	if value := strings.TrimSpace(body.WatchURL); value != "" {
		normalized, ok := normalizeYouTubeWatchURL(value)
		if !ok {
			return nil, errors.New("invalid watch_url")
		}
		config["watch_url"] = normalized
	}
	if value := strings.TrimSpace(body.OAuthAccountID); value != "" {
		config["oauth_account_id"] = value
	}
	if value := strings.TrimSpace(body.BroadcastTitleTemplate); value != "" {
		config["broadcast_title"] = value
		config["broadcast_title_template"] = value
	}
	if value := strings.TrimSpace(body.BroadcastDescription); value != "" {
		config["broadcast_description"] = value
	}
	if value := strings.TrimSpace(body.PrivacyStatus); value != "" {
		if value != "private" && value != "unlisted" && value != "public" {
			return nil, errors.New("invalid privacy_status")
		}
		config["privacy_status"] = value
	}
	if value := strings.TrimSpace(body.LatencyPreference); value != "" {
		if value != "normal" && value != "low" && value != "ultra_low" {
			return nil, errors.New("invalid latency_preference")
		}
		config["latency_preference"] = value
	}
	if body.EnableAutoStart != nil {
		config["enable_auto_start"] = *body.EnableAutoStart
	}
	if body.EnableAutoStop != nil {
		config["enable_auto_stop"] = *body.EnableAutoStop
	}
	if body.CompleteOnStop != nil {
		config["complete_on_stop"] = *body.CompleteOnStop
	}
	if mode == "live_api" && strings.TrimSpace(body.OAuthAccountID) == "" {
		return nil, errors.New("live_api requires oauth_account_id")
	}
	return config, nil
}

func normalizedYouTubeOutputMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "stream_key", "existing_stream_key", "rtmps_stream_key":
		return "stream_key"
	case "live_api_dry_run", "dry_run_live_api", "youtube_live_api_dry_run":
		return "live_api_dry_run"
	case "live_api", "youtube_live_api":
		return "live_api"
	default:
		return ""
	}
}

func youtubeOutputSecretName(id string) string {
	return "youtube_stream_key_" + strings.ToLower(strings.TrimSpace(id))
}

func youtubeOutputFromProfile(profile store.Profile, statuses []store.SecretStatus) youtubeOutputResponse {
	mode := normalizedYouTubeOutputMode(configString(profile.Config, "mode"))
	if mode == "" {
		mode = "stream_key"
	}
	secretName := strings.TrimSpace(configString(profile.Config, "stream_key_secret_name"))
	status := secretStatusByName(statuses, secretName)
	return youtubeOutputResponse{
		ID:                     profile.ID,
		Name:                   profile.Name,
		Mode:                   mode,
		RTMPURL:                configString(profile.Config, "rtmp_url"),
		StreamKeyConfigured:    status.Configured,
		StreamKeyFingerprint:   status.Fingerprint,
		WatchURL:               configString(profile.Config, "watch_url"),
		OAuthAccountID:         configString(profile.Config, "oauth_account_id"),
		BroadcastTitleTemplate: firstNonEmpty(configString(profile.Config, "broadcast_title_template"), configString(profile.Config, "broadcast_title")),
		BroadcastDescription:   configString(profile.Config, "broadcast_description"),
		PrivacyStatus:          configString(profile.Config, "privacy_status"),
		LatencyPreference:      configString(profile.Config, "latency_preference"),
		EnableAutoStart:        configBool(profile.Config, "enable_auto_start"),
		EnableAutoStop:         configBool(profile.Config, "enable_auto_stop"),
		CompleteOnStop:         youtubeCompleteOnStop(profile.Config),
		CreatedAt:              profile.CreatedAt,
		UpdatedAt:              profile.UpdatedAt,
	}
}

func secretStatusByName(statuses []store.SecretStatus, name string) store.SecretStatus {
	if name == "" {
		return store.SecretStatus{}
	}
	for _, status := range statuses {
		if status.Name == name {
			return status
		}
	}
	return store.SecretStatus{Name: name}
}

func secretStatusListForConfig(profile store.Profile, secretNameKey, fingerprintKey string) []store.SecretStatus {
	if fingerprint := strings.TrimSpace(configString(profile.Config, fingerprintKey)); fingerprint != "" {
		return []store.SecretStatus{{Name: configString(profile.Config, secretNameKey), Configured: true, Fingerprint: fingerprint}}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type oauthProviderRequest struct {
	ProviderType   string   `json:"provider_type"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	ClientID       string   `json:"client_id"`
	ClientSecret   string   `json:"client_secret"`
	Scopes         []string `json:"scopes"`
	AllowedDomains []string `json:"allowed_domains"`
	AutoProvision  bool     `json:"auto_provision"`
	DefaultRoleIDs []string `json:"default_role_ids"`
	RedirectURI    string   `json:"redirect_uri"`
}

type oauthAccountRequest struct {
	ProviderID   string   `json:"provider_id"`
	ProviderType string   `json:"provider_type"`
	AccountLabel string   `json:"account_label"`
	Subject      string   `json:"subject"`
	Email        string   `json:"email"`
	Scopes       []string `json:"scopes"`
	RefreshToken string   `json:"refresh_token"`
}

type oauthAccountStartRequest struct {
	ProviderID     string `json:"provider_id"`
	AccountLabel   string `json:"account_label"`
	AccountPurpose string `json:"account_purpose"`
	RedirectAfter  string `json:"redirect_after"`
}

type oauthAccountCallbackRequest struct {
	ProviderID   string `json:"provider_id"`
	State        string `json:"state"`
	Code         string `json:"code"`
	AccountLabel string `json:"account_label"`
}

type driveDestinationRequest struct {
	Name           string `json:"name"`
	AuthMode       string `json:"auth_mode"`
	OAuthAccountID string `json:"oauth_account_id"`
	FolderID       string `json:"folder_id"`
	SharedDrive    bool   `json:"shared_drive"`
	BasePath       string `json:"base_path"`
}

func (s *Server) listOAuthProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.integrations.ListOAuthProviders(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_oauth_providers_failed"})
		return
	}
	writeJSON(w, http.StatusOK, providers)
}

func (s *Server) createOAuthProvider(w http.ResponseWriter, r *http.Request) {
	var body oauthProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.Scopes = oauthProviderRequestScopes(body.ProviderType, body.RedirectURI, body.Scopes)
	if code := validateOAuthProviderRequest(body); code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if !s.validateOAuthProviderRolePolicy(w, r, body.AutoProvision, body.DefaultRoleIDs) {
		return
	}
	provider, err := s.integrations.CreateOAuthProvider(r.Context(), store.OAuthProvider{
		ProviderType: body.ProviderType, Name: body.Name, Enabled: body.Enabled, ClientID: body.ClientID, ClientSecret: body.ClientSecret, Scopes: body.Scopes, AllowedDomains: body.AllowedDomains, AutoProvision: body.AutoProvision, DefaultRoleIDs: body.DefaultRoleIDs, RedirectURI: body.RedirectURI,
	})
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_oauth_provider_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_provider.create", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "success", Metadata: map[string]any{"provider_type": provider.ProviderType, "client_secret_configured": provider.ClientSecretConfigured, "auto_provision": provider.AutoProvision, "default_role_count": len(provider.DefaultRoleIDs)}})
	writeJSON(w, http.StatusCreated, provider)
}

func (s *Server) getOAuthProvider(w http.ResponseWriter, r *http.Request) {
	provider, err := s.integrations.GetOAuthProvider(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_provider_failed"})
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) updateOAuthProvider(w http.ResponseWriter, r *http.Request) {
	var body oauthProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.Scopes = oauthProviderRequestScopes(body.ProviderType, body.RedirectURI, body.Scopes)
	if code := validateOAuthProviderRequest(body); code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if !s.validateOAuthProviderRolePolicy(w, r, body.AutoProvision, body.DefaultRoleIDs) {
		return
	}
	provider, err := s.integrations.UpdateOAuthProvider(r.Context(), store.OAuthProvider{
		ID: r.PathValue("id"), ProviderType: body.ProviderType, Name: body.Name, Enabled: body.Enabled, ClientID: body.ClientID, ClientSecret: body.ClientSecret, Scopes: body.Scopes, AllowedDomains: body.AllowedDomains, AutoProvision: body.AutoProvision, DefaultRoleIDs: body.DefaultRoleIDs, RedirectURI: body.RedirectURI,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_oauth_provider_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_provider.update", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "success", Metadata: map[string]any{"provider_type": provider.ProviderType, "client_secret_configured": provider.ClientSecretConfigured, "auto_provision": provider.AutoProvision, "default_role_count": len(provider.DefaultRoleIDs)}})
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) validateOAuthProviderRolePolicy(w http.ResponseWriter, r *http.Request, autoProvision bool, roleIDs []string) bool {
	if autoProvision && len(roleIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "oauth_default_role_required"})
		return false
	}
	if len(roleIDs) == 0 {
		return true
	}
	if !security.HasPermission(currentFromContext(r.Context()).Permissions, "roles.assign") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "missing_permission"})
		return false
	}
	if err := s.validateRoleAssignments(r.Context(), roleIDs); err != nil {
		writeRoleAssignmentError(w, err)
		return false
	}
	return true
}

func (s *Server) deleteOAuthProvider(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if refs, err := s.oauthProviderDeleteReferences(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_provider_failed"})
		return
	} else if oauthReferenceCount(refs) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_provider.delete", ResourceType: "oauth_provider", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "oauth_provider_in_use", "references": refs}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "oauth_provider_in_use", "references": refs})
		return
	}
	if err := s.integrations.DeleteOAuthProvider(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_provider_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_provider.delete", ResourceType: "oauth_provider", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) oauthProviderDeleteReferences(ctx context.Context, id string) (map[string]any, error) {
	refs := map[string]any{"oauth_accounts": 0, "oauth_user_links": 0}
	accounts, err := s.integrations.ListOAuthAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		if strings.TrimSpace(account.ProviderID) == id {
			refs["oauth_accounts"] = refs["oauth_accounts"].(int) + 1
		}
	}
	if s.users != nil && s.oauthLogin != nil {
		users, err := s.users.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			links, err := s.oauthLogin.ListOAuthUserLinks(ctx, user.ID)
			if err != nil {
				return nil, err
			}
			for _, link := range links {
				if strings.TrimSpace(link.ProviderID) == id {
					refs["oauth_user_links"] = refs["oauth_user_links"].(int) + 1
				}
			}
		}
	}
	return refs, nil
}

func (s *Server) startOAuthAccountConnection(w http.ResponseWriter, r *http.Request) {
	var body oauthAccountStartRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	provider, err := s.integrations.GetOAuthProvider(r.Context(), body.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "oauth_provider_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_provider_failed"})
		return
	}
	if !provider.Enabled || !supportedConnectedAccountProvider(provider.ProviderType) || strings.TrimSpace(provider.ClientID) == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_connected_account"})
		return
	}
	redirectURI, redirectCode := connectedAccountOAuthRedirectURI(r, provider)
	if redirectCode != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": redirectCode})
		return
	}
	requestedScopes, code := oauthAccountRequestedScopes(body.AccountPurpose)
	if code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	state, err := s.oauthLogin.CreateOAuthLoginState(r.Context(), store.OAuthLoginState{
		ProviderID:      provider.ID,
		ProviderType:    provider.ProviderType,
		Purpose:         "connected_account",
		RedirectAfter:   safeRedirectAfter(body.RedirectAfter),
		AccountLabel:    strings.TrimSpace(body.AccountLabel),
		RequestedScopes: requestedScopes,
	}, 10*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_state_create_failed"})
		return
	}
	provider.RedirectURI = redirectURI
	authorizationURL, err := oauthConnectedAccountAuthorizationURL(provider, state)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_connected_account"})
		return
	}
	setOAuthStateCookie(w, state)
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"provider":          publicOAuthLoginProvider(provider),
		"authorization_url": authorizationURL,
		"state":             state.StateToken,
		"nonce":             state.Nonce,
		"expires_at":        state.ExpiresAt,
		"account_label":     strings.TrimSpace(body.AccountLabel),
		"account_purpose":   store.OAuthAccountPurposeFromScopes(state.RequestedScopes),
		"scopes":            state.RequestedScopes,
	})
}

func (s *Server) oauthAccountCallback(w http.ResponseWriter, r *http.Request) {
	setOAuthCallbackNoStoreHeaders(w)
	var body oauthAccountCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	s.finishOAuthAccountConnection(w, r, body, false)
}

func (s *Server) oauthAccountRedirectCallback(w http.ResponseWriter, r *http.Request) {
	setOAuthCallbackNoStoreHeaders(w)
	body := oauthAccountCallbackRequest{
		ProviderID:   r.URL.Query().Get("provider_id"),
		State:        r.URL.Query().Get("state"),
		Code:         r.URL.Query().Get("code"),
		AccountLabel: r.URL.Query().Get("account_label"),
	}
	s.finishOAuthAccountConnection(w, r, body, true)
}

func (s *Server) finishOAuthAccountConnection(w http.ResponseWriter, r *http.Request, body oauthAccountCallbackRequest, redirectOnSuccess bool) {
	if !oauthStateTokenCookieMatches(r, body.State) {
		clearOAuthStateCookie(w)
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_cookie_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	state, err := s.oauthLogin.ConsumeOAuthLoginState(r.Context(), body.State)
	if errors.Is(err, store.ErrNotFound) {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "invalid_or_expired_state"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "oauth_state_consume_failed"})
		return
	}
	if !oauthStateCookieMatches(r, state) {
		clearOAuthStateCookie(w)
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_cookie_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	clearOAuthStateCookie(w)
	if strings.TrimSpace(body.ProviderID) != "" && strings.TrimSpace(body.ProviderID) != state.ProviderID {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_provider", ResourceID: body.ProviderID, Result: "failure", Metadata: map[string]any{"reason": "provider_state_mismatch"}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	if state.Purpose != "connected_account" {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_state", Result: "failure", Metadata: map[string]any{"reason": "state_purpose_mismatch", "purpose": state.Purpose}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_oauth_state"})
		return
	}
	provider, err := s.integrations.GetOAuthProviderForDispatch(r.Context(), state.ProviderID)
	if errors.Is(err, store.ErrNotFound) || !provider.Enabled || strings.TrimSpace(provider.ClientSecret) == "" {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_provider", ResourceID: state.ProviderID, Result: "failure", Metadata: map[string]any{"reason": "provider_unavailable"}})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_unavailable"})
		return
	}
	if err != nil {
		code := "oauth_provider_unavailable"
		if errors.Is(err, store.ErrSecretKeyRequired) {
			code = "secret_encryption_key_required"
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": code})
		return
	}
	if !supportedConnectedAccountProvider(provider.ProviderType) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_provider_not_usable_for_connected_account"})
		return
	}
	redirectURI, redirectCode := connectedAccountOAuthRedirectURI(r, provider)
	if redirectCode != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"code": redirectCode})
		return
	}
	provider.RedirectURI = redirectURI
	connected, err := s.oauthConnector.Connect(r.Context(), oauthlogin.ConnectRequest{Provider: provider, Code: body.Code, Nonce: state.Nonce})
	if err != nil {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": "oauth_connect_failed", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "oauth_connect_failed"})
		return
	}
	if connected.Identity.ProviderID != provider.ID || connected.Identity.Subject == "" || !identityAllowedForProvider(provider, connected.Identity) {
		s.writeAudit(r, store.AuditEvent{Action: "integrations.oauth_account.connect", ResourceType: "oauth_provider", ResourceID: provider.ID, Result: "failure", Metadata: map[string]any{"reason": "identity_not_allowed", "provider_type": provider.ProviderType}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "oauth_identity_not_allowed"})
		return
	}
	scopes := cleanRequestStringSlice(connected.Scopes)
	if len(scopes) == 0 {
		scopes = state.RequestedScopes
	}
	if !oauthScopesContainConnectedAccountAccess(scopes) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "oauth_connected_account_scope_required"})
		return
	}
	label := strings.TrimSpace(body.AccountLabel)
	if label == "" {
		label = strings.TrimSpace(state.AccountLabel)
	}
	if label == "" || strings.EqualFold(label, strings.TrimSpace(connected.Identity.Email)) {
		label = defaultOAuthAccountLabel(provider)
	}
	account, err := s.integrations.CreateOAuthAccount(r.Context(), store.OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: provider.ProviderType,
		AccountLabel: label,
		Subject:      connected.Identity.Subject,
		Email:        connected.Identity.Email,
		Scopes:       scopes,
		RefreshToken: connected.RefreshToken,
	})
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_oauth_account_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_account.connect", ResourceType: "oauth_account", ResourceID: account.ID, Result: "success", Metadata: map[string]any{"provider_type": account.ProviderType, "account_purpose": account.AccountPurpose, "refresh_token_configured": account.RefreshTokenConfigured}})
	if redirectOnSuccess {
		target := safeRedirectAfter(state.RedirectAfter)
		if target == "" {
			target = "/"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func defaultOAuthAccountLabel(provider store.OAuthProvider) string {
	if providerName := strings.TrimSpace(provider.Name); providerName != "" {
		return providerName
	}
	switch strings.TrimSpace(strings.ToLower(provider.ProviderType)) {
	case "google":
		return "Google接続アカウント"
	case "github":
		return "GitHub接続アカウント"
	case "discord":
		return "Discord接続アカウント"
	default:
		return "OAuth接続アカウント"
	}
}

func (s *Server) listOAuthAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.integrations.ListOAuthAccounts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_oauth_accounts_failed"})
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *Server) createOAuthAccount(w http.ResponseWriter, r *http.Request) {
	var body oauthAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_account.create", ResourceType: "oauth_account", Result: "failure", Metadata: map[string]any{"reason": "manual_oauth_account_create_disabled", "provider_type": strings.TrimSpace(body.ProviderType)}})
	writeJSON(w, http.StatusForbidden, map[string]string{"code": "manual_oauth_account_create_disabled"})
}

func (s *Server) getOAuthAccount(w http.ResponseWriter, r *http.Request) {
	account, err := s.integrations.GetOAuthAccount(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_account_failed"})
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) updateOAuthAccount(w http.ResponseWriter, r *http.Request) {
	var body oauthAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	if strings.TrimSpace(body.RefreshToken) != "" {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_account.update", ResourceType: "oauth_account", ResourceID: r.PathValue("id"), Result: "failure", Metadata: map[string]any{"reason": "manual_oauth_account_refresh_token_disabled"}})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "manual_oauth_account_refresh_token_disabled"})
		return
	}
	existing, err := s.integrations.GetOAuthAccount(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_oauth_account_failed"})
		return
	}
	if oauthAccountIdentityChanged(body, existing) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_account.update", ResourceType: "oauth_account", ResourceID: existing.ID, Result: "failure", Metadata: map[string]any{"reason": "oauth_account_identity_immutable"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "oauth_account_identity_immutable"})
		return
	}
	label := strings.TrimSpace(body.AccountLabel)
	if label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "oauth_account_label_required"})
		return
	}
	account, err := s.integrations.UpdateOAuthAccount(r.Context(), store.OAuthAccount{
		ID: existing.ID, ProviderID: existing.ProviderID, ProviderType: existing.ProviderType, AccountLabel: label, Subject: existing.Subject, Email: existing.Email, Scopes: existing.Scopes,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_oauth_account_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_account.update", ResourceType: "oauth_account", ResourceID: account.ID, Result: "success", Metadata: map[string]any{"provider_type": account.ProviderType, "identity_locked": true, "refresh_token_configured": account.RefreshTokenConfigured}})
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) deleteOAuthAccount(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if refs, err := s.oauthAccountDeleteReferences(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_account_failed"})
		return
	} else if oauthReferenceCount(refs) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_account.delete", ResourceType: "oauth_account", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "oauth_account_in_use", "references": refs}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "oauth_account_in_use", "references": refs})
		return
	}
	if err := s.integrations.DeleteOAuthAccount(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_oauth_account_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.oauth_account.delete", ResourceType: "oauth_account", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) oauthAccountDeleteReferences(ctx context.Context, id string) (map[string]any, error) {
	refs := map[string]any{"drive_destinations": 0, "youtube_outputs": 0, "stream_archive_settings": 0, "stream_youtube_runtimes": 0}
	destinations, err := s.integrations.ListDriveDestinations(ctx)
	if err != nil {
		return nil, err
	}
	for _, destination := range destinations {
		if strings.TrimSpace(destination.OAuthAccountID) == id {
			refs["drive_destinations"] = refs["drive_destinations"].(int) + 1
		}
	}
	outputs, err := s.profiles.ListProfiles(ctx, store.ProfileYouTubeOutput)
	if err != nil {
		return nil, err
	}
	for _, output := range outputs {
		if firstNonEmpty(configString(output.Config, "oauth_account_id"), configString(output.Config, "youtube_oauth_account_id")) == id {
			refs["youtube_outputs"] = refs["youtube_outputs"].(int) + 1
		}
	}
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return nil, err
	}
	for _, stream := range streams {
		if strings.TrimSpace(stream.ArchiveOAuthAccountID) == id {
			refs["stream_archive_settings"] = refs["stream_archive_settings"].(int) + 1
		}
	}
	if runtimes, ok := s.streams.(store.StreamYouTubeRuntimeStore); ok {
		items, err := runtimes.ListStreamYouTubeRuntimes(ctx)
		if err != nil {
			return nil, err
		}
		for _, runtime := range items {
			if strings.TrimSpace(runtime.OAuthAccountID) == id {
				refs["stream_youtube_runtimes"] = refs["stream_youtube_runtimes"].(int) + 1
			}
		}
	}
	return refs, nil
}

func oauthReferenceCount(refs map[string]any) int {
	total := 0
	for _, value := range refs {
		if count, ok := value.(int); ok {
			total += count
		}
	}
	return total
}

func (s *Server) listDriveDestinations(w http.ResponseWriter, r *http.Request) {
	destinations, err := s.integrations.ListDriveDestinations(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_drive_destinations_failed"})
		return
	}
	writeJSON(w, http.StatusOK, destinations)
}

func (s *Server) createDriveDestination(w http.ResponseWriter, r *http.Request) {
	var body driveDestinationRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if code, status := normalizeDriveDestinationAPIRequest(&body); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if code, status := s.validateDriveDestinationRequest(r.Context(), body); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	destination, err := s.integrations.CreateDriveDestination(r.Context(), store.DriveDestination{
		Name: body.Name, AuthMode: body.AuthMode, OAuthAccountID: body.OAuthAccountID, FolderID: body.FolderID, SharedDrive: body.SharedDrive, BasePath: body.BasePath,
	})
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_drive_destination_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.drive_destination.create", ResourceType: "drive_destination", ResourceID: destination.ID, Result: "success", Metadata: map[string]any{"auth_mode": destination.AuthMode, "shared_drive": destination.SharedDrive, "folder_id_configured": destination.FolderIDConfigured}})
	writeJSON(w, http.StatusCreated, destination)
}

func (s *Server) getDriveDestination(w http.ResponseWriter, r *http.Request) {
	destination, err := s.integrations.GetDriveDestination(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_drive_destination_failed"})
		return
	}
	writeJSON(w, http.StatusOK, destination)
}

func (s *Server) updateDriveDestination(w http.ResponseWriter, r *http.Request) {
	var body driveDestinationRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if code, status := normalizeDriveDestinationAPIRequest(&body); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if code, status := s.validateDriveDestinationRequest(r.Context(), body); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	destination, err := s.integrations.UpdateDriveDestination(r.Context(), store.DriveDestination{
		ID: r.PathValue("id"), Name: body.Name, AuthMode: body.AuthMode, OAuthAccountID: body.OAuthAccountID, FolderID: body.FolderID, SharedDrive: body.SharedDrive, BasePath: body.BasePath,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if errors.Is(err, store.ErrSecretKeyRequired) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "update_drive_destination_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.drive_destination.update", ResourceType: "drive_destination", ResourceID: destination.ID, Result: "success", Metadata: map[string]any{"auth_mode": destination.AuthMode, "shared_drive": destination.SharedDrive, "folder_id_configured": destination.FolderIDConfigured}})
	writeJSON(w, http.StatusOK, destination)
}

func (s *Server) deleteDriveDestination(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if refs, err := s.driveDestinationDeleteReferences(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_drive_destination_failed"})
		return
	} else if oauthReferenceCount(refs) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.drive_destination.delete", ResourceType: "drive_destination", ResourceID: id, Result: "failure", Metadata: map[string]any{"reason": "drive_destination_in_use", "references": refs}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "drive_destination_in_use", "references": refs})
		return
	}
	if err := s.integrations.DeleteDriveDestination(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_drive_destination_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "integrations.drive_destination.delete", ResourceType: "drive_destination", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) driveDestinationDeleteReferences(ctx context.Context, id string) (map[string]any, error) {
	refs := map[string]any{"stream_archive_settings": 0, "archive_profiles": 0}
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return nil, err
	}
	for _, stream := range streams {
		if strings.TrimSpace(stream.ArchiveDriveDestinationID) == id {
			refs["stream_archive_settings"] = refs["stream_archive_settings"].(int) + 1
		}
	}
	profiles, err := s.profiles.ListProfiles(ctx, store.ProfileArchive)
	if err != nil {
		return nil, err
	}
	for _, profile := range profiles {
		if strings.TrimSpace(configString(profile.Config, "drive_destination_id")) == id {
			refs["archive_profiles"] = refs["archive_profiles"].(int) + 1
		}
	}
	return refs, nil
}

func (s *Server) listServiceTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.services.ListServiceTokens(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_api_tokens_failed"})
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) createServiceToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServiceType  string         `json:"service_type"`
		Scopes       []string       `json:"scopes"`
		ServiceID    string         `json:"service_id,omitempty"`
		ServiceName  string         `json:"service_name,omitempty"`
		PublicURL    string         `json:"public_url,omitempty"`
		Version      string         `json:"version,omitempty"`
		Capabilities map[string]any `json:"capabilities,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.ServiceID = strings.TrimSpace(body.ServiceID)
	if stringSliceContains(body.Scopes, "service.register") && body.ServiceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "service_id_required"})
		return
	}
	if body.ServiceID != "" && !stringSliceContains(body.Scopes, "service.register") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "service_register_scope_required"})
		return
	}
	if err := validateServiceTokenScopePermissions(currentFromContext(r.Context()).Permissions, body.Scopes); err != nil {
		status := http.StatusBadRequest
		code := "invalid_service_scope"
		if errors.Is(err, store.ErrPermissionEscalation) {
			status = http.StatusForbidden
			code = "permission_escalation"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	token, err := s.services.CreateServiceToken(r.Context(), body.ServiceType, body.Scopes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_api_token_failed"})
		return
	}
	var precreatedService *store.RegisteredService
	if body.ServiceID != "" {
		service, err := s.services.PrecreateService(r.Context(), token, store.ServiceRegistration{
			ServiceID: body.ServiceID, ServiceType: body.ServiceType, ServiceName: body.ServiceName,
			PublicURL: body.PublicURL, Version: body.Version, Capabilities: body.Capabilities,
		})
		if err != nil {
			_ = s.services.RevokeServiceToken(r.Context(), token.ID)
			if errors.Is(err, store.ErrAlreadyExists) {
				writeJSON(w, http.StatusConflict, map[string]string{"code": "service_already_exists"})
				return
			}
			if errors.Is(err, store.ErrInvalidServiceRegistration) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_service_registration"})
				return
			}
			if errors.Is(err, store.ErrForbidden) {
				writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_mismatch"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "precreate_service_failed"})
			return
		}
		precreatedService = &service
	}
	current := currentFromContext(r.Context())
	metadata := map[string]any{"service_type": token.ServiceType, "scopes": token.Scopes}
	if precreatedService != nil {
		metadata["service_id"] = precreatedService.ServiceID
		metadata["precreated_service"] = true
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "api_tokens.create", ResourceType: "service_token", ResourceID: token.ID, Result: "success", Metadata: metadata})
	writeOneTimeSecretJSON(w, http.StatusCreated, token)
}

func (s *Server) createNodeRegistrationToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeType            string         `json:"node_type"`
		ServiceType         string         `json:"service_type"`
		NodeID              string         `json:"node_id"`
		ServiceID           string         `json:"service_id"`
		Name                string         `json:"name"`
		ServiceName         string         `json:"service_name"`
		Description         string         `json:"description"`
		Host                string         `json:"host"`
		Port                int            `json:"port"`
		SSLEnabled          bool           `json:"ssl_enabled"`
		PublicURL           string         `json:"public_url"`
		Version             string         `json:"version"`
		Capabilities        map[string]any `json:"capabilities,omitempty"`
		AllowRuntimeSecrets bool           `json:"allow_runtime_secrets"`
		AllowRemediation    bool           `json:"allow_remediation"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	serviceType := strings.TrimSpace(body.NodeType)
	if serviceType == "" {
		serviceType = strings.TrimSpace(body.ServiceType)
	}
	serviceID := strings.TrimSpace(body.NodeID)
	if serviceID == "" {
		serviceID = strings.TrimSpace(body.ServiceID)
	}
	serviceName := strings.TrimSpace(body.Name)
	if serviceName == "" {
		serviceName = strings.TrimSpace(body.ServiceName)
	}
	host := strings.TrimSpace(body.Host)
	port := body.Port
	sslEnabled := body.SSLEnabled
	if host == "" || port == 0 {
		parsedHost, parsedPort, parsedSSL := nodeEndpointFromURL(strings.TrimSpace(body.PublicURL))
		if host == "" {
			host = parsedHost
		}
		if port == 0 {
			port = parsedPort
		}
		if parsedHost != "" {
			sslEnabled = parsedSSL
		}
	}
	publicURL := buildNodeAgentURL(host, port, sslEnabled)
	if publicURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_endpoint"})
		return
	}
	if err := netpolicy.ServiceURLPolicyFromEnv().ValidateURL(publicURL); err != nil {
		code := "invalid_node_endpoint"
		if errors.Is(err, netpolicy.ErrBlockedServiceURL) {
			code = "node_endpoint_blocked"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if !requireNodeStreamIngestSigningKey(w, serviceType) {
		return
	}
	scopes := nodeRegistrationScopes(serviceType, body.AllowRuntimeSecrets, body.AllowRemediation)
	if err := validateServiceTokenScopePermissions(currentFromContext(r.Context()).Permissions, scopes); err != nil {
		status := http.StatusBadRequest
		code := "invalid_node_scope"
		if errors.Is(err, store.ErrPermissionEscalation) {
			status = http.StatusForbidden
			code = "permission_escalation"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	if err := validateNodeConfigurationSecretPermissions(currentFromContext(r.Context()).Permissions, serviceType); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
		return
	}
	if _, err := nodeRuntimeTokenEncryptionKey(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	token, err := s.services.CreateServiceToken(r.Context(), serviceType, scopes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "create_node_registration_token_failed"})
		return
	}
	service, err := s.services.PrecreateService(r.Context(), token, store.ServiceRegistration{
		ServiceID:    serviceID,
		ServiceType:  serviceType,
		ServiceName:  serviceName,
		Description:  strings.TrimSpace(body.Description),
		Host:         host,
		Port:         port,
		SSLEnabled:   sslEnabled,
		PublicURL:    publicURL,
		Version:      "",
		Capabilities: map[string]any{},
	})
	if err != nil {
		_ = s.services.RevokeServiceToken(r.Context(), token.ID)
		if errors.Is(err, store.ErrAlreadyExists) {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "node_already_exists"})
			return
		}
		if errors.Is(err, store.ErrInvalidServiceRegistration) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_registration"})
			return
		}
		if errors.Is(err, store.ErrForbidden) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "node_type_mismatch"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "precreate_node_failed"})
		return
	}
	service, err = s.persistNodeRuntimeToken(r.Context(), service.ServiceID, token.RawToken)
	if err != nil {
		_ = s.services.RevokeServiceToken(r.Context(), token.ID)
		_ = s.services.DeleteService(r.Context(), service.ServiceID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	configureToken, configureExpiresAt, err := s.issueNodeConfigureToken(r.Context(), service.ServiceID)
	if err != nil {
		_ = s.services.RevokeServiceToken(r.Context(), token.ID)
		_ = s.services.DeleteService(r.Context(), service.ServiceID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_node_configure_token_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "nodes.registration_token.create",
		ResourceType:  "node",
		ResourceID:    service.ServiceID,
		Result:        "success",
		Metadata:      map[string]any{"node_type": service.ServiceType, "token_id": token.ID, "scopes": token.Scopes},
	})
	writeOneTimeSecretJSON(w, http.StatusCreated, map[string]any{
		"id":                         token.ID,
		"service_type":               token.ServiceType,
		"node_type":                  token.ServiceType,
		"scopes":                     token.Scopes,
		"token":                      configureToken,
		"configure_token":            configureToken,
		"configure_token_expires_at": configureExpiresAt,
		"runtime_token_id":           token.ID,
		"runtime_token":              token.RawToken,
		"created_at":                 token.CreatedAt,
		"node":                       service,
		"configure_command":          nodeConfigureCommand(r, service.ServiceType, service.ServiceID, configureToken, ""),
		"configuration_yaml":         nodeConfigurationYAML(r, service, token.ID, token.RawToken),
	})
}

func nodeRegistrationScopes(serviceType string, allowRuntimeSecrets, allowRemediation bool) []string {
	scopes := []string{"service.register", "service.heartbeat", "service.config.read", "service.logs.write", "service.status.write"}
	switch serviceType {
	case "discord_bot":
		scopes = append(scopes, "discord.status.write", "streams.start")
	case "encoder_recorder":
		scopes = append(scopes, "encoder.status.write", "observability.ingest")
	case "worker":
		scopes = append(scopes, "worker.events.write", "observability.ingest")
	case "observability":
		scopes = append(scopes, "observability.ingest", "notifications.email.send")
		if allowRemediation {
			scopes = append(scopes, "remediation.execute")
		}
	}
	if serviceType == "encoder_recorder" || allowRuntimeSecrets {
		scopes = append(scopes, "service.secret.resolve")
	}
	return scopes
}

func (s *Server) issueNodeConfigureToken(ctx context.Context, nodeID string) (string, time.Time, error) {
	raw, err := security.RandomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	token := "ast_cfg_" + raw
	expiresAt := time.Now().UTC().Add(nodeConfigureTokenTTL())
	if _, err := s.services.SetServiceConfigureToken(ctx, nodeID, security.HashToken(token), expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (s *Server) persistNodeRuntimeToken(ctx context.Context, nodeID, rawToken string) (store.RegisteredService, error) {
	seal, err := nodeRuntimeTokenSealer()
	if err != nil || strings.TrimSpace(rawToken) == "" {
		return store.RegisteredService{}, errors.New("node runtime token encryption key is not configured")
	}
	ciphertext, nonce, err := seal(rawToken)
	if err != nil {
		return store.RegisteredService{}, err
	}
	return s.services.SetServiceNodeTokenSecret(ctx, nodeID, ciphertext, nonce)
}

func nodeRuntimeTokenEncryptionKey() (string, error) {
	key := strings.TrimSpace(os.Getenv("AUTOSTREAM_SECRET_ENCRYPTION_KEY"))
	upper := strings.ToUpper(key)
	placeholder := strings.Contains(upper, "CHANGE_ME") ||
		strings.Contains(upper, "REPLACE_ME") ||
		strings.Contains(upper, "YOUR_ENCRYPTION_KEY") ||
		(strings.HasPrefix(key, "<") && strings.HasSuffix(key, ">"))
	if len([]byte(key)) < minSecretEncryptionKeyLen || placeholder {
		return "", errors.New("node runtime token encryption key must be a non-placeholder value of at least 32 bytes")
	}
	return key, nil
}

func nodeRuntimeTokenSealer() (store.NodeTokenSealer, error) {
	key, err := nodeRuntimeTokenEncryptionKey()
	if err != nil {
		return nil, err
	}
	return func(rawToken string) (string, string, error) {
		if strings.TrimSpace(rawToken) == "" {
			return "", "", errors.New("node runtime token is empty")
		}
		return security.EncryptSecret(rawToken, key)
	}, nil
}

func nodeConfigureTokenTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("AUTOSTREAM_NODE_CONFIGURE_TOKEN_TTL"))
	if raw == "" {
		return defaultNodeConfigureTokenTTL
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl <= 0 {
		return defaultNodeConfigureTokenTTL
	}
	return ttl
}

func nodeConfigureCommand(r *http.Request, serviceType, nodeID, rawToken, configPath string) string {
	panelURL := panelBaseURL(r)
	if panelURL == "" {
		panelURL = "https://control.example.com"
	}
	if configPath == "" {
		configPath = nodeDefaultConfigPath(serviceType)
	}
	configureBinary := nodeConfigureBinary(serviceType)
	return `sudo ` + configureBinary + ` configure --panel-url ` + strconv.Quote(panelURL) +
		" --token " + strconv.Quote(rawToken) +
		" --node " + strconv.Quote(nodeID) +
		" --config " + strconv.Quote(configPath)
}

func nodeConfigureBinary(serviceType string) string {
	switch serviceType {
	case "encoder_recorder":
		return "autostream-encoder-recorder"
	case "discord_bot":
		return "autostream-discord-bot"
	case "observability":
		return "autostream-observability"
	default:
		return "autostream-worker"
	}
}

func nodeDefaultConfigPath(serviceType string) string {
	return "/etc/autostream-" + nodeServiceDirectoryName(serviceType) + "/config.yml"
}

func nodeAgentDataDir(serviceType string) string {
	return "/var/lib/autostream/" + nodeServiceDirectoryName(serviceType)
}

func nodeAgentLogDir(serviceType string) string {
	return "/var/log/autostream/" + nodeServiceDirectoryName(serviceType)
}

func nodeServiceDirectoryName(serviceType string) string {
	switch serviceType {
	case "encoder_recorder":
		return "encoder-recorder"
	case "discord_bot":
		return "discord-bot"
	case "observability":
		return "observability"
	case "worker":
		return "worker"
	default:
		value := strings.Trim(strings.ToLower(strings.ReplaceAll(serviceType, "_", "-")), "-")
		if value == "" {
			return "worker"
		}
		return value
	}
}

func panelBaseURL(r *http.Request) string {
	if publicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL")), "/"); publicURL != "" {
		return publicURL
	}
	if r != nil && r.Host != "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded == "https" || forwarded == "http" {
			scheme = forwarded
		}
		return scheme + "://" + r.Host
	}
	return ""
}

func nodeConfigurationYAML(r *http.Request, service store.RegisteredService, tokenID, rawToken string) string {
	panelURL := panelBaseURL(r)
	if panelURL == "" {
		panelURL = "https://control.example.com"
	}
	tokenValue := rawToken
	if tokenValue == "" {
		tokenValue = "<regenerate-runtime-token>"
	}
	tokenIDValue := tokenID
	if tokenIDValue == "" {
		tokenIDValue = service.TokenID
	}
	lines := []string{
		"panel:",
		"  url: " + yamlQuote(panelURL),
		"",
		"node:",
		"  id: " + yamlQuote(service.ServiceID),
		"  name: " + yamlQuote(service.ServiceName),
		"  type: " + yamlQuote(service.ServiceType),
		"  description: " + yamlQuote(service.Description),
		"",
		"api:",
		"  host: " + yamlQuote(service.Host),
		"  port: " + strconv.Itoa(service.Port),
		"  ssl_enabled: " + strconv.FormatBool(service.SSLEnabled),
		"",
		"auth:",
		"  token_id: " + yamlQuote(tokenIDValue),
		"  token: " + yamlQuote(tokenValue),
		"",
	}
	if signingKey := nodeStreamIngestSigningKey(service.ServiceType, rawToken != ""); signingKey != "" {
		lines = append(lines,
			"stream_ingest:",
			"  signing_key: "+yamlQuote(signingKey),
			"",
		)
	}
	lines = append(lines,
		"agent:",
		"  data_dir: "+yamlQuote(nodeAgentDataDir(service.ServiceType)),
		"  log_dir: "+yamlQuote(nodeAgentLogDir(service.ServiceType)),
		"",
	)
	return strings.Join(lines, "\n")
}

func nodeStreamIngestSigningKey(serviceType string, includeSecret bool) string {
	if !includeSecret {
		return ""
	}
	if nodeStreamIngestSigningKeyErrorCode(serviceType) != "" {
		return ""
	}
	switch strings.TrimSpace(serviceType) {
	case "worker", "encoder_recorder":
		return strings.TrimSpace(os.Getenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY"))
	default:
		return ""
	}
}

func nodeStreamIngestSigningKeyErrorCode(serviceType string) string {
	switch strings.TrimSpace(serviceType) {
	case "worker", "encoder_recorder":
	default:
		return ""
	}
	key := strings.TrimSpace(os.Getenv("AUTOSTREAM_STREAM_INGEST_SIGNING_KEY"))
	if key == "" {
		return "stream_ingest_signing_key_required"
	}
	upper := strings.ToUpper(key)
	placeholder := strings.Contains(upper, "CHANGE_ME") ||
		strings.Contains(upper, "REPLACE_ME") ||
		strings.Contains(upper, "YOUR_SIGNING_KEY") ||
		(strings.HasPrefix(key, "<") && strings.HasSuffix(key, ">"))
	if len([]byte(key)) < minStreamIngestSigningKeyLen || placeholder {
		return "stream_ingest_signing_key_invalid"
	}
	return ""
}

func requireNodeStreamIngestSigningKey(w http.ResponseWriter, serviceType string) bool {
	if code := nodeStreamIngestSigningKeyErrorCode(serviceType); code != "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": code})
		return false
	}
	return true
}

func yamlQuote(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

func buildNodeAgentURL(host string, port int, sslEnabled bool) string {
	host = strings.TrimSpace(host)
	if host == "" || port <= 0 {
		return ""
	}
	scheme := "http"
	if sslEnabled {
		scheme = "https"
	}
	return scheme + "://" + host + ":" + strconv.Itoa(port)
}

func nodeEndpointFromURL(raw string) (string, int, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "", 0, false
	}
	port := 0
	if parsed.Port() != "" {
		if parsedPort, err := strconv.Atoi(parsed.Port()); err == nil {
			port = parsedPort
		}
	}
	sslEnabled := parsed.Scheme == "https"
	if port == 0 {
		if sslEnabled {
			port = 443
		} else if parsed.Scheme == "http" {
			port = 80
		}
	}
	return parsed.Hostname(), port, sslEnabled
}

func (s *Server) nodeConfiguration(w http.ResponseWriter, r *http.Request) {
	service, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node":               service,
		"node_api_url":       buildNodeAgentURL(service.Host, service.Port, service.SSLEnabled),
		"configuration_yaml": nodeConfigurationYAML(r, service, service.TokenID, ""),
		"configure_command":  nodeConfigureCommand(r, service.ServiceType, service.ServiceID, "<regenerate-configure-token>", ""),
	})
}

func (s *Server) regenerateNodeConfigureToken(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !security.HasPermission(current.Permissions, "api_tokens.revoke") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	service, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	if !s.requireNodeTokenScopePermissions(w, r, service) {
		return
	}
	if !requireNodeStreamIngestSigningKey(w, service.ServiceType) {
		return
	}
	if _, err := nodeRuntimeTokenEncryptionKey(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	token, expiresAt, err := s.issueNodeConfigureToken(r.Context(), service.ServiceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_node_configure_token_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "nodes.configure_token.rotate", ResourceType: "node", ResourceID: service.ServiceID, Result: "success"})
	writeOneTimeSecretJSON(w, http.StatusCreated, map[string]any{
		"node":                       service,
		"configure_token":            token,
		"configure_token_expires_at": expiresAt,
		"configure_command":          nodeConfigureCommand(r, service.ServiceType, service.ServiceID, token, ""),
	})
}

func (s *Server) rotateNodeRuntimeToken(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !security.HasPermission(current.Permissions, "api_tokens.revoke") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	service, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	if !s.requireNodeTokenScopePermissions(w, r, service) {
		return
	}
	if !requireNodeStreamIngestSigningKey(w, service.ServiceType) {
		return
	}
	seal, err := nodeRuntimeTokenSealer()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	token, updated, err := s.services.RotateServiceNodeToken(r.Context(), service.ServiceID, service.TokenID, seal)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "runtime_token_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "rotate_node_runtime_token_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "nodes.runtime_token.rotate", ResourceType: "node", ResourceID: service.ServiceID, Result: "success", Metadata: map[string]any{"token_id": token.ID}})
	writeOneTimeSecretJSON(w, http.StatusCreated, map[string]any{
		"node":               updated,
		"runtime_token_id":   token.ID,
		"runtime_token":      token.RawToken,
		"configuration_yaml": nodeConfigurationYAML(r, updated, token.ID, token.RawToken),
	})
}

func (s *Server) nodeAgentConfigure(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID         string `json:"nodeId"`
		NodeIDSnake    string `json:"node_id"`
		ConfigureToken string `json:"configureToken"`
		Token          string `json:"configure_token"`
		Version        string `json:"version"`
		Commit         string `json:"commit"`
		BuildDate      string `json:"build_date"`
		Hostname       string `json:"hostname"`
		OS             string `json:"os"`
		Arch           string `json:"arch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	nodeID := strings.TrimSpace(body.NodeID)
	if nodeID == "" {
		nodeID = strings.TrimSpace(body.NodeIDSnake)
	}
	configureToken := strings.TrimSpace(body.ConfigureToken)
	if configureToken == "" {
		configureToken = strings.TrimSpace(body.Token)
	}
	service, err := s.services.GetService(r.Context(), nodeID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	if !requireNodeStreamIngestSigningKey(w, service.ServiceType) {
		return
	}
	seal, err := nodeRuntimeTokenSealer()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "store_node_runtime_token_failed"})
		return
	}
	token, updated, err := s.services.ConfigureServiceNode(r.Context(), nodeID, configureToken, time.Now().UTC(), store.ServiceRuntimeReport{
		ServiceID: nodeID,
		Version:   body.Version,
		Commit:    body.Commit,
		BuildDate: body.BuildDate,
		Hostname:  body.Hostname,
		OS:        body.OS,
		Arch:      body.Arch,
	}, seal)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found"})
		return
	}
	if errors.Is(err, store.ErrUnauthorized) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_configure_token"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "configure_node_failed"})
		return
	}
	writeOneTimeSecretJSON(w, http.StatusOK, map[string]any{
		"config":             nodeAgentConfigResponse(r, updated, token.ID, token.RawToken),
		"config_yml":         nodeConfigurationYAML(r, updated, token.ID, token.RawToken),
		"configuration_yaml": nodeConfigurationYAML(r, updated, token.ID, token.RawToken),
	})
}

func nodeAgentConfigResponse(r *http.Request, service store.RegisteredService, tokenID, rawToken string) map[string]any {
	panelURL := panelBaseURL(r)
	if panelURL == "" {
		panelURL = "https://control.example.com"
	}
	response := map[string]any{
		"panel": map[string]any{"url": panelURL},
		"node":  map[string]any{"id": service.ServiceID, "name": service.ServiceName, "type": service.ServiceType},
		"api":   map[string]any{"host": service.Host, "port": service.Port, "ssl_enabled": service.SSLEnabled},
		"auth":  map[string]any{"token_id": tokenID, "token": rawToken},
		"agent": map[string]any{"data_dir": nodeAgentDataDir(service.ServiceType), "log_dir": nodeAgentLogDir(service.ServiceType)},
	}
	if signingKey := nodeStreamIngestSigningKey(service.ServiceType, rawToken != ""); signingKey != "" {
		response["stream_ingest"] = map[string]any{"signing_key": signingKey}
	}
	return response
}

func (s *Server) nodeAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	s.serviceHeartbeat(w, r)
}

func (s *Server) nodeAgentReport(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.heartbeat")
	if !ok {
		return
	}
	var body store.ServiceHeartbeat
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if body.Status == "" {
		body.Status = "online"
	}
	service, err := s.services.Heartbeat(r.Context(), token, body)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_token"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "node_report_failed"})
		return
	}
	writeJSON(w, http.StatusAccepted, service)
}

func validateServiceTokenScopePermissions(actorPermissions, scopes []string) error {
	required := map[string]string{
		"service.secret.resolve": "secrets.update",
		"remediation.execute":    "remediation.execute",
		"streams.start":          "streams.start",
	}
	for _, scope := range scopes {
		permission := required[strings.TrimSpace(scope)]
		if permission == "" {
			continue
		}
		if !security.HasPermission(actorPermissions, permission) {
			return store.ErrPermissionEscalation
		}
	}
	return nil
}

func validateNodeConfigurationSecretPermissions(actorPermissions []string, serviceType string) error {
	switch strings.TrimSpace(serviceType) {
	case "worker", "encoder_recorder":
		if !security.HasPermission(actorPermissions, "secrets.update") {
			return store.ErrPermissionEscalation
		}
	}
	return nil
}

func (s *Server) requireNodeTokenScopePermissions(w http.ResponseWriter, r *http.Request, service store.RegisteredService) bool {
	tokens, err := s.services.ListServiceTokens(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_service_tokens_failed"})
		return false
	}
	for _, token := range tokens {
		if token.ID != service.TokenID || token.RevokedAt != nil {
			continue
		}
		if err := validateServiceTokenScopePermissions(currentFromContext(r.Context()).Permissions, token.Scopes); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
			return false
		}
		if err := validateNodeConfigurationSecretPermissions(currentFromContext(r.Context()).Permissions, service.ServiceType); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
			return false
		}
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"code": "runtime_token_not_found"})
	return false
}

func (s *Server) revokeServiceToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	services, err := s.services.ListServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	for _, service := range services {
		if service.TokenID == id && (service.NodeTokenCiphertext != "" || service.NodeTokenNonce != "") {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "node_runtime_token_requires_node_deletion"})
			return
		}
	}
	err = s.services.RevokeServiceToken(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "revoke_api_token_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "api_tokens.revoke", ResourceType: "service_token", ResourceID: id, Result: "success"})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) rotateServiceToken(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	if !security.HasPermission(current.Permissions, "api_tokens.revoke") {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
		return
	}
	id := r.PathValue("id")
	tokens, err := s.services.ListServiceTokens(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_service_tokens_failed"})
		return
	}
	var existing store.ServiceToken
	for _, candidate := range tokens {
		if candidate.ID == id && candidate.RevokedAt == nil {
			existing = candidate
			break
		}
	}
	if existing.ID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err := validateServiceTokenScopePermissions(current.Permissions, existing.Scopes); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_escalation"})
		return
	}
	services, err := s.services.ListServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	for _, service := range services {
		if service.TokenID == id && (service.NodeTokenCiphertext != "" || service.NodeTokenNonce != "") {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "node_runtime_token_requires_node_rotation"})
			return
		}
	}
	token, err := s.services.RotateServiceToken(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "rotate_api_token_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "api_tokens.rotate",
		ResourceType:  "service_token",
		ResourceID:    token.ID,
		Result:        "success",
		Metadata:      map[string]any{"old_token_id": id, "service_type": token.ServiceType, "scopes": token.Scopes},
	})
	writeOneTimeSecretJSON(w, http.StatusCreated, token)
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	services, err := s.services.ListServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_nodes_failed"})
		return
	}
	writeJSON(w, http.StatusOK, nodeListResponses(services, time.Now().UTC()))
}

type nodeListResponse struct {
	ID                      string         `json:"id"`
	ServiceID               string         `json:"service_id"`
	ServiceType             string         `json:"service_type"`
	ServiceName             string         `json:"service_name"`
	Description             string         `json:"description,omitempty"`
	Host                    string         `json:"host,omitempty"`
	Port                    int            `json:"port,omitempty"`
	SSLEnabled              bool           `json:"ssl_enabled"`
	PublicURL               string         `json:"public_url"`
	Version                 string         `json:"version"`
	ReportedVersion         string         `json:"reported_version,omitempty"`
	ReportedCommit          string         `json:"reported_commit,omitempty"`
	ReportedBuildDate       string         `json:"reported_build_date,omitempty"`
	Status                  string         `json:"status"`
	HealthStatus            string         `json:"health_status"`
	HeartbeatStale          bool           `json:"heartbeat_stale"`
	HeartbeatAgeSec         *int64         `json:"heartbeat_age_sec,omitempty"`
	LastHeartbeatAt         *time.Time     `json:"last_heartbeat_at,omitempty"`
	LastReportedAt          *time.Time     `json:"last_reported_at,omitempty"`
	CurrentStreamID         string         `json:"current_stream_id,omitempty"`
	ReportedHostname        string         `json:"reported_hostname,omitempty"`
	ReportedOS              string         `json:"reported_os,omitempty"`
	ReportedArch            string         `json:"reported_arch,omitempty"`
	Capabilities            map[string]any `json:"capabilities,omitempty"`
	ReportedCapabilities    map[string]any `json:"reported_capabilities,omitempty"`
	Metrics                 map[string]any `json:"metrics,omitempty"`
	ConfigureTokenExpiresAt *time.Time     `json:"configure_token_expires_at,omitempty"`
	ConfigureTokenUsedAt    *time.Time     `json:"configure_token_used_at,omitempty"`
	NodeTokenRotatedAt      *time.Time     `json:"node_token_rotated_at,omitempty"`
	CreatedAt               time.Time      `json:"created_at"`
	UpdatedAt               time.Time      `json:"updated_at"`
}

func nodeListResponses(services []store.RegisteredService, now time.Time) []nodeListResponse {
	out := make([]nodeListResponse, 0, len(services))
	for _, service := range services {
		healthStatus, heartbeatStale, heartbeatAgeSec := serviceHealthFields(service, now)
		out = append(out, nodeListResponse{
			ID:                      service.ServiceID,
			ServiceID:               service.ServiceID,
			ServiceType:             service.ServiceType,
			ServiceName:             service.ServiceName,
			Description:             service.Description,
			Host:                    service.Host,
			Port:                    service.Port,
			SSLEnabled:              service.SSLEnabled,
			PublicURL:               service.PublicURL,
			Version:                 service.Version,
			ReportedVersion:         service.ReportedVersion,
			ReportedCommit:          service.ReportedCommit,
			ReportedBuildDate:       service.ReportedBuildDate,
			Status:                  service.Status,
			HealthStatus:            healthStatus,
			HeartbeatStale:          heartbeatStale,
			HeartbeatAgeSec:         heartbeatAgeSec,
			LastHeartbeatAt:         service.LastHeartbeatAt,
			LastReportedAt:          service.LastReportedAt,
			CurrentStreamID:         service.CurrentStreamID,
			ReportedHostname:        service.ReportedHostname,
			ReportedOS:              service.ReportedOS,
			ReportedArch:            service.ReportedArch,
			Capabilities:            service.Capabilities,
			ReportedCapabilities:    service.ReportedCapabilities,
			Metrics:                 service.Metrics,
			ConfigureTokenExpiresAt: service.ConfigureTokenExpiresAt,
			ConfigureTokenUsedAt:    service.ConfigureTokenUsedAt,
			NodeTokenRotatedAt:      service.NodeTokenRotatedAt,
			CreatedAt:               service.CreatedAt,
			UpdatedAt:               service.UpdatedAt,
		})
	}
	return out
}

func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_node_failed"})
		return
	}
	var body struct {
		Name        *string `json:"name"`
		ServiceName *string `json:"service_name"`
		Description *string `json:"description"`
		Host        *string `json:"host"`
		Port        *int    `json:"port"`
		SSLEnabled  *bool   `json:"ssl_enabled"`
		PublicURL   *string `json:"public_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	serviceName := existing.ServiceName
	if body.ServiceName != nil {
		serviceName = strings.TrimSpace(*body.ServiceName)
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		serviceName = strings.TrimSpace(*body.Name)
	}
	description := existing.Description
	if body.Description != nil {
		description = strings.TrimSpace(*body.Description)
	}
	host := existing.Host
	port := existing.Port
	sslEnabled := existing.SSLEnabled
	if body.PublicURL != nil && strings.TrimSpace(*body.PublicURL) != "" {
		parsedHost, parsedPort, parsedSSL := nodeEndpointFromURL(*body.PublicURL)
		host = parsedHost
		port = parsedPort
		sslEnabled = parsedSSL
	}
	if body.Host != nil {
		host = strings.TrimSpace(*body.Host)
	}
	if body.Port != nil {
		port = *body.Port
	}
	if body.SSLEnabled != nil {
		sslEnabled = *body.SSLEnabled
	}
	publicURL := buildNodeAgentURL(host, port, sslEnabled)
	if publicURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_endpoint"})
		return
	}
	if err := netpolicy.ServiceURLPolicyFromEnv().ValidateURL(publicURL); err != nil {
		code := "invalid_node_endpoint"
		if errors.Is(err, netpolicy.ErrBlockedServiceURL) {
			code = "node_endpoint_blocked"
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	updated, err := s.services.UpdateServiceMetadata(r.Context(), existing.ServiceID, store.ServiceMetadataUpdate{
		ServiceName: serviceName,
		Description: description,
		Host:        host,
		Port:        port,
		SSLEnabled:  sslEnabled,
		PublicURL:   publicURL,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if errors.Is(err, store.ErrInvalidServiceRegistration) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_node_registration"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_node_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        "nodes.update",
		ResourceType:  "node",
		ResourceID:    updated.ServiceID,
		Result:        "success",
		Metadata:      map[string]any{"node_type": updated.ServiceType, "public_url": updated.PublicURL},
	})
	writeJSON(w, http.StatusOK, nodeListResponses([]store.RegisteredService{updated}, time.Now().UTC())[0])
}

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	services, err := s.services.ListServices(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	writeJSON(w, http.StatusOK, serviceHealthResponses(services, time.Now().UTC()))
}

type serviceHealthResponse struct {
	store.RegisteredService
	HealthStatus    string `json:"health_status"`
	HeartbeatStale  bool   `json:"heartbeat_stale"`
	HeartbeatAgeSec *int64 `json:"heartbeat_age_sec,omitempty"`
}

func serviceHealthResponses(services []store.RegisteredService, now time.Time) []serviceHealthResponse {
	out := make([]serviceHealthResponse, 0, len(services))
	for _, service := range services {
		response := serviceHealthResponse{RegisteredService: service}
		response.HealthStatus, response.HeartbeatStale, response.HeartbeatAgeSec = serviceHealthFields(service, now)
		out = append(out, response)
	}
	return out
}

func serviceHealthFields(service store.RegisteredService, now time.Time) (string, bool, *int64) {
	if service.Status == "offline" {
		return "offline", true, heartbeatAge(service.LastHeartbeatAt, now)
	}
	if service.LastHeartbeatAt == nil {
		return "unconfigured", true, nil
	}
	age := heartbeatAge(service.LastHeartbeatAt, now)
	if age != nil && time.Duration(*age)*time.Second > heartbeatOfflineAfter() {
		return "offline", true, age
	}
	if age != nil && time.Duration(*age)*time.Second > heartbeatWarningAfter() {
		return "warning", true, age
	}
	return "healthy", false, age
}

func heartbeatWarningAfter() time.Duration {
	return durationEnv("AUTOSTREAM_NODE_HEARTBEAT_WARNING_AFTER", serviceHeartbeatWarningDefault)
}

func heartbeatOfflineAfter() time.Duration {
	return durationEnv("AUTOSTREAM_NODE_HEARTBEAT_OFFLINE_AFTER", serviceHeartbeatOfflineDefault)
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func heartbeatAge(at *time.Time, now time.Time) *int64 {
	if at == nil {
		return nil
	}
	age := int64(now.Sub(*at).Seconds())
	if age < 0 {
		age = 0
	}
	return &age
}

func stringSliceContains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.services.ListWorkers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_workers_failed"})
		return
	}
	writeJSON(w, http.StatusOK, workers)
}

func (s *Server) getWorker(w http.ResponseWriter, r *http.Request) {
	worker, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_worker_failed"})
		return
	}
	if worker.ServiceType != "worker" {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) assignService(w http.ResponseWriter, r *http.Request) {
	var body struct {
		StreamID       string `json:"stream_id"`
		AssignmentRole string `json:"assignment_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.StreamID = strings.TrimSpace(body.StreamID)
	body.AssignmentRole = normalizeAssignmentRole(body.AssignmentRole)
	if body.StreamID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "stream_id_required"})
		return
	}
	if _, err := s.streams.GetStream(r.Context(), body.StreamID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "stream_not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	current := currentFromContext(r.Context())
	service, err := s.services.AssignServiceToStreamWithRole(r.Context(), existing.ServiceID, body.StreamID, current.User.ID, body.AssignmentRole)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "assign_service_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.assign", ResourceType: "service", ResourceID: service.ServiceID, Result: "success", Metadata: map[string]any{"stream_id": body.StreamID, "service_type": service.ServiceType, "assignment_role": body.AssignmentRole}})
	writeJSON(w, http.StatusOK, service)
}

func (s *Server) unassignService(w http.ResponseWriter, r *http.Request) {
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	previousStreamID := existing.CurrentStreamID
	current := currentFromContext(r.Context())
	service, err := s.services.UnassignServiceFromStream(r.Context(), existing.ServiceID, current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "unassign_service_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.unassign", ResourceType: "service", ResourceID: service.ServiceID, Result: "success", Metadata: map[string]any{"previous_stream_id": previousStreamID, "service_type": service.ServiceType}})
	writeJSON(w, http.StatusOK, service)
}

func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) {
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	if err := s.services.DeleteService(r.Context(), existing.ServiceID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_service_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.delete", ResourceType: "service", ResourceID: existing.ServiceID, Result: "success", Metadata: map[string]any{"service_type": existing.ServiceType, "previous_stream_id": existing.CurrentStreamID}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) assignWorker(w http.ResponseWriter, r *http.Request) {
	var body struct {
		StreamID       string `json:"stream_id"`
		AssignmentRole string `json:"assignment_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.StreamID = strings.TrimSpace(body.StreamID)
	body.AssignmentRole = normalizeAssignmentRole(body.AssignmentRole)
	if body.StreamID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "stream_id_required"})
		return
	}
	if _, err := s.streams.GetStream(r.Context(), body.StreamID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "stream_not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_worker_failed"})
		return
	}
	if existing.ServiceType != "worker" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "not_worker"})
		return
	}
	current := currentFromContext(r.Context())
	worker, err := s.services.AssignServiceToStreamWithRole(r.Context(), r.PathValue("id"), body.StreamID, current.User.ID, body.AssignmentRole)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "assign_worker_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "workers.assign", ResourceType: "worker", ResourceID: worker.ServiceID, Result: "success", Metadata: map[string]any{"stream_id": body.StreamID, "assignment_role": body.AssignmentRole}})
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) unassignWorker(w http.ResponseWriter, r *http.Request) {
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_worker_failed"})
		return
	}
	if existing.ServiceType != "worker" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "not_worker"})
		return
	}
	previousStreamID := existing.CurrentStreamID
	current := currentFromContext(r.Context())
	worker, err := s.services.UnassignServiceFromStream(r.Context(), existing.ServiceID, current.User.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "unassign_worker_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "workers.unassign", ResourceType: "worker", ResourceID: worker.ServiceID, Result: "success", Metadata: map[string]any{"previous_stream_id": previousStreamID}})
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) restartWorker(w http.ResponseWriter, r *http.Request) {
	existing, err := s.services.GetService(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_worker_failed"})
		return
	}
	if existing.ServiceType != "worker" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "not_worker"})
		return
	}
	worker, err := s.services.RequestServiceRestart(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "restart_worker_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "workers.restart", ResourceType: "worker", ResourceID: worker.ServiceID, Result: "success"})
	writeJSON(w, http.StatusAccepted, worker)
}

func (s *Server) serviceRegister(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.register")
	if !ok {
		return
	}
	var body store.ServiceRegistration
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeServiceAudit(r, token, "services.register", "service", "", "failure", map[string]any{"reason": "bad_request"})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	service, err := s.services.RegisterService(r.Context(), token, body)
	if errors.Is(err, store.ErrForbidden) {
		s.writeServiceAudit(r, token, "services.register", "service", body.ServiceID, "failure", map[string]any{"reason": "service_token_scope_mismatch", "requested_service_type": body.ServiceType})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_token_scope_mismatch"})
		return
	}
	if errors.Is(err, store.ErrInvalidServiceRegistration) {
		s.writeServiceAudit(r, token, "services.register", "service", body.ServiceID, "failure", map[string]any{"reason": "invalid_service_registration", "requested_service_type": body.ServiceType})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_service_registration"})
		return
	}
	if err != nil {
		s.writeServiceAudit(r, token, "services.register", "service", body.ServiceID, "failure", map[string]any{"reason": "register_service_failed", "requested_service_type": body.ServiceType})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "register_service_failed"})
		return
	}
	s.writeServiceAudit(r, token, "services.register", "service", service.ServiceID, "success", map[string]any{"service_type": service.ServiceType})
	writeJSON(w, http.StatusAccepted, service)
}

type serviceRuntimeConfigResponse struct {
	Service              serviceRuntimeConfigService         `json:"service"`
	Assignments          []store.StreamServiceAssignment     `json:"assignments"`
	Profiles             map[string][]store.Profile          `json:"profiles"`
	StreamDiscordConfigs []serviceRuntimeDiscordStreamConfig `json:"stream_discord_configs,omitempty"`
	StreamArchiveConfigs []serviceRuntimeArchiveStreamConfig `json:"stream_archive_configs,omitempty"`
	StreamYouTubeConfigs []serviceRuntimeYouTubeStreamConfig `json:"stream_youtube_configs,omitempty"`
}

type serviceRuntimeConfigService struct {
	ServiceID       string         `json:"service_id"`
	ServiceType     string         `json:"service_type"`
	ServiceName     string         `json:"service_name"`
	PublicURL       string         `json:"public_url"`
	Version         string         `json:"version"`
	Status          string         `json:"status"`
	AssignmentRole  string         `json:"assignment_role,omitempty"`
	LastHeartbeatAt *time.Time     `json:"last_heartbeat_at,omitempty"`
	CurrentStreamID string         `json:"current_stream_id,omitempty"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	Metrics         map[string]any `json:"metrics,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type serviceRuntimeDiscordStreamConfig struct {
	StreamID         string `json:"stream_id"`
	AssignmentRole   string `json:"assignment_role"`
	DiscordConfigID  string `json:"discord_config_id"`
	GuildID          string `json:"guild_id"`
	VoiceChannelID   string `json:"voice_channel_id"`
	TextChannelID    string `json:"text_channel_id,omitempty"`
	AutoStartTrigger string `json:"auto_start_trigger,omitempty"`
}

type serviceRuntimeArchiveStreamConfig struct {
	StreamID         string         `json:"stream_id"`
	AssignmentRole   string         `json:"assignment_role"`
	ArchiveProfileID string         `json:"archive_profile_id"`
	Ready            bool           `json:"ready"`
	ReadinessCode    string         `json:"readiness_code,omitempty"`
	ReadinessMessage string         `json:"readiness_message,omitempty"`
	ArchiveConfig    map[string]any `json:"archive_config,omitempty"`
}

type serviceRuntimeYouTubeStreamConfig struct {
	StreamID         string         `json:"stream_id"`
	AssignmentRole   string         `json:"assignment_role"`
	YouTubeOutputID  string         `json:"youtube_output_id"`
	Ready            bool           `json:"ready"`
	ReadinessCode    string         `json:"readiness_code,omitempty"`
	ReadinessMessage string         `json:"readiness_message,omitempty"`
	YouTubeConfig    map[string]any `json:"youtube_config,omitempty"`
	ActiveRuntime    map[string]any `json:"active_runtime,omitempty"`
}

func serviceRuntimeConfigServiceFromStore(service store.RegisteredService) serviceRuntimeConfigService {
	return serviceRuntimeConfigService{
		ServiceID:       service.ServiceID,
		ServiceType:     service.ServiceType,
		ServiceName:     service.ServiceName,
		PublicURL:       service.PublicURL,
		Version:         service.Version,
		Status:          service.Status,
		AssignmentRole:  service.AssignmentRole,
		LastHeartbeatAt: service.LastHeartbeatAt,
		CurrentStreamID: service.CurrentStreamID,
		Capabilities:    service.Capabilities,
		Metrics:         service.Metrics,
		CreatedAt:       service.CreatedAt,
		UpdatedAt:       service.UpdatedAt,
	}
}

type serviceRuntimeSecretResolveRequest struct {
	ServiceID        string `json:"service_id"`
	StreamID         string `json:"stream_id,omitempty"`
	ArchiveProfileID string `json:"archive_profile_id,omitempty"`
	SecretName       string `json:"secret_name"`
}

type serviceRuntimeSecretResolveResponse struct {
	SecretName   string `json:"secret_name"`
	Value        string `json:"value"`
	ExpiresInSec int    `json:"expires_in_sec"`
}

func (s *Server) serviceRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.config.read")
	if !ok {
		return
	}
	serviceID := strings.TrimSpace(r.URL.Query().Get("service_id"))
	if serviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "service_id_required"})
		return
	}
	service, err := s.services.GetService(r.Context(), serviceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	if service.TokenID != token.ID || service.ServiceType != token.ServiceType {
		s.writeServiceAudit(r, token, "services.runtime_config.read", "service", serviceID, "failure", map[string]any{"reason": "service_not_assigned_to_token"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_token"})
		return
	}
	payload, code, err := s.runtimeConfigForService(r.Context(), service)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": code})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeServiceAudit(r, token, "services.runtime_config.read", "service", serviceID, "success", map[string]any{"assignment_count": len(payload.Assignments)})
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) adminServiceRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	serviceID := strings.TrimSpace(r.PathValue("id"))
	if serviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "service_id_required"})
		return
	}
	service, err := s.services.GetService(r.Context(), serviceID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	payload, code, err := s.runtimeConfigForService(r.Context(), service)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": code})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.runtime_config.preview", ResourceType: "service", ResourceID: serviceID, Result: "success", Metadata: map[string]any{"assignment_count": len(payload.Assignments), "service_type": service.ServiceType}})
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) runtimeConfigForService(ctx context.Context, service store.RegisteredService) (serviceRuntimeConfigResponse, string, error) {
	assignments, err := s.services.ListServiceAssignmentsForService(ctx, service.ServiceID)
	if err != nil {
		return serviceRuntimeConfigResponse{}, "list_service_assignments_failed", err
	}
	profiles, err := s.runtimeProfilesForService(ctx, service)
	if err != nil {
		return serviceRuntimeConfigResponse{}, "list_runtime_profiles_failed", err
	}
	discordConfigs, err := s.runtimeDiscordStreamConfigs(ctx, service, assignments)
	if err != nil {
		return serviceRuntimeConfigResponse{}, "list_runtime_discord_configs_failed", err
	}
	archiveConfigs, err := s.runtimeArchiveStreamConfigs(ctx, service, assignments)
	if err != nil {
		return serviceRuntimeConfigResponse{}, "list_runtime_archive_configs_failed", err
	}
	youtubeConfigs, err := s.runtimeYouTubeStreamConfigs(ctx, service, assignments)
	if err != nil {
		return serviceRuntimeConfigResponse{}, "list_runtime_youtube_configs_failed", err
	}
	return serviceRuntimeConfigResponse{Service: serviceRuntimeConfigServiceFromStore(service), Assignments: assignments, Profiles: profiles, StreamDiscordConfigs: discordConfigs, StreamArchiveConfigs: archiveConfigs, StreamYouTubeConfigs: youtubeConfigs}, "", nil
}

func (s *Server) serviceRuntimeSecretResolve(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.secret.resolve")
	if !ok {
		return
	}
	var body serviceRuntimeSecretResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", "", "failure", map[string]any{"reason": "bad_request"})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	serviceID := strings.TrimSpace(body.ServiceID)
	secretName := strings.TrimSpace(body.SecretName)
	if serviceID == "" || secretName == "" {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "service_id_or_secret_name_required", "secret_name": secretName})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "service_id_or_secret_name_required"})
		return
	}
	if !runtimeSecretTransportAllowed(r) {
		w.Header().Set("Cache-Control", "no-store")
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "runtime_secret_transport_insecure", "secret_name": secretName})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "runtime_secret_transport_insecure"})
		return
	}
	service, err := s.services.GetService(r.Context(), serviceID)
	if errors.Is(err, store.ErrNotFound) {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "service_not_registered", "secret_name": secretName})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_service_failed"})
		return
	}
	if service.TokenID != token.ID || service.ServiceType != token.ServiceType {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "service_not_assigned_to_token", "secret_name": secretName})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_token"})
		return
	}
	allowed, err := s.runtimeSecretAllowedForService(r.Context(), service, secretName, strings.TrimSpace(body.StreamID), strings.TrimSpace(body.ArchiveProfileID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_runtime_profiles_failed"})
		return
	}
	if !allowed {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "runtime_secret_not_allowed", "secret_name": secretName})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "runtime_secret_not_allowed"})
		return
	}
	lease, err := s.runtimeLeases.ClaimRuntimeSecretLease(r.Context(), store.RuntimeSecretLease{
		ServiceID:        serviceID,
		TokenID:          token.ID,
		StreamID:         strings.TrimSpace(body.StreamID),
		ArchiveProfileID: strings.TrimSpace(body.ArchiveProfileID),
		SecretName:       secretName,
	}, runtimeSecretLeaseTTL)
	if errors.Is(err, store.ErrRuntimeSecretLeaseActive) {
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "runtime_secret_lease_active", "secret_name": secretName})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "runtime_secret_lease_active"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "runtime_secret_lease_failed"})
		return
	}
	value, err := s.runtimeSecretValue(r.Context(), secretName)
	if errors.Is(err, store.ErrNotFound) {
		_ = s.runtimeLeases.ReleaseRuntimeSecretLease(r.Context(), lease)
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "runtime_secret_not_configured", "secret_name": secretName})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "runtime_secret_not_configured"})
		return
	}
	if errors.Is(err, store.ErrUnknownSecret) {
		_ = s.runtimeLeases.ReleaseRuntimeSecretLease(r.Context(), lease)
		s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "failure", map[string]any{"reason": "runtime_secret_unknown", "secret_name": secretName})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "runtime_secret_unknown"})
		return
	}
	if err != nil {
		_ = s.runtimeLeases.ReleaseRuntimeSecretLease(r.Context(), lease)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_runtime_secret_failed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeServiceAudit(r, token, "services.runtime_secret.resolve", "service", serviceID, "success", map[string]any{"secret_name": secretName, "lease_expires_at": lease.ExpiresAt.Format(time.RFC3339)})
	writeOneTimeSecretJSON(w, http.StatusOK, serviceRuntimeSecretResolveResponse{
		SecretName:   secretName,
		Value:        value,
		ExpiresInSec: int(runtimeSecretLeaseTTL / time.Second),
	})
}

func (s *Server) runtimeProfilesForService(ctx context.Context, service store.RegisteredService) (map[string][]store.Profile, error) {
	kinds := runtimeProfileKindsForService(service.ServiceType)
	profiles := make(map[string][]store.Profile, len(kinds))
	for _, kind := range kinds {
		items, err := s.profiles.ListProfiles(ctx, kind)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if kind != store.ProfileCaption && !runtimeProfileMatchesService(item.Config, service.ServiceID) {
				continue
			}
			item.Config = sanitizeRuntimeProfileConfigForKind(kind, item.Config)
			profiles[string(kind)] = append(profiles[string(kind)], item)
		}
	}
	return profiles, nil
}

func (s *Server) runtimeDiscordStreamConfigs(ctx context.Context, service store.RegisteredService, assignments []store.StreamServiceAssignment) ([]serviceRuntimeDiscordStreamConfig, error) {
	if service.ServiceType != "discord_bot" {
		return nil, nil
	}
	streams, err := s.streams.ListStreams(ctx)
	if err != nil {
		return nil, err
	}
	assignmentRoles := assignmentRoleByStream(assignments, service.ServiceID, "discord_bot")
	items := make([]serviceRuntimeDiscordStreamConfig, 0)
	for _, stream := range streams {
		if strings.TrimSpace(stream.DiscordConfigID) == "" {
			continue
		}
		profile, err := s.profiles.GetProfile(ctx, store.ProfileDiscordConfig, stream.DiscordConfigID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !runtimeProfileMatchesService(profile.Config, service.ServiceID) {
			continue
		}
		guildID := strings.TrimSpace(firstNonEmpty(stream.DiscordGuildID, configString(profile.Config, "guild_id")))
		voiceChannelID := strings.TrimSpace(firstNonEmpty(stream.DiscordVoiceID, configString(profile.Config, "voice_channel_id")))
		if guildID == "" || voiceChannelID == "" {
			continue
		}
		assignmentRole := assignmentRoles[stream.ID]
		if assignmentRole == "" {
			assignmentRole = "primary"
		}
		items = append(items, serviceRuntimeDiscordStreamConfig{
			StreamID:         stream.ID,
			AssignmentRole:   assignmentRole,
			DiscordConfigID:  profile.ID,
			GuildID:          guildID,
			VoiceChannelID:   voiceChannelID,
			TextChannelID:    strings.TrimSpace(firstNonEmpty(stream.DiscordTextID, configString(profile.Config, "text_channel_id"))),
			AutoStartTrigger: strings.TrimSpace(stream.AutoStartTrigger),
		})
	}
	return items, nil
}

func assignmentRoleByStream(assignments []store.StreamServiceAssignment, serviceID, serviceType string) map[string]string {
	roles := make(map[string]string, len(assignments))
	for _, assignment := range assignments {
		if strings.TrimSpace(assignment.ServiceID) != serviceID || strings.TrimSpace(assignment.ServiceType) != serviceType {
			continue
		}
		role := normalizeAssignmentRole(assignment.AssignmentRole)
		if role == "" {
			role = "primary"
		}
		roles[assignment.StreamID] = role
	}
	return roles
}

func (s *Server) runtimeArchiveStreamConfigs(ctx context.Context, service store.RegisteredService, assignments []store.StreamServiceAssignment) ([]serviceRuntimeArchiveStreamConfig, error) {
	if service.ServiceType != "encoder_recorder" {
		return nil, nil
	}
	items := make([]serviceRuntimeArchiveStreamConfig, 0)
	for _, assignment := range assignments {
		if assignment.ServiceID != service.ServiceID || assignment.ServiceType != "encoder_recorder" {
			continue
		}
		stream, err := s.streams.GetStream(ctx, assignment.StreamID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(stream.ArchiveProfileID) == "" {
			continue
		}
		req := servicecall.StartRequest{ArchiveProfileID: stream.ArchiveProfileID}
		item := serviceRuntimeArchiveStreamConfig{
			StreamID:         stream.ID,
			AssignmentRole:   assignment.AssignmentRole,
			ArchiveProfileID: stream.ArchiveProfileID,
			Ready:            true,
		}
		if err := s.applyArchiveConfig(ctx, &req); err != nil {
			item.Ready = false
			item.ReadinessCode = archiveConfigCode(err)
			item.ReadinessMessage = archiveConfigReadinessMessage(err)
		} else if len(req.ArchiveConfig) > 0 {
			item.ArchiveConfig = sanitizeRuntimeProfileConfig(req.ArchiveConfig)
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Server) runtimeYouTubeStreamConfigs(ctx context.Context, service store.RegisteredService, assignments []store.StreamServiceAssignment) ([]serviceRuntimeYouTubeStreamConfig, error) {
	if service.ServiceType != "encoder_recorder" {
		return nil, nil
	}
	items := make([]serviceRuntimeYouTubeStreamConfig, 0)
	for _, assignment := range assignments {
		if assignment.ServiceID != service.ServiceID || assignment.ServiceType != "encoder_recorder" {
			continue
		}
		stream, err := s.streams.GetStream(ctx, assignment.StreamID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(stream.YouTubeOutputID) == "" {
			continue
		}
		item := serviceRuntimeYouTubeStreamConfig{
			StreamID:        stream.ID,
			AssignmentRole:  assignment.AssignmentRole,
			YouTubeOutputID: stream.YouTubeOutputID,
			Ready:           true,
		}
		profile, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, stream.YouTubeOutputID)
		if errors.Is(err, store.ErrNotFound) {
			item.Ready = false
			item.ReadinessCode = errYouTubeOutputNotFound.Error()
			item.ReadinessMessage = youtubeOutputReadinessMessage(errYouTubeOutputNotFound)
			items = append(items, item)
			continue
		}
		if err != nil {
			return nil, err
		}
		item.YouTubeConfig = youtubeRuntimeConfigFromProfile(profile)
		req := servicecall.StartRequest{YouTubeOutputID: stream.YouTubeOutputID}
		if err := s.validateYouTubeOutputReadiness(ctx, stream, &req); err != nil {
			item.Ready = false
			item.ReadinessCode = youtubeOutputCode(err)
			item.ReadinessMessage = youtubeOutputReadinessMessage(err)
		}
		if runtime := s.safeYouTubeRuntimeForStream(ctx, stream.ID); len(runtime) > 0 {
			item.ActiveRuntime = runtime
		}
		items = append(items, item)
	}
	return items, nil
}

func youtubeRuntimeConfigFromProfile(profile store.Profile) map[string]any {
	mode := normalizedYouTubeOutputMode(firstNonEmpty(configString(profile.Config, "mode"), configString(profile.Config, "output_mode")))
	if mode == "" {
		mode = "stream_key"
	}
	out := map[string]any{
		"mode":      mode,
		"output_id": profile.ID,
	}
	for key, value := range map[string]string{
		"rtmp_url":                 configString(profile.Config, "rtmp_url"),
		"stream_key_secret_name":   configString(profile.Config, "stream_key_secret_name"),
		"watch_url":                configString(profile.Config, "watch_url"),
		"oauth_account_id":         firstNonEmpty(configString(profile.Config, "oauth_account_id"), configString(profile.Config, "youtube_oauth_account_id")),
		"broadcast_title_template": firstNonEmpty(configString(profile.Config, "broadcast_title_template"), configString(profile.Config, "broadcast_title")),
		"broadcast_description":    configString(profile.Config, "broadcast_description"),
		"privacy_status":           configString(profile.Config, "privacy_status"),
		"latency_preference":       configString(profile.Config, "latency_preference"),
	} {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	out["enable_auto_start"] = configBool(profile.Config, "enable_auto_start")
	out["enable_auto_stop"] = configBool(profile.Config, "enable_auto_stop")
	out["complete_on_stop"] = youtubeCompleteOnStop(profile.Config)
	return sanitizeRuntimeProfileConfig(out)
}

func (s *Server) safeYouTubeRuntimeForStream(ctx context.Context, streamID string) map[string]any {
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return nil
	}
	runtime, err := storeWithRuntime.GetStreamYouTubeRuntime(ctx, streamID)
	if err != nil {
		return nil
	}
	out := map[string]any{
		"mode":                 runtime.Mode,
		"output_id":            runtime.YouTubeOutput,
		"dry_run":              runtime.DryRun,
		"complete_on_stop":     runtime.CompleteOnStop,
		"complete_retry_count": runtime.CompleteRetryCount,
	}
	if !runtime.CompleteNextRetryAt.IsZero() {
		out["complete_next_retry_at"] = runtime.CompleteNextRetryAt.UTC().Format(time.RFC3339)
	}
	for key, value := range map[string]string{
		"oauth_account_id":       runtime.OAuthAccountID,
		"broadcast_id":           runtime.BroadcastID,
		"live_stream_id":         runtime.LiveStreamID,
		"rtmp_url":               runtime.RTMPURL,
		"stream_key_secret_name": runtime.StreamKeySecretName,
		"complete_last_error":    runtime.CompleteLastError,
	} {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	return sanitizeRuntimeProfileConfig(out)
}

func (s *Server) runtimeSecretAllowedForService(ctx context.Context, service store.RegisteredService, secretName, streamID, archiveProfileID string) (bool, error) {
	if allowed, err := s.runtimeYouTubeStreamSecretAllowed(ctx, service, secretName, streamID); err != nil || allowed {
		return allowed, err
	}
	kinds := runtimeProfileKindsForService(service.ServiceType)
	for _, kind := range kinds {
		items, err := s.profiles.ListProfiles(ctx, kind)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			if service.ServiceType == "encoder_recorder" && kind == store.ProfileArchive && runtimeProfileConfigReferencesSecret(item.Config, secretName) {
				if !runtimeProfileKindAllowsSecret(kind, secretName) {
					continue
				}
				return s.runtimeArchiveProfileSecretAllowed(ctx, service, item.ID, streamID, archiveProfileID)
			}
			if kind != store.ProfileCaption && !runtimeProfileMatchesService(item.Config, service.ServiceID) {
				continue
			}
			if runtimeProfileConfigReferencesSecret(item.Config, secretName) {
				if !runtimeProfileKindAllowsSecret(kind, secretName) {
					continue
				}
				if service.ServiceType == "encoder_recorder" || streamScopedGenericSecretName(secretName) {
					return s.runtimeGenericStreamSecretAllowedForProfile(ctx, service, kind, item.ID, streamID)
				}
				return true, nil
			}
		}
	}
	return s.runtimeIntegrationSecretAllowedForService(ctx, service, secretName, streamID, archiveProfileID)
}

func (s *Server) runtimeYouTubeStreamSecretAllowed(ctx context.Context, service store.RegisteredService, secretName, streamID string) (bool, error) {
	if service.ServiceType != "encoder_recorder" {
		return false, nil
	}
	streamID = strings.TrimSpace(streamID)
	secretName = strings.TrimSpace(secretName)
	if streamID == "" || secretName == "" || !strings.HasPrefix(secretName, "youtube_stream_key_runtime_") {
		return false, nil
	}
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return false, nil
	}
	runtime, err := storeWithRuntime.GetStreamYouTubeRuntime(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(runtime.StreamKeySecretName) != secretName || runtime.Mode != "live_api" {
		return false, nil
	}
	if strings.TrimSpace(runtime.YouTubeOutput) == "" {
		return false, nil
	}
	if _, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, runtime.YouTubeOutput); errors.Is(err, store.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return s.servicePrimaryAssignedToStream(ctx, service.ServiceID, streamID)
}

func (s *Server) runtimeSecretValue(ctx context.Context, secretName string) (string, error) {
	if kind, id, field, ok := parseRuntimeIntegrationSecretName(secretName); ok {
		switch kind + ":" + field {
		case "drive_destination:folder_id":
			destination, err := s.integrations.GetDriveDestinationForDispatch(ctx, id)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(destination.FolderID) == "" {
				return "", store.ErrNotFound
			}
			return destination.FolderID, nil
		case "oauth_provider:client_secret":
			provider, err := s.integrations.GetOAuthProviderForDispatch(ctx, id)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(provider.ClientSecret) == "" {
				return "", store.ErrNotFound
			}
			return provider.ClientSecret, nil
		case "oauth_account:refresh_token":
			account, err := s.integrations.GetOAuthAccountForDispatch(ctx, id)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(account.RefreshToken) == "" {
				return "", store.ErrNotFound
			}
			return account.RefreshToken, nil
		default:
			return "", store.ErrUnknownSecret
		}
	}
	return s.secrets.GetSecretValue(ctx, secretName)
}

func (s *Server) runtimeIntegrationSecretAllowedForService(ctx context.Context, service store.RegisteredService, secretName, streamID, archiveProfileID string) (bool, error) {
	if service.ServiceType != "encoder_recorder" {
		return false, nil
	}
	kind, id, field, ok := parseRuntimeIntegrationSecretName(secretName)
	if !ok {
		return false, nil
	}
	if strings.TrimSpace(streamID) == "" {
		return false, nil
	}
	primaryAssigned, err := s.servicePrimaryAssignedToStream(ctx, service.ServiceID, streamID)
	if err != nil {
		return false, err
	}
	if !primaryAssigned {
		return false, nil
	}
	stream, err := s.streams.GetStream(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	streamArchiveProfileID := strings.TrimSpace(stream.ArchiveProfileID)
	if strings.TrimSpace(archiveProfileID) == "" {
		archiveProfileID = streamArchiveProfileID
	} else if strings.TrimSpace(archiveProfileID) != streamArchiveProfileID {
		return false, nil
	}
	if strings.TrimSpace(archiveProfileID) == "" {
		return false, nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileArchive, archiveProfileID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	destinationID := strings.TrimSpace(configString(profile.Config, "drive_destination_id"))
	if destinationID == "" {
		return false, nil
	}
	return s.runtimeIntegrationSecretAllowedForDriveDestination(ctx, destinationID, kind, id, field)
}

func (s *Server) runtimeArchiveProfileSecretAllowed(ctx context.Context, service store.RegisteredService, profileID, streamID, requestedProfileID string) (bool, error) {
	if service.ServiceType != "encoder_recorder" {
		return false, nil
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return false, nil
	}
	primaryAssigned, err := s.servicePrimaryAssignedToStream(ctx, service.ServiceID, streamID)
	if err != nil {
		return false, err
	}
	if !primaryAssigned {
		return false, nil
	}
	stream, err := s.streams.GetStream(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(requestedProfileID) != "" && strings.TrimSpace(requestedProfileID) != strings.TrimSpace(stream.ArchiveProfileID) {
		return false, nil
	}
	return stream.ArchiveProfileID == profileID, nil
}

func (s *Server) runtimeGenericStreamSecretAllowedForProfile(ctx context.Context, service store.RegisteredService, kind store.ProfileKind, profileID, streamID string) (bool, error) {
	if service.ServiceType != "encoder_recorder" && service.ServiceType != "worker" {
		return false, nil
	}
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return false, nil
	}
	primaryAssigned, err := s.servicePrimaryAssignedToStream(ctx, service.ServiceID, streamID)
	if err != nil {
		return false, err
	}
	if !primaryAssigned {
		return false, nil
	}
	stream, err := s.streams.GetStream(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch kind {
	case store.ProfileYouTubeOutput:
		return stream.YouTubeOutputID == profileID, nil
	case store.ProfileArchive:
		return stream.ArchiveProfileID == profileID, nil
	case store.ProfileEncoder:
		return stream.EncoderProfileID == profileID, nil
	case store.ProfileOverlay:
		return stream.OverlayProfileID == profileID, nil
	case store.ProfileCaption:
		return service.ServiceType == "worker" && stream.CaptionProfileID == profileID, nil
	default:
		return false, nil
	}
}

func (s *Server) servicePrimaryAssignedToStream(ctx context.Context, serviceID, streamID string) (bool, error) {
	assignments, err := s.services.ListServiceAssignmentsForService(ctx, serviceID)
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		role := strings.TrimSpace(assignment.AssignmentRole)
		if role == "" {
			role = "primary"
		}
		if assignment.StreamID == streamID && role == "primary" {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) runtimeIntegrationSecretAllowedForDriveDestination(ctx context.Context, destinationID, kind, id, field string) (bool, error) {
	destination, err := s.integrations.GetDriveDestinationForDispatch(ctx, destinationID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch kind + ":" + field {
	case "drive_destination:folder_id":
		return destination.ID == id, nil
	case "oauth_account:refresh_token":
		if destination.AuthMode != "oauth2" || destination.OAuthAccountID != id {
			return false, nil
		}
		account, err := s.integrations.GetOAuthAccountForDispatch(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive), nil
	case "oauth_provider:client_secret":
		if destination.AuthMode != "oauth2" || strings.TrimSpace(destination.OAuthAccountID) == "" {
			return false, nil
		}
		account, err := s.integrations.GetOAuthAccountForDispatch(ctx, destination.OAuthAccountID)
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return account.ProviderID == id && store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive), nil
	default:
		return false, nil
	}
}

func parseRuntimeIntegrationSecretName(secretName string) (kind string, id string, field string, ok bool) {
	parts := strings.Split(strings.TrimSpace(secretName), ":")
	if len(parts) != 3 {
		return "", "", "", false
	}
	kind = strings.TrimSpace(parts[0])
	id = strings.TrimSpace(parts[1])
	field = strings.TrimSpace(parts[2])
	if kind == "" || id == "" || field == "" {
		return "", "", "", false
	}
	switch kind + ":" + field {
	case "drive_destination:folder_id", "oauth_provider:client_secret", "oauth_account:refresh_token":
		return kind, id, field, true
	default:
		return "", "", "", false
	}
}

func runtimeProfileConfigReferencesSecret(config map[string]any, secretName string) bool {
	for key, value := range config {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if !strings.HasSuffix(normalizedKey, "_secret_name") && !strings.HasSuffix(normalizedKey, "_secret_ref") && !strings.HasSuffix(normalizedKey, "_secret_id") {
			continue
		}
		if stringValue, ok := value.(string); ok && strings.TrimSpace(stringValue) == secretName {
			return true
		}
	}
	return false
}

func runtimeProfileKindAllowsSecret(kind store.ProfileKind, secretName string) bool {
	secretName = strings.TrimSpace(secretName)
	if secretName == "" {
		return false
	}
	if integrationKind, _, field, ok := parseRuntimeIntegrationSecretName(secretName); ok {
		return kind == store.ProfileArchive && ((integrationKind == "drive_destination" && field == "folder_id") ||
			(integrationKind == "oauth_provider" && field == "client_secret") ||
			(integrationKind == "oauth_account" && field == "refresh_token"))
	}
	switch kind {
	case store.ProfileDiscordConfig:
		return secretName == "discord_bot_token" || strings.HasPrefix(secretName, "discord_bot_token_")
	case store.ProfileYouTubeOutput:
		return secretName == "youtube_stream_key" || strings.HasPrefix(secretName, "youtube_stream_key_")
	case store.ProfileArchive:
		return secretName == "google_drive_folder_id" ||
			strings.HasPrefix(secretName, "google_drive_folder_id_") ||
			strings.HasPrefix(secretName, "google_oauth_refresh_token_")
	case store.ProfileEncoder:
		return strings.HasPrefix(secretName, "encoder_runtime_secret_")
	case store.ProfileCaption:
		return secretName == "deepgram_api_key" || strings.HasPrefix(secretName, "deepgram_api_key_")
	default:
		return false
	}
}

func streamScopedGenericSecretName(secretName string) bool {
	secretName = strings.TrimSpace(secretName)
	for _, prefix := range []string{
		"youtube_stream_key_",
		"google_oauth_refresh_token_",
		"google_drive_folder_id_",
		"deepgram_api_key_",
	} {
		if strings.HasPrefix(secretName, prefix) {
			return true
		}
	}
	switch secretName {
	case "youtube_stream_key", "google_drive_folder_id", "deepgram_api_key":
		return true
	default:
		return false
	}
}

func runtimeProfileKindsForService(serviceType string) []store.ProfileKind {
	switch serviceType {
	case "discord_bot":
		return []store.ProfileKind{store.ProfileDiscordConfig, store.ProfileCaption}
	case "encoder_recorder":
		return []store.ProfileKind{store.ProfileEncoder, store.ProfileArchive, store.ProfileYouTubeOutput, store.ProfileOverlay}
	case "worker":
		return []store.ProfileKind{store.ProfileOverlay, store.ProfileCaption}
	default:
		return nil
	}
}

func runtimeProfileMatchesService(config map[string]any, serviceID string) bool {
	if value, ok := config["service_id"].(string); ok {
		return strings.TrimSpace(value) == serviceID
	}
	if values, ok := config["service_ids"].([]any); ok {
		for _, value := range values {
			if item, ok := value.(string); ok && strings.TrimSpace(item) == serviceID {
				return true
			}
		}
	}
	return false
}

func sanitizeRuntimeProfileConfig(config map[string]any) map[string]any {
	out := make(map[string]any, len(config))
	for key, value := range config {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" || runtimeSecretLikeKey(trimmedKey) {
			continue
		}
		out[trimmedKey] = sanitizeRuntimeProfileValue(value)
	}
	return out
}

func sanitizeRuntimeProfileConfigForKind(kind store.ProfileKind, config map[string]any) map[string]any {
	if kind == store.ProfileCaption {
		config = normalizeProfileConfig(kind, config)
	}
	out := sanitizeRuntimeProfileConfig(config)
	if kind == store.ProfileDiscordConfig {
		delete(out, "guild_id")
		delete(out, "voice_channel_id")
		delete(out, "text_channel_id")
	}
	return out
}

func sanitizeRuntimeProfileValue(value any) any {
	switch typed := value.(type) {
	case string:
		if runtimeSecretLikeValue(typed) {
			return "<redacted>"
		}
		return typed
	case bool, float64, int, int64, uint64:
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeRuntimeProfileValue(item))
		}
		return out
	case map[string]any:
		return sanitizeRuntimeProfileConfig(typed)
	default:
		return nil
	}
}

func runtimeSecretLikeKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, suffix := range []string{"_secret_name", "_secret_ref", "_secret_id", "_secret_status", "_configured", "_fingerprint"} {
		if strings.HasSuffix(normalized, suffix) {
			return false
		}
	}
	for _, token := range []string{"password", "passwd", "token", "api_key", "apikey", "private_key", "credential", "webhook_url", "stream_key", "client_secret", "refresh_token", "access_token", "authorization", "folder_id", "drive_folder_id", "google_drive_folder_id", "gdrive_folder_id"} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func runtimeSecretLikeValue(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, pattern := range []string{"bearer ", "authorization:", "password=", "token=", "access_token=", "refresh_token=", "discord.com/api/webhooks/", "hooks.slack.com/services/", "-----begin private key-----"} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return strings.Contains(lower, "://") && strings.Contains(lower, "@")
}

func (s *Server) serviceStreamEvent(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "")
	if !ok {
		return
	}
	var body store.ServiceStreamEvent
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	requiredScope := serviceStreamEventRequiredScope(token.ServiceType, body.EventType)
	if requiredScope == "" || !serviceTokenHasScope(token, requiredScope) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "missing_service_scope"})
		return
	}
	if err := s.services.WriteStreamEvent(r.Context(), token, body); errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_stream"})
		return
	} else if errors.Is(err, store.ErrInvalidServiceStreamEvent) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_event_type"})
		return
	} else if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "stream_event_rejected"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

type serviceArtifactReport struct {
	ServiceID string                          `json:"service_id"`
	StreamID  string                          `json:"stream_id"`
	Artifacts []serviceArtifactReportArtifact `json:"artifacts"`
}

type serviceArtifactReportArtifact struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	RelativePath string `json:"relative_path"`
	SizeBytes    int64  `json:"size_bytes"`
}

const maxServiceArtifactReportBodyBytes = 64 << 10

func (s *Server) serviceStreamArtifacts(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "encoder.status.write")
	if !ok {
		return
	}
	if token.ServiceType != "encoder_recorder" {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", "", "failure", map[string]any{"reason": "service_token_scope_mismatch"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_token_scope_mismatch"})
		return
	}
	var body serviceArtifactReport
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxServiceArtifactReportBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", "", "failure", map[string]any{"reason": "bad_request"})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	artifacts := serviceArtifactReportArtifacts(body.Artifacts)
	if err := store.ValidateStreamArtifactReport(body.StreamID, artifacts); err != nil {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "invalid_stream_artifact", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_artifact"})
		return
	}
	event := store.ServiceStreamEvent{
		ServiceID: body.ServiceID,
		StreamID:  body.StreamID,
		EventType: "archive.artifacts.reported",
		Payload:   map[string]any{"artifact_count": len(artifacts)},
	}
	if reporter, ok := s.streams.(store.StreamArtifactReportStore); ok {
		if err := reporter.WriteStreamArtifactReport(r.Context(), token, event, artifacts); errors.Is(err, store.ErrForbidden) {
			s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "service_not_assigned_to_stream", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_stream"})
			return
		} else if errors.Is(err, store.ErrInvalidServiceStreamEvent) {
			s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "invalid_stream_event_type", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_event_type"})
			return
		} else if errors.Is(err, store.ErrNotFound) {
			s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "service_or_stream_not_found", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_or_stream_not_found"})
			return
		} else if err != nil {
			s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "stream_artifact_rejected", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "stream_artifact_rejected"})
			return
		}
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "success", map[string]any{"service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "artifact_count": len(artifacts)})
		return
	}
	if err := s.services.WriteStreamEvent(r.Context(), token, event); errors.Is(err, store.ErrForbidden) {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "service_not_assigned_to_stream", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_stream"})
		return
	} else if errors.Is(err, store.ErrNotFound) {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "service_not_registered", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	} else if err != nil {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "stream_artifact_rejected", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "stream_artifact_rejected"})
		return
	}
	if err := s.streams.UpsertStreamArtifacts(r.Context(), body.StreamID, artifacts); errors.Is(err, store.ErrNotFound) {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "stream_not_found", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "stream_not_found"})
		return
	} else if err != nil {
		s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "failure", map[string]any{"reason": "stream_artifact_rejected", "service_id": body.ServiceID, "artifact_count": len(artifacts)})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "stream_artifact_rejected"})
		return
	}
	s.writeServiceAudit(r, token, "archive.artifacts.reported", "stream", body.StreamID, "success", map[string]any{"service_id": body.ServiceID, "artifact_count": len(artifacts)})
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "artifact_count": len(artifacts)})
}

func serviceArtifactReportArtifacts(input []serviceArtifactReportArtifact) []store.StreamArtifact {
	artifacts := make([]store.StreamArtifact, 0, len(input))
	for _, artifact := range input {
		artifacts = append(artifacts, store.StreamArtifact{
			Kind:         artifact.Kind,
			Name:         artifact.Name,
			RelativePath: artifact.RelativePath,
			SizeBytes:    artifact.SizeBytes,
		})
	}
	return artifacts
}

func serviceStreamEventRequiredScope(serviceType, eventType string) string {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if eventType == "" {
		return ""
	}
	switch serviceType {
	case "worker":
		return "worker.events.write"
	case "encoder_recorder":
		return "encoder.status.write"
	case "discord_bot":
		return "discord.status.write"
	case "observability":
		return "observability.ingest"
	default:
		return ""
	}
}

func serviceTokenHasScope(token store.ServiceToken, scope string) bool {
	for _, value := range token.Scopes {
		if value == scope {
			return true
		}
	}
	return false
}

func (s *Server) serviceHeartbeat(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "service.heartbeat")
	if !ok {
		return
	}
	var body store.ServiceHeartbeat
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	service, err := s.services.Heartbeat(r.Context(), token, body)
	if errors.Is(err, store.ErrForbidden) {
		s.writeServiceAudit(r, token, "services.heartbeat", "service", body.ServiceID, "failure", map[string]any{"reason": "service_not_assigned_to_token", "current_stream_id": body.CurrentStreamID})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_assigned_to_token"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		s.writeServiceAudit(r, token, "services.heartbeat", "service", body.ServiceID, "failure", map[string]any{"reason": "service_not_registered", "current_stream_id": body.CurrentStreamID})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "service_not_registered"})
		return
	}
	if err != nil {
		s.writeServiceAudit(r, token, "services.heartbeat", "service", body.ServiceID, "failure", map[string]any{"reason": "heartbeat_failed", "current_stream_id": body.CurrentStreamID})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "heartbeat_failed"})
		return
	}
	writeJSON(w, http.StatusAccepted, service)
}

type serviceRemediationExecuteRequest struct {
	ActionID   string `json:"action_id"`
	Action     string `json:"action"`
	IncidentID string `json:"incident_id"`
	StreamID   string `json:"stream_id"`
}

func (s *Server) serviceTokenRegisteredForType(ctx context.Context, token store.ServiceToken, serviceType string) (bool, error) {
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return false, err
	}
	for _, service := range services {
		if service.TokenID == token.ID && service.ServiceType == serviceType && strings.TrimSpace(service.Status) != "pending" {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) registeredServiceForToken(ctx context.Context, token store.ServiceToken) (store.RegisteredService, bool, error) {
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return store.RegisteredService{}, false, err
	}
	for _, service := range services {
		if service.TokenID == token.ID && service.ServiceType == token.ServiceType && strings.TrimSpace(service.Status) != "pending" {
			return service, true, nil
		}
	}
	return store.RegisteredService{}, false, nil
}

func (s *Server) serviceObservabilitySignal(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "observability.ingest")
	if !ok {
		return
	}
	if token.ServiceType != "worker" && token.ServiceType != "encoder_recorder" {
		s.writeServiceAudit(r, token, "observability.signals.ingest", "service", "", "failure", map[string]any{"reason": "service_type_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	service, registered, err := s.registeredServiceForToken(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	if !registered {
		s.writeServiceAudit(r, token, "observability.signals.ingest", "service", "", "failure", map[string]any{"reason": "service_token_not_registered"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_token_not_registered"})
		return
	}
	obs, configured, err := s.observabilityClient(r.Context())
	if err != nil || !configured {
		s.writeServiceAudit(r, token, "observability.signals.ingest", "service", service.ServiceID, "failure", map[string]any{"reason": "observability_not_configured"})
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.writeServiceAudit(r, token, "observability.signals.ingest", "service", service.ServiceID, "failure", map[string]any{"reason": "bad_request"})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	payload["service_id"] = service.ServiceID
	payload["service_type"] = service.ServiceType
	body, err := obs.Post(r.Context(), "/signals", payload)
	if err != nil {
		s.writeServiceAudit(r, token, "observability.signals.ingest", "service", service.ServiceID, "failure", map[string]any{"reason": "observability_request_failed", "signal_name": stringMapValue(payload, "name"), "stream_id": stringMapValue(payload, "stream_id")})
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "observability_request_failed"})
		return
	}
	s.writeServiceAudit(r, token, "observability.signals.ingest", "service", service.ServiceID, "success", map[string]any{"signal_name": stringMapValue(payload, "name"), "stream_id": stringMapValue(payload, "stream_id")})
	writeObservabilityJSON(w, http.StatusAccepted, "/signals", body)
}

func stringMapValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func (s *Server) serviceRemediationExecute(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "remediation.execute")
	if !ok {
		return
	}
	if token.ServiceType != "observability" {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	registered, err := s.serviceTokenRegisteredForType(r.Context(), token, "observability")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_services_failed"})
		return
	}
	if !registered {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_token_not_registered"})
		return
	}
	var body serviceRemediationExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.ActionID = strings.TrimSpace(body.ActionID)
	body.Action = strings.TrimSpace(body.Action)
	body.StreamID = strings.TrimSpace(body.StreamID)
	body.IncidentID = strings.TrimSpace(body.IncidentID)
	if body.ActionID == "" || body.IncidentID == "" || body.StreamID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "remediation_context_required"})
		return
	}
	if !isServiceRemediationActionAllowed(body.Action) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "unsupported_remediation_action"})
		return
	}
	stream, err := s.streams.GetStream(r.Context(), body.StreamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"missing_service_types": missing}))
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	archiveConfig, err := s.retryArchiveConfig(r.Context(), stream)
	if err != nil {
		code := archiveConfigCode(err)
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": code, "archive_profile_id": stream.ArchiveProfileID}))
		writeJSON(w, archiveConfigStatus(err), map[string]string{"code": code})
		return
	}
	obs, configured, err := s.observabilityClient(r.Context())
	if err != nil || !configured {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": "observability_not_configured"}))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return
	}
	if err := obs.ValidateRemediationDispatchContext(r.Context(), body.ActionID, body.Action, body.IncidentID, stream.ID); err != nil {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": "observability_context_invalid"}))
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "remediation_context_not_verified"})
		return
	}
	if err := s.remediation.ClaimRemediationExecution(r.Context(), body.ActionID, body.IncidentID, stream.ID, body.Action); errors.Is(err, store.ErrAlreadyExists) {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": "replay"}))
		writeJSON(w, http.StatusConflict, map[string]string{"code": "remediation_action_replayed"})
		return
	} else if errors.Is(err, store.ErrInvalidRemediationExecution) {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": "invalid_context"}))
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "remediation_context_required"})
		return
	} else if err != nil {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"reason": "claim_failed"}))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "remediation_claim_failed"})
		return
	}
	results := s.dispatcher.RetryArchiveUpload(r.Context(), stream, primaryAssignments, archiveConfig)
	results = sanitizeDispatchResults(results)
	if hasDispatchFailure(results) {
		s.writeAudit(r, serviceRemediationAudit(token, body, "failure", map[string]any{"dispatch": results}))
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": results})
		return
	}
	logEntry, err := s.streams.RetryArchiveUpload(r.Context(), stream.ID, "service:"+token.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "retry_upload_failed"})
		return
	}
	s.writeAudit(r, serviceRemediationAudit(token, body, "success", map[string]any{"dispatch": results}))
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":      "executed",
		"action_id":   body.ActionID,
		"action":      body.Action,
		"incident_id": body.IncidentID,
		"stream_id":   stream.ID,
		"log":         logEntry,
		"dispatch":    results,
	})
}

func isServiceRemediationActionAllowed(action string) bool {
	switch action {
	case "retry_gdrive_upload", "retry_package_remux":
		return true
	default:
		return false
	}
}

func serviceRemediationAudit(token store.ServiceToken, req serviceRemediationExecuteRequest, result string, metadata map[string]any) store.AuditEvent {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["action"] = req.Action
	metadata["action_id"] = req.ActionID
	metadata["incident_id"] = req.IncidentID
	return store.AuditEvent{
		ActorUserID:   "service:" + token.ID,
		ActorUsername: token.ServiceType,
		Action:        "remediation.execute",
		ResourceType:  "stream",
		ResourceID:    req.StreamID,
		Result:        result,
		Metadata:      metadata,
	}
}

func (s *Server) listStreams(w http.ResponseWriter, r *http.Request) {
	items, err := s.streams.ListStreams(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_streams_failed"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type streamSettingsRequest struct {
	Name                  string `json:"name,omitempty"`
	ScheduledStartAt      string `json:"scheduled_start_at,omitempty"`
	ScheduledEndAt        string `json:"scheduled_end_at,omitempty"`
	DiscordConfigID       string `json:"discord_config_id,omitempty"`
	DiscordGuildID        string `json:"discord_guild_id,omitempty"`
	DiscordVoiceID        string `json:"discord_voice_channel_id,omitempty"`
	DiscordTextID         string `json:"discord_text_channel_id,omitempty"`
	AutoStartTrigger      string `json:"auto_start_trigger,omitempty"`
	EncoderProfileID      string `json:"encoder_profile_id,omitempty"`
	CaptionProfileID      string `json:"caption_profile_id,omitempty"`
	OverlayProfileID      string `json:"overlay_profile_id,omitempty"`
	ArchiveProfileID      string `json:"archive_profile_id,omitempty"`
	ArchiveOAuthAccountID string `json:"archive_oauth_account_id,omitempty"`
	ArchiveFolderID       string `json:"archive_folder_id,omitempty"`
	ArchiveSharedDrive    bool   `json:"archive_shared_drive,omitempty"`
	ArchiveSharedDriveID  string `json:"archive_shared_drive_id,omitempty"`
	ArchiveFileName       string `json:"archive_file_name,omitempty"`
	ArchiveRetentionDays  int    `json:"archive_retention_days,omitempty"`
	YouTubeOutputID       string `json:"youtube_output_id,omitempty"`
	EncoderInputURL       string `json:"encoder_input_url,omitempty"`
	EncoderServiceID      string `json:"encoder_service_id,omitempty"`
	WorkerServiceID       string `json:"worker_service_id,omitempty"`
}

const autoStartTriggerDiscordVoiceJoin = "discord_voice_join"

func (s *Server) createStream(w http.ResponseWriter, r *http.Request) {
	var body streamSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "name_required"})
		return
	}
	settings, code := streamSettingsFromRequest(body)
	if code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if err := s.validateStreamSettingsReferences(r.Context(), settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": streamSettingsReferenceCode(err)})
		return
	}
	current := currentFromContext(r.Context())
	if code, status := s.validateStreamServiceAssignmentRequest(r.Context(), body, current.Permissions); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	stream, err := s.streams.CreateStream(r.Context(), body.Name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_stream_failed"})
		return
	}
	if streamArchiveDirectRequested(body) {
		settings, err = s.materializeStreamArchiveSettings(r.Context(), stream, settings, body)
		if err != nil {
			writeJSON(w, streamArchiveSettingsStatus(err), map[string]string{"code": streamArchiveSettingsCode(err)})
			return
		}
	}
	if streamSettingsConfigured(settings) {
		stream, err = s.streams.UpdateStreamSettings(r.Context(), stream.ID, settings)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_settings_failed"})
			return
		}
	}
	if code, status := s.applyStreamServiceAssignments(r, stream.ID, body, current); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.create", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: streamSettingsAuditMetadata(stream)})
	writeJSON(w, http.StatusCreated, stream)
}

func (s *Server) getStream(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	writeJSON(w, http.StatusOK, stream)
}

type externalE2EConfigResponse struct {
	SchemaVersion      int                           `json:"schema_version"`
	StreamID           string                        `json:"stream_id"`
	RuntimeConfig      externalE2ERuntimeConfig      `json:"runtime_config"`
	ServiceAssignments externalE2EServiceAssignments `json:"service_assignments"`
	Confirmations      externalE2EConfirmations      `json:"confirmations"`
	Readiness          externalE2EReadiness          `json:"readiness"`
}

type externalE2ERuntimeConfig struct {
	YouTubeOutputID    string `json:"youtube_output_id"`
	DriveDestinationID string `json:"drive_destination_id"`
	DiscordConfigID    string `json:"discord_config_id"`
	EncoderProfileID   string `json:"encoder_profile_id"`
	ArchiveProfileID   string `json:"archive_profile_id"`
}

type externalE2EServiceAssignments struct {
	DiscordBotServiceID             string `json:"discord_bot_service_id"`
	EncoderRecorderPrimaryServiceID string `json:"encoder_recorder_primary_service_id"`
	WorkerPrimaryServiceID          string `json:"worker_primary_service_id"`
	EncoderRecorderStandbyServiceID string `json:"encoder_recorder_standby_service_id"`
	WorkerStandbyServiceID          string `json:"worker_standby_service_id"`
}

type externalE2EConfirmations struct {
	YouTubeOutputSaved               bool `json:"youtube_output_saved"`
	DriveDestinationSaved            bool `json:"drive_destination_saved"`
	DiscordConfigSaved               bool `json:"discord_config_saved"`
	PrimaryAssignmentsSaved          bool `json:"primary_assignments_saved"`
	RuntimeConfigDistributionEnabled bool `json:"runtime_config_distribution_enabled"`
}

type externalE2EReadiness struct {
	Ready                            bool     `json:"ready"`
	MissingConfirmations             []string `json:"missing_confirmations"`
	MissingRuntimeIDs                []string `json:"missing_runtime_ids"`
	MissingPrimaryServices           []string `json:"missing_primary_services"`
	MissingRuntimeConfigCapabilities []string `json:"missing_runtime_config_capabilities"`
}

func (s *Server) externalE2EConfig(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	payload, err := s.externalE2EConfigForStream(r.Context(), stream)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "external_e2e_config_failed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) externalE2EConfigForStream(ctx context.Context, stream store.Stream) (externalE2EConfigResponse, error) {
	payload := externalE2EConfigResponse{
		SchemaVersion: 1,
		StreamID:      stream.ID,
		RuntimeConfig: externalE2ERuntimeConfig{
			YouTubeOutputID:  stream.YouTubeOutputID,
			DiscordConfigID:  stream.DiscordConfigID,
			EncoderProfileID: stream.EncoderProfileID,
			ArchiveProfileID: stream.ArchiveProfileID,
		},
	}
	if stream.YouTubeOutputID != "" {
		ok, err := s.profileExists(ctx, store.ProfileYouTubeOutput, stream.YouTubeOutputID)
		if err != nil {
			return payload, err
		}
		payload.Confirmations.YouTubeOutputSaved = ok
	}
	if stream.DiscordConfigID != "" {
		ok, err := s.profileExists(ctx, store.ProfileDiscordConfig, stream.DiscordConfigID)
		if err != nil {
			return payload, err
		}
		payload.Confirmations.DiscordConfigSaved = ok
	}
	if stream.ArchiveProfileID != "" {
		profile, err := s.profiles.GetProfile(ctx, store.ProfileArchive, stream.ArchiveProfileID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return payload, err
		}
		if err == nil {
			payload.RuntimeConfig.DriveDestinationID = strings.TrimSpace(configString(profile.Config, "drive_destination_id"))
			if payload.RuntimeConfig.DriveDestinationID != "" {
				destination, err := s.integrations.GetDriveDestination(ctx, payload.RuntimeConfig.DriveDestinationID)
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					return payload, err
				}
				payload.Confirmations.DriveDestinationSaved = err == nil && destination.FolderIDConfigured
			}
		}
	}
	assignments, err := s.services.ListStreamAssignments(ctx, stream.ID)
	if err != nil {
		return payload, err
	}
	payload.ServiceAssignments = externalE2EServiceAssignmentsFromServices(assignments)
	primaryAssignments := primaryStreamAssignments(assignments)
	payload.Confirmations.PrimaryAssignmentsSaved = len(missingServiceTypes(primaryAssignments, requiredStartServiceTypes)) == 0
	payload.Confirmations.RuntimeConfigDistributionEnabled = payload.Confirmations.PrimaryAssignmentsSaved && allServicesHaveCapability(primaryAssignments, "runtime_config")
	payload.Readiness = externalE2EReadinessFromPayload(payload, primaryAssignments)
	return payload, nil
}

func externalE2EReadinessFromPayload(payload externalE2EConfigResponse, primaryAssignments []store.RegisteredService) externalE2EReadiness {
	out := externalE2EReadiness{
		MissingConfirmations:             make([]string, 0),
		MissingRuntimeIDs:                make([]string, 0),
		MissingPrimaryServices:           make([]string, 0),
		MissingRuntimeConfigCapabilities: make([]string, 0),
	}
	for _, item := range []struct {
		name string
		ok   bool
	}{
		{name: "youtube_output_saved", ok: payload.Confirmations.YouTubeOutputSaved},
		{name: "drive_destination_saved", ok: payload.Confirmations.DriveDestinationSaved},
		{name: "discord_config_saved", ok: payload.Confirmations.DiscordConfigSaved},
		{name: "primary_assignments_saved", ok: payload.Confirmations.PrimaryAssignmentsSaved},
		{name: "runtime_config_distribution_enabled", ok: payload.Confirmations.RuntimeConfigDistributionEnabled},
	} {
		if !item.ok {
			out.MissingConfirmations = append(out.MissingConfirmations, item.name)
		}
	}
	for _, item := range []struct {
		name  string
		value string
	}{
		{name: "youtube_output_id", value: payload.RuntimeConfig.YouTubeOutputID},
		{name: "drive_destination_id", value: payload.RuntimeConfig.DriveDestinationID},
		{name: "discord_config_id", value: payload.RuntimeConfig.DiscordConfigID},
		{name: "encoder_profile_id", value: payload.RuntimeConfig.EncoderProfileID},
		{name: "archive_profile_id", value: payload.RuntimeConfig.ArchiveProfileID},
	} {
		if strings.TrimSpace(item.value) == "" {
			out.MissingRuntimeIDs = append(out.MissingRuntimeIDs, item.name)
		}
	}
	for _, serviceType := range missingServiceTypes(primaryAssignments, requiredStartServiceTypes) {
		out.MissingPrimaryServices = append(out.MissingPrimaryServices, serviceType)
	}
	for _, service := range primaryAssignments {
		if !serviceCapabilityEnabled(service, "runtime_config") {
			out.MissingRuntimeConfigCapabilities = append(out.MissingRuntimeConfigCapabilities, service.ServiceType)
		}
	}
	out.Ready = len(out.MissingConfirmations) == 0 &&
		len(out.MissingRuntimeIDs) == 0 &&
		len(out.MissingPrimaryServices) == 0 &&
		len(out.MissingRuntimeConfigCapabilities) == 0
	return out
}

func (s *Server) profileExists(ctx context.Context, kind store.ProfileKind, id string) (bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, nil
	}
	_, err := s.profiles.GetProfile(ctx, kind, id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func externalE2EServiceAssignmentsFromServices(assignments []store.RegisteredService) externalE2EServiceAssignments {
	var out externalE2EServiceAssignments
	for _, service := range assignments {
		role := normalizeAssignmentRole(service.AssignmentRole)
		switch service.ServiceType {
		case "discord_bot":
			if role == "primary" && out.DiscordBotServiceID == "" {
				out.DiscordBotServiceID = service.ServiceID
			}
		case "encoder_recorder":
			if role == "primary" && out.EncoderRecorderPrimaryServiceID == "" {
				out.EncoderRecorderPrimaryServiceID = service.ServiceID
			}
			if role == "standby" && out.EncoderRecorderStandbyServiceID == "" {
				out.EncoderRecorderStandbyServiceID = service.ServiceID
			}
		case "worker":
			if role == "primary" && out.WorkerPrimaryServiceID == "" {
				out.WorkerPrimaryServiceID = service.ServiceID
			}
			if role == "standby" && out.WorkerStandbyServiceID == "" {
				out.WorkerStandbyServiceID = service.ServiceID
			}
		}
	}
	return out
}

func allServicesHaveCapability(services []store.RegisteredService, capability string) bool {
	if len(services) == 0 {
		return false
	}
	for _, service := range services {
		if !serviceCapabilityEnabled(service, capability) {
			return false
		}
	}
	return true
}

func serviceCapabilityEnabled(service store.RegisteredService, capability string) bool {
	value, ok := service.Capabilities[capability]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func (s *Server) updateStreamSettings(w http.ResponseWriter, r *http.Request) {
	var body streamSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	settings, code := streamSettingsFromRequest(body)
	if code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": code})
		return
	}
	if err := s.validateStreamSettingsReferences(r.Context(), settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": streamSettingsReferenceCode(err)})
		return
	}
	current := currentFromContext(r.Context())
	if code, status := s.validateStreamServiceAssignmentRequest(r.Context(), body, current.Permissions); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	streamID := r.PathValue("id")
	existing, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	if streamArchiveDirectRequested(body) {
		settings, err = s.materializeStreamArchiveSettings(r.Context(), existing, settings, body)
		if err != nil {
			writeJSON(w, streamArchiveSettingsStatus(err), map[string]string{"code": streamArchiveSettingsCode(err)})
			return
		}
	}
	stream, err := s.streams.UpdateStreamSettings(r.Context(), streamID, settings)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_settings_failed"})
		return
	}
	if code, status := s.applyStreamServiceAssignments(r, stream.ID, body, current); code != "" {
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.update_settings", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: streamSettingsAuditMetadata(stream)})
	writeJSON(w, http.StatusOK, stream)
}

func streamSettingsFromRequest(body streamSettingsRequest) (store.StreamSettings, string) {
	scheduledStart, code := parseStreamScheduleTime(body.ScheduledStartAt)
	if code != "" {
		return store.StreamSettings{}, code
	}
	scheduledEnd, code := parseStreamScheduleTime(body.ScheduledEndAt)
	if code != "" {
		return store.StreamSettings{}, code
	}
	if scheduledStart != nil && scheduledEnd != nil && !scheduledEnd.After(*scheduledStart) {
		return store.StreamSettings{}, "schedule_end_before_start"
	}
	return store.StreamSettings{
		ScheduledStartAt:      scheduledStart,
		ScheduledEndAt:        scheduledEnd,
		DiscordConfigID:       strings.TrimSpace(body.DiscordConfigID),
		DiscordGuildID:        strings.TrimSpace(body.DiscordGuildID),
		DiscordVoiceID:        strings.TrimSpace(body.DiscordVoiceID),
		DiscordTextID:         strings.TrimSpace(body.DiscordTextID),
		AutoStartTrigger:      normalizeAutoStartTrigger(body.AutoStartTrigger),
		EncoderProfileID:      strings.TrimSpace(body.EncoderProfileID),
		CaptionProfileID:      strings.TrimSpace(body.CaptionProfileID),
		OverlayProfileID:      strings.TrimSpace(body.OverlayProfileID),
		ArchiveProfileID:      strings.TrimSpace(body.ArchiveProfileID),
		ArchiveOAuthAccountID: strings.TrimSpace(body.ArchiveOAuthAccountID),
		ArchiveSharedDrive:    body.ArchiveSharedDrive,
		ArchiveSharedDriveID:  strings.TrimSpace(body.ArchiveSharedDriveID),
		ArchiveFileName:       archiveSafeFileName(body.ArchiveFileName),
		YouTubeOutputID:       strings.TrimSpace(body.YouTubeOutputID),
		EncoderInputURL:       strings.TrimSpace(body.EncoderInputURL),
	}, ""
}

func parseStreamScheduleTime(value string) (*time.Time, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, ""
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, "schedule_time_invalid"
	}
	utc := parsed.UTC()
	return &utc, ""
}

func streamSettingsConfigured(settings store.StreamSettings) bool {
	return settings.ScheduledStartAt != nil || settings.ScheduledEndAt != nil || settings.DiscordConfigID != "" || settings.DiscordGuildID != "" || settings.DiscordVoiceID != "" || settings.DiscordTextID != "" || settings.AutoStartTrigger != "" || settings.EncoderProfileID != "" || settings.CaptionProfileID != "" || settings.OverlayProfileID != "" || settings.ArchiveProfileID != "" || settings.ArchiveDriveDestinationID != "" || settings.ArchiveOAuthAccountID != "" || settings.ArchiveSharedDrive || settings.ArchiveSharedDriveID != "" || settings.ArchiveFileName != "" || settings.YouTubeOutputID != "" || settings.EncoderInputURL != ""
}

func normalizeAutoStartTrigger(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func streamArchiveDirectRequested(body streamSettingsRequest) bool {
	return strings.TrimSpace(body.ArchiveOAuthAccountID) != "" ||
		strings.TrimSpace(body.ArchiveFolderID) != "" ||
		body.ArchiveSharedDrive ||
		strings.TrimSpace(body.ArchiveSharedDriveID) != "" ||
		strings.TrimSpace(body.ArchiveFileName) != "" ||
		body.ArchiveRetentionDays > 0
}

func (s *Server) materializeStreamArchiveSettings(ctx context.Context, stream store.Stream, settings store.StreamSettings, body streamSettingsRequest) (store.StreamSettings, error) {
	if s.profiles == nil {
		return settings, errArchiveSettingsStoreUnavailable
	}
	oauthAccountID := strings.TrimSpace(body.ArchiveOAuthAccountID)
	folderID := strings.TrimSpace(body.ArchiveFolderID)
	destinationID := strings.TrimSpace(stream.ArchiveDriveDestinationID)
	sharedDriveID := strings.TrimSpace(body.ArchiveSharedDriveID)
	retentionDays := normalizeArchiveRetentionDays(body.ArchiveRetentionDays)
	driveRequested := oauthAccountID != "" || folderID != "" || body.ArchiveSharedDrive || sharedDriveID != "" || strings.TrimSpace(body.ArchiveFileName) != ""
	fileName := archiveSafeFileName(body.ArchiveFileName)
	if driveRequested {
		if s.integrations == nil {
			return settings, errArchiveSettingsStoreUnavailable
		}
		if oauthAccountID == "" {
			return settings, errArchiveOAuthAccountRequired
		}
		if destinationID == "" && folderID == "" {
			return settings, errArchiveFolderIDRequired
		}
		if body.ArchiveSharedDrive && sharedDriveID == "" {
			return settings, errArchiveSharedDriveIDRequired
		}
		if err := s.validateDriveOAuthReadiness(ctx, store.DriveDestination{AuthMode: "oauth2", OAuthAccountID: oauthAccountID}); err != nil {
			return settings, err
		}
		destination, err := s.upsertStreamArchiveDriveDestination(ctx, stream, destinationID, oauthAccountID, folderID, body.ArchiveSharedDrive)
		if err != nil {
			return settings, err
		}
		if fileName == "" {
			fileName = defaultArchiveFileName(stream.Name, time.Now())
		}
		destinationID = destination.ID
		settings.ArchiveDriveDestinationID = destination.ID
		settings.ArchiveOAuthAccountID = oauthAccountID
		settings.ArchiveSharedDrive = body.ArchiveSharedDrive
		settings.ArchiveSharedDriveID = sharedDriveID
		settings.ArchiveFileName = fileName
	} else {
		destinationID = ""
		fileName = ""
	}
	profileID, err := s.upsertStreamArchiveProfile(ctx, stream, destinationID, fileName, body.ArchiveSharedDrive && driveRequested, sharedDriveID, retentionDays)
	if err != nil {
		return settings, err
	}
	settings.ArchiveProfileID = profileID
	return settings, nil
}

func (s *Server) upsertStreamArchiveDriveDestination(ctx context.Context, stream store.Stream, destinationID, oauthAccountID, folderID string, sharedDrive bool) (store.DriveDestination, error) {
	destination := store.DriveDestination{
		ID:             strings.TrimSpace(destinationID),
		Name:           streamArchiveDestinationName(stream),
		AuthMode:       "oauth2",
		OAuthAccountID: oauthAccountID,
		FolderID:       folderID,
		SharedDrive:    sharedDrive,
		BasePath:       "AutoStream",
	}
	if destination.ID != "" {
		updated, err := s.integrations.UpdateDriveDestination(ctx, destination)
		if errors.Is(err, store.ErrNotFound) {
			destination.ID = ""
		} else {
			return updated, err
		}
	}
	return s.integrations.CreateDriveDestination(ctx, destination)
}

func (s *Server) upsertStreamArchiveProfile(ctx context.Context, stream store.Stream, destinationID, fileName string, sharedDrive bool, sharedDriveID string, retentionDays int) (string, error) {
	config := map[string]any{
		"stream_archive_direct": true,
		"retention_days":        normalizeArchiveRetentionDays(retentionDays),
	}
	if strings.TrimSpace(destinationID) != "" {
		config["drive_destination_id"] = strings.TrimSpace(destinationID)
	}
	if safeFileName := archiveSafeFileName(fileName); safeFileName != "" {
		config["archive_file_name"] = safeFileName
	}
	if sharedDrive {
		config["shared_drive"] = true
	}
	if sharedDriveID != "" {
		config["shared_drive_id"] = sharedDriveID
	}
	name := streamArchiveProfileName(stream)
	if profileID, ok := s.directArchiveProfileID(ctx, stream); ok {
		profile, err := s.profiles.UpdateProfile(ctx, store.ProfileArchive, profileID, name, config)
		if err != nil {
			return "", err
		}
		return profile.ID, nil
	}
	profile, err := s.profiles.CreateProfile(ctx, store.ProfileArchive, name, config)
	if err != nil {
		fallbackName := name + "-" + strconv.FormatInt(time.Now().Unix(), 10)
		profile, err = s.profiles.CreateProfile(ctx, store.ProfileArchive, fallbackName, config)
	}
	if err != nil {
		return "", err
	}
	return profile.ID, nil
}

func (s *Server) directArchiveProfileID(ctx context.Context, stream store.Stream) (string, bool) {
	profileID := strings.TrimSpace(stream.ArchiveProfileID)
	if profileID == "" {
		return "", false
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileArchive, profileID)
	if err != nil {
		return "", false
	}
	return profile.ID, configBool(profile.Config, "stream_archive_direct")
}

func streamArchiveDestinationName(stream store.Stream) string {
	return strings.TrimSpace(stream.Name) + " archive drive " + shortID(stream.ID)
}

func streamArchiveProfileName(stream store.Stream) string {
	return strings.TrimSpace(stream.Name) + " archive " + shortID(stream.ID)
}

func normalizeArchiveRetentionDays(value int) int {
	if value <= 0 {
		return defaultArchiveRetentionDays
	}
	if value > maxArchiveRetentionDays {
		return maxArchiveRetentionDays
	}
	return value
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "stream"
	}
	return id
}

func defaultArchiveFileName(streamName string, now time.Time) string {
	base := archiveSafeFileName(streamName)
	if base == "" {
		base = "archive"
	}
	if strings.HasSuffix(strings.ToLower(base), ".mp4") {
		base = base[:len(base)-4]
	}
	return archiveSafeFileName(base + "-" + now.Format("20060102") + ".mp4")
}

func archiveSafeFileName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "\\", "_")
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, value)
	value = strings.Trim(value, " .")
	if value == "" {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(value), ".mp4") {
		value += ".mp4"
	}
	return value
}

func streamArchiveSettingsCode(err error) string {
	switch {
	case errors.Is(err, errArchiveOAuthAccountRequired):
		return "archive_oauth_account_required"
	case errors.Is(err, errArchiveFolderIDRequired):
		return "archive_folder_id_required"
	case errors.Is(err, errArchiveSharedDriveIDRequired):
		return "archive_shared_drive_id_required"
	case errors.Is(err, errDriveOAuthAccountUnavailable):
		return "drive_oauth_account_unavailable"
	case errors.Is(err, store.ErrSecretKeyRequired):
		return "secret_encryption_key_required"
	case errors.Is(err, errArchiveSettingsStoreUnavailable):
		return "archive_settings_store_unavailable"
	default:
		return "archive_settings_failed"
	}
}

func streamArchiveSettingsStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrSecretKeyRequired), errors.Is(err, errArchiveSettingsStoreUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, errDriveOAuthAccountUnavailable):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func streamSettingsAuditMetadata(stream store.Stream) map[string]any {
	return map[string]any{
		"name":                               stream.Name,
		"scheduled_start_at":                 stream.ScheduledStartAt,
		"scheduled_end_at":                   stream.ScheduledEndAt,
		"discord_config_id":                  stream.DiscordConfigID,
		"discord_guild_id":                   stream.DiscordGuildID,
		"discord_voice_channel_id":           stream.DiscordVoiceID,
		"discord_text_channel_configured":    stream.DiscordTextID != "",
		"auto_start_trigger":                 stream.AutoStartTrigger,
		"youtube_output_id":                  stream.YouTubeOutputID,
		"archive_profile_id":                 stream.ArchiveProfileID,
		"archive_drive_destination_id":       stream.ArchiveDriveDestinationID,
		"archive_oauth_account_id":           stream.ArchiveOAuthAccountID,
		"archive_folder_id_configured":       stream.ArchiveFolderIDConfigured,
		"archive_shared_drive":               stream.ArchiveSharedDrive,
		"archive_shared_drive_id_configured": stream.ArchiveSharedDriveID != "",
		"archive_file_name":                  stream.ArchiveFileName,
	}
}

func (s *Server) validateStreamSettingsReferences(ctx context.Context, settings store.StreamSettings) error {
	discordConfigID := strings.TrimSpace(settings.DiscordConfigID)
	hasDiscordOverride := strings.TrimSpace(settings.DiscordGuildID) != "" || strings.TrimSpace(settings.DiscordVoiceID) != "" || strings.TrimSpace(settings.DiscordTextID) != ""
	switch strings.TrimSpace(settings.AutoStartTrigger) {
	case "":
	case autoStartTriggerDiscordVoiceJoin:
		if discordConfigID == "" || strings.TrimSpace(settings.DiscordGuildID) == "" || strings.TrimSpace(settings.DiscordVoiceID) == "" {
			return errAutoStartDiscordRequired
		}
	default:
		return errAutoStartTriggerInvalid
	}
	if hasDiscordOverride && discordConfigID == "" {
		return errDiscordConfigRequired
	}
	if discordConfigID != "" {
		if _, err := s.profiles.GetProfile(ctx, store.ProfileDiscordConfig, settings.DiscordConfigID); errors.Is(err, store.ErrNotFound) {
			return errDiscordConfigNotFound
		} else if err != nil {
			return err
		}
	}
	if err := s.validateProfileReference(ctx, settings.YouTubeOutputID, store.ProfileYouTubeOutput, errYouTubeOutputNotFound); err != nil {
		return err
	}
	if err := s.validateProfileReference(ctx, settings.EncoderProfileID, store.ProfileEncoder, errEncoderProfileNotFound); err != nil {
		return err
	}
	if err := s.validateProfileReference(ctx, settings.CaptionProfileID, store.ProfileCaption, errCaptionProfileNotFound); err != nil {
		return err
	}
	if err := s.validateProfileReference(ctx, settings.OverlayProfileID, store.ProfileOverlay, errOverlayProfileNotFound); err != nil {
		return err
	}
	if err := s.validateProfileReference(ctx, settings.ArchiveProfileID, store.ProfileArchive, errArchiveProfileNotFound); err != nil {
		return err
	}
	if strings.TrimSpace(settings.ArchiveOAuthAccountID) != "" {
		account, err := s.integrations.GetOAuthAccount(ctx, settings.ArchiveOAuthAccountID)
		if errors.Is(err, store.ErrNotFound) {
			return errDriveOAuthAccountUnavailable
		}
		if err != nil {
			return err
		}
		if !strings.EqualFold(account.ProviderType, "google") || !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive) {
			return errDriveOAuthAccountUnavailable
		}
	}
	if err := validateEncoderInputURL(settings.EncoderInputURL); err != nil {
		return err
	}
	return nil
}

func (s *Server) validateProfileReference(ctx context.Context, id string, kind store.ProfileKind, notFound error) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if _, err := s.profiles.GetProfile(ctx, kind, id); errors.Is(err, store.ErrNotFound) {
		return notFound
	} else if err != nil {
		return err
	}
	return nil
}

func (s *Server) validateStreamServiceAssignmentRequest(ctx context.Context, body streamSettingsRequest, permissions []string) (string, int) {
	for _, item := range []struct {
		serviceID   string
		serviceType string
		permission  string
		notFound    string
		wrongType   string
	}{
		{serviceID: strings.TrimSpace(body.EncoderServiceID), serviceType: "encoder_recorder", permission: "services.assign", notFound: "encoder_service_not_found", wrongType: "encoder_service_type_invalid"},
		{serviceID: strings.TrimSpace(body.WorkerServiceID), serviceType: "worker", permission: "workers.assign", notFound: "worker_service_not_found", wrongType: "worker_service_type_invalid"},
	} {
		if item.serviceID == "" {
			continue
		}
		if !security.HasPermission(permissions, item.permission) {
			return "permission_denied", http.StatusForbidden
		}
		_, code, status := s.assignableStreamService(ctx, item.serviceID, item.serviceType, item.notFound, item.wrongType)
		if code != "" {
			return code, status
		}
	}
	return "", 0
}

func (s *Server) assignableStreamService(ctx context.Context, serviceID, serviceType, notFoundCode, wrongTypeCode string) (store.RegisteredService, string, int) {
	if s.services == nil {
		return store.RegisteredService{}, "service_registry_not_configured", http.StatusServiceUnavailable
	}
	service, err := s.services.GetService(ctx, serviceID)
	if errors.Is(err, store.ErrNotFound) {
		return store.RegisteredService{}, notFoundCode, http.StatusBadRequest
	}
	if err != nil {
		return store.RegisteredService{}, "get_service_failed", http.StatusInternalServerError
	}
	if strings.TrimSpace(service.ServiceType) != serviceType {
		return store.RegisteredService{}, wrongTypeCode, http.StatusBadRequest
	}
	return service, "", 0
}

func (s *Server) applyStreamServiceAssignments(r *http.Request, streamID string, body streamSettingsRequest, current currentUser) (string, int) {
	for _, item := range []struct {
		serviceID   string
		serviceType string
	}{
		{serviceID: strings.TrimSpace(body.EncoderServiceID), serviceType: "encoder_recorder"},
		{serviceID: strings.TrimSpace(body.WorkerServiceID), serviceType: "worker"},
	} {
		if item.serviceID == "" {
			continue
		}
		service, err := s.services.AssignServiceToStreamWithRole(r.Context(), item.serviceID, streamID, current.User.ID, "primary")
		if errors.Is(err, store.ErrNotFound) {
			return "service_not_found", http.StatusBadRequest
		}
		if err != nil {
			return "assign_service_failed", http.StatusInternalServerError
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "services.assign", ResourceType: "service", ResourceID: service.ServiceID, Result: "success", Metadata: map[string]any{"stream_id": streamID, "service_type": item.serviceType, "assignment_role": "primary", "source": "stream_settings"}})
	}
	return "", 0
}

func streamSettingsReferenceCode(err error) string {
	switch {
	case errors.Is(err, errAutoStartTriggerInvalid):
		return "auto_start_trigger_invalid"
	case errors.Is(err, errAutoStartDiscordRequired):
		return "auto_start_discord_required"
	case errors.Is(err, errDiscordConfigRequired):
		return "discord_config_required"
	case errors.Is(err, errDiscordConfigNotFound):
		return "discord_config_not_found"
	case errors.Is(err, errYouTubeOutputNotFound):
		return "youtube_output_not_found"
	case errors.Is(err, errEncoderProfileNotFound):
		return "encoder_profile_not_found"
	case errors.Is(err, errCaptionProfileNotFound):
		return "caption_profile_not_found"
	case errors.Is(err, errOverlayProfileNotFound):
		return "overlay_profile_not_found"
	case errors.Is(err, errArchiveProfileNotFound):
		return "archive_profile_not_found"
	case errors.Is(err, errDriveOAuthAccountUnavailable):
		return "drive_oauth_account_unavailable"
	case errors.Is(err, errEncoderInputURLBlocked):
		return "encoder_input_url_blocked"
	default:
		return "invalid_stream_settings_reference"
	}
}

func validateEncoderInputURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errEncoderInputURLBlocked
	}
	switch strings.ToLower(parsed.Scheme) {
	case "srt", "rtmp", "rtmps", "http", "https":
	default:
		return errEncoderInputURLBlocked
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(parsed.Hostname()), "."))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errEncoderInputURLBlocked
	}
	if ip := net.ParseIP(host); ip != nil && unsafeEncoderInputIP(ip) {
		return errEncoderInputURLBlocked
	}
	return nil
}

func unsafeEncoderInputIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func applyStreamSettingsDefaults(stream store.Stream, req *servicecall.StartRequest) {
	if strings.TrimSpace(req.DiscordConfigID) == "" {
		req.DiscordConfigID = stream.DiscordConfigID
	}
	if strings.TrimSpace(req.DiscordGuildID) == "" {
		req.DiscordGuildID = stream.DiscordGuildID
	}
	if strings.TrimSpace(req.DiscordVoiceChannelID) == "" {
		req.DiscordVoiceChannelID = stream.DiscordVoiceID
	}
	if strings.TrimSpace(req.DiscordTextChannelID) == "" {
		req.DiscordTextChannelID = stream.DiscordTextID
	}
	if strings.TrimSpace(req.EncoderProfileID) == "" {
		req.EncoderProfileID = stream.EncoderProfileID
	}
	if strings.TrimSpace(req.CaptionProfileID) == "" {
		req.CaptionProfileID = stream.CaptionProfileID
	}
	if strings.TrimSpace(req.OverlayProfileID) == "" {
		req.OverlayProfileID = stream.OverlayProfileID
	}
	if strings.TrimSpace(req.ArchiveProfileID) == "" {
		req.ArchiveProfileID = stream.ArchiveProfileID
	}
	if strings.TrimSpace(req.YouTubeOutputID) == "" {
		req.YouTubeOutputID = stream.YouTubeOutputID
	}
	if strings.TrimSpace(req.EncoderInputURL) == "" {
		req.EncoderInputURL = stream.EncoderInputURL
	}
}

func (s *Server) serviceStartStream(w http.ResponseWriter, r *http.Request) {
	token, ok := s.authenticateService(w, r, "streams.start")
	if !ok {
		return
	}
	if token.ServiceType != "discord_bot" {
		s.writeServiceAudit(r, token, "streams.start", "stream", r.PathValue("id"), "failure", map[string]any{"reason": "service_type_not_allowed"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_type_not_allowed"})
		return
	}
	streamID := strings.TrimSpace(r.PathValue("id"))
	stream, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	service, assigned, err := s.serviceTokenPrimaryAssignedToStream(r.Context(), token, stream.ID, "discord_bot")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	assignBeforeStart := false
	if !assigned {
		configuredService, configured, err := s.discordServiceTokenConfiguredForStream(r.Context(), token, stream)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
			return
		}
		if configured {
			service = configuredService
			assigned = true
			assignBeforeStart = true
		}
	}
	if !assigned {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "failure", map[string]any{"reason": "service_not_primary_assignment"})
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_primary_assignment"})
		return
	}
	if isActiveStreamStatus(stream.Status) {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "success", map[string]any{"service_id": service.ServiceID, "skipped": true, "reason": "stream_already_active"})
		writeJSON(w, http.StatusOK, map[string]any{"stream": stream, "already_active": true})
		return
	}
	if strings.TrimSpace(stream.AutoStartTrigger) != autoStartTriggerDiscordVoiceJoin {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "stream_auto_start_not_enabled"})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_auto_start_not_enabled"})
		return
	}
	if !isAutoStartableStreamStatus(stream.Status) {
		s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "stream_not_waiting", "status": stream.Status})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_not_waiting"})
		return
	}
	if assignBeforeStart {
		assignedService, err := s.services.AssignServiceToStreamWithRole(r.Context(), service.ServiceID, stream.ID, "service:"+service.ServiceID, "primary")
		if errors.Is(err, store.ErrNotFound) {
			s.writeServiceAudit(r, token, "streams.start", "stream", stream.ID, "failure", map[string]any{"service_id": service.ServiceID, "reason": "service_not_registered"})
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "service_not_primary_assignment"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "assign_stream_service_failed"})
			return
		}
		service = assignedService
	}
	r.Body = http.NoBody
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, serviceCurrentUser(service)))
	s.startStream(w, r)
}

func (s *Server) discordServiceTokenConfiguredForStream(ctx context.Context, token store.ServiceToken, stream store.Stream) (store.RegisteredService, bool, error) {
	if s.services == nil || s.profiles == nil {
		return store.RegisteredService{}, false, nil
	}
	service, registered, err := s.registeredServiceForToken(ctx, token)
	if err != nil || !registered {
		return store.RegisteredService{}, false, err
	}
	if service.ServiceType != "discord_bot" {
		return store.RegisteredService{}, false, nil
	}
	matches, err := s.streamDiscordConfigMatchesService(ctx, stream, service.ServiceID)
	if err != nil || !matches {
		return store.RegisteredService{}, false, err
	}
	assignments, err := s.services.ListStreamAssignments(ctx, stream.ID)
	if err != nil {
		return store.RegisteredService{}, false, err
	}
	for _, assignedService := range assignments {
		if strings.TrimSpace(assignedService.ServiceType) == "discord_bot" && normalizeAssignmentRole(assignedService.AssignmentRole) == "primary" && strings.TrimSpace(assignedService.ServiceID) != service.ServiceID {
			return store.RegisteredService{}, false, nil
		}
	}
	service.AssignmentRole = "primary"
	return service, true, nil
}

func (s *Server) streamDiscordConfigMatchesService(ctx context.Context, stream store.Stream, serviceID string) (bool, error) {
	if s.profiles == nil || strings.TrimSpace(stream.DiscordConfigID) == "" || strings.TrimSpace(serviceID) == "" {
		return false, nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileDiscordConfig, stream.DiscordConfigID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return runtimeProfileMatchesService(profile.Config, serviceID), nil
}

func (s *Server) serviceTokenPrimaryAssignedToStream(ctx context.Context, token store.ServiceToken, streamID, serviceType string) (store.RegisteredService, bool, error) {
	if s.services == nil {
		return store.RegisteredService{}, false, nil
	}
	assignments, err := s.services.ListStreamAssignments(ctx, streamID)
	if err != nil {
		return store.RegisteredService{}, false, err
	}
	for _, service := range assignments {
		if strings.TrimSpace(service.TokenID) == token.ID &&
			strings.TrimSpace(service.ServiceType) == serviceType &&
			strings.TrimSpace(service.AssignmentRole) == "primary" {
			return service, true, nil
		}
	}
	return store.RegisteredService{}, false, nil
}

func serviceCurrentUser(service store.RegisteredService) currentUser {
	serviceID := strings.TrimSpace(service.ServiceID)
	if serviceID == "" {
		serviceID = strings.TrimSpace(service.ServiceType)
	}
	return currentUser{User: store.User{ID: "service:" + serviceID, Username: serviceID}}
}

func isActiveStreamStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "live", "stopping":
		return true
	default:
		return false
	}
}

func isAutoStartableStreamStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created", "draft", "scheduled", "ready":
		return true
	default:
		return false
	}
}

func isManuallyStartableStreamStatus(status string) bool {
	return isAutoStartableStreamStatus(status) || strings.EqualFold(strings.TrimSpace(status), "failed")
}

func isManuallyStoppableStreamStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "live", "failed":
		return true
	default:
		return false
	}
}

func (s *Server) startStream(w http.ResponseWriter, r *http.Request) {
	var body servicecall.StartRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
	}
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	if !isManuallyStartableStreamStatus(stream.Status) {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "stream_status_not_startable", "current_status": stream.Status}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_status_not_startable", "status": stream.Status})
		return
	}
	applyStreamSettingsDefaults(stream, &body)
	if err := validateEncoderInputURL(body.EncoderInputURL); err != nil {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "encoder_input_url_blocked"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "encoder_input_url_blocked"})
		return
	}
	if err := s.validateYouTubeOutputReadiness(r.Context(), stream, &body); err != nil {
		current := currentFromContext(r.Context())
		code := youtubeOutputCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "youtube_output_id": body.YouTubeOutputID}})
		writeJSON(w, youtubeOutputStatus(err), map[string]string{"code": code})
		return
	}
	if err := s.validateArchiveConfigReadiness(r.Context(), &body); err != nil {
		current := currentFromContext(r.Context())
		code := archiveConfigCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "archive_profile_id": body.ArchiveProfileID}})
		writeJSON(w, archiveConfigStatus(err), map[string]string{"code": code})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredStartServiceTypes); len(missing) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"missing_service_types": missing}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	if err := s.applyDiscordConfig(r.Context(), primaryAssignments, &body); err != nil {
		current := currentFromContext(r.Context())
		code := discordConfigCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "discord_config_id": body.DiscordConfigID}})
		writeJSON(w, discordConfigStatus(err), map[string]string{"code": code})
		return
	}
	if checker, ok := s.dispatcher.(startReadinessChecker); ok {
		if issues := checker.StartReadinessIssues(primaryAssignments, body, time.Now().UTC()); len(issues) > 0 {
			current := currentFromContext(r.Context())
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"readiness_issues": issues}})
			writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_start_not_ready", "issues": issues})
			return
		}
	}
	if err := s.applyArchiveConfig(r.Context(), &body); err != nil {
		current := currentFromContext(r.Context())
		code := archiveConfigCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "archive_profile_id": body.ArchiveProfileID}})
		writeJSON(w, archiveConfigStatus(err), map[string]string{"code": code})
		return
	}
	if err := s.applyYouTubeOutput(r.Context(), stream, &body); err != nil {
		current := currentFromContext(r.Context())
		code := youtubeOutputCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "youtube_output_id": body.YouTubeOutputID}})
		writeJSON(w, youtubeOutputStatus(err), map[string]string{"code": code})
		return
	}
	if err := s.saveYouTubeRuntime(r.Context(), stream.ID, body.YouTubeRuntime); err != nil {
		s.clearYouTubeRuntimeSecretFromMap(r.Context(), body.YouTubeRuntime)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "save_youtube_runtime_failed"})
		return
	}
	if _, err := s.streams.UpdateStreamStatus(r.Context(), stream.ID, "starting"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	results := s.dispatcher.Start(r.Context(), stream, primaryAssignments, body)
	results = sanitizeDispatchResults(results)
	if hasDispatchFailure(results) {
		if metadata, err := s.completeYouTubeRuntime(r.Context(), stream.ID, true); err != nil {
			code := "complete_youtube_runtime_failed"
			if errors.Is(err, errYouTubeLiveAPICompleteFailed) {
				code = errYouTubeLiveAPICompleteFailed.Error()
			}
			current := currentFromContext(r.Context())
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "trigger": "start_dispatch_failed"}})
		} else if len(metadata) > 0 {
			current := currentFromContext(r.Context())
			metadata["trigger"] = "start_dispatch_failed"
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
		}
		failed, _ := s.streams.UpdateStreamStatus(r.Context(), stream.ID, "failed")
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"dispatch": results}})
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "stream": failed, "dispatch": results})
		return
	}
	s.completeStreamStart(w, r, stream, primaryAssignments, body, results)
}

func (s *Server) completeStreamStart(w http.ResponseWriter, r *http.Request, stream store.Stream, assignments []store.RegisteredService, req servicecall.StartRequest, dispatch []servicecall.DispatchResult) {
	dispatch = sanitizeDispatchResults(dispatch)
	liveStream, err := s.streams.UpdateStreamStatus(r.Context(), stream.ID, "live")
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}

	var notification *servicecall.DispatchResult
	watchURL, watchURLOK := normalizeYouTubeWatchURL(mapString(req.YouTubeRuntime, "watch_url"))
	if watchURLOK && !mapBool(req.YouTubeRuntime, "dry_run") && strings.TrimSpace(req.DiscordTextChannelID) != "" {
		result := servicecall.DispatchResult{ServiceType: "discord_bot", Code: "discord_youtube_notification_not_supported", Error: "discord youtube notification is not supported"}
		if notifier, ok := s.dispatcher.(discordLiveNotificationDispatcher); ok {
			eventID := "youtube-live-" + security.SecretFingerprint(stream.ID+":"+watchURL)
			result = notifier.NotifyDiscordYouTubeLive(r.Context(), liveStream, assignments, eventID, watchURL)
		}
		result = sanitizeDispatchResults([]servicecall.DispatchResult{result})[0]
		notification = &result
		current := currentFromContext(r.Context())
		resultName := "failure"
		if result.Success {
			resultName = "success"
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.discord_youtube_notify", ResourceType: "stream", ResourceID: stream.ID, Result: resultName, Metadata: map[string]any{"service_id": result.ServiceID, "status_code": result.StatusCode, "code": result.Code, "watch_url_fingerprint": security.SecretFingerprint(watchURL)}})
	}

	current := currentFromContext(r.Context())
	metadata := map[string]any{"status": "live", "dispatch": dispatch}
	response := map[string]any{"stream": liveStream, "dispatch": dispatch}
	if notification != nil {
		metadata["discord_notification"] = notification
		response["discord_notification"] = notification
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.start", ResourceType: "stream", ResourceID: liveStream.ID, Result: "success", Metadata: metadata})
	writeJSON(w, http.StatusOK, response)
}

var (
	errYouTubeOutputNotFound             = errors.New("youtube_output_not_found")
	errYouTubeOutputInvalidConfig        = errors.New("youtube_output_invalid_config")
	errYouTubeOutputStreamKeyUnavailable = errors.New("youtube_stream_key_unavailable")
	errYouTubeLiveAPIUnavailable         = errors.New("youtube_live_api_unavailable")
	errYouTubeOAuthAccountUnavailable    = errors.New("youtube_oauth_account_unavailable")
	errYouTubeLiveAPIPrepareFailed       = errors.New("youtube_live_api_prepare_failed")
	errYouTubeLiveAPICompleteFailed      = errors.New("youtube_live_api_complete_failed")
	errArchiveProfileNotFound            = errors.New("archive_profile_not_found")
	errArchiveProfileInvalidConfig       = errors.New("archive_profile_invalid_config")
	errDriveDestinationNotFound          = errors.New("drive_destination_not_found")
	errDriveDestinationUnavailable       = errors.New("drive_destination_unavailable")
	errDriveOAuthAccountUnavailable      = errors.New("drive_oauth_account_unavailable")
	errArchiveOAuthAccountRequired       = errors.New("archive_oauth_account_required")
	errArchiveFolderIDRequired           = errors.New("archive_folder_id_required")
	errArchiveSharedDriveIDRequired      = errors.New("archive_shared_drive_id_required")
	errArchiveSettingsStoreUnavailable   = errors.New("archive_settings_store_unavailable")
	errAutoStartTriggerInvalid           = errors.New("auto_start_trigger_invalid")
	errAutoStartDiscordRequired          = errors.New("auto_start_discord_required")
	errDiscordConfigRequired             = errors.New("discord_config_required")
	errDiscordConfigNotFound             = errors.New("discord_config_not_found")
	errDiscordConfigInvalid              = errors.New("discord_config_invalid")
	errDiscordConfigServiceMismatch      = errors.New("discord_config_service_mismatch")
	errEncoderProfileNotFound            = errors.New("encoder_profile_not_found")
	errCaptionProfileNotFound            = errors.New("caption_profile_not_found")
	errOverlayProfileNotFound            = errors.New("overlay_profile_not_found")
	errEncoderInputURLBlocked            = errors.New("encoder_input_url_blocked")
)

func (s *Server) applyDiscordConfig(ctx context.Context, assignments []store.RegisteredService, req *servicecall.StartRequest) error {
	configID := strings.TrimSpace(req.DiscordConfigID)
	if configID == "" {
		return errDiscordConfigRequired
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileDiscordConfig, configID)
	if errors.Is(err, store.ErrNotFound) {
		return errDiscordConfigNotFound
	}
	if err != nil {
		return err
	}
	serviceID := strings.TrimSpace(configString(profile.Config, "service_id"))
	if serviceID != "" {
		discordServiceID := primaryServiceID(assignments, "discord_bot")
		if discordServiceID == "" || discordServiceID != serviceID {
			return errDiscordConfigServiceMismatch
		}
	}
	guildID := strings.TrimSpace(req.DiscordGuildID)
	voiceChannelID := strings.TrimSpace(req.DiscordVoiceChannelID)
	textChannelID := strings.TrimSpace(req.DiscordTextChannelID)
	if guildID == "" || voiceChannelID == "" {
		return errDiscordConfigInvalid
	}
	req.DiscordGuildID = guildID
	req.DiscordVoiceChannelID = voiceChannelID
	req.DiscordTextChannelID = textChannelID
	return nil
}

func primaryServiceID(assignments []store.RegisteredService, serviceType string) string {
	for _, service := range assignments {
		if service.ServiceType == serviceType && service.AssignmentRole == "primary" {
			return strings.TrimSpace(service.ServiceID)
		}
	}
	return ""
}

func (s *Server) applyYouTubeOutput(ctx context.Context, stream store.Stream, req *servicecall.StartRequest) error {
	outputID := strings.TrimSpace(req.YouTubeOutputID)
	if outputID == "" {
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, outputID)
	if errors.Is(err, store.ErrNotFound) {
		return errYouTubeOutputNotFound
	}
	if err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(configString(profile.Config, "mode")))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(configString(profile.Config, "output_mode")))
	}
	if mode == "" {
		mode = "stream_key"
	}
	if value := strings.TrimSpace(configString(profile.Config, "rtmp_url")); value != "" {
		req.EncoderRTMPURL = value
	}
	switch mode {
	case "stream_key", "existing_stream_key", "rtmps_stream_key":
		return s.applyYouTubeStreamKeyOutput(ctx, profile, req)
	case "live_api_dry_run", "dry_run_live_api", "youtube_live_api_dry_run":
		return s.applyYouTubeLiveAPIDryRunOutput(ctx, stream, profile, req)
	case "live_api", "youtube_live_api":
		return s.applyYouTubeLiveAPIOutput(ctx, stream, profile, req)
	default:
		return errYouTubeOutputInvalidConfig
	}
}

func youtubeOutputRTMPURL(profile store.Profile) string {
	return strings.TrimSpace(configString(profile.Config, "rtmp_url"))
}

func (s *Server) youtubeOutputReadinessIssues(ctx context.Context, stream store.Stream, req *servicecall.StartRequest) []servicecall.ReadinessIssue {
	if err := s.validateYouTubeOutputReadiness(ctx, stream, req); err != nil {
		return []servicecall.ReadinessIssue{{
			ServiceType: "encoder_recorder",
			Code:        youtubeOutputCode(err),
			Message:     youtubeOutputReadinessMessage(err),
		}}
	}
	return nil
}

func (s *Server) validateYouTubeOutputReadiness(ctx context.Context, stream store.Stream, req *servicecall.StartRequest) error {
	outputID := strings.TrimSpace(req.YouTubeOutputID)
	if outputID == "" {
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileYouTubeOutput, outputID)
	if errors.Is(err, store.ErrNotFound) {
		return errYouTubeOutputNotFound
	}
	if err != nil {
		return err
	}
	mode := normalizedYouTubeOutputMode(firstNonEmpty(configString(profile.Config, "mode"), configString(profile.Config, "output_mode")))
	if mode == "" {
		return errYouTubeOutputInvalidConfig
	}
	if value := youtubeOutputRTMPURL(profile); value != "" {
		req.EncoderRTMPURL = value
	}
	switch mode {
	case "stream_key":
		return s.validateYouTubeStreamKeyReadiness(ctx, profile, req)
	case "live_api_dry_run":
		if strings.TrimSpace(req.EncoderRTMPURL) == "" {
			req.EncoderRTMPURL = "rtmps://a.rtmps.youtube.com/live2"
		}
		if !isSecureRTMPSURL(req.EncoderRTMPURL) {
			return errYouTubeOutputInvalidConfig
		}
		return nil
	case "live_api":
		return s.validateYouTubeLiveAPIReadiness(ctx, stream, profile)
	default:
		return errYouTubeOutputInvalidConfig
	}
}

func (s *Server) validateYouTubeStreamKeyReadiness(ctx context.Context, profile store.Profile, req *servicecall.StartRequest) error {
	rtmpURL := youtubeOutputRTMPURL(profile)
	if rtmpURL == "" {
		return errYouTubeOutputInvalidConfig
	}
	req.EncoderRTMPURL = rtmpURL
	if !isSecureRTMPSURL(req.EncoderRTMPURL) {
		return errYouTubeOutputInvalidConfig
	}
	secretName := firstNonEmpty(configString(profile.Config, "stream_key_secret_name"), configString(profile.Config, "streamKeySecretName"))
	if strings.TrimSpace(secretName) == "" {
		return errYouTubeOutputInvalidConfig
	}
	statuses, err := s.secrets.ListSecretStatus(ctx)
	if err != nil {
		return err
	}
	if status := secretStatusByName(statuses, secretName); !status.Configured {
		return errYouTubeOutputStreamKeyUnavailable
	}
	if strings.TrimSpace(req.DiscordTextChannelID) != "" {
		if _, ok := normalizeYouTubeWatchURL(configString(profile.Config, "watch_url")); !ok {
			return errYouTubeOutputInvalidConfig
		}
	}
	return nil
}

func (s *Server) validateYouTubeLiveAPIReadiness(ctx context.Context, stream store.Stream, profile store.Profile) error {
	_ = stream
	if s.youtubeLive == nil {
		return errYouTubeLiveAPIUnavailable
	}
	oauthAccountID := firstNonEmpty(configString(profile.Config, "oauth_account_id"), configString(profile.Config, "youtube_oauth_account_id"))
	if strings.TrimSpace(oauthAccountID) == "" {
		return errYouTubeOAuthAccountUnavailable
	}
	account, err := s.integrations.GetOAuthAccount(ctx, oauthAccountID)
	if errors.Is(err, store.ErrNotFound) || !account.RefreshTokenConfigured {
		return errYouTubeOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	provider, err := s.integrations.GetOAuthProvider(ctx, account.ProviderID)
	if errors.Is(err, store.ErrNotFound) || !provider.Enabled || strings.TrimSpace(provider.ClientID) == "" || !provider.ClientSecretConfigured {
		return errYouTubeOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(provider.ProviderType, "google") || !strings.EqualFold(account.ProviderType, "google") {
		return errYouTubeOAuthAccountUnavailable
	}
	if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeYouTube) {
		return errYouTubeOAuthAccountUnavailable
	}
	return nil
}

func youtubeOutputReadinessMessage(err error) string {
	switch {
	case errors.Is(err, errYouTubeOutputNotFound):
		return "selected YouTube output was not found."
	case errors.Is(err, errYouTubeOutputInvalidConfig):
		return "selected YouTube output is missing required RTMPS or mode settings."
	case errors.Is(err, errYouTubeOutputStreamKeyUnavailable):
		return "selected YouTube output stream key is not configured."
	case errors.Is(err, errYouTubeLiveAPIUnavailable):
		return "YouTube Live API client is not available on the Control Panel."
	case errors.Is(err, errYouTubeOAuthAccountUnavailable):
		return "selected YouTube OAuth connected account is not ready."
	default:
		return "selected YouTube output could not be validated."
	}
}

func (s *Server) applyYouTubeLiveAPIOutput(ctx context.Context, stream store.Stream, profile store.Profile, req *servicecall.StartRequest) error {
	if s.youtubeLive == nil {
		return errYouTubeLiveAPIUnavailable
	}
	oauthAccountID := strings.TrimSpace(configString(profile.Config, "oauth_account_id"))
	if oauthAccountID == "" {
		oauthAccountID = strings.TrimSpace(configString(profile.Config, "youtube_oauth_account_id"))
	}
	if oauthAccountID == "" {
		return errYouTubeOAuthAccountUnavailable
	}
	credentials, err := s.youtubeOAuthCredentials(ctx, oauthAccountID)
	if err != nil {
		return err
	}
	prepared, err := s.youtubeLive.Prepare(ctx, ytlive.PrepareRequest{
		Credentials:     credentials,
		StreamID:        stream.ID,
		StreamName:      stream.Name,
		OutputID:        profile.ID,
		Title:           defaultConfigString(profile.Config, "broadcast_title", stream.Name),
		Description:     configString(profile.Config, "broadcast_description"),
		PrivacyStatus:   defaultConfigString(profile.Config, "privacy_status", "private"),
		ScheduledStart:  configTime(profile.Config, "scheduled_start_at"),
		Resolution:      defaultConfigString(profile.Config, "resolution", "1080p"),
		FrameRate:       defaultConfigString(profile.Config, "frame_rate", "60fps"),
		EnableAutoStart: configBool(profile.Config, "enable_auto_start"),
		EnableAutoStop:  configBool(profile.Config, "enable_auto_stop"),
	})
	if err != nil {
		return errYouTubeLiveAPIPrepareFailed
	}
	if !isSecureRTMPSURL(prepared.RTMPURL) || strings.TrimSpace(prepared.StreamKey) == "" || youtubeWatchURLForBroadcastID(prepared.BroadcastID) == "" {
		return errYouTubeLiveAPIPrepareFailed
	}
	streamKeySecretName := youtubeLiveAPIStreamKeySecretName(stream.ID, profile.ID, prepared.BroadcastID)
	if _, err := s.secrets.UpdateSecret(ctx, streamKeySecretName, prepared.StreamKey); err != nil {
		return errYouTubeLiveAPIPrepareFailed
	}
	req.EncoderRTMPURL = prepared.RTMPURL
	req.EncoderStreamKeySecretName = streamKeySecretName
	req.YouTubeRuntime = map[string]any{
		"mode":                   "live_api",
		"output_id":              profile.ID,
		"oauth_account_id":       oauthAccountID,
		"broadcast_id":           prepared.BroadcastID,
		"watch_url":              youtubeWatchURLForBroadcastID(prepared.BroadcastID),
		"live_stream_id":         prepared.LiveStreamID,
		"rtmp_url":               prepared.RTMPURL,
		"stream_key_secret_name": streamKeySecretName,
		"dry_run":                false,
		"complete_on_stop":       youtubeCompleteOnStop(profile.Config),
	}
	return nil
}

func (s *Server) youtubeOAuthCredentials(ctx context.Context, oauthAccountID string) (ytlive.OAuthCredentials, error) {
	account, err := s.integrations.GetOAuthAccountForDispatch(ctx, oauthAccountID)
	if errors.Is(err, store.ErrNotFound) || strings.TrimSpace(account.RefreshToken) == "" {
		return ytlive.OAuthCredentials{}, errYouTubeOAuthAccountUnavailable
	}
	if err != nil {
		return ytlive.OAuthCredentials{}, err
	}
	if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeYouTube) {
		return ytlive.OAuthCredentials{}, errYouTubeOAuthAccountUnavailable
	}
	provider, err := s.integrations.GetOAuthProviderForDispatch(ctx, account.ProviderID)
	if errors.Is(err, store.ErrNotFound) || strings.TrimSpace(provider.ClientSecret) == "" || strings.TrimSpace(provider.ClientID) == "" || !provider.Enabled {
		return ytlive.OAuthCredentials{}, errYouTubeOAuthAccountUnavailable
	}
	if err != nil {
		return ytlive.OAuthCredentials{}, err
	}
	if provider.ProviderType != "google" || account.ProviderType != "google" {
		return ytlive.OAuthCredentials{}, errYouTubeOAuthAccountUnavailable
	}
	return ytlive.OAuthCredentials{ClientID: provider.ClientID, ClientSecret: provider.ClientSecret, RefreshToken: account.RefreshToken}, nil
}

func (s *Server) applyYouTubeStreamKeyOutput(ctx context.Context, profile store.Profile, req *servicecall.StartRequest) error {
	rtmpURL := youtubeOutputRTMPURL(profile)
	if rtmpURL == "" {
		return errYouTubeOutputInvalidConfig
	}
	req.EncoderRTMPURL = rtmpURL
	if !isSecureRTMPSURL(req.EncoderRTMPURL) {
		return errYouTubeOutputInvalidConfig
	}
	secretName := strings.TrimSpace(configString(profile.Config, "stream_key_secret_name"))
	if secretName == "" {
		secretName = strings.TrimSpace(configString(profile.Config, "streamKeySecretName"))
	}
	if secretName == "" {
		return errYouTubeOutputInvalidConfig
	}
	streamKey, err := s.secrets.GetSecretValue(ctx, secretName)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrUnknownSecret) {
		return errYouTubeOutputStreamKeyUnavailable
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(streamKey) == "" {
		return errYouTubeOutputStreamKeyUnavailable
	}
	req.EncoderStreamKeySecretName = secretName
	if watchURL, ok := normalizeYouTubeWatchURL(configString(profile.Config, "watch_url")); ok {
		req.YouTubeRuntime = map[string]any{
			"mode":             "stream_key",
			"output_id":        profile.ID,
			"watch_url":        watchURL,
			"dry_run":          false,
			"complete_on_stop": false,
		}
	}
	return nil
}

func (s *Server) applyYouTubeLiveAPIDryRunOutput(ctx context.Context, stream store.Stream, profile store.Profile, req *servicecall.StartRequest) error {
	if strings.TrimSpace(req.EncoderRTMPURL) == "" {
		req.EncoderRTMPURL = "rtmps://a.rtmps.youtube.com/live2"
	}
	if !isSecureRTMPSURL(req.EncoderRTMPURL) {
		return errYouTubeOutputInvalidConfig
	}
	seed := stream.ID + ":" + profile.ID + ":" + profile.UpdatedAt.UTC().Format(time.RFC3339Nano)
	fingerprint := security.SecretFingerprint(seed)
	streamKeySecretName := youtubeLiveAPIStreamKeySecretName(stream.ID, profile.ID, "dry-broadcast-"+fingerprint)
	if _, err := s.secrets.UpdateSecret(ctx, streamKeySecretName, "yt-dry-run-"+fingerprint); err != nil {
		return errYouTubeLiveAPIPrepareFailed
	}
	req.EncoderStreamKeySecretName = streamKeySecretName
	req.YouTubeRuntime = map[string]any{
		"mode":                   "live_api_dry_run",
		"output_id":              profile.ID,
		"broadcast_id":           "dry-broadcast-" + fingerprint,
		"live_stream_id":         "dry-live-stream-" + fingerprint,
		"rtmp_url":               req.EncoderRTMPURL,
		"stream_key_secret_name": streamKeySecretName,
		"dry_run":                true,
		"complete_on_stop":       youtubeCompleteOnStop(profile.Config),
	}
	return nil
}

func isSecureRTMPSURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.User == nil && parsed.Scheme == "rtmps" && parsed.Host != ""
}

func youtubeWatchURLForBroadcastID(broadcastID string) string {
	broadcastID = strings.TrimSpace(broadcastID)
	if !validYouTubeVideoID(broadcastID) {
		return ""
	}
	return "https://www.youtube.com/watch?" + url.Values{"v": []string{broadcastID}}.Encode()
}

func normalizeYouTubeWatchURL(value string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.Port() != "" {
		return "", false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	videoID := ""
	switch host {
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		if parsed.Path != "/watch" {
			return "", false
		}
		videoID = parsed.Query().Get("v")
	case "youtu.be":
		videoID = strings.Trim(parsed.Path, "/")
		if strings.Contains(videoID, "/") {
			return "", false
		}
	default:
		return "", false
	}
	if !validYouTubeVideoID(videoID) {
		return "", false
	}
	return youtubeWatchURLForBroadcastID(videoID), true
}

func validYouTubeVideoID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 6 || len(value) > 32 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func (s *Server) saveYouTubeRuntime(ctx context.Context, streamID string, runtime map[string]any) error {
	if len(runtime) == 0 {
		return nil
	}
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return nil
	}
	return storeWithRuntime.SaveStreamYouTubeRuntime(ctx, streamYouTubeRuntimeFromMap(streamID, runtime))
}

func (s *Server) deleteYouTubeRuntime(ctx context.Context, streamID string) {
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return
	}
	runtime, err := storeWithRuntime.GetStreamYouTubeRuntime(ctx, streamID)
	if err == nil {
		s.clearYouTubeRuntimeSecret(ctx, runtime)
	}
	_ = storeWithRuntime.DeleteStreamYouTubeRuntime(ctx, streamID)
}

func (s *Server) completeYouTubeRuntime(ctx context.Context, streamID string, force bool) (map[string]any, error) {
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return nil, nil
	}
	runtime, err := storeWithRuntime.GetStreamYouTubeRuntime(ctx, streamID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	shouldComplete := force || runtime.CompleteOnStop
	if runtime.Mode == "live_api" && shouldComplete {
		credentials, err := s.youtubeOAuthCredentials(ctx, runtime.OAuthAccountID)
		if err != nil {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, err)
			return nil, err
		}
		if s.youtubeLive == nil {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, errYouTubeLiveAPIUnavailable)
			return nil, errYouTubeLiveAPIUnavailable
		}
		if err := s.youtubeLive.Complete(ctx, ytlive.CompleteRequest{Credentials: credentials, BroadcastID: runtime.BroadcastID}); err != nil {
			s.recordYouTubeRuntimeCompleteFailure(ctx, storeWithRuntime, runtime, errYouTubeLiveAPICompleteFailed)
			return nil, errYouTubeLiveAPICompleteFailed
		}
	}
	if err := storeWithRuntime.DeleteStreamYouTubeRuntime(ctx, streamID); err != nil {
		return nil, err
	}
	s.clearYouTubeRuntimeSecret(ctx, runtime)
	return map[string]any{
		"mode":             runtime.Mode,
		"output_id":        runtime.YouTubeOutput,
		"oauth_account_id": runtime.OAuthAccountID,
		"broadcast_id":     runtime.BroadcastID,
		"live_stream_id":   runtime.LiveStreamID,
		"dry_run":          runtime.DryRun,
		"complete_on_stop": runtime.CompleteOnStop,
		"retry_count":      runtime.CompleteRetryCount,
		"complete_skipped": runtime.Mode == "live_api" && !shouldComplete,
	}, nil
}

func (s *Server) recordYouTubeRuntimeCompleteFailure(ctx context.Context, storeWithRuntime store.StreamYouTubeRuntimeStore, runtime store.StreamYouTubeRuntime, err error) {
	retryCount := runtime.CompleteRetryCount + 1
	nextRetryAt := time.Now().UTC().Add(youtubeCompleteRetryDelay(retryCount))
	if _, updateErr := storeWithRuntime.RecordStreamYouTubeRuntimeCompleteFailure(ctx, runtime.StreamID, youtubeCompleteRetryErrorCode(err), nextRetryAt); updateErr != nil {
		log.Printf("youtube complete retry scheduling failed: stream_id=%s error=%v", runtime.StreamID, updateErr)
	}
}

func youtubeCompleteRetryDelay(retryCount int) time.Duration {
	switch {
	case retryCount <= 1:
		return time.Minute
	case retryCount == 2:
		return 2 * time.Minute
	case retryCount == 3:
		return 5 * time.Minute
	case retryCount == 4:
		return 10 * time.Minute
	case retryCount == 5:
		return 30 * time.Minute
	default:
		return time.Hour
	}
}

func youtubeCompleteRetryErrorCode(err error) string {
	switch {
	case errors.Is(err, errYouTubeLiveAPIUnavailable):
		return errYouTubeLiveAPIUnavailable.Error()
	case errors.Is(err, errYouTubeOAuthAccountUnavailable):
		return errYouTubeOAuthAccountUnavailable.Error()
	case errors.Is(err, errYouTubeLiveAPICompleteFailed):
		return errYouTubeLiveAPICompleteFailed.Error()
	default:
		return "complete_youtube_runtime_failed"
	}
}

func (s *Server) RunYouTubeCompletionRetryLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = youtubeCompleteRetryDefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := s.CompleteDueYouTubeRuntimes(ctx, 25); err != nil {
			log.Printf("youtube complete retry scan failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) CompleteDueYouTubeRuntimes(ctx context.Context, limit int) (map[string]any, error) {
	storeWithRuntime, ok := s.streams.(store.StreamYouTubeRuntimeStore)
	if !ok {
		return map[string]any{"attempted": 0, "completed": 0, "failed": 0}, nil
	}
	runtimes, err := storeWithRuntime.ListDueStreamYouTubeRuntimes(ctx, time.Now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	attempted := 0
	completed := 0
	failed := 0
	for _, runtime := range runtimes {
		attempted++
		metadata, err := s.completeYouTubeRuntime(ctx, runtime.StreamID, true)
		if err != nil {
			failed++
			s.writeSystemAudit(ctx, store.AuditEvent{Action: "youtube.complete", ResourceType: "stream", ResourceID: runtime.StreamID, Result: "failure", Metadata: map[string]any{"reason": youtubeCompleteRetryErrorCode(err), "trigger": "auto_retry"}})
			continue
		}
		if len(metadata) == 0 {
			continue
		}
		completed++
		metadata["completed"] = true
		metadata["trigger"] = "auto_retry"
		s.writeSystemAudit(ctx, store.AuditEvent{Action: "youtube.complete", ResourceType: "stream", ResourceID: runtime.StreamID, Result: "success", Metadata: metadata})
	}
	return map[string]any{"attempted": attempted, "completed": completed, "failed": failed}, nil
}

func streamYouTubeRuntimeFromMap(streamID string, runtime map[string]any) store.StreamYouTubeRuntime {
	return store.StreamYouTubeRuntime{
		StreamID:            streamID,
		YouTubeOutput:       firstNonEmpty(mapString(runtime, "output_id"), mapString(runtime, "youtube_output")),
		OAuthAccountID:      mapString(runtime, "oauth_account_id"),
		Mode:                mapString(runtime, "mode"),
		BroadcastID:         mapString(runtime, "broadcast_id"),
		LiveStreamID:        mapString(runtime, "live_stream_id"),
		RTMPURL:             mapString(runtime, "rtmp_url"),
		StreamKeySecretName: mapString(runtime, "stream_key_secret_name"),
		DryRun:              mapBool(runtime, "dry_run"),
		CompleteOnStop:      mapBoolDefault(runtime, "complete_on_stop", true),
	}
}

func youtubeLiveAPIStreamKeySecretName(streamID, outputID, broadcastID string) string {
	seed := strings.TrimSpace(streamID) + ":" + strings.TrimSpace(outputID) + ":" + strings.TrimSpace(broadcastID)
	return "youtube_stream_key_runtime_" + security.SecretFingerprint(seed)
}

func (s *Server) clearYouTubeRuntimeSecretFromMap(ctx context.Context, runtime map[string]any) {
	if len(runtime) == 0 {
		return
	}
	s.clearYouTubeRuntimeSecret(ctx, streamYouTubeRuntimeFromMap("", runtime))
}

func (s *Server) clearYouTubeRuntimeSecret(ctx context.Context, runtime store.StreamYouTubeRuntime) {
	secretName := strings.TrimSpace(runtime.StreamKeySecretName)
	if secretName == "" {
		return
	}
	_, _ = s.secrets.UpdateSecret(ctx, secretName, "")
}

func mapString(values map[string]any, key string) string {
	text, _ := values[key].(string)
	return text
}

func mapBool(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func mapBoolDefault(values map[string]any, key string, fallback bool) bool {
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}

func (s *Server) applyArchiveConfig(ctx context.Context, req *servicecall.StartRequest) error {
	profileID := strings.TrimSpace(req.ArchiveProfileID)
	if profileID == "" {
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileArchive, profileID)
	if errors.Is(err, store.ErrNotFound) {
		return errArchiveProfileNotFound
	}
	if err != nil {
		return err
	}
	archiveConfig := map[string]any{
		"archive_profile_id": profile.ID,
	}
	if retentionDays := configInt(profile.Config, "retention_days"); retentionDays > 0 {
		archiveConfig["retention_days"] = normalizeArchiveRetentionDays(retentionDays)
	}
	destinationID := strings.TrimSpace(configString(profile.Config, "drive_destination_id"))
	if destinationID == "" {
		if len(archiveConfig) > 1 {
			req.ArchiveConfig = archiveConfig
		}
		return nil
	}
	destination, err := s.integrations.GetDriveDestinationForDispatch(ctx, destinationID)
	if errors.Is(err, store.ErrNotFound) {
		return errDriveDestinationNotFound
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(destination.FolderID) == "" {
		return errDriveDestinationUnavailable
	}
	if !strings.EqualFold(strings.TrimSpace(destination.AuthMode), "oauth2") {
		return errArchiveProfileInvalidConfig
	}
	req.ArchiveConfig = archiveConfig
	req.ArchiveConfig["drive_destination_id"] = destination.ID
	req.ArchiveConfig["auth_mode"] = "oauth2"
	req.ArchiveConfig["oauth_account_id"] = destination.OAuthAccountID
	req.ArchiveConfig["folder_id_secret_name"] = driveDestinationFolderIDSecretName(destination.ID)
	req.ArchiveConfig["base_path"] = destination.BasePath
	req.ArchiveConfig["shared_drive"] = destination.SharedDrive
	account, err := s.integrations.GetOAuthAccountForDispatch(ctx, destination.OAuthAccountID)
	if errors.Is(err, store.ErrNotFound) || strings.TrimSpace(account.RefreshToken) == "" {
		return errDriveOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive) {
		return errDriveOAuthAccountUnavailable
	}
	provider, err := s.integrations.GetOAuthProviderForDispatch(ctx, account.ProviderID)
	if errors.Is(err, store.ErrNotFound) || strings.TrimSpace(provider.ClientSecret) == "" {
		return errDriveOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	req.ArchiveConfig["oauth_provider_id"] = provider.ID
	req.ArchiveConfig["client_id"] = provider.ClientID
	req.ArchiveConfig["client_secret_secret_name"] = oauthProviderClientSecretSecretName(provider.ID)
	req.ArchiveConfig["refresh_token_secret_name"] = oauthAccountRefreshTokenSecretName(account.ID)
	if fileName := archiveSafeFileName(configString(profile.Config, "archive_file_name")); fileName != "" {
		req.ArchiveConfig["archive_file_name"] = fileName
	}
	if sharedDriveID := strings.TrimSpace(configString(profile.Config, "shared_drive_id")); sharedDriveID != "" {
		req.ArchiveConfig["shared_drive_id"] = sharedDriveID
	}
	return nil
}

func (s *Server) archiveConfigReadinessIssues(ctx context.Context, req *servicecall.StartRequest) []servicecall.ReadinessIssue {
	if err := s.validateArchiveConfigReadiness(ctx, req); err != nil {
		return []servicecall.ReadinessIssue{{
			ServiceType: "encoder_recorder",
			Code:        archiveConfigCode(err),
			Message:     archiveConfigReadinessMessage(err),
		}}
	}
	return nil
}

func (s *Server) validateArchiveConfigReadiness(ctx context.Context, req *servicecall.StartRequest) error {
	profileID := strings.TrimSpace(req.ArchiveProfileID)
	if profileID == "" {
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileArchive, profileID)
	if errors.Is(err, store.ErrNotFound) {
		return errArchiveProfileNotFound
	}
	if err != nil {
		return err
	}
	destinationID := strings.TrimSpace(configString(profile.Config, "drive_destination_id"))
	if destinationID == "" {
		return nil
	}
	destination, err := s.integrations.GetDriveDestination(ctx, destinationID)
	if errors.Is(err, store.ErrNotFound) {
		return errDriveDestinationNotFound
	}
	if err != nil {
		return err
	}
	if !destination.FolderIDConfigured {
		return errDriveDestinationUnavailable
	}
	if !strings.EqualFold(strings.TrimSpace(destination.AuthMode), "oauth2") {
		return errArchiveProfileInvalidConfig
	}
	return s.validateDriveOAuthReadiness(ctx, destination)
}

func (s *Server) validateDriveOAuthReadiness(ctx context.Context, destination store.DriveDestination) error {
	if strings.TrimSpace(destination.OAuthAccountID) == "" {
		return errDriveOAuthAccountUnavailable
	}
	account, err := s.integrations.GetOAuthAccount(ctx, destination.OAuthAccountID)
	if errors.Is(err, store.ErrNotFound) || !account.RefreshTokenConfigured {
		return errDriveOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	provider, err := s.integrations.GetOAuthProvider(ctx, account.ProviderID)
	if errors.Is(err, store.ErrNotFound) || !provider.Enabled || strings.TrimSpace(provider.ClientID) == "" || !provider.ClientSecretConfigured {
		return errDriveOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(provider.ProviderType, "google") || !strings.EqualFold(account.ProviderType, "google") {
		return errDriveOAuthAccountUnavailable
	}
	if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive) {
		return errDriveOAuthAccountUnavailable
	}
	return nil
}

func archiveConfigReadinessMessage(err error) string {
	switch {
	case errors.Is(err, errArchiveProfileNotFound):
		return "selected archive profile was not found."
	case errors.Is(err, errArchiveProfileInvalidConfig):
		return "selected archive profile or Drive destination has invalid settings."
	case errors.Is(err, errDriveDestinationNotFound):
		return "selected Drive destination was not found."
	case errors.Is(err, errDriveDestinationUnavailable):
		return "selected Drive destination folder ID is not configured."
	case errors.Is(err, errDriveOAuthAccountUnavailable):
		return "selected Drive OAuth connected account is not ready."
	default:
		return "selected archive configuration could not be validated."
	}
}

func (s *Server) retryArchiveConfig(ctx context.Context, stream store.Stream) (map[string]any, error) {
	archiveProfileID := strings.TrimSpace(stream.ArchiveProfileID)
	if archiveProfileID == "" {
		return nil, nil
	}
	req := servicecall.StartRequest{ArchiveProfileID: archiveProfileID}
	if err := s.applyArchiveConfig(ctx, &req); err != nil {
		return nil, err
	}
	return req.ArchiveConfig, nil
}

func driveDestinationFolderIDSecretName(id string) string {
	return "drive_destination:" + strings.TrimSpace(id) + ":folder_id"
}

func oauthProviderClientSecretSecretName(id string) string {
	return "oauth_provider:" + strings.TrimSpace(id) + ":client_secret"
}

func oauthAccountRefreshTokenSecretName(id string) string {
	return "oauth_account:" + strings.TrimSpace(id) + ":refresh_token"
}

func configString(config map[string]any, key string) string {
	value, ok := config[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return text
}

func defaultConfigString(config map[string]any, key, fallback string) string {
	value := strings.TrimSpace(configString(config, key))
	if value == "" {
		return fallback
	}
	return value
}

func configInt(config map[string]any, key string) int {
	value, ok := config[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return 0
}

func configBool(config map[string]any, key string) bool {
	value, ok := config[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func configBoolDefault(config map[string]any, key string, fallback bool) bool {
	value, ok := config[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}

func youtubeCompleteOnStop(config map[string]any) bool {
	return configBoolDefault(config, "complete_on_stop", true)
}

func configTime(config map[string]any, key string) time.Time {
	value := strings.TrimSpace(configString(config, key))
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func archiveConfigStatus(err error) int {
	switch {
	case errors.Is(err, errArchiveProfileNotFound), errors.Is(err, errDriveDestinationNotFound):
		return http.StatusNotFound
	case errors.Is(err, errArchiveProfileInvalidConfig), errors.Is(err, errDriveDestinationUnavailable), errors.Is(err, errDriveOAuthAccountUnavailable), errors.Is(err, store.ErrSecretKeyRequired):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func archiveConfigCode(err error) string {
	switch {
	case errors.Is(err, errArchiveProfileNotFound), errors.Is(err, errArchiveProfileInvalidConfig), errors.Is(err, errDriveDestinationNotFound), errors.Is(err, errDriveDestinationUnavailable), errors.Is(err, errDriveOAuthAccountUnavailable):
		return err.Error()
	case errors.Is(err, store.ErrSecretKeyRequired):
		return errDriveDestinationUnavailable.Error()
	default:
		return "archive_config_resolution_failed"
	}
}

func youtubeOutputStatus(err error) int {
	switch {
	case errors.Is(err, errYouTubeOutputNotFound):
		return http.StatusNotFound
	case errors.Is(err, errYouTubeOutputInvalidConfig), errors.Is(err, errYouTubeOutputStreamKeyUnavailable), errors.Is(err, errYouTubeLiveAPIUnavailable), errors.Is(err, errYouTubeOAuthAccountUnavailable), errors.Is(err, errYouTubeLiveAPIPrepareFailed), errors.Is(err, store.ErrSecretKeyRequired):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func youtubeOutputCode(err error) string {
	switch {
	case errors.Is(err, errYouTubeOutputNotFound), errors.Is(err, errYouTubeOutputInvalidConfig), errors.Is(err, errYouTubeOutputStreamKeyUnavailable), errors.Is(err, errYouTubeLiveAPIUnavailable), errors.Is(err, errYouTubeOAuthAccountUnavailable), errors.Is(err, errYouTubeLiveAPIPrepareFailed):
		return err.Error()
	case errors.Is(err, store.ErrSecretKeyRequired):
		return errYouTubeOutputStreamKeyUnavailable.Error()
	default:
		return "youtube_output_resolution_failed"
	}
}

func discordConfigStatus(err error) int {
	switch {
	case errors.Is(err, errDiscordConfigNotFound):
		return http.StatusNotFound
	case errors.Is(err, errDiscordConfigRequired), errors.Is(err, errDiscordConfigInvalid), errors.Is(err, errDiscordConfigServiceMismatch):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func discordConfigCode(err error) string {
	switch {
	case errors.Is(err, errDiscordConfigRequired), errors.Is(err, errDiscordConfigNotFound), errors.Is(err, errDiscordConfigInvalid), errors.Is(err, errDiscordConfigServiceMismatch):
		return err.Error()
	default:
		return "discord_config_resolution_failed"
	}
}

func (s *Server) startReadiness(w http.ResponseWriter, r *http.Request) {
	var body servicecall.StartRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
			return
		}
	}
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	applyStreamSettingsDefaults(stream, &body)
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	missing := missingServiceTypes(primaryAssignments, requiredStartServiceTypes)
	issues := []servicecall.ReadinessIssue{}
	if len(missing) == 0 {
		if err := s.applyDiscordConfig(r.Context(), primaryAssignments, &body); err != nil {
			writeJSON(w, discordConfigStatus(err), map[string]string{"code": discordConfigCode(err)})
			return
		}
		issues = append(issues, s.youtubeOutputReadinessIssues(r.Context(), stream, &body)...)
		issues = append(issues, s.archiveConfigReadinessIssues(r.Context(), &body)...)
		if checker, ok := s.dispatcher.(startReadinessChecker); ok {
			issues = append(issues, checker.StartReadinessIssues(primaryAssignments, body, time.Now().UTC())...)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stream_id":              stream.ID,
		"ready":                  len(missing) == 0 && len(issues) == 0,
		"missing_service_types":  missing,
		"issues":                 issues,
		"assigned_service_count": len(assignments),
		"primary_service_count":  len(primaryAssignments),
		"assignments":            assignments,
	})
}

func (s *Server) stopStream(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	if !isManuallyStoppableStreamStatus(stream.Status) {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.stop", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": "stream_status_not_stoppable", "current_status": stream.Status}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "stream_status_not_stoppable", "status": stream.Status})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredStopServiceTypes); len(missing) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.stop", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"missing_service_types": missing}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	if _, err := s.streams.UpdateStreamStatus(r.Context(), stream.ID, "stopping"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	results := s.dispatcher.Stop(r.Context(), stream, primaryAssignments)
	results = sanitizeDispatchResults(results)
	if hasDispatchFailure(results) {
		failed, _ := s.streams.UpdateStreamStatus(r.Context(), stream.ID, "failed")
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.stop", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"dispatch": results}})
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "stream": failed, "dispatch": results})
		return
	}
	if metadata, err := s.completeYouTubeRuntime(r.Context(), stream.ID, false); err != nil {
		failed, _ := s.streams.UpdateStreamStatus(r.Context(), stream.ID, "failed")
		current := currentFromContext(r.Context())
		code := "complete_youtube_runtime_failed"
		if errors.Is(err, errYouTubeLiveAPICompleteFailed) {
			code = errYouTubeLiveAPICompleteFailed.Error()
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code}})
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": code, "stream": failed})
		return
	} else if len(metadata) > 0 {
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
	}
	s.transitionWithDispatch(w, r, stream.ID, "completed", "streams.stop", results)
}

func (s *Server) completeYouTubeStream(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	current := currentFromContext(r.Context())
	metadata, err := s.completeYouTubeRuntime(r.Context(), stream.ID, true)
	if err != nil {
		code := "complete_youtube_runtime_failed"
		if errors.Is(err, errYouTubeLiveAPICompleteFailed) {
			code = errYouTubeLiveAPICompleteFailed.Error()
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "trigger": "manual_retry"}})
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": code})
		return
	}
	if len(metadata) == 0 {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"completed": false, "trigger": "manual_retry"}})
		writeJSON(w, http.StatusOK, map[string]any{"completed": false})
		return
	}
	metadata["completed"] = true
	metadata["trigger"] = "manual_retry"
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "youtube.complete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
	writeJSON(w, http.StatusOK, map[string]any{"completed": true, "youtube_runtime": metadata})
}

func (s *Server) markStreamFailed(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, r.PathValue("id"), "failed", "streams.mark_failed")
}

func (s *Server) transition(w http.ResponseWriter, r *http.Request, id, status, action string) {
	s.transitionWithDispatch(w, r, id, status, action, nil)
}

func (s *Server) transitionWithDispatch(w http.ResponseWriter, r *http.Request, id, status, action string, dispatch []servicecall.DispatchResult) {
	dispatch = sanitizeDispatchResults(dispatch)
	stream, err := s.streams.UpdateStreamStatus(r.Context(), id, status)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "update_stream_failed"})
		return
	}
	current := currentFromContext(r.Context())
	metadata := map[string]any{"status": status}
	if dispatch != nil {
		metadata["dispatch"] = dispatch
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: metadata})
	if dispatch != nil {
		writeJSON(w, http.StatusOK, map[string]any{"stream": stream, "dispatch": dispatch})
		return
	}
	writeJSON(w, http.StatusOK, stream)
}

func (s *Server) streamAssignments(ctx context.Context, streamID string) ([]store.RegisteredService, error) {
	if s.services == nil {
		return nil, nil
	}
	return s.services.ListStreamAssignments(ctx, streamID)
}

func normalizeAssignmentRole(role string) string {
	role = strings.TrimSpace(strings.ToLower(role))
	if role == "standby" {
		return "standby"
	}
	return "primary"
}

func primaryStreamAssignments(assignments []store.RegisteredService) []store.RegisteredService {
	out := make([]store.RegisteredService, 0, len(assignments))
	for _, assignment := range assignments {
		if assignment.AssignmentRole == "" || assignment.AssignmentRole == "primary" {
			if assignment.AssignmentRole == "" {
				assignment.AssignmentRole = "primary"
			}
			out = append(out, assignment)
		}
	}
	return out
}

func missingServiceTypes(assignments []store.RegisteredService, required []string) []string {
	assigned := make(map[string]bool, len(assignments))
	for _, service := range assignments {
		assigned[service.ServiceType] = true
	}
	missing := make([]string, 0, len(required))
	for _, serviceType := range required {
		if !assigned[serviceType] {
			missing = append(missing, serviceType)
		}
	}
	return missing
}

func hasDispatchFailure(results []servicecall.DispatchResult) bool {
	for _, result := range results {
		if !result.Success {
			return true
		}
	}
	return false
}

func sanitizeDispatchResults(results []servicecall.DispatchResult) []servicecall.DispatchResult {
	if results == nil {
		return nil
	}
	out := make([]servicecall.DispatchResult, len(results))
	for i, result := range results {
		out[i] = result
		out[i].Error = sanitizeDispatchError(result.Error)
	}
	return out
}

func sanitizeDispatchError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if value == "SERVICE_CALL_TOKEN is not configured" {
		return value
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "bearer ") ||
		strings.Contains(lower, "discord.com/api/webhooks") ||
		strings.Contains(lower, "hooks.slack.com/services") ||
		strings.Contains(lower, "://") {
		return "service dispatch failed"
	}
	return value
}

func (s *Server) retryUpload(w http.ResponseWriter, r *http.Request) {
	current := currentFromContext(r.Context())
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.retry_upload", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"missing_service_types": missing}})
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	archiveConfig, err := s.retryArchiveConfig(r.Context(), stream)
	if err != nil {
		code := archiveConfigCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.retry_upload", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"reason": code, "archive_profile_id": stream.ArchiveProfileID}})
		writeJSON(w, archiveConfigStatus(err), map[string]string{"code": code})
		return
	}
	results := s.dispatcher.RetryArchiveUpload(r.Context(), stream, primaryAssignments, archiveConfig)
	results = sanitizeDispatchResults(results)
	if hasDispatchFailure(results) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.retry_upload", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"dispatch": results}})
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": results})
		return
	}
	logEntry, err := s.streams.RetryArchiveUpload(r.Context(), stream.ID, current.User.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "retry_upload_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.retry_upload", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"dispatch": results}})
	writeJSON(w, http.StatusAccepted, map[string]any{"log": logEntry, "dispatch": results})
}

func (s *Server) streamAudioStatus(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	result := s.dispatcher.AudioStatus(r.Context(), stream, primaryAssignments)
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": result})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) streamEncoderPreflight(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	result := s.dispatcher.EncoderPreflight(r.Context(), stream, primaryAssignments)
	result = servicecall.RedactServicePreflightResult(result)
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": result})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) streamPreviewAsset(w http.ResponseWriter, r *http.Request) {
	s.writeStreamPreviewAsset(w, r, strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("name")), false)
}

func (s *Server) createStreamPreviewLink(w http.ResponseWriter, r *http.Request) {
	streamID := strings.TrimSpace(r.PathValue("id"))
	stream, assignments, ok := s.prepareActiveStreamPreview(w, r, streamID, false)
	if !ok {
		return
	}
	if _, ok := s.dispatcher.(previewServiceDispatcher); !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_preview_not_supported"})
		return
	}
	if strings.TrimSpace(s.previewSigningKey) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_preview_signing_key_required"})
		return
	}
	now := time.Now().UTC()
	expiresAt := now.Add(streamPreviewLinkTTL)
	token, err := ingesttoken.Issue(s.previewSigningKey, ingesttoken.Claims{
		StreamID:    stream.ID,
		ServiceID:   "control-panel",
		ServiceType: "control_panel",
		Purpose:     "stream_preview",
		Audience:    "external_player",
		ExpiresAt:   expiresAt.Unix(),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "stream_preview_link_failed"})
		return
	}
	path := "/stream-previews/" + url.PathEscape(token) + "/index.m3u8"
	previewURL := configuredStreamPreviewURL(path)
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.preview_link.create", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"expires_at": expiresAt, "assignment_count": len(assignments)}})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"stream_id": stream.ID, "url": previewURL, "expires_at": expiresAt})
}

func configuredStreamPreviewURL(path string) string {
	publicURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL")), "/")
	parsed, err := url.Parse(publicURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return path
	}
	return publicURL + path
}

func (s *Server) publicStreamPreviewAsset(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.previewSigningKey) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_preview_unavailable"})
		return
	}
	claims, err := ingesttoken.Verify(s.previewSigningKey, strings.TrimSpace(r.PathValue("token")), ingesttoken.Expected{
		ServiceID:   "control-panel",
		ServiceType: "control_panel",
		Purpose:     "stream_preview",
		Audience:    "external_player",
		Now:         time.Now().UTC(),
	})
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "stream_preview_not_found"})
		return
	}
	s.writeStreamPreviewAsset(w, r, claims.StreamID, strings.TrimSpace(r.PathValue("name")), true)
}

func (s *Server) writeStreamPreviewAsset(w http.ResponseWriter, r *http.Request, streamID, name string, public bool) {
	if !validStreamPreviewAssetName(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_preview_asset"})
		return
	}
	stream, assignments, ok := s.prepareActiveStreamPreview(w, r, streamID, public)
	if !ok {
		return
	}
	dispatcher, ok := s.dispatcher.(previewServiceDispatcher)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "stream_preview_not_supported"})
		return
	}
	result := dispatcher.PreviewAsset(r.Context(), stream, assignments, name)
	if !result.Success {
		status := http.StatusBadGateway
		code := "stream_preview_fetch_failed"
		if result.StatusCode == http.StatusNotFound {
			status = http.StatusNotFound
			code = "stream_preview_not_ready"
		} else if result.StatusCode == http.StatusConflict {
			status = http.StatusConflict
			code = "stream_preview_not_active"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	body := result.Body
	if name == "index.m3u8" {
		var valid bool
		body, valid = validatedStreamPreviewPlaylist(body)
		if !valid {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "invalid_stream_preview_playlist"})
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "private, max-age=30")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) prepareActiveStreamPreview(w http.ResponseWriter, r *http.Request, streamID string, public bool) (store.Stream, []store.RegisteredService, bool) {
	stream, err := s.streams.GetStream(r.Context(), streamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.Stream{}, nil, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return store.Stream{}, nil, false
	}
	if !isStreamPreviewActive(stream.Status) {
		status := http.StatusConflict
		if public {
			status = http.StatusGone
		}
		writeJSON(w, status, map[string]string{"code": "stream_preview_not_active"})
		return store.Stream{}, nil, false
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return store.Stream{}, nil, false
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return store.Stream{}, nil, false
	}
	return stream, primaryAssignments, true
}

func isStreamPreviewActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "starting", "live", "stopping":
		return true
	default:
		return false
	}
}

func validStreamPreviewAssetName(name string) bool {
	if name == "index.m3u8" {
		return true
	}
	if len(name) != len("segment-000000.ts") || !strings.HasPrefix(name, "segment-") || !strings.HasSuffix(name, ".ts") {
		return false
	}
	for _, char := range strings.TrimSuffix(strings.TrimPrefix(name, "segment-"), ".ts") {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func validatedStreamPreviewPlaylist(body []byte) ([]byte, bool) {
	if len(body) == 0 || len(body) > 1<<20 || strings.ContainsRune(string(body), '\x00') {
		return nil, false
	}
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) > 256 {
		return nil, false
	}
	firstContent := ""
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		for _, char := range line {
			if char < 0x20 || (char >= 0x7f && char <= 0x9f) {
				return nil, false
			}
		}
		if firstContent == "" {
			firstContent = line
		}
		if strings.HasPrefix(line, "#") {
			if strings.Contains(strings.ToUpper(line), "URI=") {
				return nil, false
			}
			continue
		}
		if !validStreamPreviewAssetName(line) || line == "index.m3u8" {
			return nil, false
		}
	}
	if firstContent != "#EXTM3U" {
		return nil, false
	}
	return []byte(strings.TrimRight(normalized, "\n") + "\n"), true
}

func (s *Server) streamWorkerEvents(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	result := servicecall.RedactWorkerEventsResult(s.dispatcher.WorkerEvents(r.Context(), stream, primaryAssignments))
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": result})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) sendWorkerTestEvent(w http.ResponseWriter, r *http.Request) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return
	}
	var body servicecall.WorkerEventRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredWorkerEventServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	result := s.dispatcher.SendWorkerEvent(r.Context(), stream, primaryAssignments, body)
	current := currentFromContext(r.Context())
	if !result.Success {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.worker_event_test", ResourceType: "stream", ResourceID: stream.ID, Result: "failure", Metadata: map[string]any{"event_type": body.EventType, "dispatch": result}})
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "service_dispatch_failed", "dispatch": result})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "streams.worker_event_test", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"event_type": body.EventType, "dispatch": result}})
	writeJSON(w, http.StatusAccepted, map[string]any{"dispatch": result})
}

func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := s.streams.ListStreamLogs(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_logs_failed"})
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) streamArtifacts(w http.ResponseWriter, r *http.Request) {
	artifacts, err := s.streams.ListStreamArtifacts(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_artifacts_failed"})
		return
	}
	writeJSON(w, http.StatusOK, artifacts)
}

func (s *Server) downloadStreamArtifact(w http.ResponseWriter, r *http.Request) {
	stream, artifact, assignments, ok := s.prepareStreamArtifactAction(w, r)
	if !ok {
		return
	}
	result := s.dispatcher.DownloadArchiveArtifact(r.Context(), stream, assignments, artifact)
	if !result.Success || result.Body == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "archive_artifact_download_failed", "dispatch": sanitizeArchiveDownloadResult(result)})
		return
	}
	defer result.Body.Close()
	fileName := artifact.Name
	if result.FileName != "" {
		fileName = result.FileName
	}
	contentType := result.ContentType
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition+`; filename="`+sanitizeDownloadFileName(fileName)+`"`)
	if result.SizeBytes >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(result.SizeBytes, 10))
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "archive.artifact.download", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"artifact_id": artifact.ID, "artifact_name": artifact.Name}})
	_, _ = io.Copy(w, result.Body)
}

func (s *Server) listStreamArtifactShares(w http.ResponseWriter, r *http.Request) {
	shareStore, ok := s.streams.(store.StreamArtifactShareStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_share_store_not_configured"})
		return
	}
	stream, artifact, _, ok := s.prepareStreamArtifactAction(w, r)
	if !ok {
		return
	}
	shares, err := shareStore.ListStreamArtifactShares(r.Context(), stream.ID, artifact.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_archive_shares_failed"})
		return
	}
	out := make([]map[string]any, 0, len(shares))
	for _, share := range shares {
		out = append(out, publicArchiveShareAdmin(share))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createStreamArtifactShare(w http.ResponseWriter, r *http.Request) {
	shareStore, ok := s.streams.(store.StreamArtifactShareStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_share_store_not_configured"})
		return
	}
	stream, artifact, _, ok := s.prepareStreamArtifactAction(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpiresAt      string `json:"expires_at"`
		ExpiresInHours int    `json:"expires_in_hours"`
		AllowDownload  *bool  `json:"allow_download"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	expiresAt, ok := archiveShareExpiry(body.ExpiresAt, body.ExpiresInHours)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_archive_share_expiry"})
		return
	}
	allowDownload := true
	if body.AllowDownload != nil {
		allowDownload = *body.AllowDownload
	}
	rawToken, err := security.RandomToken(32)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_archive_share_token_failed"})
		return
	}
	current := currentFromContext(r.Context())
	share, err := shareStore.CreateStreamArtifactShare(r.Context(), store.StreamArtifactShare{
		TokenHash:       security.HashToken(rawToken),
		StreamID:        stream.ID,
		ArtifactID:      artifact.ID,
		CreatedByUserID: current.User.ID,
		AllowDownload:   allowDownload,
		ExpiresAt:       expiresAt,
	})
	if errors.Is(err, store.ErrInvalidStreamArtifact) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_archive_share"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "create_archive_share_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "archive.artifact.share.create", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"artifact_id": artifact.ID, "artifact_name": artifact.Name, "share_id": share.ID, "expires_at": share.ExpiresAt, "allow_download": share.AllowDownload}})
	response := publicArchiveShareAdmin(share)
	response["token"] = rawToken
	response["url"] = archiveSharePageURL(r, rawToken)
	response["api_url"] = "/archive-shares/" + url.PathEscape(rawToken)
	writeOneTimeSecretJSON(w, http.StatusCreated, response)
}

func (s *Server) revokeStreamArtifactShare(w http.ResponseWriter, r *http.Request) {
	shareStore, ok := s.streams.(store.StreamArtifactShareStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_share_store_not_configured"})
		return
	}
	streamID := strings.TrimSpace(r.PathValue("id"))
	artifactID := strings.TrimSpace(r.PathValue("artifact_id"))
	shareID := strings.TrimSpace(r.PathValue("share_id"))
	if err := shareStore.RevokeStreamArtifactShare(r.Context(), streamID, artifactID, shareID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "revoke_archive_share_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "archive.artifact.share.revoke", ResourceType: "stream", ResourceID: streamID, Result: "success", Metadata: map[string]any{"artifact_id": artifactID, "share_id": shareID}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) publicArchiveShare(w http.ResponseWriter, r *http.Request) {
	share, stream, artifact, ok := s.resolvePublicArchiveShare(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, publicArchiveSharePayload(share, stream, artifact, r.PathValue("token")))
}

func (s *Server) downloadPublicArchiveShare(w http.ResponseWriter, r *http.Request) {
	share, stream, artifact, ok := s.resolvePublicArchiveShare(w, r)
	if !ok {
		return
	}
	asDownload := r.URL.Query().Get("download") == "1"
	if asDownload && !share.AllowDownload {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "archive_share_download_disabled"})
		return
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return
	}
	result := s.dispatcher.DownloadArchiveArtifact(r.Context(), stream, primaryAssignments, artifact)
	if !result.Success || result.Body == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "archive_artifact_download_failed", "dispatch": sanitizeArchiveDownloadResult(result)})
		return
	}
	defer result.Body.Close()
	fileName := artifact.Name
	if result.FileName != "" {
		fileName = result.FileName
	}
	contentType := strings.TrimSpace(result.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	disposition := "inline"
	if asDownload {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", disposition+`; filename="`+sanitizeDownloadFileName(fileName)+`"`)
	if result.SizeBytes >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(result.SizeBytes, 10))
	}
	_, _ = io.Copy(w, result.Body)
}

func (s *Server) deleteStreamArtifact(w http.ResponseWriter, r *http.Request) {
	stream, artifact, assignments, ok := s.prepareStreamArtifactAction(w, r)
	if !ok {
		return
	}
	result := s.dispatcher.DeleteArchiveArtifact(r.Context(), stream, assignments, artifact)
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "archive_artifact_delete_failed", "dispatch": sanitizeDispatchResults([]servicecall.DispatchResult{result})})
		return
	}
	admin, ok := s.streams.(store.StreamArtifactAdminStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "stream_artifact_admin_not_configured"})
		return
	}
	if err := admin.DeleteStreamArtifact(r.Context(), stream.ID, artifact.ID); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "delete_stream_artifact_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "archive.artifact.delete", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"artifact_id": artifact.ID, "artifact_name": artifact.Name}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) renameStreamArtifact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	stream, artifact, assignments, ok := s.prepareStreamArtifactAction(w, r)
	if !ok {
		return
	}
	if !store.ValidStreamArtifactFileName(body.Name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_artifact"})
		return
	}
	artifacts, err := s.streams.ListStreamArtifacts(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_artifacts_failed"})
		return
	}
	for _, existing := range artifacts {
		if existing.ID != artifact.ID && existing.Kind == artifact.Kind && existing.Name == body.Name {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_artifact_exists"})
			return
		}
	}
	result := s.dispatcher.RenameArchiveArtifact(r.Context(), stream, assignments, artifact, body.Name)
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "archive_artifact_rename_failed", "dispatch": sanitizeDispatchResults([]servicecall.DispatchResult{result})})
		return
	}
	admin, ok := s.streams.(store.StreamArtifactAdminStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "stream_artifact_admin_not_configured"})
		return
	}
	renamed, err := admin.RenameStreamArtifact(r.Context(), stream.ID, artifact.ID, body.Name)
	if errors.Is(err, store.ErrInvalidStreamArtifact) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_stream_artifact"})
		return
	}
	if errors.Is(err, store.ErrAlreadyExists) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "stream_artifact_exists"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "rename_stream_artifact_failed"})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "archive.artifact.rename", ResourceType: "stream", ResourceID: stream.ID, Result: "success", Metadata: map[string]any{"artifact_id": artifact.ID, "from": artifact.Name, "to": renamed.Name}})
	writeJSON(w, http.StatusOK, renamed)
}

func (s *Server) prepareStreamArtifactAction(w http.ResponseWriter, r *http.Request) (store.Stream, store.StreamArtifact, []store.RegisteredService, bool) {
	stream, err := s.streams.GetStream(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	artifacts, err := s.streams.ListStreamArtifacts(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_artifacts_failed"})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	artifact, ok := artifactByID(artifacts, r.PathValue("artifact_id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	assignments, err := s.streamAssignments(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_assignments_failed"})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	primaryAssignments := primaryStreamAssignments(assignments)
	if missing := missingServiceTypes(primaryAssignments, requiredRetryUploadServiceTypes); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "missing_stream_assignments", "missing_service_types": missing})
		return store.Stream{}, store.StreamArtifact{}, nil, false
	}
	return stream, artifact, primaryAssignments, true
}

func artifactByID(artifacts []store.StreamArtifact, id string) (store.StreamArtifact, bool) {
	for _, artifact := range artifacts {
		if artifact.ID == id {
			return artifact, true
		}
	}
	return store.StreamArtifact{}, false
}

func archiveShareExpiry(raw string, expiresInHours int) (time.Time, bool) {
	now := time.Now().UTC()
	var expiresAt time.Time
	if strings.TrimSpace(raw) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
		if err != nil {
			return time.Time{}, false
		}
		expiresAt = parsed.UTC()
	} else {
		if expiresInHours == 0 {
			expiresInHours = 24
		}
		expiresAt = now.Add(time.Duration(expiresInHours) * time.Hour)
	}
	if expiresAt.Before(now.Add(time.Hour)) || expiresAt.After(now.Add(30*24*time.Hour)) {
		return time.Time{}, false
	}
	return expiresAt, true
}

func archiveSharePageURL(r *http.Request, token string) string {
	base := strings.TrimRight(panelBaseURL(r), "/")
	if base == "" {
		return "/archive/share/?token=" + url.QueryEscape(token)
	}
	return base + "/archive/share/?token=" + url.QueryEscape(token)
}

func publicArchiveShareAdmin(share store.StreamArtifactShare) map[string]any {
	status := "active"
	if share.RevokedAt != nil {
		status = "revoked"
	} else if !share.ExpiresAt.After(time.Now().UTC()) {
		status = "expired"
	}
	return map[string]any{
		"id":             share.ID,
		"stream_id":      share.StreamID,
		"artifact_id":    share.ArtifactID,
		"allow_download": share.AllowDownload,
		"expires_at":     share.ExpiresAt,
		"created_at":     share.CreatedAt,
		"revoked_at":     share.RevokedAt,
		"status":         status,
	}
}

func publicArchiveSharePayload(share store.StreamArtifactShare, stream store.Stream, artifact store.StreamArtifact, token string) map[string]any {
	payload := map[string]any{
		"id":             share.ID,
		"stream_id":      stream.ID,
		"stream_name":    stream.Name,
		"artifact_id":    artifact.ID,
		"artifact_name":  artifact.Name,
		"artifact_kind":  artifact.Kind,
		"size_bytes":     artifact.SizeBytes,
		"created_at":     artifact.CreatedAt,
		"allow_download": share.AllowDownload,
		"expires_at":     share.ExpiresAt,
		"playback_url":   "/archive-shares/" + url.PathEscape(token) + "/download",
	}
	if share.AllowDownload {
		payload["download_url"] = "/archive-shares/" + url.PathEscape(token) + "/download?download=1"
	}
	return payload
}

func (s *Server) resolvePublicArchiveShare(w http.ResponseWriter, r *http.Request) (store.StreamArtifactShare, store.Stream, store.StreamArtifact, bool) {
	shareStore, ok := s.streams.(store.StreamArtifactShareStore)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_share_store_not_configured"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	share, err := shareStore.GetStreamArtifactShareByTokenHash(r.Context(), security.HashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "archive_share_lookup_failed"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	if share.RevokedAt != nil {
		writeJSON(w, http.StatusGone, map[string]string{"code": "archive_share_revoked"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	if !share.ExpiresAt.After(time.Now().UTC()) {
		writeJSON(w, http.StatusGone, map[string]string{"code": "archive_share_expired"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	stream, err := s.streams.GetStream(r.Context(), share.StreamID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "get_stream_failed"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	artifacts, err := s.streams.ListStreamArtifacts(r.Context(), stream.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_stream_artifacts_failed"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	artifact, ok := artifactByID(artifacts, share.ArtifactID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return store.StreamArtifactShare{}, store.Stream{}, store.StreamArtifact{}, false
	}
	return share, stream, artifact, true
}

func sanitizeArchiveDownloadResult(result servicecall.ArchiveArtifactDownloadResult) servicecall.ArchiveArtifactDownloadResult {
	result.Body = nil
	return result
}

func sanitizeDownloadFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "archive"
	}
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	return name
}

func (s *Server) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	if s.audit == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "audit_store_not_configured"})
		return
	}
	events, err := s.audit.ListAudit(r.Context(), auditFilterFromRequest(r, 100))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "list_audit_logs_failed"})
		return
	}
	writeJSON(w, http.StatusOK, publicAuditEvents(events))
}

func (s *Server) exportAuditLogs(w http.ResponseWriter, r *http.Request) {
	if s.audit == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "audit_store_not_configured"})
		return
	}
	events, err := s.audit.ListAudit(r.Context(), auditFilterFromRequest(r, 500))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "export_audit_logs_failed"})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="autostream-audit-logs.csv"`)
	w.WriteHeader(http.StatusOK)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "timestamp", "actor_user_id", "actor_username", "actor_ip", "user_agent", "action", "resource_type", "resource_id", "result", "request_id"})
	for _, event := range events {
		_ = writer.Write([]string{safeCSVCell(event.ID), event.Timestamp.Format(time.RFC3339), safeCSVCell(event.ActorUserID), safeCSVCell(event.ActorUsername), safeCSVCell(event.ActorIP), safeCSVCell(event.UserAgent), safeCSVCell(event.Action), safeCSVCell(event.ResourceType), safeCSVCell(event.ResourceID), safeCSVCell(event.Result), safeCSVCell(event.RequestID)})
	}
	writer.Flush()
}

func safeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}

var auditActionGroups = map[string][]string{
	"service_assignment":    {"services.assign", "services.unassign", "workers.assign", "workers.unassign"},
	"service_runtime":       {"services.register", "services.heartbeat", "archive.artifacts.reported", "nodes.registration_token.create"},
	"service_runtime_reads": {"services.runtime_config.read"},
	"stream_lifecycle":      {"streams.create", "streams.start", "streams.stop", "streams.mark_failed", "streams.retry_upload"},
	"security":              {"auth.login", "auth.logout", "auth.change_password", "users.create", "users.update", "users.disable", "users.lock", "users.unlock", "users.reset_password", "users.force_password_change", "roles.create", "roles.update", "roles.delete"},
	"secrets":               {"secrets.update", "security.settings.update", "api_tokens.create", "api_tokens.revoke", "api_tokens.rotate"},
	"notifications":         {"notification_channels.create", "notification_channels.update", "notification_channels.delete", "notification_channels.test", "notifications.email.send"},
}

func auditFilterFromRequest(r *http.Request, defaultLimit int) store.AuditFilter {
	query := r.URL.Query()
	filter := store.AuditFilter{
		Limit:  parseLimit(r, defaultLimit),
		Result: query.Get("result"),
		Query:  query.Get("q"),
		From:   parseAuditBoundary(query.Get("from"), false),
		To:     parseAuditBoundary(query.Get("to"), true),
	}
	if group := query.Get("action_group"); group != "" && group != "all" {
		filter.Actions = append(filter.Actions, auditActionGroups[group]...)
	}
	if group := query.Get("exclude_action_group"); group != "" && group != "all" {
		filter.ExcludedActions = append(filter.ExcludedActions, auditActionGroups[group]...)
	}
	for _, action := range strings.Split(query.Get("action"), ",") {
		action = strings.TrimSpace(action)
		if action != "" {
			filter.Actions = append(filter.Actions, action)
		}
	}
	return filter
}

func parseAuditBoundary(value string, exclusiveEnd bool) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC()
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		if exclusiveEnd {
			return parsed.Add(24 * time.Hour).UTC()
		}
		return parsed.UTC()
	}
	return time.Time{}
}

func publicAuditEvents(events []store.AuditEvent) []store.AuditEvent {
	out := make([]store.AuditEvent, 0, len(events))
	for _, event := range events {
		out = append(out, publicAuditEvent(event))
	}
	return out
}

func publicAuditEvent(event store.AuditEvent) store.AuditEvent {
	event.ActorUserID = redactAuditResponseString(event.ActorUserID)
	event.ActorUsername = redactAuditResponseString(event.ActorUsername)
	event.ActorIP = redactAuditResponseString(event.ActorIP)
	event.UserAgent = redactAuditResponseString(event.UserAgent)
	event.ResourceID = redactAuditResponseString(event.ResourceID)
	event.RequestID = redactAuditResponseString(event.RequestID)
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
		return event
	}
	event.Metadata = redactAuditResponseValue(event.Metadata).(map[string]any)
	return event
}

func redactAuditResponseValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if secretResponseKey(key) {
				out[key] = "<redacted>"
				continue
			}
			out[key] = redactAuditResponseValue(nested)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, nested := range typed {
			out = append(out, redactAuditResponseValue(nested))
		}
		return out
	case string:
		return redactAuditResponseString(typed)
	default:
		return value
	}
}

func redactAuditResponseString(value string) string {
	if secretResponseValue(value) {
		return "<redacted>"
	}
	return value
}

func (s *Server) passwordMeetsConfiguredPolicy(w http.ResponseWriter, r *http.Request, password string) bool {
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_unavailable"})
		return false
	}
	if err := security.ValidatePasswordWithMinLength(password, settings.PasswordMinLength); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":                "password_policy_failed",
			"password_min_length": settings.PasswordMinLength,
		})
		return false
	}
	return true
}

func (s *Server) securitySettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.settings.GetSecuritySettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_failed"})
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateSecuritySettings(w http.ResponseWriter, r *http.Request) {
	var body store.SecuritySettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	if productionEnvironment() && !productionMFASettingsAllowed(body) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "security.settings.update", ResourceType: "security_settings", Result: "failure", Metadata: map[string]any{"reason": "production_mfa_required"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "production_mfa_required"})
		return
	}
	settings, err := s.settings.UpdateSecuritySettings(r.Context(), body)
	if errors.Is(err, store.ErrInvalidSettings) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "security.settings.update", ResourceType: "security_settings", Result: "failure", Metadata: map[string]any{"reason": "invalid_settings"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_security_settings"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "security_settings_update_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "security.settings.update", ResourceType: "security_settings", Result: "success"})
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) appSettingsView(w http.ResponseWriter, r *http.Request) {
	settings, err := s.appSettings.GetAppSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "app_settings_failed"})
		return
	}
	writeJSON(w, http.StatusOK, publicAppSettings{
		AppName:                      settings.AppName,
		Timezone:                     settings.Timezone,
		TurnstileEnabled:             settings.TurnstileEnabled,
		TurnstileSiteKey:             settings.TurnstileSiteKey,
		TurnstileConfigured:          s.secretConfigured(r.Context(), store.AppTurnstileSecretName),
		GoogleAnalyticsEnabled:       settings.GoogleAnalyticsEnabled,
		GoogleAnalyticsMeasurementID: settings.GoogleAnalyticsMeasurementID,
		UpdatedAt:                    settings.UpdatedAt,
	})
}

type publicAppSettings struct {
	AppName                      string `json:"app_name"`
	Timezone                     string `json:"timezone"`
	TurnstileEnabled             bool   `json:"turnstile_enabled,omitempty"`
	TurnstileSiteKey             string `json:"turnstile_site_key,omitempty"`
	TurnstileConfigured          bool   `json:"turnstile_configured,omitempty"`
	GoogleAnalyticsEnabled       bool   `json:"google_analytics_enabled,omitempty"`
	GoogleAnalyticsMeasurementID string `json:"google_analytics_measurement_id,omitempty"`
	UpdatedAt                    string `json:"updated_at,omitempty"`
}

func (s *Server) managedAppSettingsView(w http.ResponseWriter, r *http.Request) {
	settings, err := s.appSettings.GetAppSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "app_settings_failed"})
		return
	}
	writeJSON(w, http.StatusOK, s.appSettingsWithSecretStatus(r.Context(), settings))
}

func (s *Server) updateAppSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		store.AppSettings
		SMTPPassword    string `json:"smtp_password"`
		TurnstileSecret string `json:"turnstile_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	smtpPassword := strings.TrimSpace(body.SMTPPassword)
	turnstileSecret := strings.TrimSpace(body.TurnstileSecret)
	body.SMTPPasswordConfigured = s.secretConfigured(r.Context(), store.AppSMTPPasswordSecretName)
	if smtpPassword != "" {
		body.SMTPPasswordConfigured = true
	}
	if !body.SMTPEnabled {
		body.SMTPPasswordConfigured = false
	}
	body.TurnstileConfigured = s.secretConfigured(r.Context(), store.AppTurnstileSecretName)
	if turnstileSecret != "" {
		body.TurnstileConfigured = true
	}
	if !body.TurnstileEnabled {
		body.TurnstileConfigured = false
	}
	normalized, err := store.NormalizeAppSettings(body.AppSettings)
	if errors.Is(err, store.ErrInvalidSettings) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.update", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "invalid_settings"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_app_settings"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "app_settings_update_failed"})
		return
	}
	if smtpPassword != "" || !normalized.SMTPEnabled {
		status, err := s.secrets.UpdateSecret(r.Context(), store.AppSMTPPasswordSecretName, smtpPassword)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "smtp_password_update_failed"})
			return
		}
		normalized.SMTPPasswordConfigured = status.Configured
	} else {
		normalized.SMTPPasswordConfigured = s.secretConfigured(r.Context(), store.AppSMTPPasswordSecretName)
	}
	if turnstileSecret != "" || !normalized.TurnstileEnabled {
		status, err := s.secrets.UpdateSecret(r.Context(), store.AppTurnstileSecretName, turnstileSecret)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "turnstile_secret_update_failed"})
			return
		}
		normalized.TurnstileConfigured = status.Configured
	} else {
		normalized.TurnstileConfigured = s.secretConfigured(r.Context(), store.AppTurnstileSecretName)
	}
	settings, err := s.appSettings.UpdateAppSettings(r.Context(), normalized)
	if errors.Is(err, store.ErrInvalidSettings) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.update", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "invalid_settings"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_app_settings"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "app_settings_update_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.update", ResourceType: "app_settings", Result: "success"})
	writeJSON(w, http.StatusOK, s.appSettingsWithSecretStatus(r.Context(), settings))
}

func (s *Server) sendAppSettingsTestEmail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	to, ok := normalizeSMTPTestRecipient(body.To)
	if !ok {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "invalid_recipient"}})
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_email_recipient"})
		return
	}
	maskedTo := maskEmailAddress(to)
	settings, err := s.appSettings.GetAppSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "app_settings_failed"})
		return
	}
	settings = s.appSettingsWithSecretStatus(r.Context(), settings)
	if !settings.SMTPEnabled || strings.TrimSpace(settings.SMTPHost) == "" || strings.TrimSpace(settings.SMTPFrom) == "" {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "smtp_not_configured", "target": maskedTo}})
		writeJSON(w, http.StatusConflict, map[string]string{"code": "smtp_not_configured", "target": maskedTo})
		return
	}
	password := ""
	if settings.SMTPUsername != "" || settings.SMTPPasswordConfigured {
		password, err = s.secrets.GetSecretValue(r.Context(), store.AppSMTPPasswordSecretName)
		if errors.Is(err, store.ErrSecretKeyRequired) {
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "secret_encryption_key_required", "target": maskedTo}})
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required", "target": maskedTo})
			return
		}
		if err != nil {
			s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": "smtp_not_configured", "target": maskedTo}})
			writeJSON(w, http.StatusConflict, map[string]string{"code": "smtp_not_configured", "target": maskedTo})
			return
		}
	}
	appName := strings.TrimSpace(settings.AppName)
	if appName == "" {
		appName = "AutoStream"
	}
	message := MailMessage{
		To:      to,
		Subject: appName + " SMTPテスト",
		Text: appName + " Control Panel からのテストメールです。\n\n" +
			"送信を実行したユーザー: " + current.User.Username + "\n" +
			"送信日時: " + formatMailTimestamp(time.Now(), settings.Timezone) + "\n",
	}
	if err := s.mailer.Send(r.Context(), settings, password, message); err != nil {
		code := safeErrorCode(err)
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "failure", Metadata: map[string]any{"reason": code, "target": maskedTo}})
		writeJSON(w, smtpTestStatus(code), map[string]string{"code": code, "target": maskedTo})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "app.settings.test_email", ResourceType: "app_settings", Result: "success", Metadata: map[string]any{"target": maskedTo}})
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent", "target": maskedTo})
}

func (s *Server) appSettingsWithSecretStatus(ctx context.Context, settings store.AppSettings) store.AppSettings {
	settings.SMTPPasswordConfigured = s.secretConfigured(ctx, store.AppSMTPPasswordSecretName)
	settings.TurnstileConfigured = s.secretConfigured(ctx, store.AppTurnstileSecretName)
	return settings
}

func (s *Server) turnstileFailure(ctx context.Context, r *http.Request, token, action string) (int, string) {
	settings, err := s.appSettings.GetAppSettings(ctx)
	if err != nil {
		return http.StatusInternalServerError, "app_settings_failed"
	}
	settings = s.appSettingsWithSecretStatus(ctx, settings)
	if !settings.TurnstileEnabled {
		return 0, ""
	}
	if strings.TrimSpace(settings.TurnstileSiteKey) == "" || !settings.TurnstileConfigured {
		return http.StatusServiceUnavailable, "turnstile_not_configured"
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 2048 {
		return http.StatusForbidden, "turnstile_token_required"
	}
	secret, err := s.secrets.GetSecretValue(ctx, store.AppTurnstileSecretName)
	if errors.Is(err, store.ErrSecretKeyRequired) {
		return http.StatusServiceUnavailable, "secret_encryption_key_required"
	}
	if err != nil {
		return http.StatusServiceUnavailable, "turnstile_not_configured"
	}
	result, err := s.turnstile.Verify(ctx, TurnstileVerifyRequest{Secret: secret, Token: token, RemoteIP: clientIP(r)})
	if err != nil {
		if errors.Is(err, errTurnstileFailed) {
			return http.StatusForbidden, "turnstile_failed"
		}
		return http.StatusServiceUnavailable, "turnstile_unavailable"
	}
	if !result.Success {
		return http.StatusForbidden, "turnstile_failed"
	}
	if result.Action != "" && strings.TrimSpace(action) != "" && result.Action != strings.TrimSpace(action) {
		return http.StatusForbidden, "turnstile_failed"
	}
	return 0, ""
}

func normalizeSMTPTestRecipient(value string) (string, bool) {
	recipient := strings.TrimSpace(value)
	if recipient == "" || strings.ContainsAny(recipient, "\r\n\t") {
		return "", false
	}
	address, err := mail.ParseAddress(recipient)
	if err != nil || address.Name != "" || address.Address != recipient || strings.ContainsAny(address.Address, "\r\n\t") {
		return "", false
	}
	return address.Address, true
}

func maskEmailAddress(value string) string {
	local, domain, ok := strings.Cut(strings.TrimSpace(value), "@")
	if !ok || local == "" || domain == "" {
		return "masked"
	}
	runes := []rune(local)
	if len(runes) == 1 {
		return "*@" + domain
	}
	return string(runes[0]) + "***@" + domain
}

func smtpTestStatus(code string) int {
	switch code {
	case "smtp_not_configured", "smtp_requires_tls":
		return http.StatusConflict
	default:
		return http.StatusBadGateway
	}
}

func (s *Server) secretConfigured(ctx context.Context, name string) bool {
	statuses, err := s.secrets.ListSecretStatus(ctx)
	if err != nil {
		return false
	}
	for _, status := range statuses {
		if status.Name == name {
			return status.Configured
		}
	}
	return false
}

type versionInfoResponse struct {
	Service           string                               `json:"service"`
	Version           string                               `json:"version"`
	Commit            string                               `json:"commit"`
	BuildDate         string                               `json:"build_date"`
	LatestVersion     string                               `json:"latest_version,omitempty"`
	UpdateAvailable   bool                                 `json:"update_available"`
	UpdateCheckSource string                               `json:"update_check_source"`
	UpdateCheckError  string                               `json:"update_check_error,omitempty"`
	ServiceUpdates    map[string]serviceUpdateInfoResponse `json:"service_updates"`
}

type serviceUpdateInfoResponse struct {
	LatestVersion     string `json:"latest_version,omitempty"`
	UpdateCheckSource string `json:"update_check_source"`
	UpdateCheckError  string `json:"update_check_error,omitempty"`
}

type versionUpdateTarget struct {
	serviceType       string
	latestVersionEnv  string
	updateCheckURLEnv string
	defaultURL        string
}

const (
	defaultControlPanelUpdateCheckURL  = "https://api.github.com/repos/Kome-Lab/Autostream-ControlPanel/releases/latest"
	defaultWorkerUpdateCheckURL        = "https://api.github.com/repos/Kome-Lab/Autostream-Worker/releases/latest"
	defaultEncoderUpdateCheckURL       = "https://api.github.com/repos/Kome-Lab/Autostream-Encoder-Recorder/releases/latest"
	defaultDiscordBotUpdateCheckURL    = "https://api.github.com/repos/Kome-Lab/Autostream-DiscordBot/releases/latest"
	defaultObservabilityUpdateCheckURL = "https://api.github.com/repos/Kome-Lab/Autostream-Observability/releases/latest"
)

var controlPanelVersionUpdateTarget = versionUpdateTarget{
	serviceType:       "control-panel",
	latestVersionEnv:  "AUTOSTREAM_LATEST_VERSION",
	updateCheckURLEnv: "AUTOSTREAM_UPDATE_CHECK_URL",
	defaultURL:        defaultControlPanelUpdateCheckURL,
}

var nodeVersionUpdateTargets = []versionUpdateTarget{
	{serviceType: "worker", latestVersionEnv: "AUTOSTREAM_WORKER_LATEST_VERSION", updateCheckURLEnv: "AUTOSTREAM_WORKER_UPDATE_CHECK_URL", defaultURL: defaultWorkerUpdateCheckURL},
	{serviceType: "encoder_recorder", latestVersionEnv: "AUTOSTREAM_ENCODER_RECORDER_LATEST_VERSION", updateCheckURLEnv: "AUTOSTREAM_ENCODER_RECORDER_UPDATE_CHECK_URL", defaultURL: defaultEncoderUpdateCheckURL},
	{serviceType: "discord_bot", latestVersionEnv: "AUTOSTREAM_DISCORD_BOT_LATEST_VERSION", updateCheckURLEnv: "AUTOSTREAM_DISCORD_BOT_UPDATE_CHECK_URL", defaultURL: defaultDiscordBotUpdateCheckURL},
	{serviceType: "observability", latestVersionEnv: "AUTOSTREAM_OBSERVABILITY_LATEST_VERSION", updateCheckURLEnv: "AUTOSTREAM_OBSERVABILITY_UPDATE_CHECK_URL", defaultURL: defaultObservabilityUpdateCheckURL},
}

func (s *Server) versionInfo(w http.ResponseWriter, r *http.Request) {
	targets := make([]versionUpdateTarget, 0, len(nodeVersionUpdateTargets)+1)
	targets = append(targets, controlPanelVersionUpdateTarget)
	targets = append(targets, nodeVersionUpdateTargets...)
	updates := latestVersions(r.Context(), targets)
	panelUpdate := updates[controlPanelVersionUpdateTarget.serviceType]
	serviceUpdates := make(map[string]serviceUpdateInfoResponse, len(nodeVersionUpdateTargets))
	for _, target := range nodeVersionUpdateTargets {
		serviceUpdates[target.serviceType] = updates[target.serviceType]
	}
	writeJSON(w, http.StatusOK, versionInfoResponse{
		Service:           "control-panel",
		Version:           version.Current(),
		Commit:            version.Commit,
		BuildDate:         version.BuildDate,
		LatestVersion:     panelUpdate.LatestVersion,
		UpdateAvailable:   versionIsNewer(panelUpdate.LatestVersion, version.Current()),
		UpdateCheckSource: panelUpdate.UpdateCheckSource,
		UpdateCheckError:  panelUpdate.UpdateCheckError,
		ServiceUpdates:    serviceUpdates,
	})
}

func latestVersions(ctx context.Context, targets []versionUpdateTarget) map[string]serviceUpdateInfoResponse {
	results := make([]serviceUpdateInfoResponse, len(targets))
	var wait sync.WaitGroup
	wait.Add(len(targets))
	for index, target := range targets {
		go func(index int, target versionUpdateTarget) {
			defer wait.Done()
			latest, source, checkErr := latestVersion(ctx, target)
			results[index] = serviceUpdateInfoResponse{
				LatestVersion:     latest,
				UpdateCheckSource: source,
				UpdateCheckError:  checkErr,
			}
		}(index, target)
	}
	wait.Wait()

	updates := make(map[string]serviceUpdateInfoResponse, len(targets))
	for index, target := range targets {
		updates[target.serviceType] = results[index]
	}
	return updates
}

func latestVersion(ctx context.Context, target versionUpdateTarget) (string, string, string) {
	if latest := strings.TrimSpace(os.Getenv(target.latestVersionEnv)); latest != "" {
		return latest, "env", ""
	}
	rawURL := strings.TrimSpace(os.Getenv(target.updateCheckURLEnv))
	source := "url"
	if rawURL == "" {
		rawURL = target.defaultURL
		source = "github"
	} else if strings.EqualFold(rawURL, "disabled") || strings.EqualFold(rawURL, "off") || strings.EqualFold(rawURL, "false") {
		return "", "disabled", ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", source, "invalid update check url"
	}
	if parsed.User != nil {
		return "", source, "update check url must not include credentials"
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLocalUpdateCheckHost(parsed.Hostname())) {
		return "", source, "update check url must use https"
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", source, "create update check request failed"
	}
	req.Header.Set("Accept", "application/json, text/plain")
	req.Header.Set("User-Agent", "autostream-control-panel/"+version.Current())
	if token := strings.TrimSpace(os.Getenv("AUTOSTREAM_UPDATE_CHECK_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return "", source, "update check request failed"
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", source, "read update check response failed"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", source, "update check returned HTTP " + strconv.Itoa(resp.StatusCode)
	}
	if latest := parseLatestVersionResponse(body); latest != "" {
		return latest, source, ""
	}
	return "", source, "update check response did not include a version"
}

func parseLatestVersionResponse(body []byte) string {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err == nil {
		for _, key := range []string{"latest_version", "tag_name", "version", "name"} {
			if value, ok := decoded[key].(string); ok {
				if normalized := strings.TrimSpace(value); normalized != "" {
					return normalized
				}
			}
		}
	}
	return strings.TrimSpace(string(body))
}

func versionIsNewer(latest, current string) bool {
	latestParts, latestOK := parseVersionParts(latest)
	currentParts, currentOK := parseVersionParts(current)
	if !latestOK || !currentOK {
		return false
	}
	for i := 0; i < len(latestParts); i++ {
		if latestParts[i] > currentParts[i] {
			return true
		}
		if latestParts[i] < currentParts[i] {
			return false
		}
	}
	return false
}

func parseVersionParts(raw string) ([3]int, bool) {
	var parts [3]int
	trimmed := strings.TrimSpace(strings.TrimPrefix(raw, "v"))
	if trimmed == "" || trimmed == "dev" {
		return parts, false
	}
	for i, field := range strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == '.' || r == '-' || r == '+'
	}) {
		if i >= len(parts) {
			break
		}
		value, err := strconv.Atoi(field)
		if err != nil {
			return parts, false
		}
		parts[i] = value
		if i == len(parts)-1 {
			return parts, true
		}
	}
	return parts, false
}

func isLocalUpdateCheckHost(host string) bool {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	return normalized == "localhost" || normalized == "127.0.0.1" || normalized == "::1"
}

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

func (s *Server) secretStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.secrets.ListSecretStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "secret_status_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": status})
}

func (s *Server) updateSecret(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "secret_name_required"})
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	current := currentFromContext(r.Context())
	status, err := s.secrets.UpdateSecret(r.Context(), name, body.Value)
	if errors.Is(err, store.ErrUnknownSecret) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "secrets.update", ResourceType: "secret", ResourceID: name, Result: "failure", Metadata: map[string]any{"reason": "unknown_secret"}})
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "unknown_secret"})
		return
	}
	if errors.Is(err, store.ErrSecretKeyRequired) {
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "secrets.update", ResourceType: "secret", ResourceID: name, Result: "failure", Metadata: map[string]any{"reason": "secret_encryption_key_required", "configured": body.Value != ""}})
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "secret_encryption_key_required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "secret_update_failed"})
		return
	}
	s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: "secrets.update", ResourceType: "secret", ResourceID: name, Result: "success", Metadata: map[string]any{"configured": status.Configured}})
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) observabilityGet(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs, ok := s.observabilityClientForRequest(w, r)
		if !ok {
			return
		}
		body, err := obs.Get(r.Context(), endpoint)
		if err != nil {
			writeObservabilityProxyError(w, err)
			return
		}
		writeObservabilityJSON(w, http.StatusOK, endpoint, body)
	}
}

func (s *Server) observabilityMetrics(w http.ResponseWriter, r *http.Request) {
	localMetrics, localErr := s.serviceMetricSnapshots(r.Context())
	obs, configured, err := s.observabilityClient(r.Context())
	if err != nil {
		if localErr == nil && len(localMetrics) > 0 {
			writeJSON(w, http.StatusOK, localMetrics)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return
	}
	if !configured {
		if localErr == nil && len(localMetrics) > 0 {
			writeJSON(w, http.StatusOK, localMetrics)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return
	}
	body, err := obs.Get(r.Context(), "/metrics")
	if err != nil {
		if localErr == nil && len(localMetrics) > 0 {
			writeJSON(w, http.StatusOK, localMetrics)
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"code": "observability_request_failed"})
		return
	}
	if localErr != nil {
		writeObservabilityJSON(w, http.StatusOK, "/metrics", body)
		return
	}
	merged, err := mergeMetricSnapshots(sanitizeObservabilityResponse("/metrics", body), localMetrics)
	if err != nil {
		writeObservabilityJSON(w, http.StatusOK, "/metrics", body)
		return
	}
	writeJSON(w, http.StatusOK, merged)
}

func (s *Server) serviceMetricSnapshots(ctx context.Context) ([]map[string]any, error) {
	if s.services == nil {
		return nil, nil
	}
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	history, err := s.services.ListServiceMetricSnapshots(ctx, time.Now().UTC().Add(-3*time.Hour))
	if err == nil && len(history) > 0 {
		out := make([]map[string]any, 0, len(history))
		for _, snapshot := range history {
			out = append(out, map[string]any{
				"name":         snapshot.Name,
				"service_id":   snapshot.ServiceID,
				"service_type": snapshot.ServiceType,
				"status":       snapshot.Status,
				"value":        snapshot.Value,
				"updated_at":   snapshot.ObservedAt.UTC().Format(time.RFC3339Nano),
			})
		}
		return out, nil
	}
	out := make([]map[string]any, 0)
	for _, service := range services {
		if len(service.Metrics) == 0 {
			continue
		}
		metricNames := make([]string, 0, len(service.Metrics))
		for name := range service.Metrics {
			metricNames = append(metricNames, name)
		}
		sort.Strings(metricNames)
		updatedAt := service.UpdatedAt
		if service.LastHeartbeatAt != nil {
			updatedAt = *service.LastHeartbeatAt
		}
		for _, name := range metricNames {
			value, ok := serviceMetricNumber(service.Metrics[name])
			if !ok {
				continue
			}
			out = append(out, map[string]any{
				"name":         name,
				"service_id":   service.ServiceID,
				"service_type": service.ServiceType,
				"status":       service.Status,
				"value":        value,
				"updated_at":   updatedAt.UTC().Format(time.RFC3339Nano),
			})
		}
	}
	return out, nil
}

func mergeMetricSnapshots(upstream []byte, local []map[string]any) ([]map[string]any, error) {
	var merged []map[string]any
	if len(strings.TrimSpace(string(upstream))) > 0 {
		if err := json.Unmarshal(upstream, &merged); err != nil {
			return nil, err
		}
	}
	seen := map[string]struct{}{}
	for _, row := range merged {
		if key := metricSnapshotKey(row); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, row := range local {
		if key := metricSnapshotKey(row); key != "" {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
		}
		merged = append(merged, row)
	}
	return merged, nil
}

func metricSnapshotKey(row map[string]any) string {
	serviceID := jsonStringField(row, "service_id")
	streamID := jsonStringField(row, "stream_id")
	name := jsonStringField(row, "name")
	updatedAt := jsonStringField(row, "updated_at")
	if serviceID == "" || name == "" {
		return ""
	}
	return serviceID + "\x00" + streamID + "\x00" + name + "\x00" + updatedAt
}

func jsonStringField(row map[string]any, key string) string {
	value, _ := row[key].(string)
	return strings.TrimSpace(value)
}

func serviceMetricNumber(raw any) (float64, bool) {
	switch value := raw.(type) {
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case float32:
		return float64(value), true
	case float64:
		return value, true
	case json.Number:
		parsed, err := value.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (s *Server) observabilityGetAction(template string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs, ok := s.observabilityClientForRequest(w, r)
		if !ok {
			return
		}
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		body, err := obs.Get(r.Context(), endpoint)
		if err != nil {
			writeObservabilityProxyError(w, err)
			return
		}
		writeObservabilityJSON(w, http.StatusOK, endpoint, body)
	}
}

func (s *Server) observabilityPostAction(template string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs, ok := s.observabilityClientForRequest(w, r)
		if !ok {
			return
		}
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		body, err := obs.Post(r.Context(), endpoint, map[string]any{})
		if err != nil {
			writeObservabilityProxyError(w, err)
			return
		}
		current := currentFromContext(r.Context())
		action := "remediation.approve"
		if strings.HasSuffix(template, "/execute") {
			action = "remediation.execute"
		}
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: "remediation_action", ResourceID: r.PathValue("id"), Result: "success"})
		writeObservabilityJSON(w, http.StatusOK, endpoint, body)
	}
}

func (s *Server) observabilityPostActionWithAudit(template, action, resourceType string) http.HandlerFunc {
	return s.observabilityPostActionWithAuditStatus(template, action, resourceType, http.StatusOK)
}

func (s *Server) observabilityPostActionWithAuditStatus(template, action, resourceType string, successStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs, ok := s.observabilityClientForRequest(w, r)
		if !ok {
			return
		}
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		body, err := obs.Post(r.Context(), endpoint, map[string]any{})
		if err != nil {
			writeObservabilityProxyError(w, err)
			return
		}
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: resourceType, ResourceID: r.PathValue("id"), Result: "success"})
		writeObservabilityJSON(w, successStatus, endpoint, body)
	}
}

func (s *Server) observabilityPostProxy(endpoint, action, resourceType string) http.HandlerFunc {
	return s.observabilityPostProxyStatus(endpoint, action, resourceType, http.StatusOK)
}

func (s *Server) observabilityPostProxyStatus(endpoint, action, resourceType string, successStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.observabilityProxyJSONStatus(w, r, http.MethodPost, endpoint, action, resourceType, successStatus)
	}
}

func (s *Server) observabilityNotificationChannelPostProxyStatus(endpoint, action, resourceType string, successStatus int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.observabilityProxyJSONStatusWithTransform(w, r, http.MethodPost, endpoint, action, resourceType, successStatus, sanitizeNotificationChannelCreateProxyPayload)
	}
}

func (s *Server) observabilityPutProxy(template, action, resourceType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		s.observabilityProxyJSONStatus(w, r, http.MethodPut, endpoint, action, resourceType, http.StatusOK)
	}
}

func (s *Server) observabilityNotificationChannelPutProxy(template, action, resourceType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		s.observabilityProxyJSONStatusWithTransform(w, r, http.MethodPut, endpoint, action, resourceType, http.StatusOK, sanitizeNotificationChannelUpdateProxyPayload)
	}
}

func (s *Server) observabilityDeleteProxy(template, action, resourceType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		obs, ok := s.observabilityClientForRequest(w, r)
		if !ok {
			return
		}
		endpoint, err := replacePathID(template, r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_observability_id"})
			return
		}
		body, err := obs.Delete(r.Context(), endpoint)
		if err != nil {
			writeObservabilityProxyError(w, err)
			return
		}
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{ActorUserID: current.User.ID, ActorUsername: current.User.Username, Action: action, ResourceType: resourceType, ResourceID: r.PathValue("id"), Result: "success"})
		writeObservabilityJSON(w, http.StatusOK, endpoint, body)
	}
}

func (s *Server) observabilityProxyJSON(w http.ResponseWriter, r *http.Request, method, endpoint, action, resourceType string) {
	s.observabilityProxyJSONStatus(w, r, method, endpoint, action, resourceType, http.StatusOK)
}

func (s *Server) observabilityProxyJSONStatus(w http.ResponseWriter, r *http.Request, method, endpoint, action, resourceType string, successStatus int) {
	s.observabilityProxyJSONStatusWithTransform(w, r, method, endpoint, action, resourceType, successStatus, nil)
}

func (s *Server) observabilityProxyJSONStatusWithTransform(w http.ResponseWriter, r *http.Request, method, endpoint, action, resourceType string, successStatus int, transform func(map[string]any)) {
	obs, ok := s.observabilityClientForRequest(w, r)
	if !ok {
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request"})
		return
	}
	if transform != nil {
		transform(payload)
	}
	var (
		body json.RawMessage
		err  error
	)
	if method == http.MethodPut {
		body, err = obs.Put(r.Context(), endpoint, payload)
	} else {
		body, err = obs.Post(r.Context(), endpoint, payload)
	}
	if err != nil {
		status, code := observabilityProxyError(err)
		current := currentFromContext(r.Context())
		s.writeAudit(r, store.AuditEvent{
			ActorUserID:   current.User.ID,
			ActorUsername: current.User.Username,
			Action:        action,
			ResourceType:  resourceType,
			ResourceID:    r.PathValue("id"),
			Result:        "failure",
			Metadata: map[string]any{
				"reason":            code,
				"has_webhook_url":   payload["webhook_url"] != nil,
				"has_smtp_password": payload["smtp_password"] != nil,
			},
		})
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	current := currentFromContext(r.Context())
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   current.User.ID,
		ActorUsername: current.User.Username,
		Action:        action,
		ResourceType:  resourceType,
		ResourceID:    r.PathValue("id"),
		Result:        "success",
		Metadata: map[string]any{
			"has_webhook_url":   payload["webhook_url"] != nil,
			"has_smtp_password": payload["smtp_password"] != nil,
		},
	})
	writeObservabilityJSON(w, successStatus, endpoint, body)
}

func sanitizeNotificationChannelCreateProxyPayload(payload map[string]any) {
	for key := range payload {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "smtp_") {
			delete(payload, key)
		}
	}
	channelType, _ := payload["type"].(string)
	if strings.EqualFold(strings.TrimSpace(channelType), "email") {
		payload["uses_global_smtp"] = true
	}
}

func sanitizeNotificationChannelUpdateProxyPayload(payload map[string]any) {
	for key := range payload {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if strings.HasPrefix(normalized, "smtp_") || normalized == "uses_global_smtp" {
			delete(payload, key)
		}
	}
}

var publicObservabilityErrorCodes = map[string]struct{}{
	"bad_request":                       {},
	"event_type_required":               {},
	"incident_context_missing":          {},
	"invalid_incident_status":           {},
	"invalid_notification_channel":      {},
	"invalid_notification_event":        {},
	"invalid_smtp_channel":              {},
	"invalid_webhook_url":               {},
	"not_found":                         {},
	"remediation_action_not_executable": {},
	"remediation_action_terminal":       {},
	"stream_context_missing":            {},
}

func writeObservabilityProxyError(w http.ResponseWriter, err error) {
	status, code := observabilityProxyError(err)
	writeJSON(w, status, map[string]string{"code": code})
}

func observabilityProxyError(err error) (int, string) {
	var upstream *observability.ResponseError
	if !errors.As(err, &upstream) {
		return http.StatusBadGateway, "observability_request_failed"
	}
	if upstream.StatusCode == http.StatusUnauthorized || upstream.StatusCode == http.StatusForbidden {
		return http.StatusBadGateway, "observability_auth_failed"
	}
	if upstream.StatusCode == http.StatusServiceUnavailable {
		if upstream.Code == "secret_encryption_key_required" {
			return http.StatusServiceUnavailable, upstream.Code
		}
		return http.StatusServiceUnavailable, "observability_unavailable"
	}
	if upstream.StatusCode == http.StatusTooManyRequests {
		return http.StatusTooManyRequests, "observability_rate_limited"
	}
	if upstream.StatusCode >= 400 && upstream.StatusCode < 500 {
		code := "observability_request_rejected"
		if _, ok := publicObservabilityErrorCodes[upstream.Code]; ok {
			code = upstream.Code
		}
		return upstream.StatusCode, code
	}
	return http.StatusBadGateway, "observability_request_failed"
}

func (s *Server) observabilityClientForRequest(w http.ResponseWriter, r *http.Request) (observability.Client, bool) {
	client, ok, err := s.observabilityClient(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return observability.Client{}, false
	}
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "observability_not_configured"})
		return observability.Client{}, false
	}
	return client, true
}

func (s *Server) observabilityClient(ctx context.Context) (observability.Client, bool, error) {
	if s.services == nil {
		return observability.Client{}, false, nil
	}
	services, err := s.services.ListServices(ctx)
	if err != nil {
		return observability.Client{}, false, err
	}
	for _, service := range services {
		if service.ServiceType != "observability" || strings.TrimSpace(service.PublicURL) == "" || strings.TrimSpace(service.Status) == "pending" {
			continue
		}
		token, err := nodeRuntimeToken(service)
		if err != nil {
			return observability.Client{}, false, err
		}
		client := observability.Client{
			BaseURL: service.PublicURL,
			Token:   token,
			Timeout: s.obs.Timeout,
			HTTP:    s.obs.HTTP,
		}
		if client.Timeout <= 0 {
			client.Timeout = 5 * time.Second
		}
		return client, true, nil
	}
	return observability.Client{}, false, nil
}

func nodeRuntimeToken(service store.RegisteredService) (string, error) {
	if strings.TrimSpace(service.NodeTokenCiphertext) == "" || strings.TrimSpace(service.NodeTokenNonce) == "" {
		return "", errors.New("node runtime token is not configured")
	}
	key, err := nodeRuntimeTokenEncryptionKey()
	if err != nil {
		return "", err
	}
	token, err := security.DecryptSecret(service.NodeTokenCiphertext, service.NodeTokenNonce, key)
	if err != nil || strings.TrimSpace(token) == "" {
		return "", errors.New("node runtime token could not be decrypted")
	}
	return token, nil
}

func replacePathID(template, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", errors.New("invalid path id")
	}
	return strings.ReplaceAll(template, "{id}", id), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOneTimeSecretJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeJSON(w, status, value)
}

func writeRawJSON(w http.ResponseWriter, status int, body json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(redactRawJSON(body))
}

func writeObservabilityJSON(w http.ResponseWriter, status int, endpoint string, body json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(sanitizeObservabilityResponse(endpoint, body))
}

func sanitizeObservabilityResponse(endpoint string, body json.RawMessage) []byte {
	endpoint = strings.TrimSpace(endpoint)
	switch {
	case endpoint == "/notification-channels":
		return sanitizeNotificationChannelBody(body)
	case strings.HasPrefix(endpoint, "/notification-channels/") && strings.HasSuffix(endpoint, "/test"):
		return sanitizeNotificationDeliveryBody(body)
	case strings.HasPrefix(endpoint, "/notification-channels/"):
		return sanitizeNotificationChannelBody(body)
	case endpoint == "/notification-deliveries":
		return sanitizeNotificationDeliveryBody(body)
	default:
		return redactRawJSON(body)
	}
}

func sanitizeNotificationChannelBody(body json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return []byte(`{"code":"invalid_upstream_json"}`)
	}
	return marshalSanitizedObservabilityValue(value, publicNotificationChannelFromValue)
}

func sanitizeNotificationDeliveryBody(body json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return []byte(`{"code":"invalid_upstream_json"}`)
	}
	return marshalSanitizedObservabilityValue(value, publicNotificationDeliveryFromValue)
}

func marshalSanitizedObservabilityValue(value any, project func(map[string]any) map[string]any) []byte {
	switch typed := value.(type) {
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok {
				out = append(out, project(row))
			}
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			return []byte(`[]`)
		}
		return encoded
	case map[string]any:
		encoded, err := json.Marshal(project(typed))
		if err != nil {
			return []byte(`{}`)
		}
		return encoded
	default:
		return redactRawJSON(mustMarshalRaw(value))
	}
}

func publicNotificationChannelFromValue(row map[string]any) map[string]any {
	out := map[string]any{}
	copyAllowedJSONField(out, row, "id")
	copyAllowedJSONField(out, row, "name")
	copyAllowedJSONField(out, row, "type")
	copyAllowedJSONField(out, row, "enabled")
	copyAllowedJSONField(out, row, "uses_global_smtp")
	copyAllowedJSONField(out, row, "masked_webhook_url")
	copyAllowedJSONField(out, row, "smtp_password_configured")
	copyAllowedJSONField(out, row, "masked_email_target")
	copyAllowedJSONField(out, row, "severity_filter")
	copyAllowedJSONField(out, row, "event_type_filter")
	copyAllowedJSONField(out, row, "created_at")
	copyAllowedJSONField(out, row, "updated_at")
	return redactAllowedJSONValues(out)
}

func publicNotificationDeliveryFromValue(row map[string]any) map[string]any {
	out := map[string]any{}
	copyAllowedJSONField(out, row, "id")
	copyAllowedJSONField(out, row, "event_type")
	copyAllowedJSONField(out, row, "channel")
	copyAllowedJSONField(out, row, "incident_id")
	copyAllowedJSONField(out, row, "status")
	copyAllowedJSONField(out, row, "target")
	copyAllowedJSONField(out, row, "error")
	copyAllowedJSONField(out, row, "metadata")
	copyAllowedJSONField(out, row, "created_at")
	return redactAllowedJSONValues(out)
}

func copyAllowedJSONField(out, row map[string]any, key string) {
	if value, ok := row[key]; ok {
		out[key] = value
	}
}

func redactAllowedJSONValues(value map[string]any) map[string]any {
	for key, nested := range value {
		if secretResponseKey(key) {
			delete(value, key)
			continue
		}
		value[key] = redactJSONValueForResponse(nested)
	}
	return value
}

func mustMarshalRaw(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return json.RawMessage(encoded)
}

func redactRawJSON(body json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return []byte(`{"code":"invalid_upstream_json"}`)
	}
	value = redactJSONValueForResponse(value)
	redacted, err := json.Marshal(value)
	if err != nil {
		return body
	}
	return redacted
}

func redactJSONValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if secretResponseKey(key) {
				delete(typed, key)
				continue
			}
			typed[key] = redactJSONValueForResponse(nested)
		}
	case []any:
		for i, nested := range typed {
			typed[i] = redactJSONValueForResponse(nested)
		}
	}
}

func redactJSONValueForResponse(value any) any {
	switch typed := value.(type) {
	case string:
		if secretResponseValue(typed) {
			return "<redacted>"
		}
		return typed
	case map[string]any:
		redactJSONValue(typed)
		return typed
	case []any:
		redactJSONValue(typed)
		return typed
	default:
		return value
	}
}

func secretResponseKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	allowedMaskedKeys := []string{"masked_webhook_url", "masked_email_target", "configured", "fingerprint", "has_webhook_url", "has_smtp_password", "smtp_password_configured"}
	for _, allowed := range allowedMaskedKeys {
		if normalized == allowed {
			return false
		}
	}
	secretTokens := []string{"webhook_url", "token", "secret", "password", "private_key", "credential", "authorization", "stream_key", "refresh_token", "access_token", "folder_id", "drive_folder_id", "google_drive_folder_id", "gdrive_folder_id", "email_recipients", "smtp_host", "smtp_port", "smtp_tls", "smtp_from", "smtp_username"}
	for _, token := range secretTokens {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func secretResponseValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, "<WEBHOOK_PATH>") || strings.Contains(trimmed, "****") {
		return false
	}
	lower := strings.ToLower(trimmed)
	patterns := []string{
		"ast_svc_",
		"ast_ingest_v1.",
		"ya29.",
		"discord.com/api/webhooks/",
		"hooks.slack.com/services/",
		"token=",
		"api_key=",
		"apikey=",
		"client_secret=",
		"stream_key=",
		"passphrase=",
		"password=",
		"secret=",
		"access_token",
		"refresh_token",
		"authorization",
		"bearer ",
		"private_key",
		"credential",
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	if strings.Contains(lower, "://") {
		afterScheme := lower[strings.Index(lower, "://")+3:]
		if at := strings.Index(afterScheme, "@"); at >= 0 {
			userInfo := afterScheme[:at]
			return strings.Contains(userInfo, ":")
		}
	}
	return false
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

type currentUser struct {
	User        store.User
	Session     store.Session
	Permissions []string
}

type currentUserKey struct{}

func (s *Server) requirePermission(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isOAuthCallbackPath(r.URL.Path) {
			setOAuthCallbackNoStoreHeaders(w)
		}
		current, ok := s.authenticate(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		if isUnsafeMethod(r.Method) && !security.VerifyTokenHash(r.Header.Get("X-CSRF-Token"), current.Session.CSRFTokenHash) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "csrf_failed"})
			return
		}
		if current.User.Status == "pending_password_change" && !isPasswordChangeAllowedPath(r.URL.Path) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "password_change_required"})
			return
		}
		if permission != "" && !security.HasPermission(current.Permissions, permission) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
			return
		}
		ctx := context.WithValue(r.Context(), currentUserKey{}, current)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func (s *Server) requireAnyPermission(permissions []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isOAuthCallbackPath(r.URL.Path) {
			setOAuthCallbackNoStoreHeaders(w)
		}
		current, ok := s.authenticate(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized"})
			return
		}
		if isUnsafeMethod(r.Method) && !security.VerifyTokenHash(r.Header.Get("X-CSRF-Token"), current.Session.CSRFTokenHash) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "csrf_failed"})
			return
		}
		if current.User.Status == "pending_password_change" && !isPasswordChangeAllowedPath(r.URL.Path) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "password_change_required"})
			return
		}
		allowed := len(permissions) == 0
		for _, permission := range permissions {
			if permission != "" && security.HasPermission(current.Permissions, permission) {
				allowed = true
				break
			}
		}
		if !allowed {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "permission_denied"})
			return
		}
		ctx := context.WithValue(r.Context(), currentUserKey{}, current)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func (s *Server) authenticate(r *http.Request) (currentUser, bool) {
	if s.auth == nil {
		return currentUser{}, false
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return currentUser{}, false
	}
	session, err := s.auth.GetSession(r.Context(), cookie.Value)
	if err != nil {
		return currentUser{}, false
	}
	user, err := s.auth.GetUser(r.Context(), session.UserID)
	if err != nil || user.Status == "disabled" || user.Status == "locked" {
		return currentUser{}, false
	}
	permissions, err := s.auth.GetUserPermissions(r.Context(), user.ID)
	if err != nil {
		return currentUser{}, false
	}
	return currentUser{User: user, Session: session, Permissions: permissions}, true
}

func (s *Server) authenticateService(w http.ResponseWriter, r *http.Request, requiredScope string) (store.ServiceToken, bool) {
	if s.services == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "service_registry_not_configured"})
		return store.ServiceToken{}, false
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(raw) == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "missing_service_token"})
		return store.ServiceToken{}, false
	}
	token, err := s.services.AuthenticateServiceToken(r.Context(), strings.TrimSpace(raw), requiredScope)
	if errors.Is(err, store.ErrForbidden) {
		writeJSON(w, http.StatusForbidden, map[string]string{"code": "missing_service_scope"})
		return store.ServiceToken{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_service_token"})
		return store.ServiceToken{}, false
	}
	return token, true
}

func currentFromContext(ctx context.Context) currentUser {
	current, _ := ctx.Value(currentUserKey{}).(currentUser)
	return current
}

func userHasRoleName(user store.User, roleName string) bool {
	for _, role := range user.Roles {
		if role == roleName {
			return true
		}
	}
	return false
}

func isUnsafeMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func isPasswordChangeAllowedPath(path string) bool {
	switch path {
	case "/auth/me", "/auth/change-password", "/auth/logout":
		return true
	default:
		return false
	}
}

func isOAuthCallbackPath(path string) bool {
	return path == "/auth/oauth/callback" || path == "/integrations/oauth-accounts/callback"
}

func publicUser(user store.User) map[string]any {
	roles := append([]string{}, user.Roles...)
	roleIDs := append([]string{}, user.RoleIDs...)
	return map[string]any{"id": user.ID, "username": user.Username, "email": user.Email, "status": user.Status, "roles": roles, "role_ids": roleIDs, "last_login_at": user.LastLoginAt, "last_login_ip": user.LastLoginIP}
}

func publicUsers(users []store.User) []map[string]any {
	out := make([]map[string]any, 0, len(users))
	for _, user := range users {
		out = append(out, publicUser(user))
	}
	return out
}

func publicOAuthLoginProvider(provider store.OAuthProvider) map[string]any {
	return map[string]any{
		"id":            provider.ID,
		"provider_type": provider.ProviderType,
		"name":          provider.Name,
		"scopes":        loginOAuthScopes(provider.ProviderType),
		"redirect_uri":  provider.RedirectURI,
	}
}

func supportedLoginOAuthProvider(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "google", "github", "discord":
		return true
	default:
		return false
	}
}

func supportedConnectedAccountProvider(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "google":
		return true
	default:
		return false
	}
}

func validateOAuthProviderRequest(body oauthProviderRequest) string {
	providerType := strings.ToLower(strings.TrimSpace(body.ProviderType))
	if !supportedLoginOAuthProvider(providerType) {
		return "invalid_oauth_provider_type"
	}
	if !validOAuthRedirectURI(body.RedirectURI, "/auth/oauth/callback") {
		return "oauth_redirect_uri_invalid"
	}
	return ""
}

func oauthProviderRequestScopes(providerType, redirectURI string, requested []string) []string {
	_ = redirectURI
	_ = requested
	return loginOAuthScopes(providerType)
}

func loginOAuthScopes(providerType string) []string {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "google":
		return []string{"openid", "email", "profile"}
	case "github":
		return []string{"read:user", "user:email"}
	case "discord":
		return []string{"identify", "email"}
	default:
		return []string{}
	}
}

func oauthAccountRequestedScopes(purpose string) ([]string, string) {
	base := []string{"openid", "email", "profile"}
	switch normalizeOAuthAccountPurpose(purpose) {
	case "drive":
		return append(base, "https://www.googleapis.com/auth/drive.file"), ""
	case "youtube":
		return append(base, "https://www.googleapis.com/auth/youtube.force-ssl"), ""
	case "drive_youtube":
		return append(base, "https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/youtube.force-ssl"), ""
	default:
		return nil, "invalid_oauth_account_purpose"
	}
}

func normalizeOAuthAccountPurpose(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "drive_youtube", "both", "all", "stream_archive", "archive_youtube":
		return "drive_youtube"
	case "drive", "archive", "google_drive":
		return "drive"
	case "youtube", "youtube_live":
		return "youtube"
	default:
		return "invalid"
	}
}

func cleanRequestStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func oauthScopesContainConnectedAccountAccess(scopes []string) bool {
	return store.OAuthAccountPurposeFromScopes(scopes) != store.OAuthAccountPurposeUnknown
}

func oauthAccountIdentityChanged(body oauthAccountRequest, existing store.OAuthAccount) bool {
	if value := strings.TrimSpace(body.ProviderID); value != "" && value != existing.ProviderID {
		return true
	}
	if value := strings.TrimSpace(body.ProviderType); value != "" && !strings.EqualFold(value, existing.ProviderType) {
		return true
	}
	if value := strings.TrimSpace(body.Subject); value != "" && value != existing.Subject {
		return true
	}
	if value := strings.TrimSpace(body.Email); value != "" && !strings.EqualFold(value, existing.Email) {
		return true
	}
	if len(body.Scopes) > 0 && !sameStringSet(body.Scopes, existing.Scopes) {
		return true
	}
	return false
}

func sameStringSet(left, right []string) bool {
	seen := map[string]int{}
	for _, item := range left {
		value := strings.TrimSpace(item)
		if value == "" {
			continue
		}
		seen[value]++
	}
	for _, item := range right {
		value := strings.TrimSpace(item)
		if value == "" {
			continue
		}
		seen[value]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

func (s *Server) validateOAuthAccountRequest(ctx context.Context, body oauthAccountRequest) (string, int) {
	provider, err := s.integrations.GetOAuthProvider(ctx, body.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return "oauth_provider_not_found", http.StatusNotFound
	}
	if err != nil {
		return "get_oauth_provider_failed", http.StatusInternalServerError
	}
	if strings.TrimSpace(body.ProviderType) != "" && !strings.EqualFold(body.ProviderType, provider.ProviderType) {
		return "oauth_provider_type_mismatch", http.StatusBadRequest
	}
	if !supportedConnectedAccountProvider(provider.ProviderType) {
		return "oauth_connected_account_provider_unsupported", http.StatusBadRequest
	}
	if !oauthScopesContainConnectedAccountAccess(body.Scopes) {
		return "oauth_connected_account_scope_required", http.StatusBadRequest
	}
	return "", 0
}

func (s *Server) validateDriveDestinationRequest(ctx context.Context, body driveDestinationRequest) (string, int) {
	if strings.EqualFold(strings.TrimSpace(body.AuthMode), "oauth2") {
		account, err := s.integrations.GetOAuthAccount(ctx, body.OAuthAccountID)
		if errors.Is(err, store.ErrNotFound) {
			return "oauth_account_not_found", http.StatusNotFound
		}
		if err != nil {
			return "get_oauth_account_failed", http.StatusInternalServerError
		}
		if !strings.EqualFold(account.ProviderType, "google") {
			return "drive_destination_oauth_account_not_google", http.StatusBadRequest
		}
		if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive) {
			return "drive_destination_drive_scope_required", http.StatusBadRequest
		}
	}
	return "", 0
}

func normalizeDriveDestinationAPIRequest(body *driveDestinationRequest) (string, int) {
	authMode := strings.TrimSpace(body.AuthMode)
	if authMode != "" && !strings.EqualFold(authMode, "oauth2") {
		return "drive_destination_auth_mode_unsupported", http.StatusBadRequest
	}
	body.AuthMode = "oauth2"
	return "", 0
}

func (s *Server) validateYouTubeOutputOAuthAccount(ctx context.Context, config map[string]any) (string, int) {
	mode := normalizedYouTubeOutputMode(configString(config, "mode"))
	if mode != "live_api" && mode != "live_api_dry_run" {
		return "", 0
	}
	accountID := firstNonEmpty(configString(config, "oauth_account_id"), configString(config, "youtube_oauth_account_id"))
	account, err := s.integrations.GetOAuthAccount(ctx, accountID)
	if errors.Is(err, store.ErrNotFound) {
		return "oauth_account_not_found", http.StatusNotFound
	}
	if err != nil {
		return "get_oauth_account_failed", http.StatusInternalServerError
	}
	if !strings.EqualFold(account.ProviderType, "google") {
		return "youtube_output_oauth_account_not_google", http.StatusBadRequest
	}
	if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeYouTube) {
		return "youtube_output_youtube_scope_required", http.StatusBadRequest
	}
	return "", 0
}

func oauthAuthorizationURL(provider store.OAuthProvider, state store.OAuthLoginState) (string, error) {
	var endpoint string
	values := url.Values{}
	values.Set("client_id", provider.ClientID)
	values.Set("redirect_uri", provider.RedirectURI)
	values.Set("response_type", "code")
	values.Set("state", state.StateToken)
	scopes := loginOAuthScopes(provider.ProviderType)
	switch strings.ToLower(strings.TrimSpace(provider.ProviderType)) {
	case "google":
		endpoint = "https://accounts.google.com/o/oauth2/v2/auth"
		values.Set("nonce", state.Nonce)
	case "github":
		endpoint = "https://github.com/login/oauth/authorize"
	case "discord":
		endpoint = "https://discord.com/oauth2/authorize"
	default:
		return "", errors.New("unsupported oauth provider")
	}
	values.Set("scope", strings.Join(scopes, " "))
	return endpoint + "?" + values.Encode(), nil
}

func oauthConnectedAccountAuthorizationURL(provider store.OAuthProvider, state store.OAuthLoginState) (string, error) {
	providerType := strings.ToLower(strings.TrimSpace(provider.ProviderType))
	if providerType != "google" {
		return "", errors.New("unsupported connected account provider")
	}
	values := url.Values{}
	values.Set("client_id", provider.ClientID)
	values.Set("redirect_uri", provider.RedirectURI)
	values.Set("response_type", "code")
	values.Set("state", state.StateToken)
	values.Set("nonce", state.Nonce)
	values.Set("access_type", "offline")
	values.Set("prompt", "consent")
	scopes := state.RequestedScopes
	if len(scopes) == 0 {
		scopes, _ = oauthAccountRequestedScopes("")
	}
	values.Set("scope", strings.Join(scopes, " "))
	return "https://accounts.google.com/o/oauth2/v2/auth?" + values.Encode(), nil
}

func connectedOAuthRedirectURI(r *http.Request) string {
	base := strings.TrimRight(panelBaseURL(r), "/")
	if base == "" {
		return ""
	}
	return base + "/integrations/oauth-accounts/callback"
}

func connectedAccountOAuthRedirectURI(r *http.Request, provider store.OAuthProvider) (string, string) {
	redirectURI := strings.TrimSpace(provider.RedirectURI)
	if validOAuthRedirectURI(redirectURI, "/auth/oauth/callback") || validOAuthRedirectURI(redirectURI, "/integrations/oauth-accounts/callback") {
		return redirectURI, ""
	}
	if redirectURI == "" {
		legacyRedirectURI := connectedOAuthRedirectURI(r)
		if validOAuthRedirectURI(legacyRedirectURI, "/integrations/oauth-accounts/callback") {
			return legacyRedirectURI, ""
		}
	}
	return "", "oauth_connected_account_redirect_uri_unavailable"
}

func validOAuthRedirectURI(raw, expectedPath string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return false
	}
	if parsed.User != nil || parsed.Path != expectedPath || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	publicURL := strings.TrimSpace(os.Getenv("AUTOSTREAM_PUBLIC_URL"))
	if publicURL == "" {
		return isLocalOAuthRedirectHost(parsed.Hostname())
	}
	publicParsed, err := url.Parse(publicURL)
	if err != nil || publicParsed.Scheme == "" || publicParsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Scheme, publicParsed.Scheme) && strings.EqualFold(parsed.Host, publicParsed.Host)
}

func isLocalOAuthRedirectHost(host string) bool {
	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if normalized == "localhost" || strings.HasSuffix(normalized, ".localhost") {
		return true
	}
	ip := net.ParseIP(normalized)
	return ip != nil && ip.IsLoopback()
}

func safeRedirectAfter(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return ""
	}
	decoded, err := url.PathUnescape(value)
	if err != nil || unsafeRedirectCharacters(value) || unsafeRedirectCharacters(decoded) {
		return ""
	}
	return value
}

func unsafeRedirectCharacters(value string) bool {
	for _, char := range value {
		if char < 0x20 || (char >= 0x7f && char <= 0x9f) || char == '\\' {
			return true
		}
	}
	return false
}

func emailAllowedForProvider(email string, allowedDomains []string) bool {
	if len(allowedDomains) == 0 {
		return true
	}
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return false
	}
	domain := email[at+1:]
	for _, allowed := range allowedDomains {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		if domain == allowed {
			return true
		}
	}
	return false
}

func identityAllowedForProvider(provider store.OAuthProvider, identity oauthlogin.Identity) bool {
	if len(provider.AllowedDomains) == 0 {
		return true
	}
	if !identity.EmailVerified {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(provider.ProviderType), "google") && strings.TrimSpace(identity.HostedDomain) != "" {
		return domainAllowedForProvider(identity.HostedDomain, provider.AllowedDomains)
	}
	return emailAllowedForProvider(identity.Email, provider.AllowedDomains)
}

func domainAllowedForProvider(domain string, allowedDomains []string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return false
	}
	for _, allowed := range allowedDomains {
		if domain == strings.ToLower(strings.TrimSpace(allowed)) {
			return true
		}
	}
	return false
}

func (s *Server) writeAudit(r *http.Request, event store.AuditEvent) {
	if s.audit == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.RequestID == "" {
		event.RequestID = requestID()
	}
	if event.ActorIP == "" {
		event.ActorIP = clientIP(r)
	}
	if event.UserAgent == "" {
		event.UserAgent = r.UserAgent()
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	current := currentFromContext(r.Context())
	if event.ActorUserID == "" {
		event.ActorUserID = current.User.ID
	}
	if event.ActorUsername == "" {
		event.ActorUsername = current.User.Username
	}
	if err := s.audit.WriteAudit(r.Context(), event); err != nil {
		log.Printf("audit write failed: action=%s resource_type=%s result=%s error=%v", event.Action, event.ResourceType, event.Result, err)
		return
	}
	s.notifyAdminAuditEvent(event)
}

func (s *Server) writeSystemAudit(ctx context.Context, event store.AuditEvent) {
	if s.audit == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.ActorUsername == "" {
		event.ActorUsername = "system"
	}
	if event.RequestID == "" {
		event.RequestID = requestID()
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	if err := s.audit.WriteAudit(ctx, event); err != nil {
		log.Printf("system audit write failed: action=%s resource_type=%s result=%s error=%v", event.Action, event.ResourceType, event.Result, err)
		return
	}
	s.notifyAdminAuditEvent(event)
}

func (s *Server) notifyAdminAuditEvent(event store.AuditEvent) {
	if !adminAuditEventNotificationAllowed(event) {
		return
	}
	redacted := store.RedactAuditEvent(event)
	payload := map[string]any{
		"event_type":     "admin.audit",
		"severity":       adminAuditNotificationSeverity(redacted),
		"status":         strings.TrimSpace(redacted.Result),
		"action":         strings.TrimSpace(redacted.Action),
		"resource_type":  strings.TrimSpace(redacted.ResourceType),
		"resource_id":    strings.TrimSpace(redacted.ResourceID),
		"actor_username": strings.TrimSpace(redacted.ActorUsername),
		"summary":        adminAuditNotificationSummary(redacted),
		"timestamp":      redacted.Timestamp.UTC().Format(time.RFC3339),
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), adminAuditNotificationTimeout)
		defer cancel()
		obs, configured, err := s.observabilityClient(ctx)
		if err != nil || !configured {
			return
		}
		if _, err := obs.Post(ctx, "/notification-events", payload); err != nil {
			log.Printf("admin audit notification failed: action=%s resource_type=%s result=%s error=%v", redacted.Action, redacted.ResourceType, redacted.Result, err)
		}
	}()
}

func adminAuditEventNotificationAllowed(event store.AuditEvent) bool {
	action := strings.ToLower(strings.TrimSpace(event.Action))
	if action == "" {
		return false
	}
	actorUserID := strings.ToLower(strings.TrimSpace(event.ActorUserID))
	actorUsername := strings.ToLower(strings.TrimSpace(event.ActorUsername))
	if strings.HasPrefix(actorUserID, "service:") || strings.HasPrefix(actorUsername, "service:") {
		return false
	}
	if strings.TrimSpace(event.ActorUserID) != "" {
		return true
	}
	return actorUsername == "system"
}

func adminAuditNotificationSeverity(event store.AuditEvent) string {
	result := strings.ToLower(strings.TrimSpace(event.Result))
	if result != "" && result != "success" && result != "ok" {
		return "warning"
	}
	action := strings.ToLower(strings.TrimSpace(event.Action))
	if strings.HasPrefix(action, "security.") || strings.HasPrefix(action, "secrets.") || strings.HasPrefix(action, "users.delete") || strings.HasPrefix(action, "roles.delete") {
		return "warning"
	}
	return "info"
}

func adminAuditNotificationSummary(event store.AuditEvent) string {
	action := strings.TrimSpace(event.Action)
	if action == "" {
		action = "admin action"
	}
	result := strings.TrimSpace(event.Result)
	if result == "" {
		result = "recorded"
	}
	return "管理イベント: " + action + " / " + result
}

func (s *Server) writeServiceAudit(r *http.Request, token store.ServiceToken, action, resourceType, resourceID, result string, metadata map[string]any) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["service_type"] = token.ServiceType
	s.writeAudit(r, store.AuditEvent{
		ActorUserID:   "service:" + token.ServiceType,
		ActorUsername: token.ServiceType,
		Action:        action,
		ResourceType:  resourceType,
		ResourceID:    resourceID,
		Result:        result,
		Metadata:      metadata,
	})
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
