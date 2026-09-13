package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertControlPanelInstallerOperatorPaths(t *testing.T, root string) {
	unitBytes, err := os.ReadFile(filepath.Join(root, "systemd", "autostream-control-panel.service.example"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(unitBytes)
	if !strings.Contains(unit, "ExecStart=/usr/local/bin/control-panel") {
		t.Fatal("Control Panel systemd unit must use the stable public binary path")
	}
	if strings.Contains(unit, "ExecStart=/opt/autostream/control-panel/current/") {
		t.Fatal("Control Panel systemd unit exposes installer-owned release internals")
	}

	guideBytes, err := os.ReadFile(filepath.Join(root, "release", "README.install.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(guideBytes)
	for _, marker := range []string{
		"sudo ./install-autostream-control-panel",
		"gh attestation verify /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz",
		"--repo Kome-Lab/Autostream-ControlPanel",
		"sudo install -o root -g root -m 0644 /tmp/autostream-control-panel_vX.Y.Z_linux_amd64.tar.gz",
		"sudo tar --no-same-owner --no-same-permissions -xzf",
		"サーバーへ転送する release asset は、この `.tar.gz` 1 個だけです",
		"AUTOSTREAM_WEB_DIR=/usr/share/autostream-control-panel",
		"installer-owned",
	} {
		if !strings.Contains(guide, marker) {
			t.Fatalf("install guide is missing simple installer marker %q", marker)
		}
	}
}
