package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRepositoryFile(t *testing.T, path ...string) string {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func requireMarkers(t *testing.T, subject string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(subject, marker) {
			t.Fatalf("required marker is missing: %q", marker)
		}
	}
}

func rejectMarkers(t *testing.T, subject string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(subject, marker) {
			t.Fatalf("forbidden embedded-runtime marker remains: %q", marker)
		}
	}
}

func TestControlPanelReleaseRequiresExactCommitCI(t *testing.T) {
	workflow := readRepositoryFile(t, ".github", "workflows", "release-host.yml")
	requireMarkers(t, workflow,
		"Require successful CI for exact release commit",
		"actions: read",
		"actions/workflows/ci.yml/runs?head_sha=${GITHUB_SHA}&status=completed",
		".head_sha == $sha and .conclusion == \"success\"",
		"No successful ci.yml run exists for exact release commit",
	)
	rejectMarkers(t, workflow,
		"go test ./...",
		"npm run test:system-updates",
		"docker run",
		"./cmd/autostream-updater",
		"./cmd/autostream-host-agent",
		"./cmd/autostream-local-executor",
		"./cmd/autostream-update-host",
	)
}

func TestControlPanelReleaseIsControlPanelOnly(t *testing.T) {
	workflow := readRepositoryFile(t, ".github", "workflows", "release-host.yml")
	builder := readRepositoryFile(t, "scripts", "ci", "build-control-panel-release-candidate.sh")
	requireMarkers(t, workflow,
		"bash scripts/ci/build-control-panel-release-candidate.sh",
		"Attest Control Panel archives",
		"artifacts/autostream-control-panel_*.tar.gz",
		"artifacts/release-manifest.json",
		"Publish immutable Control Panel release",
	)
	requireMarkers(t, builder,
		"./cmd/control-panel",
		"systemd/autostream-control-panel.service.example",
		"release/install-autostream-control-panel",
		"release/autostream-backup-control-panel.example",
		"web/out",
		"service: \"control-panel\"",
		"embedded_updater_members=0",
	)
	rejectMarkers(t, workflow,
		"autostream-host-agent_",
		"autostream-updater_",
		"autostream-local-executor",
		"autostream-update-host",
	)
	rejectMarkers(t, builder,
		"go build -trimpath -ldflags=\"${ldflags}\" -o \"${root}/bin/autostream-updater\"",
		"./cmd/autostream-host-agent",
		"./cmd/autostream-local-executor",
		"./cmd/autostream-update-host",
	)
}

func TestControlPanelReleaseManualDispatchCannotPublish(t *testing.T) {
	workflow := readRepositoryFile(t, ".github", "workflows", "release-host.yml")
	requireMarkers(t, workflow,
		"if [[ \"${GITHUB_EVENT_NAME}\" == \"push\" && \"${GITHUB_REF_TYPE}\" == \"tag\" ]]",
		"elif [[ \"${GITHUB_EVENT_NAME}\" == \"workflow_dispatch\" ]]",
		"push_release=false",
		"if: needs.release-host.outputs.push_release == 'true'",
		"Release ${RELEASE_VERSION} already exists; publish a new version instead of mutating it.",
	)
	if strings.Count(workflow, "push_release=true") != 1 {
		t.Fatalf("publish authorization must have exactly one tag-only source")
	}
}

func TestCIVerifiesRemovalAndReleaseRehearsal(t *testing.T) {
	workflow := readRepositoryFile(t, ".github", "workflows", "ci.yml")
	requireMarkers(t, workflow,
		"Verify embedded Updater runtime is absent",
		"bash scripts/ci/verify-no-embedded-updater.sh",
		"Control Panel only release rehearsal",
		"bash scripts/ci/build-control-panel-release-candidate.sh",
		"RELEASE_REHEARSAL_RESULT",
	)
	rejectMarkers(t, workflow,
		"./internal/updateagent",
		"./cmd/autostream-updater",
		"./cmd/autostream-host-agent",
		"./cmd/autostream-local-executor",
		"run-local-executor-installer-smoke.sh",
		"run-host-agent-installer",
	)
}

func TestEmbeddedUpdaterRuntimePathsAreAbsent(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, path := range []string{
		"cmd/autostream-updater",
		"cmd/autostream-update-host",
		"cmd/autostream-host-agent",
		"cmd/autostream-local-executor",
		"internal/updateagent",
		"release/install-autostream-host-agent",
		"release/install-autostream-local-executor",
		"release/install-autostream-update-host",
		"systemd/autostream-updater.service.example",
		"systemd/autostream-host-agent.service.example",
		"systemd/autostream-local-executor.socket.example",
	} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Fatalf("embedded runtime path must be absent: %s: %v", path, err)
		}
	}
	for _, path := range []string{
		"internal/updateradapter/policy.go",
		"internal/httpapi/system_update_v2_adapter.go",
		"scripts/ci/verify-no-embedded-updater.sh",
		"scripts/ci/build-control-panel-release-candidate.sh",
	} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("required adapter/release path is missing: %s: %v", path, err)
		}
	}
}
