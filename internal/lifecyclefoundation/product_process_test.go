//go:build lifecycle_product_integration

package lifecyclefoundation_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
	va "github.com/example/autostream-control-panel/internal/v3authorization"
)

type productProcessConfig struct {
	Binding lf.Binding
	Mode    string
	Stage   lf.Phase
}
type productChild struct {
	cmd    *exec.Cmd
	output *bufio.Reader
	input  io.WriteCloser
}

func startProductChild(t *testing.T, config productProcessConfig) *productChild {
	t.Helper()
	binary, err := os.Executable()
	require(t, err)
	body, err := json.Marshal(config)
	require(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestB1ProductProcessHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "AUTOSTREAM_B1_PROCESS="+string(body))
	output, err := cmd.StdoutPipe()
	require(t, err)
	input, err := cmd.StdinPipe()
	require(t, err)
	t.Cleanup(func() { input.Close() })
	cmd.Stderr = os.Stderr
	require(t, cmd.Start())
	return &productChild{cmd: cmd, output: bufio.NewReader(output), input: input}
}
func (p *productChild) await(t *testing.T, prefix string) string {
	t.Helper()
	for {
		line, err := p.output.ReadString('\n')
		require(t, err)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
}
func TestB1ProductProcessHelper(t *testing.T) {
	raw := os.Getenv("AUTOSTREAM_B1_PROCESS")
	if raw == "" {
		return
	}
	var config productProcessConfig
	require(t, json.Unmarshal([]byte(raw), &config))
	env := lf.ResumeProductTestEnvironment(t, config.Binding, "operator-1", productCheckpoint)
	authority := va.NewSQLAuthority(env.DB).WithLifecycleGate(env.Gate)
	pause := func() { fmt.Println("B1_READY"); _, err := bufio.NewReader(os.Stdin).ReadString('\n'); require(t, err) }
	switch config.Mode {
	case "COMPETE":
		env.BeforePort = func(operation string) {
			if operation == "Reserve" {
				pause()
			}
		}
	case "STOP":
		env.AfterPort = func(operation string) {
			if config.Stage == lf.Reserved && operation == "Reserve" || config.Stage == lf.Finalized && operation == "Finalize" || config.Stage == lf.Completed && operation == "Complete" {
				pause()
			}
		}
		env.AfterCPCommit(func(count int) {
			if config.Stage == lf.CPCommitted && count == 1 || config.Stage == lf.ReceiptSaved && count == 2 {
				pause()
			}
		})
	case "PROBE":
	default:
		t.Fatal("unknown product child mode")
	}
	record := va.AuthorityRecord{Kind: va.HostRecord, Host: &va.HostRegistration{Realm: "realm-1", Host: "host-1", OwnerDomain: "domain-2", State: "ACTIVE"}}
	next, err := authority.Save(context.Background(), 1, []va.AuthorityRecord{record})
	outcome := "HELD"
	if err == nil && next == 2 {
		outcome = "COMPLETE"
	}
	if config.Mode == "PROBE" {
		check(t, err != nil && next == 0, "Restart replayed product Save")
	}
	fmt.Println("B1_RESULT:" + outcome)
}
func TestB1ProductIndependentProcesses(t *testing.T) {
	f := newProductFixture(t)
	a := startProductChild(t, productProcessConfig{Binding: f.env.Binding, Mode: "COMPETE"})
	b := startProductChild(t, productProcessConfig{Binding: f.env.Binding, Mode: "COMPETE"})
	a.await(t, "B1_READY")
	b.await(t, "B1_READY")
	_, err := io.WriteString(a.input, "go\n")
	require(t, err)
	_, err = io.WriteString(b.input, "go\n")
	require(t, err)
	left := a.await(t, "B1_RESULT:")
	right := b.await(t, "B1_RESULT:")
	require(t, a.cmd.Wait())
	require(t, b.cmd.Wait())
	check(t, left == "COMPLETE" && right == "HELD" || left == "HELD" && right == "COMPLETE", "two independent product owners committed or neither completed")
	check(t, f.generation(t) == 2, "native product generation CAS failed")
}
func TestB1ProductProcessDeathRestart(t *testing.T) {
	for _, stage := range []lf.Phase{lf.Reserved, lf.CPCommitted, lf.Finalized, lf.ReceiptSaved, lf.Completed} {
		t.Run(string(stage), func(t *testing.T) {
			f := newProductFixture(t)
			child := startProductChild(t, productProcessConfig{Binding: f.env.Binding, Mode: "STOP", Stage: stage})
			child.await(t, "B1_READY")
			require(t, child.cmd.Process.Kill())
			check(t, child.cmd.Wait() != nil, "child was not killed")
			before := f.generation(t)
			expected := uint64(2)
			if stage == lf.Reserved {
				expected = 1
			}
			check(t, before == expected, "process stopped at wrong persisted boundary")
			probe := startProductChild(t, productProcessConfig{Binding: f.env.Binding, Mode: "PROBE", Stage: stage})
			check(t, probe.await(t, "B1_RESULT:") == "HELD", "restarted product resumed old writer")
			require(t, probe.cmd.Wait())
			check(t, f.generation(t) == before, "Restart changed product state")
		})
	}
}
