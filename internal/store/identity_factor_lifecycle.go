package store

import (
	"context"

	lf "github.com/example/autostream-control-panel/internal/lifecyclefoundation"
)

// The proof and one-use semantics of the remaining factor APIs have not been
// adopted for Identity participation. Refuse them before SQL or secret work;
// unconfigured legacy stores keep their existing behavior.
func (s MariaDBAuthStore) identityFactorUnavailable(ctx context.Context) error {
	if s.identityGate != nil {
		return lf.ErrHeld
	}
	if _, bound, err := lf.SQL(ctx, s.db); err != nil || bound {
		return lf.ErrHeld
	}
	return nil
}

func (s MariaDBAuthStore) GetMFAConfig(ctx context.Context, userID string) (MFAConfig, error) {
	return identityValue(ctx, s.identityGate, "identity.mfa.read", userID, func(partCtx context.Context) (MFAConfig, error) {
		return s.identityGetMFAConfig(partCtx, userID)
	})
}

// Recovery consumption is a distinct durable stage. It does not imply that a
// later Session issuance participates in the same transaction.
func (s MariaDBAuthStore) ConsumeRecoveryCode(ctx context.Context, userID, recoveryCodeHash string) error {
	return s.identityRun(ctx, "identity.mfa.consume_recovery", userID, func(partCtx context.Context) error {
		return s.identityConsumeRecoveryCode(partCtx, userID, recoveryCodeHash)
	})
}
