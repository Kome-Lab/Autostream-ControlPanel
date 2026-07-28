package security

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestLocalExecutorUnitHasExactWritePathAllowlist(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "systemd", "autostream-local-executor.service.example"))
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	scanner := bufio.NewScanner(strings.NewReader(string(payload)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "ReadWritePaths=") {
			got = append(got, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)

	want := []string{
		"ReadWritePaths=-/opt/autostream/control-panel",
		"ReadWritePaths=-/opt/autostream/discord-bot",
		"ReadWritePaths=-/opt/autostream/encoder-recorder",
		"ReadWritePaths=-/opt/autostream/host-agent",
		"ReadWritePaths=-/opt/autostream/local-executor",
		"ReadWritePaths=-/opt/autostream/observability",
		"ReadWritePaths=-/opt/autostream/worker",
		"ReadWritePaths=/etc/autostream-host-agent",
		"ReadWritePaths=/var/lib/autostream-local-executor",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("local executor write paths=%q want exact allowlist=%q", got, want)
	}
}

func TestLocalExecutorInstallerOnlyOwnsItsPrivateOptSubtree(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "release", "install-autostream-local-executor"))
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	scanner := bufio.NewScanner(strings.NewReader(string(payload)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "/opt/autostream") {
			got = append(got, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	want := []string{
		`readonly EXECUTOR_DATA_DIR="/opt/autostream/local-executor"`,
		`readonly HOST_RUNTIME_ROOT="/opt/autostream/host-agent"`,
		`if [[ -e /opt/autostream || -L /opt/autostream ]]; then`,
		`ensure_root_directory /opt/autostream 0755`,
		`remove_fresh_empty_directory /opt/autostream "${release_root_existed}" "root:root:755"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installer /opt/autostream authority=%q want=%q", got, want)
	}

	var hostRuntimeUses []string
	scanner = bufio.NewScanner(strings.NewReader(string(payload)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "HOST_RUNTIME_") {
			hostRuntimeUses = append(hostRuntimeUses, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	wantHostRuntimeUses := []string{
		`readonly HOST_RUNTIME_ROOT="/opt/autostream/host-agent"`,
		`readonly HOST_RUNTIME_CURRENT="${HOST_RUNTIME_ROOT}/current"`,
		`[[ $(readlink -- "${BINARY_DEST}") == "${HOST_RUNTIME_CURRENT}/bin/autostream-local-executor" ]] || \`,
		`[[ -L ${HOST_RUNTIME_CURRENT} ]] || \`,
		`[[ $(stat -c '%U:%G' -- "${HOST_RUNTIME_CURRENT}") == "root:root" ]] || \`,
		`current_target=$(readlink -- "${HOST_RUNTIME_CURRENT}")`,
		`expected_resolved="${HOST_RUNTIME_ROOT}/${current_target}/bin/autostream-local-executor"`,
	}
	if !reflect.DeepEqual(hostRuntimeUses, wantHostRuntimeUses) {
		t.Fatalf("installer Host Agent runtime access=%q want fixed read-validation allowlist=%q", hostRuntimeUses, wantHostRuntimeUses)
	}
}
