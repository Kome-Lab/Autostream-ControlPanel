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
			for _, required := range []string{
				"Test production Docker port reconfiguration daemon boundary",
				`smoke_fixture="${RUNNER_TEMP}/docker-port-fixture"`,
				"go test -c ./internal/updateagent -o \"${smoke_binary}\"",
				"GOCACHE=\"${smoke_cache}\"",
				"TMPDIR=\"${smoke_tmp}\"",
				"-test.run '^TestDockerPortDaemonSmoke$'",
				"trap cleanup_docker_port_smoke EXIT",
				"/opt/autostream/.docker-port-smoke-owner",
				"/etc/autostream-local-executor/.docker-port-smoke-owner",
				"/run/autostream-updater/.autostream-host-lifecycle.lock",
				"/run/autostream-updater/.autostream-updater-f7868912f69c.lock",
			} {
				if !strings.Contains(stepTail, required) {
					t.Fatalf(
						"%s does not enforce Docker smoke contract %q",
						workflowName, required,
					)
				}
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
			normalizedStep := strings.ReplaceAll(
				stepTail, "\\\r\n", " ",
			)
			normalizedStep = strings.ReplaceAll(
				normalizedStep, "\\\n", " ",
			)
			commands := strings.Split(normalizedStep, "\n")
			fixtureBuild := -1
			rootExecution := -1
			for index, command := range commands {
				commands[index] = strings.Join(strings.Fields(command), " ")
				if strings.Contains(
					commands[index],
					"./internal/updateagent/testdata/docker-port-fixture",
				) {
					fixtureBuild = index
				}
				if strings.HasPrefix(
					commands[index],
					"sudo --preserve-env=AUTOSTREAM_DOCKER_PORT_DAEMON_SMOKE",
				) {
					rootExecution = index
				}
			}
			if fixtureBuild < 0 || rootExecution < 0 ||
				fixtureBuild >= rootExecution {
				t.Fatalf(
					"%s does not build the Docker fixture before root execution",
					workflowName,
				)
			}
			for _, required := range []string{
				"CGO_ENABLED=0",
				"GOOS=linux",
				"go build",
				`-o "${smoke_fixture}"`,
				"./internal/updateagent/testdata/docker-port-fixture",
			} {
				if !strings.Contains(commands[fixtureBuild], required) {
					t.Fatalf(
						"%s fixture build does not enforce %q: %s",
						workflowName, required, commands[fixtureBuild],
					)
				}
			}
			for _, required := range []string{
				"sudo --preserve-env=AUTOSTREAM_DOCKER_PORT_DAEMON_SMOKE",
				"env",
				`AUTOSTREAM_DOCKER_PORT_FIXTURE_BINARY="${smoke_fixture}"`,
				`"${smoke_binary}"`,
			} {
				if !strings.Contains(commands[rootExecution], required) {
					t.Fatalf(
						"%s root execution does not enforce %q: %s",
						workflowName, required, commands[rootExecution],
					)
				}
			}
		})
	}
}
