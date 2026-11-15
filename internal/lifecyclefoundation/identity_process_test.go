//go:build lifecycle_identity_integration

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
)

type b2ProcessConfig struct { Binding lf.Binding; Mode string; Stage lf.Phase; Attempt string }
type b2Child struct { cmd *exec.Cmd; output *bufio.Reader; input io.WriteCloser }

func startB2Child(t *testing.T,config b2ProcessConfig)*b2Child{
	t.Helper();binary,err:=os.Executable();b2Must(t,err);raw,err:=json.Marshal(config);b2Must(t,err)
	ctx,cancel:=context.WithTimeout(context.Background(),45*time.Second);t.Cleanup(cancel)
	cmd:=exec.CommandContext(ctx,binary,"-test.run=^TestIdentityProcessHelper$","-test.count=1")
	cmd.Env=append(os.Environ(),"AUTOSTREAM_B2_PROCESS="+string(raw));output,err:=cmd.StdoutPipe();b2Must(t,err);input,err:=cmd.StdinPipe();b2Must(t,err)
	t.Cleanup(func(){input.Close()});cmd.Stderr=os.Stderr;b2Must(t,cmd.Start());return &b2Child{cmd:cmd,output:bufio.NewReader(output),input:input}
}
func(p *b2Child)await(t *testing.T,prefix string)string{
	t.Helper();for{line,err:=p.output.ReadString('\n');b2Must(t,err);if strings.HasPrefix(line,prefix){return strings.TrimSpace(strings.TrimPrefix(line,prefix))}}
}

func TestIdentityProcessHelper(t *testing.T){
	raw:=os.Getenv("AUTOSTREAM_B2_PROCESS");if raw==""{return};var config b2ProcessConfig;b2Must(t,json.Unmarshal([]byte(raw),&config))
	e:=lf.ResumeIdentityTestEnvironment(t,config.Binding);e.FixedRevision=1
	pause:=func(){fmt.Println("B2_READY");_,err:=bufio.NewReader(os.Stdin).ReadString('\n');b2Must(t,err)}
	switch config.Mode{
	case "COMPETE":e.BeforePort=func(operation string){if operation=="Reserve"{pause()}}
	case "STOP":
		e.AfterPort=func(operation string){if config.Stage==lf.Reserved&&operation=="Reserve"||config.Stage==lf.Finalized&&operation=="Finalize"||config.Stage==lf.Completed&&operation=="Complete"{pause()}}
		e.AfterCPCommit(func(count int){if config.Stage==lf.CPCommitted&&count==1||config.Stage==lf.ReceiptSaved&&count==2{pause()}})
	case "INSPECT":
		// Restart only reopens independent pools and inspects the existing Attempt.
		// It never calls the original User writer, retries, aborts or clears a slot.
		_,err:=e.Gate.Inspect(context.Background(),"member-1",config.Attempt);b2Must(t,err);fmt.Println("B2_RESULT:INSPECTED");return
	default:t.Fatal("unknown B2 process mode")
	}
	user,err:=b2Store(e).SetUserStatus(context.Background(),"member-1","disabled");outcome:="HELD";if err==nil&&user.Status=="disabled"{outcome="COMPLETE"};fmt.Println("B2_RESULT:"+outcome)
}

func TestB2IdentityIndependentProcesses(t *testing.T){
	e:=lf.NewIdentityTestEnvironment(t)
	a:=startB2Child(t,b2ProcessConfig{Binding:e.Binding,Mode:"COMPETE"});b:=startB2Child(t,b2ProcessConfig{Binding:e.Binding,Mode:"COMPETE"})
	a.await(t,"B2_READY");b.await(t,"B2_READY");_,err:=io.WriteString(a.input,"go\n");b2Must(t,err);_,err=io.WriteString(b.input,"go\n");b2Must(t,err)
	left:=a.await(t,"B2_RESULT:");right:=b.await(t,"B2_RESULT:");b2Must(t,a.cmd.Wait());b2Must(t,b.cmd.Wait())
	b2Check(t,left=="COMPLETE"&&right=="HELD"||left=="HELD"&&right=="COMPLETE","independent stale Identity writers did not produce exactly one winner")
	b2Check(t,b2Projection(t,e).Revision==2&&b2Count(t,e.DB,"SELECT COUNT(*) FROM v3_cp_identity_history")==1,"two OS processes committed the same Identity generation")
}

func TestB2IdentityProcessDeathRestartInspect(t *testing.T){
	for _,stage:=range []lf.Phase{lf.Reserved,lf.CPCommitted,lf.Finalized,lf.ReceiptSaved,lf.Completed}{t.Run(string(stage),func(t *testing.T){
		e:=lf.NewIdentityTestEnvironment(t);child:=startB2Child(t,b2ProcessConfig{Binding:e.Binding,Mode:"STOP",Stage:stage});child.await(t,"B2_READY");b2Must(t,child.cmd.Process.Kill());b2Check(t,child.cmd.Wait()!=nil,"child did not terminate at the selected boundary")
		before:=b2Projection(t,e);want:=uint64(2);if stage==lf.Reserved{want=1};b2Check(t,before.Revision==want,"process died at a different actual commit boundary")
		var attempt string;b2Must(t,e.DB.QueryRow("SELECT attempt FROM b0_test_ledger_attempts WHERE deployment=?",e.Binding.Deployment).Scan(&attempt))
		ledgerBefore:=b2Count(t,e.DB,"SELECT COUNT(*) FROM b0_test_ledger_history WHERE deployment=?",e.Binding.Deployment)
		probe:=startB2Child(t,b2ProcessConfig{Binding:e.Binding,Mode:"INSPECT",Attempt:attempt});b2Check(t,probe.await(t,"B2_RESULT:")=="INSPECTED","restart did not inspect the original Attempt");b2Must(t,probe.cmd.Wait())
		b2Check(t,b2Projection(t,e)==before&&b2Count(t,e.DB,"SELECT COUNT(*) FROM b0_test_ledger_history WHERE deployment=?",e.Binding.Deployment)==ledgerBefore,"restart/Inspect replayed, repaired or completed an old Identity write")
	})}
}
