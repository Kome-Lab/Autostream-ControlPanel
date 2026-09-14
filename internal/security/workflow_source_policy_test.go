package security

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

var localWorkflowRef = regexp.MustCompile(`^\./\.github/workflows/[A-Za-z0-9_-][A-Za-z0-9_.-]*\.ya?ml$`)
var externalCommitRef = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*(/[A-Za-z0-9_.-]+)*@[0-9a-f]{40}$`)

func workflowField(node *yaml.Node, name string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == name {
			return node.Content[i+1]
		}
	}
	return nil
}

func parseWorkflow(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("workflow must be a mapping")
	}
	var validate func(*yaml.Node) error
	validate = func(node *yaml.Node) error {
		if node.Kind == yaml.AliasNode || node.Anchor != "" {
			return fmt.Errorf("workflow source aliases are not allowed")
		}
		if node.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(node.Content); i += 2 {
				key := node.Content[i]
				if key.Kind != yaml.ScalarNode || seen[key.Value] {
					return fmt.Errorf("ambiguous workflow key")
				}
				seen[key.Value] = true
			}
		}
		for _, child := range node.Content {
			if err := validate(child); err != nil {
				return err
			}
		}
		return nil
	}
	return doc.Content[0], validate(&doc)
}

func checkLocalWorkflow(root, ref string) error {
	return checkLocalWorkflowStat(root, ref, os.Lstat)
}

func checkLocalWorkflowStat(root, ref string, lstat func(string) (os.FileInfo, error)) error {
	if !localWorkflowRef.MatchString(ref) || strings.Contains(ref, "..") {
		return fmt.Errorf("invalid local workflow reference")
	}
	// Lstat each component: even an in-repository link is not a regular source file.
	path := root
	parts := strings.Split(strings.TrimPrefix(ref, "./"), "/")
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := lstat(path)
		if err != nil {
			return fmt.Errorf("local workflow unavailable: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return fmt.Errorf("local workflow must use regular repository files")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc, err := parseWorkflow(data)
	if err != nil {
		return err
	}
	on := workflowField(doc, "on")
	callable := workflowField(on, "workflow_call") != nil
	if on != nil && on.Kind == yaml.ScalarNode {
		callable = on.Value == "workflow_call"
	}
	if on != nil && on.Kind == yaml.SequenceNode {
		for _, trigger := range on.Content {
			callable = callable || (trigger.Kind == yaml.ScalarNode && trigger.Value == "workflow_call")
		}
	}
	if !callable {
		return fmt.Errorf("local workflow lacks workflow_call")
	}
	return nil
}

func checkWorkflowSources(root string, data []byte) error {
	doc, err := parseWorkflow(data)
	if err != nil {
		return err
	}
	jobs := workflowField(doc, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode || len(jobs.Content) == 0 {
		return fmt.Errorf("workflow jobs required")
	}
	allowed := map[*yaml.Node]bool{}
	for i := 1; i < len(jobs.Content); i += 2 {
		job := jobs.Content[i]
		if uses := workflowField(job, "uses"); uses != nil {
			if workflowField(job, "steps") != nil || workflowField(job, "runs-on") != nil {
				return fmt.Errorf("reusable workflow cannot be a steps job")
			}
			allowed[uses] = true
		}
		if steps := workflowField(job, "steps"); steps != nil && steps.Kind == yaml.SequenceNode {
			for _, step := range steps.Content {
				if uses := workflowField(step, "uses"); uses != nil {
					allowed[uses] = false
				}
			}
		}
	}
	var visit func(*yaml.Node) error
	visit = func(node *yaml.Node) error {
		if node.Kind == yaml.MappingNode {
			for i := 0; i < len(node.Content); i += 2 {
				key, value := node.Content[i], node.Content[i+1]
				if key.Value == "uses" {
					jobLevel, found := allowed[value]
					if !found || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
						return fmt.Errorf("line %d: invalid uses position or value", key.Line)
					}
					ref := value.Value
					if strings.HasPrefix(ref, "./") && jobLevel {
						if err := checkLocalWorkflow(root, ref); err != nil {
							return fmt.Errorf("line %d: %w", key.Line, err)
						}
					} else if !externalCommitRef.MatchString(ref) || strings.Contains(ref, "/../") || strings.Contains(ref, "/./") {
						return fmt.Errorf("line %d: uses requires a full commit SHA or callable local job", key.Line)
					}
				}
			}
		}
		for _, child := range node.Content {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(doc)
}

func TestWorkflowSourcePolicy(t *testing.T) {
	root := t.TempDir()
	workflows := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflows, 0700); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"called.yml": "on:\n  workflow_call:\n", "called.yaml": "on: [workflow_call]\n", "ordinary.yml": "on: push\n", "text.yml": "name: workflow_call\non: push\n"} {
		if err := os.WriteFile(filepath.Join(workflows, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pin := "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"
	for _, ref := range []string{"./.github/workflows/called.yml", "./.github/workflows/called.yaml", "org/repo/.github/workflows/called.yml@3d3c42e5aac5ba805825da76410c181273ba90b1"} {
		if err := checkWorkflowSources(root, []byte("jobs:\n  example:\n    uses: "+ref+"\n")); err != nil {
			t.Fatal(ref, err)
		}
	}
	if err := checkWorkflowSources(root, []byte("# uses: bad@main\njobs:\n  example:\n    steps:\n      - uses: "+pin+"\n      - run: |\n          uses: bad@main\n")); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"./.github/workflows/called.yml@main", "./.github/workflows/called.yml?q", "./.github/workflows/called.yml#fragment", "./.github/workflows/../called.yml", "./.github/workflows/sub/called.yml", "./.github/workflows/missing.yml", "./.github/workflows/ordinary.yml", "./.github/workflows/text.yml", "./other.yml", "./.github/workflows/called.txt", "/.github/workflows/called.yml", "https://github.com/org/repo@" + strings.Repeat("a", 40), "${{ inputs.workflow }}", "org/repo@main", "org/repo@v1", "org/repo@abcdef1", "org/repo", "org/repo@" + strings.Repeat("a", 40) + "extra"} {
		t.Run(ref, func(t *testing.T) {
			if err := checkWorkflowSources(root, []byte("jobs:\n  example:\n    uses: '"+ref+"'\n")); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
	for name, source := range map[string]string{
		"step-local":     "jobs:\n  example:\n    steps:\n      - uses: ./.github/workflows/called.yml\n",
		"wrong-position": "uses: " + pin + "\njobs:\n  example: {}\n",
		"mixed-job":      "jobs:\n  example:\n    uses: ./.github/workflows/called.yml\n    steps: []\n",
		"duplicate":      "jobs:\n  example:\n    uses: " + pin + "\n    uses: org/repo@main\n",
		"non-scalar":     "jobs:\n  example:\n    uses: [" + pin + "]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkWorkflowSources(root, []byte(source)); err == nil {
				t.Fatal("invalid structure accepted")
			}
		})
	}
	if err := os.Mkdir(filepath.Join(workflows, "directory.yml"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkLocalWorkflow(root, "./.github/workflows/directory.yml"); err == nil {
		t.Fatal("directory accepted")
	}
}

type symbolicWorkflowInfo struct{ os.FileInfo }

func (info symbolicWorkflowInfo) Mode() os.FileMode { return info.FileInfo.Mode() | os.ModeSymlink }

func TestWorkflowSourcePolicyRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	workflows := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflows, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workflows, "escape.yml")
	if err := os.WriteFile(target, []byte("on: workflow_call\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Exercise the real policy at its Lstat boundary without requiring Windows
	// symlink privileges. Both a directory link and a final file link must fail.
	for _, linked := range []string{filepath.Join(root, ".github"), workflows, target} {
		calls := 0
		err := checkLocalWorkflowStat(root, "./.github/workflows/escape.yml", func(path string) (os.FileInfo, error) {
			info, err := os.Lstat(path)
			if path == linked && err == nil {
				calls++
				return symbolicWorkflowInfo{info}, nil
			}
			return info, err
		})
		if err == nil || calls != 1 {
			t.Fatal("symlink component accepted", linked, calls, err)
		}
	}
}
