package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertControlPanelInstallerPackaging(t *testing.T, root string) {
	workflowBytes, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-host.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowBytes)
	for _, marker := range []string{
		`bash scripts/ci/build-control-panel-release-candidate.sh`,
		`- name: Attest Control Panel archives`,
		`autostream-control-panel_*.tar.gz`,
		`release-manifest.json`,
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("host release workflow is missing installer packaging marker %q", marker)
		}
	}
	ciBytes, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ciBytes), `bash -n release/install-autostream-control-panel`) {
		t.Fatal("CI must syntax-check the service installer")
	}
	if !strings.Contains(
		string(ciBytes),
		`sudo bash release/test-install-autostream-control-panel-integration.sh`,
	) {
		t.Fatal("CI must execute the service installer migration integration test")
	}
}
