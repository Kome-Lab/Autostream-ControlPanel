package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitHubWorkflowActionsArePinnedToCommitSHA(t *testing.T) {
	root := filepath.Join("..", "..")
	workflowPaths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(workflowPaths) == 0 {
		t.Fatal("expected GitHub workflow files")
	}
	yamlPaths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	workflowPaths = append(workflowPaths, yamlPaths...)
	for _, workflowPath := range workflowPaths {
		data, err := os.ReadFile(workflowPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkWorkflowSources(root, data); err != nil {
			t.Fatalf("%s: %v", workflowPath, err)
		}
	}
}
