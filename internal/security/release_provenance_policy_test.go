package security

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	v194RunbookSHA256     = "d5d22224e5914dec7f7581e03711c765754d4e8253af9408f10bf2511bbed121"
	releaseVersionPattern = regexp.MustCompile(`\bv[0-9]+\.[0-9]+\.[0-9]+\b`)
	releaseAssetPattern   = regexp.MustCompile(
		`autostream-[a-z0-9-]+_v[0-9]+\.[0-9]+\.[0-9]+_linux_[a-z0-9]+\.tar\.gz`,
	)
	releaseCommitPattern   = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	releaseDigestPattern   = regexp.MustCompile(`\b[0-9a-f]{64}\b`)
	releaseWorkflowPattern = regexp.MustCompile(
		`\.github/workflows/[A-Za-z0-9._/-]+\.ya?ml`,
	)
	releaseRepositoryTablePattern = regexp.MustCompile(
		"(?m)^\\| Repository \\| `([^`]+)` \\|$",
	)
	releaseRepositoryAssignmentPattern = regexp.MustCompile(
		`(?m)(?:^|[[:space:]])(?:readonly[[:space:]]+)?(?:local[[:space:]]+)?(?:REPO|repo)='([^']+)'`,
	)
)

func TestV194ReleaseExceptionRunbookIsPinnedAndDoesNotRelaxDefaultPolicy(t *testing.T) {
	root := filepath.Join("..", "..")
	runbookPath := filepath.Join(
		root, "docs", "operations", "v1.9.4-blacksmith-release-exception.md",
	)
	payload, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatal(err)
	}
	runbookSource := string(payload)
	if err := validateV194ReleaseExceptionScope(runbookSource); err != nil {
		t.Fatal(err)
	}
	if err := validateRunbookBashBlocksFailClosed(runbookSource); err != nil {
		t.Fatal(err)
	}
	if err := validateV194RunbookDigest(runbookSource); err != nil {
		t.Fatal(err)
	}
	runbook := strings.Join(strings.Fields(runbookSource), " ")
	for _, marker := range []string{
		"v1.9.4 only",
		"d13a3c45aa000ba941eb4c94ee9eb35326151300",
		"44cc5829d882e19f2096772e827dda0d52b851625be9aa4254429df3aa430ae8",
		"5766a06bc8763692588c7330af667c9e5095da4d79b9078921f4045e3ed41987",
		"gh release verify \"$VERSION\"",
		"gh release verify-asset \"$VERSION\" \"$asset\"",
		"--signer-digest \"$commit\"",
		"--source-ref \"refs/tags/$version\"",
		"--source-digest \"$commit\"",
		"--predicate-type https://slsa.dev/provenance/v1",
		"runnerEnvironment == \"self-hosted\"",
		"runner_environment == \"self-hosted\"",
		"sudo /opt/autostream/releases/artifacts/autostream-control-panel_v1.9.4_linux_amd64/install-autostream-control-panel",
		"sudo /opt/autostream/releases/artifacts/autostream-host-agent_v1.9.4_linux_amd64/install/install-autostream-host-agent --upgrade",
		"/usr/local/bin/control-panel --version",
		"autostream-control-panel v1.9.4",
		"(.version == \"v1.9.4\") and (.service_type == \"control_panel\")",
		"/usr/local/bin/autostream-host-agent --version",
		"autostream-host-agent v1.9.4",
		"/usr/local/libexec/autostream-local-executor --version",
		"autostream-local-executor v1.9.4",
		"automatic updater remains fail-closed",
		"expensive production artifact build use `blacksmith-16vcpu-ubuntu-2404`",
		"publication/attestation job uses `ubuntu-24.04`",
		"flag proves that the **attestation issuer** was not self-hosted",
	} {
		if !strings.Contains(runbook, marker) {
			t.Fatalf("v1.9.4 exception runbook is missing %q", marker)
		}
	}

	for _, artifact := range []struct {
		name      string
		installer string
		markers   []string
	}{
		{
			name:      "Control Panel",
			installer: "sudo /opt/autostream/releases/artifacts/autostream-control-panel_v1.9.4_linux_amd64/install-autostream-control-panel",
			markers: []string{
				"require_root_owned_directory /opt",
				"ensure_root_owned_directory /opt/autostream",
				"ensure_root_owned_directory /opt/autostream/releases",
				"ensure_root_owned_directory /opt/autostream/releases/artifacts",
				"sudo install -o root -g root -m 0644",
				"44cc5829d882e19f2096772e827dda0d52b851625be9aa4254429df3aa430ae8",
				"sha256sum --check --strict -",
				"sudo tar --no-same-owner --no-same-permissions",
				"-C /opt/autostream/releases/artifacts",
				"-xzf /opt/autostream/releases/artifacts/autostream-control-panel_v1.9.4_linux_amd64.tar.gz",
				"sudo /opt/autostream/releases/artifacts/autostream-control-panel_v1.9.4_linux_amd64/install-autostream-control-panel",
			},
		},
		{
			name:      "Host Agent",
			installer: "sudo /opt/autostream/releases/artifacts/autostream-host-agent_v1.9.4_linux_amd64/install/install-autostream-host-agent --upgrade",
			markers: []string{
				"require_root_owned_directory /opt",
				"require_root_owned_directory /opt/autostream",
				"require_root_owned_directory /opt/autostream/releases",
				"require_root_owned_directory /opt/autostream/releases/artifacts",
				"sudo install -o root -g root -m 0644",
				"5766a06bc8763692588c7330af667c9e5095da4d79b9078921f4045e3ed41987",
				"sha256sum --check --strict -",
				"sudo tar --no-same-owner --no-same-permissions",
				"-C /opt/autostream/releases/artifacts",
				"-xzf /opt/autostream/releases/artifacts/autostream-host-agent_v1.9.4_linux_amd64.tar.gz",
				"sudo /opt/autostream/releases/artifacts/autostream-host-agent_v1.9.4_linux_amd64/install/install-autostream-host-agent --upgrade",
			},
		},
	} {
		block, err := runbookBashBlockContaining(runbookSource, artifact.installer)
		if err != nil {
			t.Fatal(err)
		}
		if err := requireOrderedRunbookMarkers(block, artifact.markers); err != nil {
			t.Fatalf("%s archive binding: %v", artifact.name, err)
		}
	}

	for _, runtime := range []struct {
		name    string
		anchor  string
		markers []string
	}{
		{
			name:   "Control Panel",
			anchor: "/usr/local/bin/control-panel --version",
			markers: []string{
				"sudo systemctl restart autostream-control-panel",
				"/usr/local/bin/control-panel --version",
				"autostream-control-panel v1.9.4",
				"commit: d13a3c45aa000ba941eb4c94ee9eb35326151300",
				"curl -fsS http://127.0.0.1:8080/health",
				"curl -fsS http://127.0.0.1:8080/updater/version",
				"(.version == \"v1.9.4\") and (.service_type == \"control_panel\")",
			},
		},
		{
			name:   "Host Agent and Local Executor",
			anchor: "/usr/local/bin/autostream-host-agent --version",
			markers: []string{
				"/usr/local/bin/autostream-host-agent --version",
				"/usr/local/libexec/autostream-local-executor --version",
				"autostream-host-agent v1.9.4",
				"commit: d13a3c45aa000ba941eb4c94ee9eb35326151300",
				"autostream-local-executor v1.9.4",
				"commit: d13a3c45aa000ba941eb4c94ee9eb35326151300",
				"/usr/local/bin/autostream-host-agent validate-config",
				"/usr/local/libexec/autostream-local-executor validate-policy",
			},
		},
	} {
		block, err := runbookBashBlockContaining(runbookSource, runtime.anchor)
		if err != nil {
			t.Fatal(err)
		}
		if err := requireOrderedRunbookMarkers(block, runtime.markers); err != nil {
			t.Fatalf("%s runtime verification: %v", runtime.name, err)
		}
	}
	rootReadme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		string(rootReadme),
		"docs/operations/v1.9.4-blacksmith-release-exception.md",
	) {
		t.Fatal("root README must link to the v1.9.4 release exception runbook")
	}

	for _, guide := range []string{
		filepath.Join(root, "release", "README.install.md"),
		filepath.Join(root, "release", "README.host-agent.md"),
		filepath.Join(root, "release", "README.bootstrap.md"),
	} {
		payload, err := os.ReadFile(guide)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(payload), "--deny-self-hosted-runners") {
			t.Fatalf("default release guide %s must continue to deny self-hosted runners", guide)
		}
	}
	for _, guide := range []string{
		filepath.Join(root, "release", "README.install.md"),
		filepath.Join(root, "release", "README.host-agent.md"),
	} {
		payload, err := os.ReadFile(guide)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{
			"constrains the job that issues this attestation",
			"trusted\nBlacksmith build job",
			"does not claim that compilation ran on a GitHub-hosted runner",
		} {
			if !strings.Contains(string(payload), marker) {
				t.Fatalf("default release guide %s is missing trust-boundary marker %q", guide, marker)
			}
		}
	}
	bootstrapGuide, err := os.ReadFile(filepath.Join(root, "release", "README.bootstrap.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"GitHub-hosted attestation-issuing publication",
		"separately trusted Blacksmith build boundary",
		"does\n not prove that compilation was GitHub-hosted",
	} {
		if !strings.Contains(string(bootstrapGuide), marker) {
			t.Fatalf("bootstrap guide is missing trust-boundary marker %q", marker)
		}
	}
}

func TestV194ReleaseExceptionScopeValidationRejectsExpansion(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join(
		"..", "..", "docs", "operations", "v1.9.4-blacksmith-release-exception.md",
	))
	if err != nil {
		t.Fatal(err)
	}
	base := string(payload)
	for _, expansion := range []string{
		"\nVERSION='v1.9.5'\n",
		"\nautostream-control-panel_v1.9.4_linux_arm64.tar.gz\n",
		"\nautostream-update-host_v1.9.4_linux_amd64.tar.gz\n",
		"\nautostream-worker_v1.9.4_linux_amd64.tar.gz\n",
		"\nREPO='Kome-Lab/Another-Repository'\n",
		"\n.github/workflows/another-release.yml\n",
		"\n0123456789abcdef0123456789abcdef01234567\n",
		"\n0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n",
	} {
		if err := validateV194ReleaseExceptionScope(base + expansion); err == nil {
			t.Fatalf("scope validator accepted expansion %q", strings.TrimSpace(expansion))
		}
	}
}

func TestV194ReleaseExceptionBashBlockValidationRejectsNonFailClosedBlock(t *testing.T) {
	err := validateRunbookBashBlocksFailClosed("```bash\nprintf 'unsafe\\n'\n```\n")
	if err == nil {
		t.Fatal("bash block validator accepted a block without set -euo pipefail")
	}
}

func TestV194ReleaseExceptionDocumentDigestRejectsAnyMutation(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join(
		"..", "..", "docs", "operations", "v1.9.4-blacksmith-release-exception.md",
	))
	if err != nil {
		t.Fatal(err)
	}
	base := string(payload)
	mutations := []struct {
		name string
		old  string
		new  string
	}{
		{
			name: "direct repository argument",
			old:  `--repo "$repo"`,
			new:  `--repo Kome-Lab/Another-Repository`,
		},
		{
			name: "repository-qualified signer workflow",
			old:  `--signer-workflow "$repo/.github/workflows/release-host.yml"`,
			new:  `--signer-workflow Kome-Lab/Another-Repository/.github/workflows/release-host.yml`,
		},
		{
			name: "dynamically composed architecture",
			old:  `readonly PANEL_ASSET='autostream-control-panel_v1.9.4_linux_amd64.tar.gz'`,
			new:  "readonly ARCH='arm64'\nreadonly PANEL_ASSET=\"autostream-control-panel_${VERSION}_linux_${ARCH}.tar.gz\"",
		},
		{
			name: "dynamic commit",
			old:  `readonly COMMIT='d13a3c45aa000ba941eb4c94ee9eb35326151300'`,
			new:  `readonly COMMIT="$(gh api "repos/$REPO/commits/$VERSION" --jq .sha)"`,
		},
		{
			name: "uppercase digest",
			old:  `readonly PANEL_SHA256='44cc5829d882e19f2096772e827dda0d52b851625be9aa4254429df3aa430ae8'`,
			new:  `readonly PANEL_SHA256='44CC5829D882E19F2096772E827DDA0D52B851625BE9AA4254429DF3AA430AE8'`,
		},
		{
			name: "commented digest check",
			old:  "  sha256sum --check --strict -\n\nsudo test ! -e /opt/autostream/releases/artifacts/autostream-control-panel_v1.9.4_linux_amd64",
			new:  "  # sha256sum --check --strict -\n\nsudo test ! -e /opt/autostream/releases/artifacts/autostream-control-panel_v1.9.4_linux_amd64",
		},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutated := strings.Replace(base, mutation.old, mutation.new, 1)
			if mutated == base {
				t.Fatalf("test fixture did not find mutation source %q", mutation.old)
			}
			if err := validateV194RunbookDigest(mutated); err == nil {
				t.Fatal("document digest validator accepted a scope bypass")
			}
		})
	}
	for _, extraBlock := range []string{
		"\n```bash\ngh release download v1.9.4 --repo Kome-Lab/Another-Repository\n```\n",
		"\n```sh\ngh release download v1.9.4 --repo Kome-Lab/Another-Repository\n```\n",
	} {
		if err := validateV194RunbookDigest(base + extraBlock); err == nil {
			t.Fatal("document digest validator accepted an additional executable block")
		}
	}
}

func validateV194ReleaseExceptionScope(runbook string) error {
	const (
		repository = "Kome-Lab/Autostream-ControlPanel"
		workflow   = ".github/workflows/release-host.yml"
		commit     = "d13a3c45aa000ba941eb4c94ee9eb35326151300"
		panelSHA   = "44cc5829d882e19f2096772e827dda0d52b851625be9aa4254429df3aa430ae8"
		agentSHA   = "5766a06bc8763692588c7330af667c9e5095da4d79b9078921f4045e3ed41987"
	)
	if strings.Contains(runbook, "vX.Y.Z") {
		return fmt.Errorf("v1.9.4 exception runbook contains a reusable version placeholder")
	}
	versions := releaseVersionPattern.FindAllString(runbook, -1)
	if len(versions) == 0 {
		return fmt.Errorf("v1.9.4 exception runbook does not pin a release version")
	}
	for _, version := range versions {
		if version != "v1.9.4" {
			return fmt.Errorf("v1.9.4 exception runbook expands to version %q", version)
		}
	}
	allowedAssets := map[string]bool{
		"autostream-control-panel_v1.9.4_linux_amd64.tar.gz": true,
		"autostream-host-agent_v1.9.4_linux_amd64.tar.gz":    true,
	}
	assets := releaseAssetPattern.FindAllString(runbook, -1)
	if len(assets) == 0 {
		return fmt.Errorf("v1.9.4 exception runbook does not pin release assets")
	}
	seenAssets := make(map[string]bool, len(allowedAssets))
	for _, asset := range assets {
		if !allowedAssets[asset] {
			return fmt.Errorf("v1.9.4 exception runbook expands to asset %q", asset)
		}
		seenAssets[asset] = true
	}
	for asset := range allowedAssets {
		if !seenAssets[asset] {
			return fmt.Errorf("v1.9.4 exception runbook omits required asset %q", asset)
		}
	}

	repositoryRows := releaseRepositoryTablePattern.FindAllStringSubmatch(runbook, -1)
	if len(repositoryRows) != 1 || repositoryRows[0][1] != repository {
		return fmt.Errorf("v1.9.4 exception runbook must have one fixed repository row")
	}
	repositoryAssignments := releaseRepositoryAssignmentPattern.FindAllStringSubmatch(runbook, -1)
	if len(repositoryAssignments) == 0 {
		return fmt.Errorf("v1.9.4 exception runbook does not pin repository assignments")
	}
	for _, assignment := range repositoryAssignments {
		if assignment[1] != repository {
			return fmt.Errorf("v1.9.4 exception runbook expands to repository %q", assignment[1])
		}
	}

	workflows := releaseWorkflowPattern.FindAllString(runbook, -1)
	if len(workflows) == 0 {
		return fmt.Errorf("v1.9.4 exception runbook does not pin a signer workflow")
	}
	for _, candidate := range workflows {
		if candidate != workflow {
			return fmt.Errorf("v1.9.4 exception runbook expands to workflow %q", candidate)
		}
	}

	commits := releaseCommitPattern.FindAllString(runbook, -1)
	if len(commits) == 0 {
		return fmt.Errorf("v1.9.4 exception runbook does not pin a commit")
	}
	for _, candidate := range commits {
		if candidate != commit {
			return fmt.Errorf("v1.9.4 exception runbook expands to commit %q", candidate)
		}
	}

	allowedDigests := map[string]bool{panelSHA: true, agentSHA: true}
	seenDigests := make(map[string]bool, len(allowedDigests))
	digests := releaseDigestPattern.FindAllString(runbook, -1)
	if len(digests) == 0 {
		return fmt.Errorf("v1.9.4 exception runbook does not pin artifact digests")
	}
	for _, digest := range digests {
		if !allowedDigests[digest] {
			return fmt.Errorf("v1.9.4 exception runbook expands to digest %q", digest)
		}
		seenDigests[digest] = true
	}
	for digest := range allowedDigests {
		if !seenDigests[digest] {
			return fmt.Errorf("v1.9.4 exception runbook omits required digest %q", digest)
		}
	}
	return nil
}

func validateRunbookBashBlocksFailClosed(runbook string) error {
	blocks, err := runbookBashBlocks(runbook)
	if err != nil {
		return err
	}
	for blockIndex, block := range blocks {
		firstCommand := ""
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				firstCommand = line
				break
			}
		}
		if firstCommand != "set -euo pipefail" {
			return fmt.Errorf(
				"v1.9.4 exception bash block %d starts with %q, want set -euo pipefail",
				blockIndex+1, firstCommand,
			)
		}
	}
	return nil
}

func validateV194RunbookDigest(runbook string) error {
	normalized := strings.ReplaceAll(runbook, "\r\n", "\n")
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(normalized)))
	if digest != v194RunbookSHA256 {
		return fmt.Errorf(
			"v1.9.4 exception runbook digest is %s, want %s",
			digest, v194RunbookSHA256,
		)
	}
	return nil
}

func runbookBashBlocks(runbook string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(runbook, "\r\n", "\n"), "\n")
	blocks := make([]string, 0)
	for lineIndex := 0; lineIndex < len(lines); lineIndex++ {
		if strings.TrimSpace(lines[lineIndex]) != "```bash" {
			continue
		}
		block := make([]string, 0)
		closed := false
		for lineIndex++; lineIndex < len(lines); lineIndex++ {
			line := strings.TrimSpace(lines[lineIndex])
			if line == "```" {
				closed = true
				break
			}
			block = append(block, lines[lineIndex])
		}
		if !closed {
			return nil, fmt.Errorf("v1.9.4 exception runbook has an unterminated bash block")
		}
		blocks = append(blocks, strings.Join(block, "\n"))
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("v1.9.4 exception runbook has no bash blocks")
	}
	return blocks, nil
}

func runbookBashBlockContaining(runbook, marker string) (string, error) {
	blocks, err := runbookBashBlocks(runbook)
	if err != nil {
		return "", err
	}
	for _, block := range blocks {
		if strings.Contains(block, marker) {
			return block, nil
		}
	}
	return "", fmt.Errorf("v1.9.4 exception runbook has no bash block containing %q", marker)
}

func requireOrderedRunbookMarkers(block string, markers []string) error {
	offset := 0
	for _, marker := range markers {
		position := strings.Index(block[offset:], marker)
		if position < 0 {
			return fmt.Errorf("missing ordered marker %q", marker)
		}
		offset += position + len(marker)
	}
	return nil
}
