package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateHostInstallerCreatesOnlyPlannedSystemdPathsInsideTransaction(t *testing.T) {
	installerPath := filepath.Join("..", "..", "release", "install-autostream-update-host")
	payload, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	installer := string(payload)

	for _, marker := range []string{
		`^/opt/autostream/(control-panel|worker|encoder-recorder|discord-bot|observability)(/releases)?$`,
		`"${binary_stage}" installer-systemd-paths --config "${config_stage}"`,
		`mapfile -t systemd_bootstrap_paths`,
		`bootstrap_created_dirs+=("${current}")`,
		`rmdir -- "${bootstrap_created_dirs[bootstrap_index]}"`,
	} {
		if !strings.Contains(installer, marker) {
			t.Fatalf("installer is missing systemd bootstrap transaction marker %q", marker)
		}
	}

	transactionStart := strings.Index(installer, "commit_started=true")
	createLoop := -1
	if transactionStart >= 0 {
		if relative := strings.Index(installer[transactionStart:], `for bootstrap_path in "${systemd_bootstrap_paths[@]}"; do`); relative >= 0 {
			createLoop = transactionStart + relative
		}
	}
	activateKey := strings.Index(installer, `mv -f -- "${authorized_keys_stage}" "${AUTHORIZED_KEYS_DEST}"`)
	if transactionStart < 0 || createLoop < transactionStart || activateKey < createLoop {
		t.Fatalf("systemd path creation must occur inside the rollback transaction and before key activation")
	}
}
