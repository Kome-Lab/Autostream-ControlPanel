package security

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGitHubWorkflowActionsArePinnedToCommitSHA(t *testing.T) {
	workflowPaths, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(workflowPaths) == 0 {
		t.Fatal("expected GitHub workflow files")
	}

	usesLine := regexp.MustCompile(`^\s*(?:-\s*)?uses:\s*([^#\s]+)`)
	pinnedRef := regexp.MustCompile(`@[0-9a-f]{40}$`)
	for _, workflowPath := range workflowPaths {
		file, err := os.Open(workflowPath)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		for lineNumber := 1; scanner.Scan(); lineNumber++ {
			match := usesLine.FindStringSubmatch(scanner.Text())
			if len(match) == 0 {
				continue
			}
			ref := strings.TrimSpace(match[1])
			if !pinnedRef.MatchString(ref) {
				t.Fatalf("%s:%d uses action %q without a full commit SHA", workflowPath, lineNumber, ref)
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("scan %s: %v", workflowPath, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("close %s: %v", workflowPath, err)
		}
	}
}
