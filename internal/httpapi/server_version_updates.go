package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/version"
)

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
	LatestVersion       string `json:"latest_version,omitempty"`
	UpdateCheckSource   string `json:"update_check_source"`
	UpdateCheckError    string `json:"update_check_error,omitempty"`
	ManifestVerified    bool   `json:"-"`
	ManifestErrorCode   string `json:"-"`
	MinimumAgentVersion string `json:"-"`
}

type updateReleaseAsset struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

const latestVersionCacheTTL = 5 * time.Minute

const latestVersionNegativeCacheTTL = 30 * time.Second

const maxHostUpdateArtifactBytes = int64(256 << 20)

type latestVersionCacheEntry struct {
	result    serviceUpdateInfoResponse
	expiresAt time.Time
	ready     chan struct{}
	loading   bool
}

type latestVersionResultCache struct {
	mu      sync.Mutex
	entries map[string]*latestVersionCacheEntry
}

var processLatestVersionCache = &latestVersionResultCache{entries: map[string]*latestVersionCacheEntry{}}

func (c *latestVersionResultCache) get(ctx context.Context, key string, load func(context.Context) serviceUpdateInfoResponse) serviceUpdateInfoResponse {
	now := time.Now().UTC()
	c.mu.Lock()
	if entry := c.entries[key]; entry != nil {
		if entry.loading {
			ready := entry.ready
			c.mu.Unlock()
			select {
			case <-ready:
				return entry.result
			case <-ctx.Done():
				return serviceUpdateInfoResponse{UpdateCheckSource: "cache", UpdateCheckError: "update check request canceled", ManifestErrorCode: "release_manifest_invalid"}
			}
		}
		if now.Before(entry.expiresAt) {
			result := entry.result
			c.mu.Unlock()
			return result
		}
	}
	entry := &latestVersionCacheEntry{ready: make(chan struct{}), loading: true}
	c.entries[key] = entry
	c.mu.Unlock()

	go func() {
		loadCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		result := load(loadCtx)
		ttl := latestVersionCacheTTL
		if result.UpdateCheckError != "" || result.ManifestErrorCode != "" {
			ttl = latestVersionNegativeCacheTTL
		}
		c.mu.Lock()
		entry.result = result
		entry.expiresAt = time.Now().UTC().Add(ttl)
		entry.loading = false
		close(entry.ready)
		c.mu.Unlock()
	}()

	select {
	case <-entry.ready:
		return entry.result
	case <-ctx.Done():
		return serviceUpdateInfoResponse{UpdateCheckSource: "cache", UpdateCheckError: "update check request canceled", ManifestErrorCode: "release_manifest_invalid"}
	}
}

func (c *latestVersionResultCache) clear() {
	c.mu.Lock()
	c.entries = map[string]*latestVersionCacheEntry{}
	c.mu.Unlock()
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
	defaultDockerUpdateCheckURL        = "https://api.github.com/repos/Kome-Lab/Autostream-Docker/releases/latest"
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

var dockerVersionUpdateTarget = versionUpdateTarget{
	serviceType:       "docker",
	latestVersionEnv:  "AUTOSTREAM_DOCKER_LATEST_VERSION",
	updateCheckURLEnv: "AUTOSTREAM_DOCKER_UPDATE_CHECK_URL",
	defaultURL:        defaultDockerUpdateCheckURL,
}

func (s *Server) versionInfo(w http.ResponseWriter, r *http.Request) {
	targets := make([]versionUpdateTarget, 0, len(nodeVersionUpdateTargets)+2)
	targets = append(targets, controlPanelVersionUpdateTarget)
	targets = append(targets, nodeVersionUpdateTargets...)
	targets = append(targets, dockerVersionUpdateTarget)
	updates := latestVersions(r.Context(), targets)
	panelUpdate := updates[controlPanelVersionUpdateTarget.serviceType]
	serviceUpdates := make(map[string]serviceUpdateInfoResponse, len(nodeVersionUpdateTargets)+1)
	for _, target := range nodeVersionUpdateTargets {
		serviceUpdates[target.serviceType] = updates[target.serviceType]
	}
	serviceUpdates[dockerVersionUpdateTarget.serviceType] = updates[dockerVersionUpdateTarget.serviceType]
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
			results[index] = latestVersion(ctx, target)
		}(index, target)
	}
	wait.Wait()

	updates := make(map[string]serviceUpdateInfoResponse, len(targets))
	for index, target := range targets {
		updates[target.serviceType] = results[index]
	}
	return updates
}

func latestVersion(ctx context.Context, target versionUpdateTarget) serviceUpdateInfoResponse {
	latestOverride := strings.TrimSpace(os.Getenv(target.latestVersionEnv))
	rawURL := strings.TrimSpace(os.Getenv(target.updateCheckURLEnv))
	if rawURL == "" {
		rawURL = target.defaultURL
	}
	cacheKey := strings.Join([]string{target.serviceType, latestOverride, rawURL, security.HashToken(strings.TrimSpace(os.Getenv("AUTOSTREAM_UPDATE_CHECK_TOKEN")))}, "\x00")
	return processLatestVersionCache.get(ctx, cacheKey, func(loadCtx context.Context) serviceUpdateInfoResponse {
		return latestVersionUncached(loadCtx, target, latestOverride, rawURL)
	})
}

func latestVersionUncached(ctx context.Context, target versionUpdateTarget, latestOverride, rawURL string) serviceUpdateInfoResponse {
	if latestOverride != "" {
		return serviceUpdateInfoResponse{LatestVersion: latestOverride, UpdateCheckSource: "env", ManifestErrorCode: "manifest_unverified"}
	}
	source := "url"
	if strings.TrimSpace(os.Getenv(target.updateCheckURLEnv)) == "" {
		source = "github"
	} else if strings.EqualFold(rawURL, "disabled") || strings.EqualFold(rawURL, "off") || strings.EqualFold(rawURL, "false") {
		return serviceUpdateInfoResponse{UpdateCheckSource: "disabled"}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return serviceUpdateInfoResponse{UpdateCheckSource: source, UpdateCheckError: "invalid update check url", ManifestErrorCode: "release_manifest_invalid"}
	}
	if parsed.User != nil {
		return serviceUpdateInfoResponse{UpdateCheckSource: source, UpdateCheckError: "update check url must not include credentials", ManifestErrorCode: "release_manifest_invalid"}
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLocalUpdateCheckHost(parsed.Hostname())) {
		return serviceUpdateInfoResponse{UpdateCheckSource: source, UpdateCheckError: "update check url must use https", ManifestErrorCode: "release_manifest_invalid"}
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return serviceUpdateInfoResponse{UpdateCheckSource: source, UpdateCheckError: "create update check request failed", ManifestErrorCode: "release_manifest_invalid"}
	}
	req.Header.Set("Accept", "application/json, text/plain")
	req.Header.Set("User-Agent", "autostream-control-panel/"+version.Current())
	if token := strings.TrimSpace(os.Getenv("AUTOSTREAM_UPDATE_CHECK_TOKEN")); token != "" && source == "github" && trustedGitHubUpdateAPIURL(parsed) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return serviceUpdateInfoResponse{UpdateCheckSource: source, UpdateCheckError: "update check request failed", ManifestErrorCode: "release_manifest_invalid"}
	}
	defer resp.Body.Close()
	body, err := readUpdateResponseLimited(resp.Body, 64*1024)
	if err != nil {
		return serviceUpdateInfoResponse{UpdateCheckSource: source, UpdateCheckError: "read update check response failed", ManifestErrorCode: "release_manifest_invalid"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return serviceUpdateInfoResponse{UpdateCheckSource: source, UpdateCheckError: "update check returned HTTP " + strconv.Itoa(resp.StatusCode), ManifestErrorCode: "release_manifest_invalid"}
	}
	if source != "github" {
		if latest := parseLatestVersionResponse(body); latest != "" {
			return serviceUpdateInfoResponse{LatestVersion: latest, UpdateCheckSource: source, ManifestErrorCode: "manifest_unverified"}
		}
		return serviceUpdateInfoResponse{UpdateCheckSource: source, UpdateCheckError: "update check response did not include a version", ManifestErrorCode: "manifest_unverified"}
	}

	var release struct {
		TagName string               `json:"tag_name"`
		Assets  []updateReleaseAsset `json:"assets"`
	}
	if err := json.Unmarshal(body, &release); err != nil || strings.TrimSpace(release.TagName) == "" {
		return serviceUpdateInfoResponse{UpdateCheckSource: source, UpdateCheckError: "GitHub release response did not include a valid tag", ManifestErrorCode: "release_manifest_invalid"}
	}
	latest := strings.TrimSpace(release.TagName)
	assets := make(map[string]updateReleaseAsset, len(release.Assets))
	var manifestAsset updateReleaseAsset
	for _, asset := range release.Assets {
		assets[strings.TrimSpace(asset.Name)] = asset
		if asset.Name == "release-manifest.json" {
			manifestAsset = asset
		}
	}
	if strings.TrimSpace(manifestAsset.URL) == "" && strings.TrimSpace(manifestAsset.BrowserDownloadURL) == "" {
		return serviceUpdateInfoResponse{LatestVersion: latest, UpdateCheckSource: source, UpdateCheckError: "release-manifest.json asset is missing", ManifestErrorCode: "release_manifest_missing"}
	}
	minimumAgentVersion, err := verifyReleaseManifest(ctx, parsed, manifestAsset, assets, latest, target.serviceType)
	if err != nil {
		return serviceUpdateInfoResponse{LatestVersion: latest, UpdateCheckSource: source, UpdateCheckError: "release-manifest.json is unavailable or invalid", ManifestErrorCode: "release_manifest_invalid"}
	}
	return serviceUpdateInfoResponse{LatestVersion: latest, UpdateCheckSource: source, ManifestVerified: true, MinimumAgentVersion: minimumAgentVersion}
}
