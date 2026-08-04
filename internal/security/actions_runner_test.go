package security

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	controlPanelStandardRunner = "blacksmith-16vcpu-ubuntu-2404"
	controlPanelLargeRunner    = "blacksmith-32vcpu-ubuntu-2404"
	controlPanelPublishRunner  = "ubuntu-24.04"
)

func TestControlPanelActionsUseScopedLinuxRunners(t *testing.T) {
	workflows := []struct {
		name            string
		requiredRunners map[string]string
	}{
		{
			name: "ci.yml",
			requiredRunners: map[string]string{
				"service-installer": controlPanelLargeRunner,
				"go":                controlPanelLargeRunner,
				"web":               controlPanelStandardRunner,
			},
		},
		{
			name: "release-host.yml",
			requiredRunners: map[string]string{
				"release-host":    controlPanelLargeRunner,
				"publish-release": controlPanelPublishRunner,
			},
		},
	}
	workflowDir := filepath.Join("..", "..", ".github", "workflows")
	for _, workflow := range workflows {
		t.Run(workflow.name, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join(workflowDir, workflow.name))
			if err != nil {
				t.Fatal(err)
			}
			if err := validateControlPanelWorkflowRunners(
				string(payload), workflow.requiredRunners,
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestControlPanelRunnerValidationAllowsNonLinuxJobs(t *testing.T) {
	workflow := `jobs:
  linux:
    runs-on: blacksmith-32vcpu-ubuntu-2404
  windows:
    runs-on: windows-latest
  macos:
    runs-on: macos-latest
`
	if err := validateControlPanelWorkflowRunners(
		workflow, map[string]string{"linux": controlPanelLargeRunner},
	); err != nil {
		t.Fatal(err)
	}
}

func TestControlPanelRunnerValidationRejectsMissingAndLegacyLinuxJobs(t *testing.T) {
	tests := []struct {
		name            string
		workflow        string
		requiredRunners map[string]string
		wantError       string
	}{
		{
			name: "missing required job",
			workflow: `jobs:
  windows:
    runs-on: windows-latest
`,
			requiredRunners: map[string]string{"linux": controlPanelLargeRunner},
			wantError:       `required job "linux" does not declare a scalar runs-on`,
		},
		{
			name: "legacy runner on additional Linux job",
			workflow: `jobs:
  linux:
    runs-on: blacksmith-32vcpu-ubuntu-2404
  legacy-linux:
    runs-on: ubuntu-24.04
`,
			requiredRunners: map[string]string{"linux": controlPanelLargeRunner},
			wantError:       `job "legacy-linux" uses unscoped Linux runner "ubuntu-24.04"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateControlPanelWorkflowRunners(
				test.workflow, test.requiredRunners,
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validate error = %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestControlPanelReleaseJobsUseSeparatedRunnerGuards(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join(
		"..", "..", ".github", "workflows", "release-host.yml",
	))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(payload)
	guards := []struct {
		job         string
		name        string
		environment string
	}{
		{
			job:         "release-host",
			name:        "Require Blacksmith self-hosted build runner",
			environment: "self-hosted",
		},
		{
			job:         "publish-release",
			name:        "Require GitHub-hosted publication runner",
			environment: "github-hosted",
		},
	}
	for _, guardPolicy := range guards {
		block, err := workflowJobBlock(workflow, guardPolicy.job)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{
			"- name: " + guardPolicy.name,
			"RUNNER_ENVIRONMENT: ${{ runner.environment }}",
			`if [[ "${RUNNER_ENVIRONMENT}" != "` + guardPolicy.environment + `" ]]; then`,
			"exit 1",
		} {
			if !strings.Contains(block, marker) {
				t.Fatalf("release job %q is missing runner guard %q", guardPolicy.job, marker)
			}
		}
		steps := strings.Index(block, "\n    steps:")
		guard := strings.Index(block, "\n      - name: "+guardPolicy.name)
		if steps < 0 || guard < 0 {
			t.Fatalf("release job %q does not have a guarded steps block", guardPolicy.job)
		}
		firstStep := strings.Index(block[steps:], "\n      - ")
		if firstStep < 0 || steps+firstStep != guard {
			t.Fatalf("release job %q must run its runner guard as the first step", guardPolicy.job)
		}
	}
}

func TestControlPanelActionlintRecognizesBlacksmithRunner(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join(
		"..", "..", ".github", "actionlint.yaml",
	))
	if err != nil {
		t.Fatal(err)
	}
	config := string(payload)
	for _, runner := range []string{
		controlPanelStandardRunner,
		controlPanelLargeRunner,
	} {
		if strings.Count(config, runner) != 1 {
			t.Fatalf(
				"actionlint config must declare exact Blacksmith runner %q once",
				runner,
			)
		}
	}
}

func validateControlPanelWorkflowRunners(
	workflow string, requiredRunners map[string]string,
) error {
	runners, err := parseWorkflowJobRunners(workflow)
	if err != nil {
		return err
	}
	for job, requiredRunner := range requiredRunners {
		runner, ok := runners[job]
		if !ok {
			return fmt.Errorf("required job %q does not declare a scalar runs-on", job)
		}
		if runner != requiredRunner {
			return fmt.Errorf(
				"required job %q uses runner %q, want %q",
				job, runner, requiredRunner,
			)
		}
	}

	jobs := make([]string, 0, len(runners))
	for job := range runners {
		jobs = append(jobs, job)
	}
	sort.Strings(jobs)
	for _, job := range jobs {
		runner := runners[job]
		if _, ok := requiredRunners[job]; isLinuxRunnerLabel(runner) && !ok {
			return fmt.Errorf(
				"job %q uses unscoped Linux runner %q",
				job, runner,
			)
		}
	}
	return nil
}

func parseWorkflowJobRunners(workflow string) (map[string]string, error) {
	lines := strings.Split(strings.ReplaceAll(workflow, "\r\n", "\n"), "\n")
	runners := make(map[string]string)
	inJobs := false
	currentJob := ""
	for lineIndex, line := range lines {
		if line == "jobs:" {
			inJobs = true
			currentJob = ""
			continue
		}
		if !inJobs {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if line[0] != ' ' {
			break
		}
		if hasExactSpaceIndent(line, 2) && strings.HasSuffix(trimmed, ":") {
			currentJob = strings.TrimSuffix(trimmed, ":")
			continue
		}
		const runnerPrefix = "    runs-on:"
		if currentJob == "" || !strings.HasPrefix(line, runnerPrefix) {
			continue
		}
		if _, exists := runners[currentJob]; exists {
			return nil, fmt.Errorf(
				"job %q declares runs-on more than once (line %d)",
				currentJob, lineIndex+1,
			)
		}
		runner := normalizeRunnerLabel(strings.TrimPrefix(line, runnerPrefix))
		if runner == "" {
			return nil, fmt.Errorf(
				"job %q has an empty runs-on declaration (line %d)",
				currentJob, lineIndex+1,
			)
		}
		runners[currentJob] = runner
	}
	if !inJobs {
		return nil, fmt.Errorf("workflow does not declare jobs")
	}
	return runners, nil
}

func workflowJobBlock(workflow, requestedJob string) (string, error) {
	lines := strings.Split(strings.ReplaceAll(workflow, "\r\n", "\n"), "\n")
	inJobs := false
	collecting := false
	block := make([]string, 0)
	for _, line := range lines {
		if line == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && line[0] != ' ' {
			break
		}
		if trimmed != "" && hasExactSpaceIndent(line, 2) && strings.HasSuffix(trimmed, ":") {
			if collecting {
				return strings.Join(block, "\n"), nil
			}
			collecting = strings.TrimSuffix(trimmed, ":") == requestedJob
		}
		if collecting {
			block = append(block, line)
		}
	}
	if collecting {
		return strings.Join(block, "\n"), nil
	}
	return "", fmt.Errorf("workflow does not declare job %q", requestedJob)
}

func hasExactSpaceIndent(line string, count int) bool {
	if len(line) <= count || line[:count] != strings.Repeat(" ", count) {
		return false
	}
	return line[count] != ' '
}

func normalizeRunnerLabel(label string) string {
	label = strings.TrimSpace(label)
	if comment := strings.Index(label, " #"); comment >= 0 {
		label = strings.TrimSpace(label[:comment])
	}
	return strings.Trim(label, `"'`)
}

func isLinuxRunnerLabel(label string) bool {
	label = strings.ToLower(label)
	return strings.Contains(label, "ubuntu") || strings.Contains(label, "linux")
}
