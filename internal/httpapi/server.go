package httpapi

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/oauthlogin"
	"github.com/example/autostream-control-panel/internal/observability"
	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
	"github.com/example/autostream-control-panel/internal/videocover"
	ytlive "github.com/example/autostream-control-panel/internal/youtube"
	"golang.org/x/oauth2"
)

const (
	sessionCookieName            = "autostream_session"
	oauthStateCookieName         = "autostream_oauth_state"
	maxControlRequestBytes       = 1 << 20
	defaultNodeConfigureTokenTTL = 24 * time.Hour
	minStreamIngestSigningKeyLen = 32
	minSecretEncryptionKeyLen    = 32
)

type Server struct {
	mux                      *http.ServeMux
	handler                  http.Handler
	streams                  store.StreamStore
	auth                     store.AuthStore
	audit                    store.AuditStore
	users                    store.UserAdminStore
	roles                    store.RoleStore
	services                 store.ServiceRegistryStore
	profiles                 store.ProfileStore
	integrations             store.IntegrationStore
	settings                 store.SecuritySettingsStore
	appSettings              store.AppSettingsStore
	secrets                  store.SecretStore
	runtimeLeases            store.RuntimeSecretLeaseStore
	remediation              store.RemediationExecutionStore
	systemUpdates            store.SystemUpdateStore
	updaterPolicies          store.UpdaterPolicyAdminStore
	hostSelfUpdateReleases   HostSelfUpdateReleaseResolver
	updateHostBootstrapJobs  *UpdateHostBootstrapBroker
	systemUpdateOperationMu  sync.Mutex
	streamLifecycleMu        sync.Mutex
	streamLifecycleLocks     map[string]*streamLifecycleLock
	youtubeIngestHealthMu    sync.Mutex
	youtubeIngestHealthState map[string]string
	mfa                      store.MFAStore
	emailChanges             store.EmailChangeStore
	passkeys                 store.PasskeyStore
	avatars                  store.UserAvatarStore
	oauthLogin               store.OAuthLoginStore
	oauthVerifier            oauthlogin.Verifier
	oauthConnector           oauthlogin.Connector
	mailer                   Mailer
	turnstile                TurnstileVerifier
	obs                      observability.Client
	dispatcher               serviceDispatcher
	youtubeLive              ytlive.LiveClient
	setupToken               string
	previewSigningKey        string
	loginFailures            *loginFailureLimiter
	serviceEmailLimiter      *serviceEmailRateLimiter
	mediaAssets              mediaassets.Repository
	uiPreferences            store.UserUIPreferenceStore
	discordTargetPresets     store.DiscordTargetPresetStore
	streamVisual             streamvisual.Repository
	videoCovers              videocover.Repository
	videoCoverDispatcher     servicecall.VideoCoverDispatcher
}

type serviceDispatcher interface {
	Start(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.StartRequest) []servicecall.DispatchResult
	Stop(ctx context.Context, stream store.Stream, services []store.RegisteredService) []servicecall.DispatchResult
	RetryArchiveUpload(ctx context.Context, stream store.Stream, services []store.RegisteredService, archiveConfig map[string]any) []servicecall.DispatchResult
	AudioStatus(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.AudioStatusResult
	WorkerEvents(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.WorkerEventsResult
	EncoderPreflight(ctx context.Context, stream store.Stream, services []store.RegisteredService) servicecall.ServicePreflightResult
	SendWorkerEvent(ctx context.Context, stream store.Stream, services []store.RegisteredService, req servicecall.WorkerEventRequest) servicecall.DispatchResult
	DownloadArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, byteRange string) servicecall.ArchiveArtifactDownloadResult
	DeleteArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact) servicecall.DispatchResult
	RenameArchiveArtifact(ctx context.Context, stream store.Stream, services []store.RegisteredService, artifact store.StreamArtifact, name string) servicecall.DispatchResult
}

type startReadinessChecker interface {
	StartReadinessIssues(services []store.RegisteredService, req servicecall.StartRequest, now time.Time) []servicecall.ReadinessIssue
}

type previewServiceDispatcher interface {
	PreviewAsset(ctx context.Context, stream store.Stream, services []store.RegisteredService, name, byteRange string) servicecall.PreviewAssetResult
}

type oauthAccessTokenRefresher interface {
	RefreshAccessToken(ctx context.Context, credentials ytlive.OAuthCredentials) (*oauth2.Token, error)
}

type discordLiveNotificationDispatcher interface {
	NotifyDiscordYouTubeLive(ctx context.Context, stream store.Stream, services []store.RegisteredService, eventID, watchURL string) servicecall.DispatchResult
}

type encoderRuntimeSettingsDispatcher interface {
	UpdateEncoderRuntimeSettings(ctx context.Context, stream store.Stream, services []store.RegisteredService, audioGainDB float64, overlayProfileID string) servicecall.DispatchResult
}

type workerCaptionRuntimeSettingsDispatcher interface {
	UpdateWorkerCaptionRuntimeSettings(ctx context.Context, stream store.Stream, services []store.RegisteredService, captionProfileID string) servicecall.DispatchResult
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

func WithSystemUpdateStore(systemUpdates store.SystemUpdateStore) ServerOption {
	return func(s *Server) { s.systemUpdates = systemUpdates }
}

func WithUpdaterPolicyStore(updaterPolicies store.UpdaterPolicyAdminStore) ServerOption {
	return func(s *Server) { s.updaterPolicies = updaterPolicies }
}

func WithHostSelfUpdateReleaseResolver(
	resolver HostSelfUpdateReleaseResolver,
) ServerOption {
	return func(s *Server) { s.hostSelfUpdateReleases = resolver }
}

func WithUpdateHostBootstrapBroker(broker *UpdateHostBootstrapBroker) ServerOption {
	return func(s *Server) { s.updateHostBootstrapJobs = broker }
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
	s.youtubeIngestHealthState = make(map[string]string)
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
	if provider, ok := s.streams.(interface {
		AssignmentGuardMemoryStore() *store.MemoryStreamStore
	}); ok {
		memoryStreams := provider.AssignmentGuardMemoryStore()
		if memoryAuth, ok := s.auth.(*store.MemoryAuthStore); ok {
			memoryAuth.BindStreamAssignmentGuard(memoryStreams)
		}
		if memoryServices, ok := s.services.(*store.MemoryAuthStore); ok {
			memoryServices.BindStreamAssignmentGuard(memoryStreams)
		}
	}
	if s.profiles == nil {
		s.profiles = store.NewMemoryProfileStore()
	}
	// The production ProfileStore and relay claim reservation share a database
	// fence. Mirror that boundary for the in-memory implementation used by the
	// HTTP tests, otherwise an output update could race a static relay reserve.
	if profiles, ok := s.profiles.(*store.MemoryProfileStore); ok {
		if streams, ok := s.streams.(*store.MemoryStreamStore); ok {
			profiles.BindStreamYouTubeRelayBindingClaims(streams)
		}
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
	if s.systemUpdates == nil {
		s.systemUpdates = store.NewMemorySystemUpdateStore()
	}
	if s.updaterPolicies == nil {
		s.updaterPolicies = store.NewMemoryUpdaterPolicyStore()
	}
	if s.hostSelfUpdateReleases == nil {
		s.hostSelfUpdateReleases = productionHostSelfUpdateReleaseResolver{}
	}
	if s.updateHostBootstrapJobs == nil {
		s.updateHostBootstrapJobs = NewUpdateHostBootstrapBroker()
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
	if s.uiPreferences == nil {
		s.uiPreferences = store.NewMemoryUserUIPreferenceStore()
	}
	if s.discordTargetPresets == nil {
		s.discordTargetPresets = store.NewMemoryDiscordTargetPresetStore()
	}
	if s.streamVisual == nil {
		s.streamVisual = streamvisual.NewMemoryRepository(s.streams)
	}
	if s.videoCovers == nil {
		s.videoCovers = videocover.NewMemoryRepository()
	}
	if s.oauthConnector == nil {
		if connector, ok := s.oauthVerifier.(oauthlogin.Connector); ok {
			s.oauthConnector = connector
		} else {
			s.oauthConnector = oauthlogin.HTTPVerifier{}
		}
	}
	s.routes()
	s.handler = secureHeaders(limitRequestBody(systemUpdateV2ContractBoundary(s.mux), maxControlRequestBytes))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/updater/version" {
		w.Header().Set("Cache-Control", "no-store")
	}
	s.handler.ServeHTTP(w, r)
}
