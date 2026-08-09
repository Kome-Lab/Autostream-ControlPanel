package youtube

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
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

type LiveClient interface {
	Prepare(ctx context.Context, req PrepareRequest) (PreparedOutput, error)
	Complete(ctx context.Context, req CompleteRequest) error
}

type LiveAPIClient struct {
	HTTPClient *http.Client
}

var (
	ErrMissingCredentials = errors.New("youtube_oauth_credentials_missing")
	ErrMissingBroadcastID = errors.New("youtube_broadcast_id_missing")
	ErrMissingIngestInfo  = errors.New("youtube_ingest_info_missing")
)

// minimumScheduledStartLead keeps an immediately requested broadcast just far
// enough in the future for YouTube's liveBroadcasts.insert validation while
// avoiding an operator-visible artificial wait.
const minimumScheduledStartLead = 15 * time.Second

func (c LiveAPIClient) Prepare(ctx context.Context, req PrepareRequest) (PreparedOutput, error) {
	if err := validateCredentials(req.Credentials); err != nil {
		return PreparedOutput{}, err
	}
	service, err := c.service(ctx, req.Credentials)
	if err != nil {
		return PreparedOutput{}, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(req.StreamName)
	}
	if title == "" {
		title = "AutoStream Broadcast"
	}
	privacy := strings.TrimSpace(req.PrivacyStatus)
	if privacy == "" {
		privacy = "private"
	}
	start := normalizedScheduledStart(req.ScheduledStart, time.Now())
	broadcast, err := service.LiveBroadcasts.Insert([]string{"snippet", "status", "contentDetails"}, &youtubeapi.LiveBroadcast{
		Snippet: &youtubeapi.LiveBroadcastSnippet{
			Title:              title,
			Description:        req.Description,
			ScheduledStartTime: start.Format(time.RFC3339),
		},
		Status: &youtubeapi.LiveBroadcastStatus{PrivacyStatus: privacy},
		ContentDetails: &youtubeapi.LiveBroadcastContentDetails{
			EnableAutoStart: req.EnableAutoStart,
			EnableAutoStop:  req.EnableAutoStop,
		},
	}).Context(ctx).Do()
	if err != nil {
		return PreparedOutput{}, err
	}
	stream, err := service.LiveStreams.Insert([]string{"snippet", "cdn"}, &youtubeapi.LiveStream{
		Snippet: &youtubeapi.LiveStreamSnippet{Title: title + " input"},
		Cdn: &youtubeapi.CdnSettings{
			FrameRate:     defaultString(req.FrameRate, "60fps"),
			IngestionType: "rtmp",
			Resolution:    defaultString(req.Resolution, "1080p"),
		},
	}).Context(ctx).Do()
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

func normalizedScheduledStart(requested, now time.Time) time.Time {
	minimum := now.UTC().Add(minimumScheduledStartLead)
	if requested.IsZero() || !requested.UTC().After(minimum) {
		return minimum
	}
	return requested.UTC()
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

func RedactedError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrMissingCredentials) || errors.Is(err, ErrMissingBroadcastID) || errors.Is(err, ErrMissingIngestInfo) {
		return err.Error()
	}
	return fmt.Sprintf("%T", err)
}
