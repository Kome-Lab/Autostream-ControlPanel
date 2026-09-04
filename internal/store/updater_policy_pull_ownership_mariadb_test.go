package store_test

import (
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBSavePullUpdaterPolicyCASAndOwnershipRevision(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	created, err := fixture.policies.GetUpdaterPolicy(ctx, fixture.params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.TransportMode != store.SystemUpdateTransportPullV2 {
		t.Fatalf("created policy = %#v", created)
	}
	if _, err := fixture.policies.SavePullUpdaterPolicy(
		ctx, fixture.updates, fixture.params.ServiceID, 0, 0, created,
	); err == nil {
		t.Fatal("stale policy revision was accepted")
	}

	activated, err := fixture.policies.ActivatePullUpdaterOwnership(ctx, fixture.auth, fixture.updates, fixture.params)
	if err != nil {
		t.Fatal(err)
	}
	created.PollIntervalSeconds = 20
	created.LocalExecutorPolicySHA256 = "sha256:" + strings.Repeat("b", 64)
	updated, err := fixture.policies.SavePullUpdaterPolicy(
		ctx, fixture.updates, fixture.params.ServiceID, created.Revision,
		activated.Ownership.OwnershipEpoch, created,
	)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := fixture.updates.GetSystemUpdateExecutionHost(ctx, fixture.params.ExecutionHostID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || ownership.PolicyRevision != updated.ProjectionRevision ||
		ownership.OwnershipEpoch != activated.Ownership.OwnershipEpoch {
		t.Fatalf("updated policy/ownership = %#v / %#v", updated, ownership)
	}
}
