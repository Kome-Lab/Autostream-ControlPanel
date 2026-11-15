package lifecyclefoundation

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
)

type processConfig struct {
	Binding Binding
	Request Request
	Mode    string
	Stage   Phase
}

type childProcess struct {
	cmd    *exec.Cmd
	reader *bufio.Reader
	input  io.WriteCloser
	cancel context.CancelFunc
}

func startChild(t *testing.T, config processConfig) *childProcess {
	t.Helper()
	executable, err := os.Executable()
	must(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	command := exec.CommandContext(ctx, executable, "-test.run=^TestLifecycleProcessHelper$", "-test.count=1")
	command.Env = append(os.Environ(), "AUTOSTREAM_B0_PROCESS="+encode(config))
	output, err := command.StdoutPipe()
	must(t, err)
	input, err := command.StdinPipe()
	must(t, err)
	command.Stderr = os.Stderr
	must(t, command.Start())
	child := &childProcess{cmd: command, reader: bufio.NewReader(output), input: input, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		input.Close()
	})
	return child
}

func (p *childProcess) await(t *testing.T, prefix string) string {
	t.Helper()
	for {
		line, err := p.reader.ReadString('\n')
		if err != nil {
			t.Fatal("child exited before", prefix, err)
		}
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
}

func TestLifecycleProcessHelper(t *testing.T) {
	raw := os.Getenv("AUTOSTREAM_B0_PROCESS")
	if raw == "" {
		return
	}
	var config processConfig
	must(t, json.Unmarshal([]byte(raw), &config))
	must(t, config.Binding.Validate())
	must(t, config.Request.Validate())
	db := disposableDB(t)
	cp, err := NewSQLStore(db, config.Binding)
	must(t, err)
	ledger := &syntheticLedger{db: db}
	auth := &syntheticAuthorizer{db: db, deployment: config.Binding.Deployment}
	service, err := NewService(cp, ledger, auth)
	must(t, err)
	f := &fixture{db: db, cp: cp, ledger: ledger, auth: auth, service: service, binding: config.Binding, before: initialCheckpoint()}
	switch config.Mode {
	case "COMPETE":
		ledger.onReserve = func() {
			fmt.Println("B0_READY")
			_, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
			must(t, readErr)
		}
		result, runErr := service.Run(context.Background(), config.Request, f.effect)
		if result.Outcome == "COMPLETE" {
			must(t, runErr)
		} else {
			expect(t, runErr != nil, "rejected competition missing error")
		}
		fmt.Println("B0_RESULT:" + result.Outcome)
	case "STOP":
		r := reserveFixture(t, f, config.Request)
		if config.Stage != Reserved {
			committed, applyErr := cp.apply(context.Background(), r, auth, f.effect)
			must(t, applyErr)
			if config.Stage != CPCommitted {
				receipt, finalizeErr := ledger.Finalize(context.Background(), committed)
				must(t, finalizeErr)
				if config.Stage != Finalized {
					saved, saveErr := cp.saveReceipt(context.Background(), receipt)
					must(t, saveErr)
					if config.Stage == Completed {
						must(t, ledger.Complete(context.Background(), saved))
					}
				}
			}
		}
		fmt.Println("B0_READY")
		// Parent kills the actual process at this persisted stage.
		for {
			time.Sleep(time.Second)
		}
	case "PROBE":
		invoked := false
		result, runErr := service.Run(context.Background(), config.Request, func(context.Context, *Transaction) (Checkpoint, error) {
			invoked = true
			return initialCheckpoint(), nil
		})
		expect(t, runErr != nil && result.Outcome != "COMPLETE" && !invoked, "Restart replayed original effect")
		fmt.Println("B0_RESULT:HELD")
	default:
		t.Fatal("unknown child scenario")
	}
}

func TestLifecycleIndependentProcessesCAS(t *testing.T) {
	f := newFixture(t)
	left := startChild(t, processConfig{Binding: f.binding, Request: writeRequest(), Mode: "COMPETE"})
	right := startChild(t, processConfig{Binding: f.binding, Request: writeRequest(), Mode: "COMPETE"})
	left.await(t, "B0_READY")
	right.await(t, "B0_READY")
	_, err := io.WriteString(left.input, "go\n")
	must(t, err)
	_, err = io.WriteString(right.input, "go\n")
	must(t, err)
	a := left.await(t, "B0_RESULT:")
	b := right.await(t, "B0_RESULT:")
	must(t, left.cmd.Wait())
	must(t, right.cmd.Wait())
	expect(t, a == "COMPLETE" && b == "HELD" || a == "HELD" && b == "COMPLETE", "independent processes both committed or both failed")
	expect(t, f.value(t) == 1, "native CAS allowed two effects")
}

func TestLifecycleProcessDeathRestart(t *testing.T) {
	for _, stage := range []Phase{Reserved, CPCommitted, Finalized, ReceiptSaved, Completed} {
		t.Run(string(stage), func(t *testing.T) {
			f := newFixture(t)
			r := writeRequest()
			child := startChild(t, processConfig{Binding: f.binding, Request: r, Mode: "STOP", Stage: stage})
			child.await(t, "B0_READY")
			must(t, child.cmd.Process.Kill())
			expect(t, child.cmd.Wait() != nil, "child was not actually terminated")
			probe := startChild(t, processConfig{Binding: f.binding, Request: r, Mode: "PROBE"})
			probe.await(t, "B0_RESULT:")
			must(t, probe.cmd.Wait())
			expected := 1
			if stage == Reserved {
				expected = 0
			}
			expect(t, f.value(t) == expected, "restart changed the committed effect")
		})
	}
}
