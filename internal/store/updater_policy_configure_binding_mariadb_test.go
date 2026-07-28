package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

func TestMariaDBBindPullUpdaterConfigurePolicyIsRevisionBoundAndIdempotent(
	t *testing.T,
) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, true)
	generatedDigest := "sha256:" + strings.Repeat("b", 64)
	params := store.BindPullUpdaterConfigurePolicyParams{
		ServiceID:                           fixture.params.ServiceID,
		ExpectedSourcePolicyRevision:        fixture.params.ExpectedSourcePolicyRevision,
		ExpectedProjectionRevision:          fixture.params.ExpectedProjectionRevision,
		ExpectedLocalExecutorPolicyRevision: fixture.params.ExpectedLocalExecutorPolicyRevision,
		LocalExecutorPolicySHA256:           generatedDigest,
	}
	bound, err := fixture.policies.BindPullUpdaterConfigurePolicy(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if bound.LocalExecutorPolicySHA256 != generatedDigest ||
		bound.Revision != params.ExpectedSourcePolicyRevision ||
		bound.ProjectionRevision != params.ExpectedProjectionRevision ||
		bound.LocalExecutorPolicyRevision != params.ExpectedLocalExecutorPolicyRevision {
		t.Fatalf("bound policy = %#v", bound)
	}
	replayed, err := fixture.policies.BindPullUpdaterConfigurePolicy(ctx, params)
	if err != nil || replayed.LocalExecutorPolicySHA256 != generatedDigest {
		t.Fatalf("idempotent bind = %#v, %v", replayed, err)
	}
	stale := params
	stale.ExpectedProjectionRevision++
	stale.LocalExecutorPolicySHA256 = "sha256:" + strings.Repeat("c", 64)
	if _, err := fixture.policies.BindPullUpdaterConfigurePolicy(ctx, stale); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale bind error = %v", err)
	}
	current, err := fixture.policies.GetUpdaterPolicy(ctx, params.ServiceID)
	if err != nil || current.LocalExecutorPolicySHA256 != generatedDigest {
		t.Fatalf("stale bind mutated policy = %#v, %v", current, err)
	}
}
