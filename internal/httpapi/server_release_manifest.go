package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/example/autostream-control-panel/internal/version"
)

type updateReleaseManifest struct {
	SchemaVersion       int    `json:"schema_version"`
	ReleaseID           string `json:"release_id"`
	Channel             string `json:"channel"`
	PublishedAt         string `json:"published_at"`
	MinimumAgentVersion string `json:"minimum_agent_version"`
	BundleVersion       string `json:"bundle_version"`
	GeneratedAt         string `json:"generated_at"`
	Components          []struct {
		Service            string            `json:"service"`
		ServiceType        string            `json:"service_type"`
		SourceVersion      string            `json:"source_version"`
		Commit             string            `json:"commit"`
		Image              string            `json:"image"`
		ManifestDigest     string            `json:"manifest_digest"`
		PlatformDigests    map[string]string `json:"platform_digests"`
		RollbackCompatible bool              `json:"rollback_compatible"`
		DatabaseSchema     string            `json:"database_schema"`
		Artifacts          []struct {
			OS     string `json:"os"`
			Arch   string `json:"arch"`
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"artifacts"`
	} `json:"components"`
}

func verifyReleaseManifest(ctx context.Context, releaseURL *url.URL, asset updateReleaseAsset, assets map[string]updateReleaseAsset, releaseID, serviceType string) (string, error) {
	body, err := fetchReleaseUpdateAsset(ctx, releaseURL, asset, 256*1024, "application/json")
	if err != nil {
		return "", err
	}
	sidecarAsset, ok := assets["release-manifest.json.sha256"]
	if !ok {
		return "", errors.New("release manifest checksum asset is missing")
	}
	sidecar, err := fetchReleaseUpdateAsset(ctx, releaseURL, sidecarAsset, 64*1024, "text/plain")
	if err != nil || !releaseManifestSidecarMatches(body, sidecar) {
		return "", errors.New("release manifest checksum asset is invalid")
	}
	var manifest updateReleaseManifest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return "", err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", errors.New("release manifest contains trailing JSON")
	}
	publishedAtText := strings.TrimSpace(manifest.PublishedAt)
	publishedAt, publishedErr := time.Parse(time.RFC3339, publishedAtText)
	if manifest.SchemaVersion != 1 || strings.TrimSpace(manifest.ReleaseID) != releaseID || !validMinimumUpdateAgentVersion(manifest.MinimumAgentVersion) || publishedErr != nil || publishedAt.IsZero() || !strings.HasSuffix(publishedAtText, "Z") {
		return "", errors.New("release manifest identity mismatch")
	}
	if serviceType == "docker" {
		if strings.TrimSpace(manifest.BundleVersion) != releaseID || strings.TrimSpace(manifest.GeneratedAt) != publishedAtText {
			return "", errors.New("Docker release manifest identity mismatch")
		}
		if err := validateDockerUpdateManifest(manifest, assets); err != nil {
			return "", err
		}
		return manifest.MinimumAgentVersion, nil
	}
	if err := validateHostUpdateManifest(manifest, assets, releaseID, serviceType); err != nil {
		return "", err
	}
	return manifest.MinimumAgentVersion, nil
}

func fetchReleaseUpdateAsset(ctx context.Context, releaseURL *url.URL, asset updateReleaseAsset, maxBytes int64, browserAccept string) ([]byte, error) {
	rawAssetURL := strings.TrimSpace(asset.URL)
	apiAsset := rawAssetURL != ""
	if !apiAsset {
		rawAssetURL = strings.TrimSpace(asset.BrowserDownloadURL)
	}
	assetURL, err := url.Parse(rawAssetURL)
	if err != nil || assetURL.Scheme == "" || assetURL.Host == "" || assetURL.User != nil || assetURL.Fragment != "" {
		return nil, errors.New("invalid release asset URL")
	}
	if assetURL.Scheme != "https" && !(assetURL.Scheme == "http" && isLocalUpdateCheckHost(assetURL.Hostname())) {
		return nil, errors.New("release asset URL must use https")
	}
	sameHost := strings.EqualFold(assetURL.Host, releaseURL.Host)
	githubAssetHost := strings.EqualFold(releaseURL.Hostname(), "api.github.com") && strings.EqualFold(assetURL.Hostname(), "github.com")
	if !sameHost && !githubAssetHost {
		return nil, errors.New("release asset host is not trusted")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, assetURL.String(), nil)
	if err != nil {
		return nil, err
	}
	if apiAsset {
		req.Header.Set("Accept", "application/octet-stream")
	} else {
		req.Header.Set("Accept", browserAccept)
	}
	req.Header.Set("User-Agent", "autostream-control-panel/"+version.Current())
	if apiAsset && sameHost && trustedGitHubUpdateAPIURL(releaseURL) {
		if token := strings.TrimSpace(os.Getenv("AUTOSTREAM_UPDATE_CHECK_TOKEN")); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many release asset redirects")
		}
		if next.URL.Scheme != "https" && !(next.URL.Scheme == "http" && isLocalUpdateCheckHost(next.URL.Hostname())) {
			return errors.New("release asset redirect must use https")
		}
		if len(via) > 0 && !strings.EqualFold(next.URL.Host, via[len(via)-1].URL.Host) {
			next.Header.Del("Authorization")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("release asset request failed")
	}
	return readUpdateResponseLimited(resp.Body, maxBytes)
}

func readUpdateResponseLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("invalid update response size limit")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New("update response exceeds size limit")
	}
	return body, nil
}

func releaseManifestSidecarMatches(manifestBody, sidecarBody []byte) bool {
	line := string(sidecarBody)
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	if strings.ContainsAny(line, "\r\n") {
		return false
	}
	digest, fileName, ok := strings.Cut(line, "  ")
	if !ok || fileName != "release-manifest.json" || !validUpdateManifestSHA256(digest) {
		return false
	}
	actual := sha256.Sum256(manifestBody)
	return digest == hex.EncodeToString(actual[:])
}

func validateHostUpdateManifest(manifest updateReleaseManifest, assets map[string]updateReleaseAsset, releaseID, serviceType string) error {
	if manifest.Channel != "host" || strings.TrimSpace(manifest.BundleVersion) != "" || strings.TrimSpace(manifest.GeneratedAt) != "" {
		return errors.New("release manifest channel mismatch")
	}
	if _, ok := assets["release-manifest.json.sha256"]; !ok {
		return errors.New("release manifest checksum asset is missing")
	}
	if len(manifest.Components) != 1 {
		return errors.New("host release manifest must contain exactly one component")
	}
	wanted := updateManifestServiceName(serviceType)
	expectedDatabaseSchema := "none"
	if wanted == "control-panel" || wanted == "observability" {
		expectedDatabaseSchema = "backward_compatible"
	}
	matches := 0
	for _, component := range manifest.Components {
		name := strings.TrimSpace(component.Service)
		if name != wanted {
			continue
		}
		matches++
		if strings.TrimSpace(component.ServiceType) != "" || strings.TrimSpace(component.Image) != "" || strings.TrimSpace(component.ManifestDigest) != "" || len(component.PlatformDigests) != 0 || strings.TrimSpace(component.SourceVersion) != releaseID || !validUpdateManifestCommit(component.Commit) || !component.RollbackCompatible || component.DatabaseSchema != expectedDatabaseSchema {
			return errors.New("release manifest component version mismatch")
		}
		arches := map[string]bool{}
		for _, artifact := range component.Artifacts {
			expectedName := updateManifestArtifactPrefix(wanted) + "_" + releaseID + "_linux_" + artifact.Arch + ".tar.gz"
			if artifact.OS != "linux" || (artifact.Arch != "amd64" && artifact.Arch != "arm64") || arches[artifact.Arch] || artifact.Name != expectedName || artifact.Size <= 0 || artifact.Size > maxHostUpdateArtifactBytes || !validUpdateManifestSHA256(artifact.SHA256) {
				return errors.New("release manifest artifact is invalid")
			}
			if _, ok := assets[artifact.Name]; !ok {
				return errors.New("release manifest artifact asset is missing")
			}
			if _, ok := assets[artifact.Name+".sha256"]; !ok {
				return errors.New("release manifest checksum asset is missing")
			}
			arches[artifact.Arch] = true
		}
		if len(component.Artifacts) != 2 || !arches["amd64"] || !arches["arm64"] {
			return errors.New("release manifest artifacts are incomplete")
		}
	}
	if matches != 1 {
		return errors.New("release manifest component is missing or duplicated")
	}
	return nil
}

func validateDockerUpdateManifest(manifest updateReleaseManifest, assets map[string]updateReleaseAsset) error {
	if manifest.Channel != "docker" {
		return errors.New("release manifest channel mismatch")
	}
	if _, ok := assets["release-manifest.json.sha256"]; !ok {
		return errors.New("Docker release manifest checksum asset is missing")
	}
	required := map[string]bool{"control-panel": false, "worker": false, "encoder-recorder": false, "discord-bot": false, "observability": false}
	for _, component := range manifest.Components {
		name := strings.TrimSpace(component.Service)
		if _, ok := required[name]; !ok || required[name] {
			return errors.New("Docker release manifest component set is invalid")
		}
		expectedImage := "ghcr.io/kome-lab/autostream-docker/" + name + ":" + manifest.ReleaseID
		expectedDatabaseSchema := "none"
		if name == "control-panel" || name == "observability" {
			expectedDatabaseSchema = "backward_compatible"
		}
		if strings.TrimSpace(component.ServiceType) != "" || strings.TrimSpace(component.Commit) != "" || !component.RollbackCompatible || component.DatabaseSchema != expectedDatabaseSchema || len(component.Artifacts) != 0 || !validSystemUpdateVersion(component.SourceVersion) || strings.TrimSpace(component.Image) != expectedImage || !validUpdateManifestDigest(component.ManifestDigest) || len(component.PlatformDigests) != 2 || !validUpdateManifestDigest(component.PlatformDigests["linux/amd64"]) || !validUpdateManifestDigest(component.PlatformDigests["linux/arm64"]) {
			return errors.New("Docker release manifest component is invalid")
		}
		required[name] = true
	}
	for _, present := range required {
		if !present {
			return errors.New("Docker release manifest components are incomplete")
		}
	}
	return nil
}

func updateManifestServiceName(serviceType string) string {
	switch strings.TrimSpace(serviceType) {
	case "control_panel", "control-panel":
		return "control-panel"
	case "encoder_recorder":
		return "encoder-recorder"
	case "discord_bot":
		return "discord-bot"
	default:
		return strings.TrimSpace(serviceType)
	}
}

func updateManifestArtifactPrefix(serviceName string) string {
	return "autostream-" + strings.TrimSpace(serviceName)
}

func validUpdateManifestSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if value != strings.ToLower(value) {
		return false
	}
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validUpdateManifestCommit(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validMinimumUpdateAgentVersion(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != 'v' {
		return false
	}
	parts, ok := parseVersionParts(value)
	if !ok {
		return false
	}
	return value == "v"+strconv.Itoa(parts[0])+"."+strconv.Itoa(parts[1])+"."+strconv.Itoa(parts[2])
}

func validUpdateManifestDigest(value string) bool {
	value = strings.TrimSpace(value)
	if value != strings.ToLower(value) {
		return false
	}
	return strings.HasPrefix(value, "sha256:") && validUpdateManifestSHA256(strings.TrimPrefix(value, "sha256:"))
}
