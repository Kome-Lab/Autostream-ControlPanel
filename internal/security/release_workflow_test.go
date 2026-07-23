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
		`cp release/autostream-updater.json.example "${root}/autostream-updater.json.example"`,
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
		"-F draft=false",
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
		"softprops/action-gh-release",
		"overwrite_files:",
		"--clobber",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("release workflow contains unsafe direct-publication marker %q", forbidden)
		}
	}
}
