package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostReleaseStagesVerifiesAndThenPublishes(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release-host.yml")
	payload, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(payload)
	if !strings.Contains(workflow, "MINIMUM_AGENT_VERSION: v1.7.0") || strings.Contains(workflow, `minimum_agent_version: "v1.0.0"`) {
		t.Fatal("host release does not require the first central-only updater version")
	}

	packagingContract := []string{
		"- name: Package linux artifacts",
		`component: "control-panel"`,
		`minimum_agent_version: "v1.7.0"`,
		`database_schema: "backward_compatible"`,
		`"${root}/artifact-manifest.json"`,
		`find . -type f ! -path './checksums.txt' -print0 | sort -z | xargs -0 sha256sum > checksums.txt`,
		`tar -C staging -czf "artifacts/${artifact}.tar.gz" "${artifact}"`,
		`component: "host-agent"`,
		`minimum_panel_version: $version`,
		`database_schema: "none"`,
		`"${host_agent_root}/artifact-manifest.json"`,
		"- name: Verify embedded artifact manifests against published manifests",
		`tar -xOzf "artifacts/${control_name}"`,
		`"${control_root}/checksums.txt" > "${control_checksums}"`,
		`grep -Fx -- "${control_embedded_sha}  ./artifact-manifest.json"`,
		`tar -xOzf "artifacts/${host_name}"`,
		`"${host_root}/checksums.txt" > "${host_checksums}"`,
		`grep -Fx -- "${host_embedded_sha}  ./artifact-manifest.json"`,
	}
	position := 0
	for _, marker := range packagingContract {
		relative := strings.Index(workflow[position:], marker)
		if relative < 0 {
			t.Fatalf("release workflow is missing ordered updater packaging marker %q", marker)
		}
		position += relative + len(marker)
	}
	for marker, minimumCount := range map[string]int{
		`(( size > 268435456 ))`:                                  2,
		`(.size | type == "number" and . > 0 and . <= 268435456)`: 2,
		`"s/<version>/${version}/g"`:                              1,
		`"s/<arch>/${arch}/g"`:                                    1,
		`"s/linux_amd64/linux_${arch}/g"`:                         2,
		`autostream-host-agent_${version}_linux_${arch}.tar.gz`:   1,
	} {
		if strings.Count(workflow, marker) < minimumCount {
			t.Fatalf(
				"release workflow needs at least %d occurrence(s) of %q",
				minimumCount,
				marker,
			)
		}
	}
	for _, marker := range []string{
		`recovery_protocol_version: 2,`,
		`(.recovery_protocol_version == 2) and`,
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf(
				"release workflow is missing current self-update recovery protocol marker %q",
				marker,
			)
		}
	}
	for _, stale := range []string{
		`recovery_protocol_version: 1,`,
		`(.recovery_protocol_version == 1) and`,
	} {
		if strings.Contains(workflow, stale) {
			t.Fatalf(
				"release workflow still emits or accepts legacy recovery protocol marker %q",
				stale,
			)
		}
	}

	orderedContract := []string{
		"group: host-release-publish-${{ needs.release-host.outputs.version }}",
		"- name: Require repository immutable releases",
		`"repos/${GITHUB_REPOSITORY}/immutable-releases"`,
		"(.enabled == true)",
		"- name: Validate immutable release namespace and local asset set",
		"workflow_dispatch may not overwrite or reuse it",
		`host-release-body.md`,
		"AutoStream Host ${RELEASE_VERSION}",
		"This ${RELEASE_VERSION} patch makes interrupted pull_v2 updates converge",
		"Every Stage error retains the immutable active plan",
		"root-ledger durability result can be uncertain",
		"next lease generation reconciles without restaging or reapplying",
		"stage_required means only that the exact job has no durable mutation ledger or apply-authorized state",
		"safely removes an exact orphan stage left before ledger commit",
		"it never claims that no staged directory existed",
		"failed at 100 percent with remote_stage_missing",
		"reconciling 99 proceeds directly to terminal 100",
		"stale lease or sequence drops only its rejected report cursor while active state is retained",
		"structured terminal job matching the immutable intent",
		"successful terminal report body must exactly match the committed job identity",
		"v1.9.9 and v1.9.10 Agents predate this terminal-proof contract",
		"system_update_terminal_proof_upgrade_required with HTTP 409",
		"registered Agent older than v1.9.11",
		"Control Panel ${RELEASE_VERSION} first",
		"matching verified Host Agent and Local Executor archive",
		"Normal --upgrade still rejects an active job",
		"sudo ./install/install-autostream-host-agent --upgrade --recover-active-job",
		"needs no Configure Token and never reruns configure",
		"runs once as the service account",
		"cannot claim a new job or invoke Stage or Apply",
		"transient systemd guard is armed before stopping the old Agent",
		"any failure restores and proves the previous Agent",
		"Never delete or edit the Host journal, root ledger",
		"Existing identity and policy are preserved",
		`host-release-body.sha256`,
		"- name: Create unpublished staging release",
		`-f body="$(< "${release_body_path}")"`,
		"-F draft=true",
		"- name: Upload all assets to staging release",
		"https://uploads.github.com/repos/${GITHUB_REPOSITORY}/releases/${DRAFT_RELEASE_ID}/assets?name=${name}",
		"- name: Verify staging release assets",
		`jq -j '.body'`,
		`cmp -s "${release_body_path}" "${draft_body_path}"`,
		"Draft release notes digest differs from the deterministic body",
		".digest | type == \"string\" and test(\"^sha256:[0-9a-f]{64}$\")",
		"- name: Attest release manifest",
		"- name: Attest update host bootstrap manifest",
		"- name: Attest Host Agent archives",
		"- name: Publish verified release atomically",
		"moved during staging; refusing to publish mismatched assets",
		"Cannot re-confirm immutable releases immediately before publication",
		`final_draft_json="${RUNNER_TEMP}/host-release-final-draft.json"`,
		"appeared immediately before publication; refusing to overwrite it",
		"does not resolve to workflow commit ${GITHUB_SHA} immediately before publish",
		"-F draft=false",
		"(.immutable == true)",
		`(.body | type == "string" and length > 0)`,
		`(.body | test("^Unpublished .* staging"; "i") | not)`,
		"(.state == \"uploaded\")",
		`cmp -s "${release_body_path}" "${published_body_path}"`,
		"Published release notes digest differs from the deterministic body",
		"Published tag ${RELEASE_VERSION} does not resolve to workflow commit ${GITHUB_SHA}",
		"gh api --method DELETE \"repos/${GITHUB_REPOSITORY}/git/refs/tags/${DRAFT_TAG}\"",
		"- name: Preserve failed release state for manual recovery",
		"if: ${{ always() && steps.create-draft.outputs.release_id != '' }}",
		"all refs for manual recovery; no release or ref was deleted",
	}
	position = 0
	for _, marker := range orderedContract {
		relative := strings.Index(workflow[position:], marker)
		if relative < 0 {
			t.Fatalf("release workflow is missing ordered atomic-publication marker %q", marker)
		}
		position += relative + len(marker)
	}
	publishPosition := strings.Index(workflow, `gh api --method PATCH "repos/${GITHUB_REPOSITORY}/releases/${DRAFT_RELEASE_ID}"`)
	publishedTagPosition := strings.LastIndex(workflow, "Published tag ${RELEASE_VERSION} does not resolve to workflow commit ${GITHUB_SHA}")
	stagingTagDeletePosition := strings.Index(workflow, `gh api --method DELETE "repos/${GITHUB_REPOSITORY}/git/refs/tags/${DRAFT_TAG}"`)
	cleanupPosition := strings.Index(workflow, "- name: Preserve failed release state for manual recovery")
	if !(publishPosition >= 0 &&
		publishedTagPosition > publishPosition &&
		stagingTagDeletePosition > publishedTagPosition &&
		cleanupPosition > stagingTagDeletePosition) {
		t.Fatal("workflow-owned staging tag may be deleted only after successful published release and final-tag verification")
	}
	if strings.Count(workflow, `gh api --method DELETE "repos/${GITHUB_REPOSITORY}/git/refs/tags/${DRAFT_TAG}"`) != 1 {
		t.Fatal("workflow-owned staging tag must have exactly one success-only deletion")
	}

	for _, forbidden := range []string{
		"autostream-updater.json.example",
		"softprops/action-gh-release",
		"overwrite_files:",
		"--clobber",
		"Unpublished AutoStream host release staging",
		`gh api --method DELETE "repos/${GITHUB_REPOSITORY}/releases/${DRAFT_RELEASE_ID}"`,
		`gh api --method DELETE "repos/${GITHUB_REPOSITORY}/git/refs/tags/${RELEASE_VERSION}"`,
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("release workflow contains unsafe direct-publication marker %q", forbidden)
		}
	}
	if _, err := os.Stat(filepath.Join("..", "..", "release", "autostream-updater.json.example")); !os.IsNotExist(err) {
		t.Fatalf("obsolete updater policy sample must not be shipped; stat error = %v", err)
	}
}

func TestCIAndHostReleaseRunEveryMariaDBContractWithoutSkips(t *testing.T) {
	for _, workflowName := range []string{"ci.yml", "release-host.yml"} {
		t.Run(workflowName, func(t *testing.T) {
			workflowPath := filepath.Join(
				"..",
				"..",
				".github",
				"workflows",
				workflowName,
			)
			payload, err := os.ReadFile(workflowPath)
			if err != nil {
				t.Fatal(err)
			}
			workflow := string(payload)
			for _, marker := range []string{
				`go test ./internal/store -list '^TestMariaDB'`,
				`if [[ "${#mariadb_tests[@]}" -eq 0 ]]; then`,
				`-run '^TestMariaDB'`,
				`startswith("TestMariaDB")`,
				`for test_name in "${mariadb_tests[@]}"; do`,
				`.Test == $test_name`,
				`((.Test // "") == "")`,
			} {
				if !strings.Contains(workflow, marker) {
					t.Fatalf(
						"MariaDB release gate is missing %q",
						marker,
					)
				}
			}
			if strings.Contains(
				workflow,
				`-run '^TestMariaDBUpdateAgentRegistrationSmoke$'`,
			) {
				t.Fatal("MariaDB release gate regressed to a single smoke test")
			}
		})
	}
}

func TestCIAndHostReleaseRunSystemUpdatesWebRegressionBeforeBuild(t *testing.T) {
	for _, workflowName := range []string{"ci.yml", "release-host.yml"} {
		t.Run(workflowName, func(t *testing.T) {
			workflowPath := filepath.Join(
				"..",
				"..",
				".github",
				"workflows",
				workflowName,
			)
			payload, err := os.ReadFile(workflowPath)
			if err != nil {
				t.Fatal(err)
			}
			workflow := string(payload)
			installPosition := strings.Index(workflow, "npm ci")
			regressionPosition := strings.Index(workflow, "npm run test:system-updates")
			buildPosition := strings.Index(workflow, "npm run build")
			if installPosition < 0 ||
				regressionPosition <= installPosition ||
				buildPosition <= regressionPosition {
				t.Fatalf(
					"%s must run the system updates Web regression after npm ci and before the production build",
					workflowName,
				)
			}
		})
	}
}

func TestHostReleaseCleanupRequiresPostPublicationVerification(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release-host.yml")
	payload, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(payload)

	cleanupMarker := "- name: Preserve failed release state for manual recovery"
	cleanupPosition := strings.Index(workflow, cleanupMarker)
	if cleanupPosition < 0 {
		t.Fatalf("release workflow is missing cleanup step %q", cleanupMarker)
	}
	publishStep := workflow[:cleanupPosition]
	finalAssetCheck := strings.LastIndex(publishStep, `"${RUNNER_TEMP}/host-release-published-assets.json"`)
	verifiedOutput := strings.LastIndex(publishStep, `echo "verification_state=verified" >> "${GITHUB_OUTPUT}"`)
	if finalAssetCheck < 0 || verifiedOutput <= finalAssetCheck {
		t.Fatal("publish step must emit verification_state=verified only after the final published asset comparison")
	}

	cleanupStep := workflow[cleanupPosition:]
	orderedCleanupContract := []string{
		`PUBLISH_VERIFICATION_STATE: ${{ steps.publish.outputs.verification_state }}`,
		`elif [[ "${release_tag}" == "${RELEASE_VERSION}" ]]; then`,
		`if [[ "${PUBLISH_VERIFICATION_STATE}" == "verified" ]]; then`,
		`echo "Verified release ${RELEASE_VERSION} is published; no staging draft remains."`,
		"else",
		"published-but-unverified",
		"Recovery:",
		"exit 1",
	}
	position := 0
	for _, marker := range orderedCleanupContract {
		relative := strings.Index(cleanupStep[position:], marker)
		if relative < 0 {
			t.Fatalf("release cleanup is missing ordered post-publication state marker %q", marker)
		}
		position += relative + len(marker)
	}
	if strings.Contains(cleanupStep, `--method DELETE "repos/${GITHUB_REPOSITORY}/releases/${DRAFT_RELEASE_ID}"`) {
		t.Fatal("release cleanup must preserve drafts because GitHub has no conditional release DELETE")
	}
}

func TestHostReleaseManualDispatchCannotPublish(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release-host.yml")
	payload, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(payload)

	resolveMarker := "- name: Resolve release metadata"
	resolvePosition := strings.Index(workflow, resolveMarker)
	if resolvePosition < 0 {
		t.Fatalf("release workflow is missing metadata step %q", resolveMarker)
	}
	testPosition := strings.Index(workflow[resolvePosition:], "- name: Test")
	if testPosition < 0 {
		t.Fatal("release workflow metadata step has no following test boundary")
	}
	resolveStep := workflow[resolvePosition : resolvePosition+testPosition]
	orderedContract := []string{
		`if [[ "${GITHUB_EVENT_NAME}" == "push" && "${GITHUB_REF_TYPE}" == "tag" ]]; then`,
		`push_release="true"`,
		`elif [[ "${GITHUB_EVENT_NAME}" == "workflow_dispatch" ]]; then`,
		`version="${INPUT_VERSION}"`,
		`if [[ "${INPUT_PUSH_RELEASE}" == "true" ]]; then`,
		"workflow_dispatch may build artifacts only",
		"runtime provenance requires a push event for refs/tags/<version>",
		"exit 1",
		`push_release="false"`,
		`echo "push_release=${push_release}" >> "${GITHUB_OUTPUT}"`,
	}
	position := 0
	for _, marker := range orderedContract {
		relative := strings.Index(resolveStep[position:], marker)
		if relative < 0 {
			t.Fatalf("release metadata is missing ordered manual-dispatch guard %q", marker)
		}
		position += relative + len(marker)
	}
	if strings.Contains(resolveStep, `push_release="${INPUT_PUSH_RELEASE}"`) {
		t.Fatal("workflow_dispatch push_release input must never flow directly to the publish job")
	}
}

func TestHostReleaseVerifiesActualArchivesAndPinsExactLegacyAssets(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release-host.yml")
	payload, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(payload)

	const stableVersionGuard = `if [[ ! "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then`
	if !strings.Contains(workflow, stableVersionGuard) {
		t.Fatal("stable Host Release workflow must reject prerelease version suffixes")
	}

	for _, marker := range []string{
		`bash -n .github/scripts/verify-release-archive.sh`,
		`bash -n .github/scripts/test-verify-release-archive.sh`,
		`bash .github/scripts/test-verify-release-archive.sh .github/scripts/verify-release-archive.sh`,
		`bash .github/scripts/verify-release-archive.sh "artifacts/${artifact}.tar.gz" "${artifact}"`,
		`bash .github/scripts/verify-release-archive.sh "artifacts/${host_agent_artifact}.tar.gz" "${host_agent_artifact}"`,
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("Host Release workflow is missing archive verifier marker %q", marker)
		}
	}

	expectedAssets := []string{
		"autostream-control-panel_${RELEASE_VERSION}_linux_amd64.tar.gz",
		"autostream-control-panel_${RELEASE_VERSION}_linux_amd64.tar.gz.sha256",
		"autostream-control-panel_${RELEASE_VERSION}_linux_arm64.tar.gz",
		"autostream-control-panel_${RELEASE_VERSION}_linux_arm64.tar.gz.sha256",
		"autostream-update-host_${RELEASE_VERSION}_linux_amd64.tar.gz",
		"autostream-update-host_${RELEASE_VERSION}_linux_amd64.tar.gz.sha256",
		"autostream-update-host_${RELEASE_VERSION}_linux_arm64.tar.gz",
		"autostream-update-host_${RELEASE_VERSION}_linux_arm64.tar.gz.sha256",
		"autostream-host-agent_${RELEASE_VERSION}_linux_amd64.tar.gz",
		"autostream-host-agent_${RELEASE_VERSION}_linux_amd64.tar.gz.sha256",
		"autostream-host-agent_${RELEASE_VERSION}_linux_arm64.tar.gz",
		"autostream-host-agent_${RELEASE_VERSION}_linux_arm64.tar.gz.sha256",
		"host-agent-manifest.json",
		"host-agent-manifest.json.sha256",
		"release-manifest.json",
		"release-manifest.json.sha256",
		"update-host-bootstrap-manifest.json",
		"update-host-bootstrap-manifest.json.sha256",
	}
	assertExactExpectedReleaseAssets(t, workflow, expectedAssets)

	verifierPath := filepath.Join("..", "..", ".github", "scripts", "verify-release-archive.sh")
	verifier, err := os.ReadFile(verifierPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		`release archive contains a non-file/non-directory member`,
		`release archive contains a non-canonical member name`,
		`release archive contains duplicate canonical member names`,
		`release checksums.txt does not cover the exact regular-file inventory`,
		`sha256sum --check --strict checksums.txt`,
	} {
		if !strings.Contains(string(verifier), marker) {
			t.Fatalf("release archive verifier is missing %q", marker)
		}
	}

	fixturePath := filepath.Join("..", "..", ".github", "scripts", "test-verify-release-archive.sh")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"extra-file",
		"missing-checksum",
		"stale-checksum",
		"duplicate-member",
		"symlink-entry",
		"fifo-entry",
		"canonical-alias",
	} {
		if !strings.Contains(string(fixture), marker) {
			t.Fatalf("release archive verifier fixture is missing %q", marker)
		}
	}
}

func assertExactExpectedReleaseAssets(t *testing.T, workflow string, expected []string) {
	t.Helper()

	lines := strings.Split(workflow, "\n")
	var actual []string
	inExpectedNames := false
	foundBlock := false
	for _, line := range lines {
		if strings.Contains(line, `cat > "${expected_names}" <<EOF`) {
			if foundBlock {
				t.Fatal("release workflow contains multiple expected_names heredocs")
			}
			foundBlock = true
			inExpectedNames = true
			continue
		}
		if !inExpectedNames {
			continue
		}
		name := strings.TrimSpace(line)
		if name == "EOF" {
			inExpectedNames = false
			continue
		}
		if name == "" {
			t.Fatal("expected_names heredoc contains an empty asset name")
		}
		actual = append(actual, name)
	}
	if !foundBlock || inExpectedNames {
		t.Fatal("release workflow expected_names heredoc is missing or unterminated")
	}

	seen := make(map[string]struct{}, len(actual))
	for _, name := range actual {
		if _, duplicate := seen[name]; duplicate {
			t.Fatalf("release workflow expected_names contains duplicate %q", name)
		}
		seen[name] = struct{}{}
	}
	if got, want := strings.Join(actual, "\n"), strings.Join(expected, "\n"); got != want {
		t.Fatalf("release workflow expected_names mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
