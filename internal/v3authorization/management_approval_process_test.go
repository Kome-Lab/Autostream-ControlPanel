package v3authorization

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	lifecycle "github.com/Kome-Lab/Autostream-Contracts/v3lifecycle"
	approval "github.com/example/autostream-control-panel/internal/bootstrapapproval"
)

type iuApprovalProcessRequest struct {
	Actor       ManagementActor
	Metadata    ManagementMetadata
	Grant       ManagementGrant
	Reference   approval.Reference
	WantPresent bool
}

// A new OS process has neither the parent's Core nor its private contexts or
// process-local uncertain gate. Its only operation is inspection of the exact
// original attempt against durable synthetic Origin/CP state.
func TestIUWS1ApprovalRestartProcessInspect(t *testing.T) {
	if os.Getenv("AUTOSTREAM_IU_WS1_INSPECT_PROCESS") == "synthetic-only" {
		var request iuApprovalProcessRequest
		if json.NewDecoder(io.LimitReader(os.Stdin, 16384)).Decode(&request) != nil {
			t.Fatal("synthetic process request missing")
		}
		db := sql.OpenDB(authorityConnector(t))
		defer db.Close()
		originDB := sql.OpenDB(authorityConnector(t))
		defer originDB.Close()
		origin := &iuApprovalOrigin{db: originDB, qualification: approval.Qualification{ProfileID: approval.FixtureProfile, FixtureOnly: true}}
		core, err := approval.NewFixtureCore(context.Background(), db, origin, iuApprovalLimits())
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewApprovalManagementService(NewSQLAuthority(db), core)
		if err != nil {
			t.Fatal(err)
		}
		inspection, err := service.Inspect(context.Background(), request.Actor, request.Metadata, request.Grant, request.Reference)
		if err != nil || inspection.OriginKnowledge != lifecycle.Known || !inspection.OriginConsumed || inspection.CPKnowledge != lifecycle.Known || inspection.CPPresent != request.WantPresent || inspection.Gate != lifecycle.Held || origin.verifyCalls.Load() != 0 || origin.inspectCalls.Load() != 1 {
			t.Fatal("independent process did not preserve same-attempt knowledge/hold", inspection, err)
		}
		t.Log("IU_WS1_PROCESS_INSPECT_ONLY_HELD")
		return
	}
	for _, mode := range []int32{1, 2} {
		name := "committed-ack-lost"
		if mode == 2 {
			name = "rolled-back-ack-lost"
		}
		t.Run(name, func(t *testing.T) {
			f := newIUApprovalFixture(t)
			fault := &atomic.Int32{}
			db := sql.OpenDB(authorityFaultConnector{authorityConnector(t), fault})
			defer db.Close()
			core, err := approval.NewFixtureCore(context.Background(), db, f.origin, iuApprovalLimits())
			if err != nil {
				t.Fatal(err)
			}
			service, err := NewApprovalManagementService(NewSQLAuthority(db), core)
			if err != nil {
				t.Fatal(err)
			}
			fault.Store(mode)
			if result := service.Bootstrap(context.Background(), f.actor, f.meta, f.grant, f.ref); result.Status != Unknown {
				t.Fatal("parent uncertainty missing", result)
			}
			before := iuLocalCounts(t, f.db)
			audits := managementCount(t, f.db, "v3_cp_management_audit")
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "-test.run=^TestIUWS1ApprovalRestartProcessInspect$", "-test.v")
			// Only the explicitly disposable DSN and minimal OS execution settings
			// cross to the helper. No ambient credentials are enumerated or copied.
			command.Env = []string{"AUTOSTREAM_IU_WS1_INSPECT_PROCESS=synthetic-only", "AUTOSTREAM_V3_AUTH_TEST_DSN=" + os.Getenv("AUTOSTREAM_V3_AUTH_TEST_DSN"), "PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "TEMP=" + os.Getenv("TEMP"), "TMP=" + os.Getenv("TMP")}
			payload, _ := json.Marshal(iuApprovalProcessRequest{Actor: f.actor, Metadata: f.meta, Grant: f.grant, Reference: f.ref, WantPresent: mode == 1})
			command.Stdin = bytes.NewReader(payload)
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "IU_WS1_PROCESS_INSPECT_ONLY_HELD") {
				t.Fatal("independent synthetic process failed", err, string(output))
			}
			after := iuLocalCounts(t, f.db)
			for i, n := range before {
				if after[i] != n {
					t.Fatal("helper inspected by writing state")
				}
			}
			if managementCount(t, f.db, "v3_cp_management_audit") != audits || f.origin.verifyCalls.Load() != 1 {
				t.Fatal("helper mutated audit or reconsumed approval")
			}
		})
	}
}
