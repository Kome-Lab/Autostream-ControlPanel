package security

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const controlPanelLinuxRunner = "blacksmith-16vcpu-ubuntu-2404"

func TestControlPanelActionsUseBlacksmithLinux16VCPU(t *testing.T) {
	workflows := []struct {
		name         string
		requiredJobs []string
	}{
		{
			name:         "ci.yml",
			requiredJobs: []string{"service-installer", "go", "web"},
		},
		{
			name:         "release-host.yml",
			requiredJobs: []string{"release-host", "publish-release"},
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
				string(payload), workflow.requiredJobs,
			); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestControlPanelRunnerValidationAllowsNonLinuxJobs(t *testing.T) {
	workflow := `jobs:
  linux:
    runs-on: blacksmith-16vcpu-ubuntu-2404
  windows:
    runs-on: windows-latest
  macos:
    runs-on: macos-latest
`
	if err := validateControlPanelWorkflowRunners(workflow, []string{"linux"}); err != nil {
		t.Fatal(err)
	}
}

func TestControlPanelRunnerValidationRejectsMissingAndLegacyLinuxJobs(t *testing.T) {
	tests := []struct {
		name         string
		workflow     string
		requiredJobs []string
		wantError    string
	}{
		{
			name: "missing required job",
			workflow: `jobs:
  windows:
    runs-on: windows-latest
`,
			requiredJobs: []string{"linux"},
			wantError:    `required job "linux" does not declare a scalar runs-on`,
		},
		{
			name: "legacy runner on additional Linux job",
			workflow: `jobs:
  linux:
    runs-on: blacksmith-16vcpu-ubuntu-2404
  legacy-linux:
    runs-on: ubuntu-24.04
`,
			requiredJobs: []string{"linux"},
			wantError:    `job "legacy-linux" uses Linux runner "ubuntu-24.04"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateControlPanelWorkflowRunners(test.workflow, test.requiredJobs)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validate error = %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func validateControlPanelWorkflowRunners(
	workflow string, requiredJobs []string,
) error {
	runners, err := parseWorkflowJobRunners(workflow)
	if err != nil {
		return err
	}
	for _, job := range requiredJobs {
		runner, ok := runners[job]
		if !ok {
			return fmt.Errorf("required job %q does not declare a scalar runs-on", job)
		}
		if runner != controlPanelLinuxRunner {
			return fmt.Errorf(
				"required job %q uses runner %q, want %q",
				job, runner, controlPanelLinuxRunner,
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
		if isLinuxRunnerLabel(runner) && runner != controlPanelLinuxRunner {
			return fmt.Errorf(
				"job %q uses Linux runner %q, want %q",
				job, runner, controlPanelLinuxRunner,
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
