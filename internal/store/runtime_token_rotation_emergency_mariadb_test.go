package store_test

import (
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/example/autostream-control-panel/internal/store"
	"testing"
	"time"
)

func TestMariaDBEmergencyConfigureRetryActivatesWithRevokedAnchor(t *testing.T) {
	db, ctx := openMariaDBPullActivationTest(t)
	fixture := newMariaDBPullActivationFixture(t, ctx, db, false)
	activated, err := fixture.policies.ActivatePullUpdaterOwnership(
		ctx, fixture.auth, fixture.updates, fixture.params,
	)
	if err != nil {
		t.Fatal(err)
	}
	params, rotationSeal, _ := mariaDBRuntimeTokenRotationStageParams(
		t,
		fixture,
		activated,
		"mariadb-emergency-configure-"+fixture.suffix,
	)
	stagedRotation, err := fixture.updates.StageSystemUpdateRuntimeTokenRotation(
		ctx,
		fixture.auth,
		fixture.policies,
		params,
		rotationSeal,
	)
	if err != nil {
		t.Fatal(err)
	}
	emergencyAt := params.Now.Add(time.Second)
	emergency, applied, err :=
		fixture.updates.EmergencyRevokeSystemUpdateRuntimeToken(
			ctx,
			fixture.auth,
			store.EmergencyRevokeSystemUpdateRuntimeTokenParams{
				RotationID:       stagedRotation.Rotation.ID,
				ExecutionHostID:  params.ExecutionHostID,
				ExpectedRevision: stagedRotation.Rotation.Revision,
				TokenID:          stagedRotation.Rotation.PreviousTokenID,
				Now:              emergencyAt,
			},
		)
	if err != nil || !applied ||
		emergency.Status != store.SystemUpdateRuntimeTokenRotationCanceled {
		t.Fatalf("emergency=%#v applied=%v err=%v", emergency, applied, err)
	}
	var revokedBefore time.Time
	if err := db.QueryRowContext(
		ctx,
		`SELECT revoked_at FROM service_tokens WHERE id = ?`,
		stagedRotation.Rotation.PreviousTokenID,
	).Scan(&revokedBefore); err != nil {
		t.Fatal(err)
	}
	if recovery, err := fixture.updates.IsSystemUpdateEmergencyIdentityRecovery(
		ctx,
		fixture.auth,
		params.ServiceID,
	); err != nil || !recovery {
		t.Fatalf("initial emergency recovery=%v err=%v", recovery, err)
	}
	beforePolicy, err := fixture.policies.GetUpdaterPolicy(ctx, params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}

	configureSeal := store.NodeTokenSealer(func(raw string) (string, string, error) {
		return "sealed:" + security.HashToken(raw), "configure-nonce", nil
	})
	firstConfigureToken := "first-emergency-configure-" + fixture.suffix
	if _, err := fixture.auth.SetServiceConfigureToken(
		ctx,
		params.ServiceID,
		security.HashToken(firstConfigureToken),
		emergencyAt.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	abandoned, err := fixture.auth.StageServiceNodeConfiguration(
		ctx,
		params.ServiceID,
		firstConfigureToken,
		emergencyAt.Add(time.Second),
		configureSeal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovery, err := fixture.updates.IsSystemUpdateEmergencyIdentityRecovery(
		ctx,
		fixture.auth,
		params.ServiceID,
	); err != nil || !recovery {
		t.Fatalf("staged emergency retry recovery=%v err=%v", recovery, err)
	}

	secondConfigureToken := "second-emergency-configure-" + fixture.suffix
	if _, err := fixture.auth.SetServiceConfigureToken(
		ctx,
		params.ServiceID,
		security.HashToken(secondConfigureToken),
		emergencyAt.Add(2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	tombstone, err := fixture.auth.GetService(ctx, params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if tombstone.StagedNodeTokenID != abandoned.Token.ID ||
		tombstone.StagedNodePreviousTokenID != "" ||
		tombstone.StagedNodeTokenHash != "" ||
		len(tombstone.StagedNodeTokenScopes) != 0 ||
		tombstone.StagedNodeTokenCiphertext != "" ||
		tombstone.StagedNodeTokenNonce != "" ||
		tombstone.StagedNodeActivationTokenHash != "" ||
		tombstone.StagedNodeTokenAt != nil {
		t.Fatalf("regenerated Configure Token retained staged secrets: %#v", tombstone)
	}
	replacement, err := fixture.auth.StageServiceNodeConfiguration(
		ctx,
		params.ServiceID,
		secondConfigureToken,
		emergencyAt.Add(2*time.Second),
		configureSeal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Token.ID == abandoned.Token.ID {
		t.Fatal("Configure retry reused the abandoned staged token")
	}
	active, service, alreadyActivated, err :=
		fixture.auth.ActivateServiceNodeConfiguration(
			ctx,
			params.ServiceID,
			replacement.Token.ID,
			replacement.ActivationToken,
			emergencyAt.Add(3*time.Second),
			store.ServiceRuntimeReport{Version: "v2.0.0"},
		)
	if err != nil || alreadyActivated ||
		active.ID != replacement.Token.ID ||
		service.TokenID != replacement.Token.ID {
		t.Fatalf(
			"emergency Configure activation token=%#v service=%#v replay=%v err=%v",
			active,
			service,
			alreadyActivated,
			err,
		)
	}
	if _, err := fixture.auth.AuthenticateServiceToken(
		ctx,
		replacement.Token.RawToken,
		"updates.claim",
	); err != nil {
		t.Fatalf("replacement runtime token is not active: %v", err)
	}
	if _, err := fixture.auth.AuthenticateServiceToken(
		ctx,
		abandoned.Token.RawToken,
		"updates.claim",
	); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("abandoned staged token became active: %v", err)
	}
	var revokedAfter time.Time
	if err := db.QueryRowContext(
		ctx,
		`SELECT revoked_at FROM service_tokens WHERE id = ?`,
		stagedRotation.Rotation.PreviousTokenID,
	).Scan(&revokedAfter); err != nil {
		t.Fatal(err)
	}
	if !revokedAfter.Equal(revokedBefore) {
		t.Fatalf(
			"emergency revocation timestamp changed: before=%s after=%s",
			revokedBefore,
			revokedAfter,
		)
	}
	afterPolicy, err := fixture.policies.GetUpdaterPolicy(ctx, params.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	if afterPolicy.Revision != beforePolicy.Revision ||
		afterPolicy.ProjectionRevision != beforePolicy.ProjectionRevision ||
		afterPolicy.LocalExecutorPolicyRevision !=
			beforePolicy.LocalExecutorPolicyRevision ||
		afterPolicy.LocalExecutorPolicySHA256 !=
			beforePolicy.LocalExecutorPolicySHA256 {
		t.Fatalf(
			"emergency Configure changed policy: before=%#v after=%#v",
			beforePolicy,
			afterPolicy,
		)
	}
}
