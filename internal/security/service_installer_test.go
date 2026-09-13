package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestControlPanelReleaseShipsManagedServiceInstaller(t *testing.T) {
	root := filepath.Join("..", "..")
	installerPath := filepath.Join(root, "release", "install-autostream-control-panel")
	installerBytes, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(installerBytes)

	assertControlPanelInstallerTransactions(t, installer)
	assertControlPanelInstallerPackaging(t, root)
	assertControlPanelInstallerIntegration(t, root, installer)
	assertControlPanelInstallerOperatorPaths(t, root)
}
