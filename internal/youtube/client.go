package youtube

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	youtubeapi "google.golang.org/api/youtube/v3"
)

type OAuthCredentials struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
}

type PrepareRequest struct {
	Credentials     OAuthCredentials
	StreamID        string
	StreamName      string
	OutputID        string
	Title           string
	Description     string
	PrivacyStatus   string
	ScheduledStart  time.Time
	Resolution      string
	FrameRate       string
	EnableAutoStart bool
	EnableAutoStop  bool
	// PreferredStreamKey selects an operator-created reusable LiveStream by its
	// secret stream name. This keeps Studio-only stream settings (for example
	// manual 16:9 resolution and dual-stream selection) while the Control Panel
	// continues to create and bind each Broadcast through the Live API. The
	// value is used only for a constant-time in-memory comparison and must never
	// be logged or returned by diagnostic surfaces.
	PreferredStreamKey string
	// ReuseAccountStream asks the provider client to bind each broadcast to
	// one reusable LiveStream owned by this OAuth account. It keeps the
	// account from accumulating a new stream key for every broadcast.
	ReuseAccountStream bool
}

// RelayStaticPrepareRequest creates a broadcast bound to a pre-provisioned,
// reusable YouTube LiveStream. The caller keeps the fixed ingest path outside
// this client; no ingestion address or stream key is fetched or returned.
type RelayStaticPrepareRequest struct {
	PrepareRequest
	ReusableLiveStreamID string
}

// RelayStaticBindError carries only the non-secret identity needed for a
// caller to retain or release its reusable-stream claim after a bind failure.
// CleanupConfirmed distinguishes a safely removed Broadcast from a state that
// must be reconciled before reusing the fixed LiveStream.
type RelayStaticBindError struct {
	BroadcastID      string
	LiveStreamID     string
	CleanupConfirmed bool
}

func (e *RelayStaticBindError) Error() string {
	if e != nil && !e.CleanupConfirmed {
		return ErrRelayStaticBindCleanupUncertain.Error()
	}
	return ErrRelayStaticBindFailed.Error()
}

func (e *RelayStaticBindError) Unwrap() []error {
	if e != nil && !e.CleanupConfirmed {
		return []error{ErrRelayStaticBindFailed, ErrRelayStaticBindCleanupUncertain}
	}
	return []error{ErrRelayStaticBindFailed}
}

// RelayStaticBroadcastCreateError means liveBroadcasts.insert may have reached
// YouTube, but this client did not receive a usable Broadcast identity. The
// caller must retain the fixed LiveStream claim and reconcile it; the original
// provider error is deliberately not retained because it may contain details
// that are unsafe to surface.
type RelayStaticBroadcastCreateError struct {
	LiveStreamID string
}

func (e *RelayStaticBroadcastCreateError) Error() string {
	return ErrRelayStaticBroadcastCreateUncertain.Error()
}

func (e *RelayStaticBroadcastCreateError) Unwrap() error {
	return ErrRelayStaticBroadcastCreateUncertain
}

// RelayStaticBroadcastCleanupRequest identifies a known static-relay
// Broadcast that was created but must be removed before the fixed relay claim
// can be released. BroadcastID is a provider resource identity, never a key.
type RelayStaticBroadcastCleanupRequest struct {
	Credentials OAuthCredentials
	BroadcastID string
}

// RelayStaticBroadcastCleanupError means a DELETE request could have reached
// YouTube but confirmed cleanup was not obtained. Its identity fields are safe
// for durable recovery state; it intentionally does not unwrap provider errors.
type RelayStaticBroadcastCleanupError struct {
	BroadcastID string
}

func (e *RelayStaticBroadcastCleanupError) Error() string {
	return ErrRelayStaticBroadcastCleanupUncertain.Error()
}

func (e *RelayStaticBroadcastCleanupError) Unwrap() []error {
	return []error{ErrRelayStaticBroadcastCleanupFailed, ErrRelayStaticBroadcastCleanupUncertain}
}

// RelayStaticBroadcastCompletionError means a transition to complete could not
// be confirmed by a follow-up status read. The Broadcast identity is safe to
// retain for the caller's fenced retry state; provider details are never
// retained or exposed.
type RelayStaticBroadcastCompletionError struct {
	BroadcastID string
}

func (e *RelayStaticBroadcastCompletionError) Error() string {
	return ErrRelayStaticBroadcastCompletionUncertain.Error()
}

func (e *RelayStaticBroadcastCompletionError) Unwrap() []error {
	return []error{ErrRelayStaticBroadcastCompletionFailed, ErrRelayStaticBroadcastCompletionUncertain}
}

type PreparedOutput struct {
	RTMPURL      string
	StreamKey    string
	BroadcastID  string
	LiveStreamID string
}

type CompleteRequest struct {
	Credentials OAuthCredentials
	BroadcastID string
}

// BroadcastTransitionRequest identifies a prepared Broadcast that should be
// moved into YouTube's live lifecycle after the Encoder has started sending.
// It intentionally carries only OAuth credentials and the public Broadcast
// identity; ingest URLs and stream keys never cross this boundary.
type BroadcastTransitionRequest struct {
	Credentials OAuthCredentials
	BroadcastID string
}

// BroadcastLifecycleRequest contains only the OAuth credentials needed for a
// read and the public Broadcast resource identity. It deliberately does not
// expose ingest settings or any stream key.
type BroadcastLifecycleRequest struct {
	Credentials OAuthCredentials
	BroadcastID string
}

// BroadcastIngestHealthRequest identifies one exact Broadcast whose bound
// LiveStream health should be inspected. The request deliberately excludes
// ingest addresses and stream keys.
type BroadcastIngestHealthRequest struct {
	Credentials OAuthCredentials
	BroadcastID string
}

// BroadcastIngestHealthIssue contains only bounded provider codes and numeric
// dimensions derived from a provider description. The free-form description
// itself is never returned or persisted.
type BroadcastIngestHealthIssue struct {
	Type       string   `json:"type"`
	Severity   string   `json:"severity"`
	Dimensions []string `json:"dimensions,omitempty"`
}

// BroadcastIngestHealthSnapshot is a secret-free view of the exact LiveStream
// bound to a Broadcast. Resolution and frame rate are the provider LiveStream
// configuration; ConfigurationIssues reports what YouTube observed at ingest.
type BroadcastIngestHealthSnapshot struct {
	BroadcastID           string                       `json:"broadcast_id"`
	LiveStreamID          string                       `json:"live_stream_id"`
	ConfiguredResolution  string                       `json:"configured_resolution,omitempty"`
	ConfiguredFrameRate   string                       `json:"configured_frame_rate,omitempty"`
	StreamStatus          string                       `json:"stream_status,omitempty"`
	HealthStatus          string                       `json:"health_status,omitempty"`
	LastUpdateTimeSeconds uint64                       `json:"last_update_time_seconds,omitempty"`
	ConfigurationIssues   []BroadcastIngestHealthIssue `json:"configuration_issues,omitempty"`
}

type LiveClient interface {
	Prepare(ctx context.Context, req PrepareRequest) (PreparedOutput, error)
	Complete(ctx context.Context, req CompleteRequest) error
}

// BroadcastLifecycleClient is an optional extension so existing LiveClient
// fakes remain source-compatible. Callers that use it must wait for exactly
// the provider's `live` lifecycle before announcing a live-api broadcast.
type BroadcastLifecycleClient interface {
	BroadcastLifecycle(ctx context.Context, req BroadcastLifecycleRequest) (string, error)
}

// BroadcastIngestHealthClient is an optional read-only extension so existing
// LiveClient fakes remain source-compatible. Implementations must not request
// or return ingestionInfo because it contains the stream key.
type BroadcastIngestHealthClient interface {
	BroadcastIngestHealth(ctx context.Context, req BroadcastIngestHealthRequest) (BroadcastIngestHealthSnapshot, error)
}

// BroadcastTransitionClient is an optional extension so existing LiveClient
// fakes remain source-compatible. Production clients use it to make the
// provider-side live transition explicit when AutoStart did not take effect.
type BroadcastTransitionClient interface {
	TransitionBroadcastLive(ctx context.Context, req BroadcastTransitionRequest) error
}

// RelayStaticLiveClient is intentionally separate from LiveClient so existing
// live_api callers retain their stable contract while relay-static callers opt
// into the non-secret, reusable-LiveStream path.
type RelayStaticLiveClient interface {
	PrepareRelayStatic(ctx context.Context, req RelayStaticPrepareRequest) (PreparedOutput, error)
}

// RelayStaticBroadcastCleanupClient is deliberately separate from
// RelayStaticLiveClient so existing relay-static fakes and callers do not gain
// a destructive capability implicitly. A nil error means DELETE was confirmed
// by YouTube or the Broadcast was already absent; any other error requires the
// caller to retain the fixed relay claim for recovery.
type RelayStaticBroadcastCleanupClient interface {
	DeleteRelayStaticBroadcast(ctx context.Context, req RelayStaticBroadcastCleanupRequest) error
}

// RelayStaticBroadcastCompletionClient confirms completion of a possibly
// dispatched fixed-relay Broadcast. Unlike the generic completion method, it
// reconciles an ambiguous transition response against the provider's current
// lifecycle status before the caller releases its fixed-relay claim.
type RelayStaticBroadcastCompletionClient interface {
	CompleteRelayStaticBroadcast(ctx context.Context, req CompleteRequest) error
}

type LiveAPIClient struct {
	HTTPClient *http.Client
}

var (
	ErrMissingCredentials                      = errors.New("youtube_oauth_credentials_missing")
	ErrMissingBroadcastID                      = errors.New("youtube_broadcast_id_missing")
	ErrBroadcastNotFound                       = errors.New("youtube_broadcast_not_found")
	ErrBroadcastLifecycleUnavailable           = errors.New("youtube_broadcast_lifecycle_unavailable")
	ErrBroadcastLiveStreamUnavailable          = errors.New("youtube_broadcast_live_stream_unavailable")
	ErrLiveStreamNotFound                      = errors.New("youtube_live_stream_not_found")
	ErrMissingIngestInfo                       = errors.New("youtube_ingest_info_missing")
	ErrMissingReusableLiveStreamID             = errors.New("youtube_reusable_live_stream_id_missing")
	ErrReusableLiveStreamNotFound              = errors.New("youtube_reusable_live_stream_not_found")
	ErrReusableLiveStreamNotReusable           = errors.New("youtube_reusable_live_stream_not_reusable")
	ErrReusableLiveStreamFormatMismatch        = errors.New("youtube_reusable_live_stream_format_mismatch")
	ErrPreferredStreamKeyNotFound              = errors.New("youtube_preferred_stream_key_not_found")
	ErrRelayStaticBroadcastCreateUncertain     = errors.New("youtube_relay_static_broadcast_create_uncertain")
	ErrRelayStaticBindFailed                   = errors.New("youtube_relay_static_bind_failed")
	ErrRelayStaticBindCleanupUncertain         = errors.New("youtube_relay_static_bind_cleanup_uncertain")
	ErrRelayStaticBroadcastCleanupFailed       = errors.New("youtube_relay_static_broadcast_cleanup_failed")
	ErrRelayStaticBroadcastCleanupUncertain    = errors.New("youtube_relay_static_broadcast_cleanup_uncertain")
	ErrRelayStaticBroadcastCompletionFailed    = errors.New("youtube_relay_static_broadcast_completion_failed")
	ErrRelayStaticBroadcastCompletionUncertain = errors.New("youtube_relay_static_broadcast_completion_uncertain")
)

const reusableAccountStreamTitle = "AutoStream account ingest"

var reusableAccountStreamMu sync.Mutex

// minimumScheduledStartLead keeps an automatically-started broadcast within a
// small admission window. The LiveBroadcasts insert endpoint requires a
// scheduledStartTime in the future even when enableAutoStart is enabled; the
// encoder input then causes YouTube to transition the broadcast to live.
const minimumScheduledStartLead = 15 * time.Second

func youtubeImmediateStart(now time.Time) time.Time {
	return now.UTC().Add(minimumScheduledStartLead)
}

const (
	relayStaticCleanupTimeout    = 5 * time.Second
	relayStaticCompletionTimeout = 5 * time.Second
)

func (c LiveAPIClient) Prepare(ctx context.Context, req PrepareRequest) (PreparedOutput, error) {
	if err := validateCredentials(req.Credentials); err != nil {
		return PreparedOutput{}, err
	}
	service, err := c.service(ctx, req.Credentials)
	if err != nil {
		return PreparedOutput{}, err
	}
	var stream *youtubeapi.LiveStream
	if strings.TrimSpace(req.PreferredStreamKey) != "" {
		stream, err = findReusableAccountLiveStreamByKey(ctx, service, req)
	} else if req.ReuseAccountStream {
		// The provider has no idempotency key for liveStreams.insert. Serialize
		// the find-or-create section in this process so concurrent starts do not
		// create two account streams before either response is observed.
		reusableAccountStreamMu.Lock()
		defer reusableAccountStreamMu.Unlock()
		stream, err = ensureReusableAccountLiveStream(ctx, service, req)
	} else {
		stream, err = service.LiveStreams.Insert([]string{"snippet", "cdn", "contentDetails"}, &youtubeapi.LiveStream{
			Snippet: &youtubeapi.LiveStreamSnippet{Title: broadcastTitle(req) + " input"},
			ContentDetails: &youtubeapi.LiveStreamContentDetails{
				IsReusable:      false,
				ForceSendFields: []string{"IsReusable"},
			},
			Cdn: &youtubeapi.CdnSettings{
				FrameRate:     defaultString(req.FrameRate, "60fps"),
				IngestionType: "rtmp",
				Resolution:    defaultString(req.Resolution, "1080p"),
			},
		}).
			Context(ctx).
			Do()
	}
	if err != nil {
		return PreparedOutput{}, err
	}
	broadcast, err := prepareBroadcast(ctx, service, req)
	if err != nil {
		return PreparedOutput{}, err
	}
	if _, err := service.LiveBroadcasts.Bind(broadcast.Id, []string{"id", "contentDetails"}).StreamId(stream.Id).Context(ctx).Do(); err != nil {
		return PreparedOutput{}, err
	}
	if stream.Cdn == nil || stream.Cdn.IngestionInfo == nil {
		return PreparedOutput{}, ErrMissingIngestInfo
	}
	rtmpURL, streamKey, err := rtmpsIngest(stream.Cdn.IngestionInfo)
	if err != nil {
		return PreparedOutput{}, ErrMissingIngestInfo
	}
	return PreparedOutput{RTMPURL: rtmpURL, StreamKey: streamKey, BroadcastID: broadcast.Id, LiveStreamID: stream.Id}, nil
}

func findReusableAccountLiveStreamByKey(ctx context.Context, service *youtubeapi.Service, req PrepareRequest) (*youtubeapi.LiveStream, error) {
	preferredKey := strings.TrimSpace(req.PreferredStreamKey)
	if preferredKey == "" {
		return nil, ErrPreferredStreamKeyNotFound
	}
	wantedResolution := defaultString(req.Resolution, "1080p")
	wantedFrameRate := defaultString(req.FrameRate, "60fps")
	pageToken := ""
	for {
		query := service.LiveStreams.List([]string{"id", "cdn", "contentDetails"}).
			Mine(true).
			MaxResults(50).
			Fields("items(id,cdn/resolution,cdn/frameRate,cdn/ingestionInfo,contentDetails/isReusable),nextPageToken").
			Context(ctx)
		if pageToken != "" {
			query = query.PageToken(pageToken)
		}
		streams, err := query.Do()
		if err != nil {
			return nil, err
		}
		for _, stream := range streams.Items {
			if stream == nil || stream.Cdn == nil || stream.Cdn.IngestionInfo == nil {
				continue
			}
			candidateKey := strings.TrimSpace(stream.Cdn.IngestionInfo.StreamName)
			if subtle.ConstantTimeCompare([]byte(candidateKey), []byte(preferredKey)) != 1 {
				continue
			}
			if stream.ContentDetails == nil || !stream.ContentDetails.IsReusable {
				return nil, ErrReusableLiveStreamNotReusable
			}
			if !strings.EqualFold(strings.TrimSpace(stream.Cdn.Resolution), wantedResolution) ||
				!strings.EqualFold(strings.TrimSpace(stream.Cdn.FrameRate), wantedFrameRate) {
				return nil, ErrReusableLiveStreamFormatMismatch
			}
			return stream, nil
		}
		if streams.NextPageToken == "" {
			break
		}
		pageToken = streams.NextPageToken
	}
	return nil, ErrPreferredStreamKeyNotFound
}

func ensureReusableAccountLiveStream(ctx context.Context, service *youtubeapi.Service, req PrepareRequest) (*youtubeapi.LiveStream, error) {
	var existingReusable *youtubeapi.LiveStream
	wantedResolution := defaultString(req.Resolution, "1080p")
	wantedFrameRate := defaultString(req.FrameRate, "60fps")
	pageToken := ""
	for {
		query := service.LiveStreams.List([]string{"id", "snippet", "cdn", "contentDetails"}).
			Mine(true).
			MaxResults(50).
			Fields("items(id,snippet/title,cdn/resolution,cdn/frameRate,cdn/ingestionInfo,contentDetails/isReusable)").
			Context(ctx)
		if pageToken != "" {
			query = query.PageToken(pageToken)
		}
		streams, err := query.Do()
		if err != nil {
			return nil, err
		}
		for _, stream := range streams.Items {
			if stream == nil || stream.ContentDetails == nil || !stream.ContentDetails.IsReusable ||
				stream.Cdn == nil || stream.Cdn.IngestionInfo == nil ||
				!strings.EqualFold(strings.TrimSpace(stream.Cdn.Resolution), wantedResolution) ||
				!strings.EqualFold(strings.TrimSpace(stream.Cdn.FrameRate), wantedFrameRate) {
				continue
			}
			if existingReusable == nil {
				existingReusable = stream
			}
			if stream.Snippet != nil && strings.TrimSpace(stream.Snippet.Title) == reusableAccountStreamTitle {
				return stream, nil
			}
		}
		if streams.NextPageToken == "" {
			break
		}
		pageToken = streams.NextPageToken
	}
	// Older AutoStream installations may already have a reusable stream with a
	// different title. Reuse the first account-owned stream only when its CDN
	// format exactly matches the Encoder output. Binding a 1080p Encoder to an
	// existing 2160p LiveStream makes YouTube advertise 4K and pillarbox the
	// actual scene inside that larger canvas.
	if existingReusable != nil {
		return existingReusable, nil
	}
	return service.LiveStreams.Insert([]string{"snippet", "cdn", "contentDetails"}, &youtubeapi.LiveStream{
		Snippet:        &youtubeapi.LiveStreamSnippet{Title: reusableAccountStreamTitle},
		ContentDetails: &youtubeapi.LiveStreamContentDetails{IsReusable: true},
		Cdn: &youtubeapi.CdnSettings{
			FrameRate:     wantedFrameRate,
			IngestionType: "rtmp",
			Resolution:    wantedResolution,
		},
	}).Context(ctx).Do()
}

// PrepareRelayStatic creates a broadcast and binds it to the fixed
// pre-provisioned LiveStream used by a managed static output relay. It requests
// only the stream's ID, reusable flag, and non-secret CDN format; it never reads
// or returns ingestion details.
func (c LiveAPIClient) PrepareRelayStatic(ctx context.Context, req RelayStaticPrepareRequest) (PreparedOutput, error) {
	if err := validateCredentials(req.Credentials); err != nil {
		return PreparedOutput{}, err
	}
	reusableLiveStreamID := strings.TrimSpace(req.ReusableLiveStreamID)
	if reusableLiveStreamID == "" {
		return PreparedOutput{}, ErrMissingReusableLiveStreamID
	}
	service, err := c.service(ctx, req.Credentials)
	if err != nil {
		return PreparedOutput{}, err
	}
	liveStreams, err := service.LiveStreams.List([]string{"id", "contentDetails", "cdn"}).
		Id(reusableLiveStreamID).
		Fields("items(id,contentDetails/isReusable,cdn/resolution,cdn/frameRate)").
		Context(ctx).
		Do()
	if err != nil {
		if isYouTubeNotFound(err) {
			return PreparedOutput{}, ErrReusableLiveStreamNotFound
		}
		return PreparedOutput{}, err
	}
	if len(liveStreams.Items) != 1 || liveStreams.Items[0] == nil || strings.TrimSpace(liveStreams.Items[0].Id) != reusableLiveStreamID {
		return PreparedOutput{}, ErrReusableLiveStreamNotFound
	}
	liveStream := liveStreams.Items[0]
	if liveStream.ContentDetails == nil || !liveStream.ContentDetails.IsReusable {
		return PreparedOutput{}, ErrReusableLiveStreamNotReusable
	}
	wantedResolution := defaultString(req.Resolution, "1080p")
	wantedFrameRate := defaultString(req.FrameRate, "60fps")
	if liveStream.Cdn == nil ||
		!strings.EqualFold(strings.TrimSpace(liveStream.Cdn.Resolution), wantedResolution) ||
		!strings.EqualFold(strings.TrimSpace(liveStream.Cdn.FrameRate), wantedFrameRate) {
		return PreparedOutput{}, ErrReusableLiveStreamFormatMismatch
	}
	broadcast, err := prepareBroadcast(ctx, service, req.PrepareRequest)
	if err != nil {
		return PreparedOutput{LiveStreamID: reusableLiveStreamID}, &RelayStaticBroadcastCreateError{LiveStreamID: reusableLiveStreamID}
	}
	if _, err := service.LiveBroadcasts.Bind(broadcast.Id, []string{"id", "contentDetails"}).StreamId(reusableLiveStreamID).Context(ctx).Do(); err != nil {
		prepared := PreparedOutput{BroadcastID: broadcast.Id, LiveStreamID: reusableLiveStreamID}
		return prepared, &RelayStaticBindError{
			BroadcastID:      prepared.BroadcastID,
			LiveStreamID:     prepared.LiveStreamID,
			CleanupConfirmed: cleanupRelayStaticBroadcast(ctx, service, broadcast.Id),
		}
	}
	return PreparedOutput{BroadcastID: broadcast.Id, LiveStreamID: reusableLiveStreamID}, nil
}

func cleanupRelayStaticBroadcast(ctx context.Context, service *youtubeapi.Service, broadcastID string) bool {
	return deleteRelayStaticBroadcast(ctx, service, broadcastID) == nil
}

// DeleteRelayStaticBroadcast performs the only safe cleanup for an unstarted
// fixed-relay Broadcast. It never transitions the Broadcast to complete.
// Success and a confirmed 404 are safe to release; all other provider or
// transport outcomes are typed uncertainty and must retain the relay claim.
func (c LiveAPIClient) DeleteRelayStaticBroadcast(ctx context.Context, req RelayStaticBroadcastCleanupRequest) error {
	if err := validateCredentials(req.Credentials); err != nil {
		return err
	}
	broadcastID := strings.TrimSpace(req.BroadcastID)
	if broadcastID == "" {
		return ErrMissingBroadcastID
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), relayStaticCleanupTimeout)
	defer cancel()
	service, err := c.service(cleanupCtx, req.Credentials)
	if err != nil {
		return &RelayStaticBroadcastCleanupError{BroadcastID: broadcastID}
	}
	if err := deleteRelayStaticBroadcastWithContext(cleanupCtx, service, broadcastID); err != nil {
		return &RelayStaticBroadcastCleanupError{BroadcastID: broadcastID}
	}
	return nil
}

func deleteRelayStaticBroadcast(ctx context.Context, service *youtubeapi.Service, broadcastID string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), relayStaticCleanupTimeout)
	defer cancel()
	return deleteRelayStaticBroadcastWithContext(cleanupCtx, service, broadcastID)
}

func deleteRelayStaticBroadcastWithContext(ctx context.Context, service *youtubeapi.Service, broadcastID string) error {
	err := service.LiveBroadcasts.Delete(broadcastID).Context(ctx).Do()
	if err == nil || isYouTubeNotFound(err) {
		return nil
	}
	return err
}

func prepareBroadcast(ctx context.Context, service *youtubeapi.Service, req PrepareRequest) (*youtubeapi.LiveBroadcast, error) {
	title := broadcastTitle(req)
	privacy := strings.TrimSpace(req.PrivacyStatus)
	if privacy == "" {
		privacy = "private"
	}
	start := normalizedScheduledStart(req.ScheduledStart, time.Now())
	// AutoStart broadcasts are intended to go directly live when YouTube sees
	// ingest. The API defaults monitorStream.enableMonitorStream to true, which
	// is only needed for a separate testing stage and would require a testing
	// transition before live. Disable it explicitly for that direct-start path,
	// while preserving the existing monitor/testing path for manual starts.
	enableMonitorStream := !req.EnableAutoStart
	return service.LiveBroadcasts.Insert([]string{"snippet", "status", "contentDetails"}, &youtubeapi.LiveBroadcast{
		Snippet: &youtubeapi.LiveBroadcastSnippet{
			Title:              title,
			Description:        req.Description,
			ScheduledStartTime: start.Format(time.RFC3339),
		},
		Status: &youtubeapi.LiveBroadcastStatus{PrivacyStatus: privacy},
		ContentDetails: &youtubeapi.LiveBroadcastContentDetails{
			MonitorStream:   &youtubeapi.MonitorStreamInfo{EnableMonitorStream: &enableMonitorStream},
			EnableAutoStart: req.EnableAutoStart,
			EnableAutoStop:  req.EnableAutoStop,
			Projection:      "rectangular",
		},
	}).Context(ctx).Do()
}

func broadcastTitle(req PrepareRequest) string {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(req.StreamName)
	}
	if title == "" {
		return "AutoStream Broadcast"
	}
	return title
}

func isYouTubeNotFound(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound
}

func normalizedScheduledStart(requested, now time.Time) time.Time {
	now = now.UTC()
	if requested.IsZero() {
		return youtubeImmediateStart(now)
	}
	requested = requested.UTC()
	if !requested.After(now) {
		return youtubeImmediateStart(now)
	}
	minimum := now.Add(minimumScheduledStartLead)
	if requested.Before(minimum) {
		return minimum
	}
	return requested
}

func rtmpsIngest(info *youtubeapi.IngestionInfo) (string, string, error) {
	if info == nil {
		return "", "", ErrMissingIngestInfo
	}
	rtmpURL := strings.TrimSpace(info.RtmpsIngestionAddress)
	streamKey := strings.TrimSpace(info.StreamName)
	if rtmpURL == "" || streamKey == "" || !strings.HasPrefix(strings.ToLower(rtmpURL), "rtmps://") {
		return "", "", ErrMissingIngestInfo
	}
	return rtmpURL, streamKey, nil
}

func (c LiveAPIClient) Complete(ctx context.Context, req CompleteRequest) error {
	if err := validateCredentials(req.Credentials); err != nil {
		return err
	}
	broadcastID := strings.TrimSpace(req.BroadcastID)
	if broadcastID == "" {
		return ErrMissingBroadcastID
	}
	service, err := c.service(ctx, req.Credentials)
	if err != nil {
		return err
	}
	_, err = service.LiveBroadcasts.Transition("complete", broadcastID, []string{"id", "status"}).Context(ctx).Do()
	return err
}

// TransitionBroadcastLive explicitly starts a prepared YouTube Broadcast.
// This is called only after the selected Encoder has accepted the stream. A
// caller may reconcile an already-live response through BroadcastLifecycle.
func (c LiveAPIClient) TransitionBroadcastLive(ctx context.Context, req BroadcastTransitionRequest) error {
	if err := validateCredentials(req.Credentials); err != nil {
		return err
	}
	broadcastID := strings.TrimSpace(req.BroadcastID)
	if broadcastID == "" {
		return ErrMissingBroadcastID
	}
	service, err := c.service(ctx, req.Credentials)
	if err != nil {
		return err
	}
	_, err = service.LiveBroadcasts.Transition("live", broadcastID, []string{"id", "status"}).Context(ctx).Do()
	return err
}

// BroadcastLifecycle returns the provider's current normalized lifecycle for
// one exact Broadcast. An empty, missing, or non-matching response is not
// interpreted as live: callers retain a pending notification instead.
func (c LiveAPIClient) BroadcastLifecycle(ctx context.Context, req BroadcastLifecycleRequest) (string, error) {
	if err := validateCredentials(req.Credentials); err != nil {
		return "", err
	}
	broadcastID := strings.TrimSpace(req.BroadcastID)
	if broadcastID == "" {
		return "", ErrMissingBroadcastID
	}
	service, err := c.service(ctx, req.Credentials)
	if err != nil {
		return "", err
	}
	response, err := service.LiveBroadcasts.List([]string{"id", "status"}).
		Id(broadcastID).
		Fields("items(id,status/lifeCycleStatus)").
		Context(ctx).
		Do()
	if err != nil {
		if isYouTubeNotFound(err) {
			return "", ErrBroadcastNotFound
		}
		return "", err
	}
	for _, broadcast := range response.Items {
		if broadcast == nil || strings.TrimSpace(broadcast.Id) != broadcastID || broadcast.Status == nil {
			continue
		}
		lifecycle := strings.ToLower(strings.TrimSpace(broadcast.Status.LifeCycleStatus))
		if lifecycle != "" {
			return lifecycle, nil
		}
	}
	return "", ErrBroadcastLifecycleUnavailable
}

// BroadcastIngestHealth resolves the LiveStream currently bound to one exact
// Broadcast and returns only secret-free CDN/status fields. In particular, the
// partial-response field mask omits cdn.ingestionInfo, which contains the
// provider stream key and ingest addresses.
func (c LiveAPIClient) BroadcastIngestHealth(ctx context.Context, req BroadcastIngestHealthRequest) (BroadcastIngestHealthSnapshot, error) {
	if err := validateCredentials(req.Credentials); err != nil {
		return BroadcastIngestHealthSnapshot{}, err
	}
	broadcastID := strings.TrimSpace(req.BroadcastID)
	if broadcastID == "" {
		return BroadcastIngestHealthSnapshot{}, ErrMissingBroadcastID
	}
	service, err := c.service(ctx, req.Credentials)
	if err != nil {
		return BroadcastIngestHealthSnapshot{}, err
	}
	broadcasts, err := service.LiveBroadcasts.List([]string{"id", "contentDetails"}).
		Id(broadcastID).
		Fields("items(id,contentDetails/boundStreamId)").
		Context(ctx).
		Do()
	if err != nil {
		if isYouTubeNotFound(err) {
			return BroadcastIngestHealthSnapshot{}, ErrBroadcastNotFound
		}
		return BroadcastIngestHealthSnapshot{}, err
	}
	liveStreamID := ""
	broadcastFound := false
	for _, broadcast := range broadcasts.Items {
		if broadcast == nil || strings.TrimSpace(broadcast.Id) != broadcastID {
			continue
		}
		broadcastFound = true
		if broadcast.ContentDetails != nil {
			liveStreamID = strings.TrimSpace(broadcast.ContentDetails.BoundStreamId)
		}
		break
	}
	if !broadcastFound {
		return BroadcastIngestHealthSnapshot{}, ErrBroadcastNotFound
	}
	if liveStreamID == "" {
		return BroadcastIngestHealthSnapshot{}, ErrBroadcastLiveStreamUnavailable
	}

	streams, err := service.LiveStreams.List([]string{"id", "cdn", "status"}).
		Id(liveStreamID).
		Fields("items(id,cdn(resolution,frameRate),status(streamStatus,healthStatus(status,lastUpdateTimeSeconds,configurationIssues(type,severity,description))))").
		Context(ctx).
		Do()
	if err != nil {
		if isYouTubeNotFound(err) {
			return BroadcastIngestHealthSnapshot{}, ErrLiveStreamNotFound
		}
		return BroadcastIngestHealthSnapshot{}, err
	}
	for _, stream := range streams.Items {
		if stream == nil || strings.TrimSpace(stream.Id) != liveStreamID {
			continue
		}
		snapshot := BroadcastIngestHealthSnapshot{
			BroadcastID:  broadcastID,
			LiveStreamID: liveStreamID,
		}
		if stream.Cdn != nil {
			snapshot.ConfiguredResolution = safeYouTubeHealthCode(strings.ToLower(stream.Cdn.Resolution))
			snapshot.ConfiguredFrameRate = safeYouTubeHealthCode(strings.ToLower(stream.Cdn.FrameRate))
		}
		if stream.Status != nil {
			snapshot.StreamStatus = safeYouTubeHealthCode(strings.ToLower(stream.Status.StreamStatus))
			if health := stream.Status.HealthStatus; health != nil {
				snapshot.HealthStatus = safeYouTubeHealthCode(strings.ToLower(health.Status))
				snapshot.LastUpdateTimeSeconds = health.LastUpdateTimeSeconds
				for _, issue := range health.ConfigurationIssues {
					if issue == nil {
						continue
					}
					snapshot.ConfigurationIssues = append(snapshot.ConfigurationIssues, BroadcastIngestHealthIssue{
						Type:       safeYouTubeHealthCode(issue.Type),
						Severity:   safeYouTubeHealthCode(strings.ToLower(issue.Severity)),
						Dimensions: youtubeHealthDimensions(issue.Description),
					})
				}
			}
		}
		sort.Slice(snapshot.ConfigurationIssues, func(i, j int) bool {
			left, right := snapshot.ConfigurationIssues[i], snapshot.ConfigurationIssues[j]
			if left.Type != right.Type {
				return left.Type < right.Type
			}
			if left.Severity != right.Severity {
				return left.Severity < right.Severity
			}
			return strings.Join(left.Dimensions, ",") < strings.Join(right.Dimensions, ",")
		})
		return snapshot, nil
	}
	return BroadcastIngestHealthSnapshot{}, ErrLiveStreamNotFound
}

// CompleteRelayStaticBroadcast is the fixed-relay completion boundary. A
// transport error or redundant transition can mean that YouTube already
// accepted the requested completion. In that case it reads only the safe
// Broadcast lifecycle field and succeeds exclusively once the provider reports
// the terminal complete status; all other outcomes retain the caller's claim.
func (c LiveAPIClient) CompleteRelayStaticBroadcast(ctx context.Context, req CompleteRequest) error {
	if err := validateCredentials(req.Credentials); err != nil {
		return err
	}
	broadcastID := strings.TrimSpace(req.BroadcastID)
	if broadcastID == "" {
		return ErrMissingBroadcastID
	}
	service, err := c.service(ctx, req.Credentials)
	if err != nil {
		return &RelayStaticBroadcastCompletionError{BroadcastID: broadcastID}
	}
	if _, err = service.LiveBroadcasts.Transition("complete", broadcastID, []string{"id", "status"}).Context(ctx).Do(); err == nil {
		return nil
	}
	confirmationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), relayStaticCompletionTimeout)
	defer cancel()
	confirmationService, confirmationErr := c.service(confirmationCtx, req.Credentials)
	if confirmationErr != nil || !relayStaticBroadcastIsComplete(confirmationCtx, confirmationService, broadcastID) {
		return &RelayStaticBroadcastCompletionError{BroadcastID: broadcastID}
	}
	return nil
}

func relayStaticBroadcastIsComplete(ctx context.Context, service *youtubeapi.Service, broadcastID string) bool {
	if service == nil {
		return false
	}
	response, err := service.LiveBroadcasts.List([]string{"id", "status"}).
		Id(broadcastID).
		Fields("items(id,status/lifeCycleStatus)").
		Context(ctx).
		Do()
	if err != nil {
		return false
	}
	for _, broadcast := range response.Items {
		if broadcast != nil && strings.TrimSpace(broadcast.Id) == broadcastID && broadcast.Status != nil && strings.EqualFold(strings.TrimSpace(broadcast.Status.LifeCycleStatus), "complete") {
			return true
		}
	}
	return false
}

// RefreshAccessToken obtains a fresh short-lived access token from the
// provider. The returned access token must remain in memory only; callers may
// persist the rotated refresh token, but never the access token itself.
func (c LiveAPIClient) RefreshAccessToken(ctx context.Context, creds OAuthCredentials) (*oauth2.Token, error) {
	if err := validateCredentials(creds); err != nil {
		return nil, err
	}
	return c.tokenSource(ctx, creds).Token()
}

func (c LiveAPIClient) service(ctx context.Context, creds OAuthCredentials) (*youtubeapi.Service, error) {
	tokenSource := c.tokenSource(ctx, creds)
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = oauth2.NewClient(ctx, tokenSource)
	} else {
		httpClient = &http.Client{Transport: &oauth2.Transport{Source: tokenSource, Base: httpClient.Transport}, Timeout: httpClient.Timeout}
	}
	return youtubeapi.NewService(ctx, option.WithHTTPClient(httpClient))
}

func (c LiveAPIClient) tokenSource(ctx context.Context, creds OAuthCredentials) oauth2.TokenSource {
	if c.HTTPClient != nil && ctx.Value(oauth2.HTTPClient) == nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, c.HTTPClient)
	}
	oauthConfig := oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{youtubeapi.YoutubeScope},
	}
	return oauthConfig.TokenSource(ctx, &oauth2.Token{RefreshToken: creds.RefreshToken})
}

func validateCredentials(creds OAuthCredentials) error {
	if strings.TrimSpace(creds.ClientID) == "" || strings.TrimSpace(creds.ClientSecret) == "" || strings.TrimSpace(creds.RefreshToken) == "" {
		return ErrMissingCredentials
	}
	return nil
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

var youtubeHealthDimensionsPattern = regexp.MustCompile(`(?i)\b([1-9][0-9]{1,4})\s*[x×]\s*([1-9][0-9]{1,4})\b`)

// safeYouTubeHealthCode keeps only the token-shaped values documented for
// LiveStream CDN/status fields. Unexpected free-form provider content is
// replaced instead of being copied into logs or audit metadata.
func safeYouTubeHealthCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 64 {
		return "unknown"
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return "unknown"
	}
	return value
}

// youtubeHealthDimensions extracts only numeric WxH tokens from a provider
// description. It never retains the surrounding free-form text.
func youtubeHealthDimensions(description string) []string {
	matches := youtubeHealthDimensionsPattern.FindAllStringSubmatch(description, 4)
	seen := make(map[string]bool, len(matches))
	dimensions := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		dimension := match[1] + "x" + match[2]
		if seen[dimension] {
			continue
		}
		seen[dimension] = true
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	return dimensions
}

func RedactedError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrMissingCredentials) || errors.Is(err, ErrMissingBroadcastID) || errors.Is(err, ErrBroadcastNotFound) || errors.Is(err, ErrBroadcastLifecycleUnavailable) ||
		errors.Is(err, ErrBroadcastLiveStreamUnavailable) || errors.Is(err, ErrLiveStreamNotFound) || errors.Is(err, ErrMissingIngestInfo) ||
		errors.Is(err, ErrMissingReusableLiveStreamID) || errors.Is(err, ErrReusableLiveStreamNotFound) || errors.Is(err, ErrReusableLiveStreamNotReusable) || errors.Is(err, ErrReusableLiveStreamFormatMismatch) || errors.Is(err, ErrPreferredStreamKeyNotFound) ||
		errors.Is(err, ErrRelayStaticBroadcastCreateUncertain) || errors.Is(err, ErrRelayStaticBindFailed) || errors.Is(err, ErrRelayStaticBindCleanupUncertain) ||
		errors.Is(err, ErrRelayStaticBroadcastCleanupFailed) || errors.Is(err, ErrRelayStaticBroadcastCleanupUncertain) ||
		errors.Is(err, ErrRelayStaticBroadcastCompletionFailed) || errors.Is(err, ErrRelayStaticBroadcastCompletionUncertain) {
		return err.Error()
	}
	return fmt.Sprintf("%T", err)
}
