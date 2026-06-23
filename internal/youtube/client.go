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
	start := req.ScheduledStart.UTC()
	if start.IsZero() {
		start = time.Now().UTC().Add(5 * time.Minute)
	}
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

func (c LiveAPIClient) service(ctx context.Context, creds OAuthCredentials) (*youtubeapi.Service, error) {
	oauthConfig := oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{youtubeapi.YoutubeScope},
	}
	tokenSource := oauthConfig.TokenSource(ctx, &oauth2.Token{RefreshToken: creds.RefreshToken})
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = oauth2.NewClient(ctx, tokenSource)
	} else {
		httpClient = &http.Client{Transport: &oauth2.Transport{Source: tokenSource, Base: httpClient.Transport}, Timeout: httpClient.Timeout}
	}
	return youtubeapi.NewService(ctx, option.WithHTTPClient(httpClient))
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
