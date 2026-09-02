package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestControlPanelBackupScriptRejectsUnsafeDatabaseArguments(t *testing.T) {
	script := filepath.Join("..", "..", "release", "autostream-backup-control-panel.example")

	for _, test := range []struct {
		name     string
		args     []string
		wantText string
	}{
		{name: "empty name", args: []string{""}, wantText: "invalid database name"},
		{name: "unsafe name", args: []string{"database;touch-pwned"}, wantText: "invalid database name"},
		{name: "path traversal", args: []string{"../database"}, wantText: "invalid database name"},
		{name: "leading option", args: []string{"--all-databases"}, wantText: "invalid database name"},
		{name: "too many arguments", args: []string{"database_a", "database_b"}, wantText: "usage:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := runBackupScript(t, script, test.args...)
			if err == nil {
				t.Fatalf("backup script unexpectedly accepted %q", test.args)
			}
			if !strings.Contains(output, test.wantText) {
				t.Fatalf("backup script output = %q, want %q", output, test.wantText)
			}
			if strings.Contains(output, "/usr/bin/mariadb-dump is unavailable") {
				t.Fatal("database arguments must be rejected before host backup prerequisites are inspected")
			}
		})
	}
}

func TestControlPanelBackupScriptDeclaresDefaultAndQuotedDatabaseArgument(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "release", "autostream-backup-control-panel.example"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	for _, want := range []string{
		"readonly DEFAULTS_FILE=/etc/autostream-local-executor/mariadb-backup.cnf",
		"readonly DEFAULT_DATABASE=autostream_control_panel",
		`readonly DATABASE="${1-$DEFAULT_DATABASE}"`,
		`--databases "$DATABASE"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("Control Panel backup script is missing %q", want)
		}
	}
	if strings.Contains(script, "DEFAULTS_FILE=/etc/autostream/mariadb-backup.cnf") {
		t.Fatal("Control Panel backup script can still read service credentials from /etc/autostream")
	}
}

func TestControlPanelInstallGuidePassesConfiguredDatabaseName(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "release", "README.install.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := strings.Join(strings.Fields(string(body)), " ")
	for _, want := range []string{
		"DATABASE_NAME='autostream_control_panel'",
		"/etc/autostream-local-executor/mariadb-backup.cnf",
		"exact `DATABASE_NAME` must be used for the MariaDB grant, the real dump, and the",
		"GRANT SELECT, SHOW VIEW, TRIGGER ON \\`${DATABASE_NAME}\\`.*",
		`sudo /usr/local/sbin/autostream-backup-control-panel "$DATABASE_NAME"`,
		"Save this exact database name in **Application Info > System Updates**",
		"server-owned `database_name`",
	} {
		if !strings.Contains(guide, want) {
			t.Fatalf("Control Panel install guide is missing %q", want)
		}
	}
	grant := strings.Index(guide, "GRANT SELECT, SHOW VIEW, TRIGGER")
	dump := strings.Index(guide, `sudo /usr/local/sbin/autostream-backup-control-panel "$DATABASE_NAME"`)
	settings := strings.Index(guide, "Save this exact database name in **Application Info > System Updates**")
	if grant < 0 || dump <= grant || settings <= dump {
		t.Fatal("Control Panel install guide must use one selected database name for grant, real dump, then server-owned settings")
	}
	if strings.Contains(guide, `"backup_argv":`) {
		t.Fatal("pull_v2 Control Panel install guide must not require hand-editing backup_argv")
	}
}

func TestControlPanelCIRunsRootBackupSmoke(t *testing.T) {
	for _, workflow := range []string{"ci.yml"} {
		body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", workflow))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, want := range []string{
			"Test backup script with root-owned Linux fixtures",
			"run-backup-script-root-smoke.sh",
			"autostream-kometubu_panel",
			"ubuntu@sha256:4fbb8e6a8395de5a7550b33509421a2bafbc0aab6c06ba2cef9ebffbc7092d90",
			"--user 0:0",
			"--network none",
			"--cap-drop ALL",
			"--security-opt no-new-privileges",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s is missing root backup smoke contract %q", workflow, want)
			}
		}
	}
}

func runBackupScript(t *testing.T, script string, args ...string) (string, error) {
	t.Helper()

	absScript, err := filepath.Abs(script)
	if err != nil {
		t.Fatal(err)
	}
	bash := "bash"
	if runtime.GOOS == "windows" {
		const gitBash = `C:\Program Files\Git\bin\bash.exe`
		if _, err := os.Stat(gitBash); err != nil {
			t.Skipf("Git Bash is unavailable: %v", err)
		}
		bash = gitBash
		absScript = filepath.ToSlash(absScript)
	}

	commandArgs := append([]string{absScript}, args...)
	output, err := exec.Command(bash, commandArgs...).CombinedOutput()
	return string(output), err
}
