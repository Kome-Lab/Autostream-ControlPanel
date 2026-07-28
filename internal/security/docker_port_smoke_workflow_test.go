package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerPortDaemonSmokeRunsCompiledProductionHarnessAsRoot(t *testing.T) {
	t.Parallel()
	for _, workflowName := range []string{"ci.yml", "release-host.yml"} {
		workflowName := workflowName
		t.Run(workflowName, func(t *testing.T) {
			t.Parallel()
			payload, err := os.ReadFile(filepath.Join(
				"..", "..", ".github", "workflows", workflowName,
			))
			if err != nil {
				t.Fatal(err)
			}
			workflow := string(payload)
			for _, required := range []string{
				"Test production Docker port reconfiguration daemon boundary",
				"go test -c ./internal/updateagent -o \"${smoke_binary}\"",
				"sudo --preserve-env=AUTOSTREAM_DOCKER_PORT_DAEMON_SMOKE",
				"GOCACHE=\"${smoke_cache}\"",
				"TMPDIR=\"${smoke_tmp}\"",
				"\"${smoke_binary}\"",
				"-test.run '^TestDockerPortDaemonSmoke$'",
				"trap cleanup_docker_port_smoke EXIT",
				"/opt/autostream/.docker-port-smoke-owner",
				"/etc/autostream-local-executor/.docker-port-smoke-owner",
				"/run/autostream-updater/.autostream-host-lifecycle.lock",
				"/run/autostream-updater/.autostream-updater-f7868912f69c.lock",
			} {
				if !strings.Contains(workflow, required) {
					t.Fatalf(
						"%s does not enforce Docker smoke contract %q",
						workflowName, required,
					)
				}
			}
			stepStart := strings.Index(
				workflow,
				"- name: Test production Docker port reconfiguration daemon boundary",
			)
			if stepStart < 0 {
				t.Fatalf("%s has no Docker production smoke step", workflowName)
			}
			stepTail := workflow[stepStart:]
			if next := strings.Index(stepTail[1:], "\n      - name:"); next >= 0 {
				stepTail = stepTail[:next+1]
			}
			if strings.Contains(
				stepTail,
				"go test ./internal/updateagent",
			) {
				t.Fatalf(
					"%s recompiles the integration package as root",
					workflowName,
				)
			}
		})
	}
}
