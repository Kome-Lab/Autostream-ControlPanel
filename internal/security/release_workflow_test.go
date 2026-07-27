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
		`find . -type f ! -path './checksums.txt' -print0 | sort -z | xargs -0 sha256sum > checksums.txt`,
		`tar -C staging -czf "artifacts/${artifact}.tar.gz" "${artifact}"`,
	}
	position := 0
	for _, marker := range packagingContract {
		relative := strings.Index(workflow[position:], marker)
		if relative < 0 {
			t.Fatalf("release workflow is missing ordered updater packaging marker %q", marker)
		}
		position += relative + len(marker)
	}

	orderedContract := []string{
		"group: host-release-publish-${{ needs.release-host.outputs.version }}",
		"- name: Require repository immutable releases",
		`"repos/${GITHUB_REPOSITORY}/immutable-releases"`,
		"(.enabled == true)",
		"- name: Validate immutable release namespace and local asset set",
		"workflow_dispatch may not overwrite or reuse it",
		"- name: Create unpublished staging release",
		"-F draft=true",
		"- name: Upload all assets to staging release",
		"https://uploads.github.com/repos/${GITHUB_REPOSITORY}/releases/${DRAFT_RELEASE_ID}/assets?name=${name}",
		"- name: Verify staging release assets",
		".digest | type == \"string\" and test(\"^sha256:[0-9a-f]{64}$\")",
		"- name: Attest release manifest",
		"- name: Attest update host bootstrap manifest",
		"- name: Publish verified release atomically",
		"moved during staging; refusing to publish mismatched assets",
		"gh api --method DELETE \"repos/${GITHUB_REPOSITORY}/git/refs/tags/${DRAFT_TAG}\"",
		"Cannot re-confirm immutable releases immediately before publication",
		"-F draft=false",
		"(.immutable == true)",
		"(.state == \"uploaded\")",
		"- name: Delete unpublished staging release",
		"if: ${{ always() && steps.create-draft.outputs.release_id != '' }}",
		"gh api --method DELETE \"repos/${GITHUB_REPOSITORY}/releases/${DRAFT_RELEASE_ID}\"",
	}
	position = 0
	for _, marker := range orderedContract {
		relative := strings.Index(workflow[position:], marker)
		if relative < 0 {
			t.Fatalf("release workflow is missing ordered atomic-publication marker %q", marker)
		}
		position += relative + len(marker)
	}

	for _, forbidden := range []string{
		"autostream-updater.json.example",
		"softprops/action-gh-release",
		"overwrite_files:",
		"--clobber",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("release workflow contains unsafe direct-publication marker %q", forbidden)
		}
	}
	if _, err := os.Stat(filepath.Join("..", "..", "release", "autostream-updater.json.example")); !os.IsNotExist(err) {
		t.Fatalf("obsolete updater policy sample must not be shipped; stat error = %v", err)
	}
}

func TestHostReleaseCleanupRequiresPostPublicationVerification(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release-host.yml")
	payload, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(payload)

	cleanupMarker := "- name: Delete unpublished staging release"
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
