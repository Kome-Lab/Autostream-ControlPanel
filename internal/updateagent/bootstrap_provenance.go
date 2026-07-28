package updateagent

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/snappy"
	bundlev1 "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	updateHostBootstrapRepositoryID      = int64(1277646977)
	updateHostBootstrapRepositoryOwnerID = "94940953"
	updateHostBootstrapWorkflowPath      = ".github/workflows/release-host.yml"
	hostAgentManifestName                = "host-agent-manifest.json"
	updateHostBootstrapSLSAPredicateType = "https://slsa.dev/provenance/v1"
	updateHostBootstrapBuildType         = "https://actions.github.io/buildtypes/workflow/v1"
	updateHostBootstrapActionsIssuer     = "https://token.actions.githubusercontent.com"
	updateHostBootstrapBundleMaxBytes    = int64(8 << 20)
	updateHostBootstrapBundleHost        = "tmaproduction.blob.core.windows.net"
	updateHostBootstrapTUFHTTPTimeout    = 15 * time.Second
	updateHostBootstrapTUFMaxBytes       = int64(8 << 20)
	updateHostBootstrapTUFUserAgent      = "autostream-updater"
	updateHostBootstrapTUFHost           = "tuf-repo-cdn.sigstore.dev"
	updateHostBootstrapProvenanceTimeout = 2 * time.Minute
	updateHostBootstrapAttestationPages  = 5
	updateHostBootstrapAttestationsPage  = 100
	updateHostBootstrapAttestationBytes  = int64(4 << 20)
	updateHostBootstrapAttestationAPI    = "2026-03-10"
	updateHostBootstrapBundleMediaType   = "application/x-snappy"
	updateHostBootstrapDecodedMaxBytes   = int64(16 << 20)
)

type bootstrapProvenanceVerifier interface {
	Verify(
		ctx context.Context,
		downloader ReleaseDownloader,
		version string,
		manifestDigest string,
		tagCommit string,
	) error
}

type sigstoreBootstrapProvenanceVerifier struct{}

type sigstoreHostAgentProvenanceVerifier struct{}

type updateHostBootstrapAttestationList struct {
	Attestations []updateHostBootstrapAttestation `json:"attestations"`
}

type updateHostBootstrapAttestation struct {
	RepositoryID int64  `json:"repository_id"`
	BundleURL    string `json:"bundle_url"`
}

type updateHostBootstrapProvenanceStatement struct {
	Type          string `json:"_type"`
	PredicateType string `json:"predicateType"`
	Subject       []struct {
		Name   string            `json:"name"`
		Digest map[string]string `json:"digest"`
	} `json:"subject"`
	Predicate struct {
		BuildDefinition struct {
			BuildType          string `json:"buildType"`
			ExternalParameters struct {
				Workflow struct {
					Ref        string `json:"ref"`
					Repository string `json:"repository"`
					Path       string `json:"path"`
				} `json:"workflow"`
			} `json:"externalParameters"`
			InternalParameters struct {
				GitHub struct {
					EventName         string `json:"event_name"`
					RepositoryID      string `json:"repository_id"`
					RepositoryOwnerID string `json:"repository_owner_id"`
					RunnerEnvironment string `json:"runner_environment"`
				} `json:"github"`
			} `json:"internalParameters"`
			ResolvedDependencies []struct {
				URI    string            `json:"uri"`
				Digest map[string]string `json:"digest"`
			} `json:"resolvedDependencies"`
		} `json:"buildDefinition"`
		RunDetails struct {
			Builder struct {
				ID string `json:"id"`
			} `json:"builder"`
		} `json:"runDetails"`
	} `json:"predicate"`
}

func (d ReleaseDownloader) verifyUpdateHostBootstrapProvenance(
	ctx context.Context,
	version string,
	manifestDigest string,
	tagCommit string,
) error {
	provenanceVerifier := d.bootstrapProvenanceVerifier
	if provenanceVerifier == nil {
		provenanceVerifier = sigstoreBootstrapProvenanceVerifier{}
	}
	if err := provenanceVerifier.Verify(ctx, d, version, manifestDigest, tagCommit); err != nil {
		return fmt.Errorf("update host bootstrap manifest has no trusted Actions provenance: %w", err)
	}
	return nil
}

func (sigstoreBootstrapProvenanceVerifier) Verify(
	ctx context.Context,
	downloader ReleaseDownloader,
	version string,
	manifestDigest string,
	tagCommit string,
) error {
	return verifyTrustedReleaseManifestProvenance(
		ctx,
		downloader,
		updateHostBootstrapManifestName,
		version,
		manifestDigest,
		tagCommit,
	)
}

func (sigstoreHostAgentProvenanceVerifier) Verify(
	ctx context.Context,
	downloader ReleaseDownloader,
	version string,
	manifestDigest string,
	tagCommit string,
) error {
	return verifyTrustedReleaseManifestProvenance(
		ctx,
		downloader,
		hostAgentManifestName,
		version,
		manifestDigest,
		tagCommit,
	)
}

func verifyTrustedReleaseManifestProvenance(
	ctx context.Context,
	downloader ReleaseDownloader,
	manifestName string,
	version string,
	manifestDigest string,
	tagCommit string,
) error {
	var cancel context.CancelFunc
	var err error
	ctx, cancel, err = newUpdateHostBootstrapProvenanceContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	if (manifestName != updateHostBootstrapManifestName &&
		manifestName != hostAgentManifestName) ||
		!versionPattern.MatchString(version) ||
		!updateHostBootstrapCommitPattern.MatchString(tagCommit) ||
		len(manifestDigest) != 64 ||
		manifestDigest != strings.ToLower(manifestDigest) {
		return errors.New("provenance binding is invalid")
	}
	digestBytes, err := hex.DecodeString(manifestDigest)
	if err != nil {
		return errors.New("provenance digest is invalid")
	}

	attestations, err := downloader.listUpdateHostBootstrapAttestations(ctx, manifestDigest)
	if err != nil {
		return err
	}
	if len(attestations) == 0 {
		return errors.New("GitHub returned no build provenance attestations")
	}

	tufOptions := tuf.DefaultOptions().
		WithFetcher(newUpdateHostBootstrapTUFFetcher(ctx, updateHostBootstrapTUFHTTPTimeout))
	tufClient, err := tuf.New(tufOptions)
	if err != nil {
		return fmt.Errorf("initialize Sigstore trust metadata: %w", err)
	}
	trustedMaterial, err := root.GetTrustedRoot(tufClient)
	if err != nil {
		return fmt.Errorf("load Sigstore trusted root: %w", err)
	}
	sigstoreVerifier, err := verify.NewVerifier(
		trustedMaterial,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return errors.New("initialize Sigstore verifier")
	}
	identity, err := updateHostBootstrapCertificateIdentity(version, tagCommit)
	if err != nil {
		return errors.New("build trusted workflow identity")
	}

	for _, attestation := range attestations {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("verify update host bootstrap provenance: %w", err)
		}
		if attestation.RepositoryID != updateHostBootstrapRepositoryID {
			continue
		}
		bundleJSON, err := downloader.downloadUpdateHostBootstrapAttestationBundle(ctx, attestation.BundleURL)
		if err != nil {
			continue
		}
		signedBundle, err := parseUpdateHostBootstrapAttestationBundle(bundleJSON)
		if err != nil {
			continue
		}
		result, err := sigstoreVerifier.Verify(
			signedBundle,
			verify.NewPolicy(
				verify.WithArtifactDigest("sha256", digestBytes),
				verify.WithCertificateIdentity(identity),
			),
		)
		if err != nil {
			continue
		}
		if err := validateTrustedReleaseManifestProvenanceResult(
			result,
			identity,
			manifestName,
			version,
			manifestDigest,
			tagCommit,
		); err == nil {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("verify update host bootstrap provenance: %w", err)
	}
	return errors.New("no attestation matched the trusted release workflow and source commit")
}

func (d ReleaseDownloader) listUpdateHostBootstrapAttestations(
	ctx context.Context,
	manifestDigest string,
) ([]updateHostBootstrapAttestation, error) {
	endpoint, err := d.updateHostBootstrapAttestationEndpoint(manifestDigest)
	if err != nil {
		return nil, err
	}
	nextEndpoint := endpoint.String()
	attestations := make(
		[]updateHostBootstrapAttestation,
		0,
		updateHostBootstrapAttestationPages*updateHostBootstrapAttestationsPage,
	)
	for page := 0; page < updateHostBootstrapAttestationPages; page++ {
		response, linkHeaders, err := d.getUpdateHostBootstrapAttestationPage(ctx, nextEndpoint)
		if err != nil {
			return nil, fmt.Errorf("resolve update host bootstrap attestations: %w", err)
		}
		if len(response.Attestations) > updateHostBootstrapAttestationsPage {
			return nil, errors.New("GitHub returned too many build provenance attestations in one page")
		}
		attestations = append(attestations, response.Attestations...)
		if len(attestations) > updateHostBootstrapAttestationPages*updateHostBootstrapAttestationsPage {
			return nil, errors.New("GitHub returned too many build provenance attestations")
		}

		nextRaw, err := updateHostBootstrapNextLink(linkHeaders)
		if err != nil {
			return nil, fmt.Errorf("parse GitHub attestation pagination: %w", err)
		}
		if nextRaw == "" {
			return attestations, nil
		}
		if page == updateHostBootstrapAttestationPages-1 {
			return nil, errors.New("GitHub attestation pagination exceeds the page limit")
		}
		nextEndpoint, err = validateUpdateHostBootstrapAttestationNextURL(endpoint, nextRaw)
		if err != nil {
			return nil, err
		}
	}
	return nil, errors.New("GitHub attestation pagination exceeded its limit")
}

func (d ReleaseDownloader) updateHostBootstrapAttestationEndpoint(
	manifestDigest string,
) (*url.URL, error) {
	base := strings.TrimRight(strings.TrimSpace(d.APIBase), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	apiBase, err := url.Parse(base)
	if err != nil ||
		apiBase.Opaque != "" ||
		apiBase.Host == "" ||
		apiBase.User != nil ||
		apiBase.RawQuery != "" ||
		apiBase.Fragment != "" {
		return nil, errors.New("configured GitHub API base URL is invalid")
	}
	if apiBase.Scheme != "https" && !(d.AllowHTTPForTest && apiBase.Scheme == "http") {
		return nil, errors.New("configured GitHub API base URL must use HTTPS")
	}

	endpoint := *apiBase
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") +
		"/repos/" + url.PathEscape(updateHostBootstrapRepository.Owner) +
		"/" + url.PathEscape(updateHostBootstrapRepository.Repo) +
		"/attestations/" + url.PathEscape("sha256:"+manifestDigest)
	endpoint.RawPath = ""
	query := endpoint.Query()
	query.Set("per_page", strconv.Itoa(updateHostBootstrapAttestationsPage))
	query.Set("predicate_type", "provenance")
	endpoint.RawQuery = query.Encode()
	return &endpoint, nil
}

func (d ReleaseDownloader) getUpdateHostBootstrapAttestationPage(
	ctx context.Context,
	endpoint string,
) (updateHostBootstrapAttestationList, []string, error) {
	request, err := d.newRequest(ctx, endpoint)
	if err != nil {
		return updateHostBootstrapAttestationList{}, nil, err
	}
	request.Header.Set("X-GitHub-Api-Version", updateHostBootstrapAttestationAPI)
	client := d.httpClient()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(request)
	if err != nil {
		return updateHostBootstrapAttestationList{}, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return updateHostBootstrapAttestationList{}, nil,
			fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > updateHostBootstrapAttestationBytes {
		return updateHostBootstrapAttestationList{}, nil,
			errors.New("GitHub attestation page exceeds the size limit")
	}
	data, err := io.ReadAll(
		io.LimitReader(response.Body, updateHostBootstrapAttestationBytes+1),
	)
	if err != nil {
		return updateHostBootstrapAttestationList{}, nil, err
	}
	if int64(len(data)) == 0 || int64(len(data)) > updateHostBootstrapAttestationBytes {
		return updateHostBootstrapAttestationList{}, nil,
			errors.New("GitHub attestation page is empty or exceeds the size limit")
	}
	var page updateHostBootstrapAttestationList
	if err := json.Unmarshal(data, &page); err != nil {
		return updateHostBootstrapAttestationList{}, nil, err
	}
	return page, response.Header.Values("Link"), nil
}

func updateHostBootstrapNextLink(headers []string) (string, error) {
	raw := strings.TrimSpace(strings.Join(headers, ","))
	if raw == "" {
		return "", nil
	}
	var next string
	for _, segment := range strings.Split(raw, ",") {
		segment = strings.TrimSpace(segment)
		if !strings.HasPrefix(segment, "<") {
			return "", errors.New("GitHub returned a malformed Link header")
		}
		closeIndex := strings.IndexByte(segment, '>')
		if closeIndex <= 1 {
			return "", errors.New("GitHub returned a malformed Link target")
		}
		target := segment[1:closeIndex]
		parameters := strings.TrimSpace(segment[closeIndex+1:])
		if !strings.HasPrefix(parameters, ";") {
			return "", errors.New("GitHub returned a Link without parameters")
		}
		var relations []string
		relationSeen := false
		for _, parameter := range strings.Split(parameters[1:], ";") {
			name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok {
				return "", errors.New("GitHub returned a malformed Link parameter")
			}
			if !strings.EqualFold(strings.TrimSpace(name), "rel") {
				continue
			}
			if relationSeen {
				return "", errors.New("GitHub returned duplicate Link relations")
			}
			relationSeen = true
			value = strings.TrimSpace(value)
			if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
				return "", errors.New("GitHub returned a malformed Link relation")
			}
			relations = strings.Fields(value[1 : len(value)-1])
			if len(relations) == 0 {
				return "", errors.New("GitHub returned an empty Link relation")
			}
		}
		for _, relation := range relations {
			if relation != "next" {
				continue
			}
			if next != "" {
				return "", errors.New("GitHub returned duplicate next links")
			}
			next = target
		}
	}
	return next, nil
}

func validateUpdateHostBootstrapAttestationNextURL(
	initial *url.URL,
	raw string,
) (string, error) {
	if initial == nil {
		return "", errors.New("GitHub attestation endpoint is unavailable")
	}
	next, err := url.Parse(raw)
	if err != nil ||
		next.Opaque != "" ||
		next.Host == "" ||
		next.User != nil ||
		next.Fragment != "" ||
		next.ForceQuery ||
		!strings.EqualFold(next.Scheme, initial.Scheme) ||
		!strings.EqualFold(next.Host, initial.Host) ||
		!updateHostBootstrapAttestationPathMatches(initial.Path, next.Path) {
		return "", errors.New("GitHub attestation next link is outside the trusted endpoint")
	}
	query, err := url.ParseQuery(next.RawQuery)
	if err != nil ||
		len(query) != 3 ||
		len(query["per_page"]) != 1 ||
		query.Get("per_page") != strconv.Itoa(updateHostBootstrapAttestationsPage) ||
		len(query["predicate_type"]) != 1 ||
		query.Get("predicate_type") != "provenance" {
		return "", errors.New("GitHub attestation next link has unexpected query parameters")
	}
	before, hasBefore := query["before"]
	after, hasAfter := query["after"]
	if hasBefore == hasAfter ||
		(hasBefore && len(before) != 1) ||
		(hasAfter && len(after) != 1) {
		return "", errors.New("GitHub attestation next link must contain exactly one cursor")
	}
	cursor := ""
	if hasBefore {
		cursor = before[0]
	} else {
		cursor = after[0]
	}
	if len(cursor) == 0 || len(cursor) > 2048 {
		return "", errors.New("GitHub attestation next cursor is invalid")
	}
	for _, character := range cursor {
		if character < 0x21 || character > 0x7e {
			return "", errors.New("GitHub attestation next cursor is invalid")
		}
	}
	return next.String(), nil
}

func updateHostBootstrapAttestationPathMatches(initialPath string, nextPath string) bool {
	if nextPath == initialPath {
		return true
	}
	repositoryPath := "/repos/" +
		updateHostBootstrapRepository.Owner + "/" +
		updateHostBootstrapRepository.Repo +
		"/attestations/"
	repositoryIndex := strings.LastIndex(initialPath, repositoryPath)
	if repositoryIndex < 0 {
		return false
	}
	subject := initialPath[repositoryIndex+len(repositoryPath):]
	if subject == "" || strings.Contains(subject, "/") {
		return false
	}
	canonicalPath := initialPath[:repositoryIndex] +
		"/repositories/" +
		strconv.FormatInt(updateHostBootstrapRepositoryID, 10) +
		"/attestations/" +
		subject
	return nextPath == canonicalPath
}

func (d ReleaseDownloader) downloadUpdateHostBootstrapAttestationBundle(
	ctx context.Context,
	raw string,
) ([]byte, error) {
	if err := d.validateUpdateHostBootstrapAttestationBundleURL(raw); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", updateHostBootstrapBundleMediaType)
	request.Header.Set("User-Agent", "autostream-updater")

	client := d.httpClient()
	priorRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if err := d.validateUpdateHostBootstrapAttestationBundleURL(request.URL.String()); err != nil {
			return err
		}
		if priorRedirect != nil {
			return priorRedirect(request, via)
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("attestation bundle endpoint returned HTTP %d", response.StatusCode)
	}
	if response.Header.Get("Content-Type") != updateHostBootstrapBundleMediaType {
		return nil, errors.New("attestation bundle endpoint returned an unexpected media type")
	}
	if response.ContentLength > updateHostBootstrapBundleMaxBytes {
		return nil, errors.New("attestation bundle exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, updateHostBootstrapBundleMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) == 0 || int64(len(data)) > updateHostBootstrapBundleMaxBytes {
		return nil, errors.New("attestation bundle is empty or exceeds size limit")
	}
	return decodeUpdateHostBootstrapAttestationBundle(data)
}

func decodeUpdateHostBootstrapAttestationBundle(compressed []byte) ([]byte, error) {
	decodedLength, err := snappy.DecodedLen(compressed)
	if err != nil {
		return nil, errors.New("attestation bundle is not valid Snappy data")
	}
	if decodedLength <= 0 || int64(decodedLength) > updateHostBootstrapDecodedMaxBytes {
		return nil, errors.New("decoded attestation bundle is empty or exceeds size limit")
	}
	decoded, err := snappy.Decode(make([]byte, 0, decodedLength), compressed)
	if err != nil {
		return nil, errors.New("decode attestation bundle Snappy data")
	}
	if len(decoded) != decodedLength || int64(len(decoded)) > updateHostBootstrapDecodedMaxBytes {
		return nil, errors.New("decoded attestation bundle length is invalid")
	}
	return decoded, nil
}

func parseUpdateHostBootstrapAttestationBundle(data []byte) (*bundle.Bundle, error) {
	var protobufBundle bundlev1.Bundle
	if err := protojson.Unmarshal(data, &protobufBundle); err != nil {
		return nil, fmt.Errorf("parse attestation bundle protobuf JSON: %w", err)
	}
	signedBundle, err := bundle.NewBundle(&protobufBundle)
	if err != nil {
		return nil, fmt.Errorf("validate attestation bundle: %w", err)
	}
	return signedBundle, nil
}

func (d ReleaseDownloader) validateUpdateHostBootstrapAttestationBundleURL(raw string) error {
	bundleURL, err := url.Parse(raw)
	if err != nil ||
		bundleURL.Host == "" ||
		bundleURL.User != nil ||
		bundleURL.Fragment != "" {
		return errors.New("GitHub returned an invalid attestation bundle URL")
	}
	if d.AllowHTTPForTest {
		apiBase, apiErr := url.Parse(d.APIBase)
		if apiErr != nil ||
			bundleURL.Scheme != apiBase.Scheme ||
			!strings.EqualFold(bundleURL.Host, apiBase.Host) {
			return errors.New("test attestation bundle URL must use the configured API origin")
		}
		return nil
	}
	if bundleURL.Scheme != "https" ||
		bundleURL.Port() != "" ||
		!strings.EqualFold(bundleURL.Hostname(), updateHostBootstrapBundleHost) {
		return errors.New("attestation bundle URL must use GitHub's production attestation storage")
	}
	return nil
}

func updateHostBootstrapCertificateIdentity(
	version string,
	tagCommit string,
) (verify.CertificateIdentity, error) {
	workflowURI := updateHostBootstrapWorkflowURI(version)
	sanMatcher, err := verify.NewSANMatcher(workflowURI, "")
	if err != nil {
		return verify.CertificateIdentity{}, err
	}
	issuerMatcher, err := verify.NewIssuerMatcher(updateHostBootstrapActionsIssuer, "")
	if err != nil {
		return verify.CertificateIdentity{}, err
	}
	repositoryURI := updateHostBootstrapRepositoryURI()
	return verify.NewCertificateIdentity(
		sanMatcher,
		issuerMatcher,
		certificate.Extensions{
			BuildSignerURI:                      workflowURI,
			BuildSignerDigest:                   tagCommit,
			RunnerEnvironment:                   "github-hosted",
			SourceRepositoryURI:                 repositoryURI,
			SourceRepositoryDigest:              tagCommit,
			SourceRepositoryRef:                 "refs/tags/" + version,
			SourceRepositoryIdentifier:          strconv.FormatInt(updateHostBootstrapRepositoryID, 10),
			SourceRepositoryOwnerURI:            "https://github.com/" + updateHostBootstrapRepository.Owner,
			SourceRepositoryOwnerIdentifier:     updateHostBootstrapRepositoryOwnerID,
			BuildConfigURI:                      workflowURI,
			BuildConfigDigest:                   tagCommit,
			BuildTrigger:                        "push",
			SourceRepositoryVisibilityAtSigning: "public",
		},
	)
}

func validateUpdateHostBootstrapProvenanceResult(
	result *verify.VerificationResult,
	identity verify.CertificateIdentity,
	version string,
	manifestDigest string,
	tagCommit string,
) error {
	return validateTrustedReleaseManifestProvenanceResult(
		result,
		identity,
		updateHostBootstrapManifestName,
		version,
		manifestDigest,
		tagCommit,
	)
}

func validateTrustedReleaseManifestProvenanceResult(
	result *verify.VerificationResult,
	identity verify.CertificateIdentity,
	manifestName string,
	version string,
	manifestDigest string,
	tagCommit string,
) error {
	if result == nil ||
		result.Statement == nil ||
		result.Signature == nil ||
		result.Signature.Certificate == nil ||
		result.VerifiedIdentity == nil {
		return errors.New("Sigstore verification result is incomplete")
	}
	if err := identity.Verify(*result.Signature.Certificate); err != nil {
		return errors.New("verified certificate identity changed")
	}
	statementJSON, err := protojson.Marshal(result.Statement)
	if err != nil {
		return errors.New("marshal verified provenance statement")
	}
	var statement updateHostBootstrapProvenanceStatement
	if err := json.Unmarshal(statementJSON, &statement); err != nil {
		return errors.New("parse verified provenance statement")
	}

	expectedRef := "refs/tags/" + version
	expectedRepository := updateHostBootstrapRepositoryURI()
	expectedWorkflowURI := updateHostBootstrapWorkflowURI(version)
	if statement.Type != "https://in-toto.io/Statement/v1" ||
		statement.PredicateType != updateHostBootstrapSLSAPredicateType ||
		len(statement.Subject) != 1 ||
		statement.Subject[0].Name != manifestName ||
		statement.Subject[0].Digest["sha256"] != manifestDigest ||
		statement.Predicate.BuildDefinition.BuildType != updateHostBootstrapBuildType ||
		statement.Predicate.BuildDefinition.ExternalParameters.Workflow.Ref != expectedRef ||
		statement.Predicate.BuildDefinition.ExternalParameters.Workflow.Repository != expectedRepository ||
		statement.Predicate.BuildDefinition.ExternalParameters.Workflow.Path != updateHostBootstrapWorkflowPath ||
		statement.Predicate.BuildDefinition.InternalParameters.GitHub.EventName != "push" ||
		statement.Predicate.BuildDefinition.InternalParameters.GitHub.RepositoryID != strconv.FormatInt(updateHostBootstrapRepositoryID, 10) ||
		statement.Predicate.BuildDefinition.InternalParameters.GitHub.RepositoryOwnerID != updateHostBootstrapRepositoryOwnerID ||
		statement.Predicate.BuildDefinition.InternalParameters.GitHub.RunnerEnvironment != "github-hosted" ||
		statement.Predicate.RunDetails.Builder.ID != expectedWorkflowURI {
		return errors.New("verified provenance statement does not match the trusted release workflow")
	}
	expectedDependencyURI := "git+" + expectedRepository + "@" + expectedRef
	for _, dependency := range statement.Predicate.BuildDefinition.ResolvedDependencies {
		if dependency.URI == expectedDependencyURI && dependency.Digest["gitCommit"] == tagCommit {
			return nil
		}
	}
	return errors.New("verified provenance statement does not contain the trusted source commit")
}

func updateHostBootstrapRepositoryURI() string {
	return "https://github.com/" + updateHostBootstrapRepository.Owner + "/" + updateHostBootstrapRepository.Repo
}

func updateHostBootstrapWorkflowURI(version string) string {
	return updateHostBootstrapRepositoryURI() + "/" + updateHostBootstrapWorkflowPath + "@refs/tags/" + version
}

type updateHostBootstrapTUFFetcher struct {
	ctx            context.Context
	requestTimeout time.Duration
	expectedScheme string
	expectedHost   string
	client         *http.Client
}

func newUpdateHostBootstrapProvenanceContext(
	parent context.Context,
) (context.Context, context.CancelFunc, error) {
	if parent == nil {
		return nil, nil, errors.New("provenance context is required")
	}
	ctx, cancel := context.WithTimeout(parent, updateHostBootstrapProvenanceTimeout)
	return ctx, cancel, nil
}

func newUpdateHostBootstrapTUFFetcher(
	ctx context.Context,
	timeout time.Duration,
) *updateHostBootstrapTUFFetcher {
	if timeout <= 0 || timeout > updateHostBootstrapTUFHTTPTimeout {
		timeout = updateHostBootstrapTUFHTTPTimeout
	}
	return &updateHostBootstrapTUFFetcher{
		ctx:            ctx,
		requestTimeout: timeout,
		expectedScheme: "https",
		expectedHost:   updateHostBootstrapTUFHost,
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("Sigstore TUF redirects are not allowed")
			},
		},
	}
}

func (f *updateHostBootstrapTUFFetcher) DownloadFile(
	rawURL string,
	maxLength int64,
	_ time.Duration,
) ([]byte, error) {
	if f == nil ||
		f.ctx == nil ||
		f.client == nil ||
		f.requestTimeout <= 0 ||
		f.expectedScheme == "" ||
		f.expectedHost == "" {
		return nil, errors.New("Sigstore TUF fetcher is not initialized")
	}
	requestURL, err := url.Parse(rawURL)
	if err != nil ||
		requestURL.Opaque != "" ||
		requestURL.User != nil ||
		requestURL.Fragment != "" ||
		requestURL.RawQuery != "" ||
		!strings.EqualFold(requestURL.Scheme, f.expectedScheme) ||
		!strings.EqualFold(requestURL.Host, f.expectedHost) {
		return nil, errors.New("Sigstore TUF URL is outside the trusted origin")
	}
	if maxLength <= 0 {
		return nil, errors.New("Sigstore TUF download length is invalid")
	}
	if maxLength > updateHostBootstrapTUFMaxBytes {
		maxLength = updateHostBootstrapTUFMaxBytes
	}

	requestContext, cancel := context.WithTimeout(f.ctx, f.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", updateHostBootstrapTUFUserAgent)

	response, err := f.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download Sigstore TUF metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &metadata.ErrDownloadHTTP{
			StatusCode: response.StatusCode,
			URL:        rawURL,
		}
	}
	if response.ContentLength > maxLength {
		return nil, &metadata.ErrDownloadLengthMismatch{
			Msg: fmt.Sprintf(
				"Sigstore TUF response length %d exceeds the maximum %d",
				response.ContentLength,
				maxLength,
			),
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxLength+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) == 0 {
		return nil, errors.New("Sigstore TUF response is empty")
	}
	if int64(len(data)) > maxLength {
		return nil, &metadata.ErrDownloadLengthMismatch{
			Msg: fmt.Sprintf("Sigstore TUF response exceeds the maximum %d", maxLength),
		}
	}
	return data, nil
}
