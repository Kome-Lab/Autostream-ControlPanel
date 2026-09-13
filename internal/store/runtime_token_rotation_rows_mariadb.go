package store

import (
	"database/sql"
	"encoding/json"
)

type runtimeTokenRotationScanner interface {
	Scan(dest ...any) error
}

func scanSystemUpdateRuntimeTokenRotation(
	scanner runtimeTokenRotationScanner,
) (SystemUpdateRuntimeTokenRotation, error) {
	var (
		rotation                                      SystemUpdateRuntimeTokenRotation
		scopesJSON                                    string
		localStageReceiptID, emergencyRevokedTokenID  sql.NullString
		stagedTokenCiphertext, stagedTokenNonce       sql.NullString
		credentialClaimIDHash                         sql.NullString
		credentialClaimRevision                       sql.NullInt64
		credentialClaimedAt, localStageAcknowledgedAt sql.NullTime
		localStagedAt                                 sql.NullTime
		heartbeatProvedAt, activatedAt                sql.NullTime
		cancelRequestedAt, cancelAcknowledgedAt       sql.NullTime
		canceledAt                                    sql.NullTime
		emergencyRevokedAt                            sql.NullTime
	)
	err := scanner.Scan(
		&rotation.ID, &rotation.ServiceID, &rotation.ExecutionHostID,
		&rotation.idempotencyKey, &rotation.intentSHA256, &rotation.Status,
		&rotation.Revision, &rotation.ExpectedOwnershipEpoch,
		&rotation.ExpectedSourcePolicyRevision, &rotation.ExpectedProjectionRevision,
		&rotation.ExpectedLocalExecutorPolicyRevision,
		&rotation.PreviousTokenID, &rotation.StagedTokenID,
		&rotation.stagedTokenHash, &scopesJSON,
		&stagedTokenCiphertext, &stagedTokenNonce,
		&localStageReceiptID, &credentialClaimIDHash, &credentialClaimRevision,
		&credentialClaimedAt,
		&localStageAcknowledgedAt, &localStagedAt,
		&heartbeatProvedAt, &activatedAt,
		&cancelRequestedAt, &cancelAcknowledgedAt, &canceledAt,
		&emergencyRevokedTokenID, &emergencyRevokedAt,
		&rotation.CreatedAt, &rotation.UpdatedAt,
	)
	if err != nil {
		return SystemUpdateRuntimeTokenRotation{}, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &rotation.stagedTokenScopes); err != nil {
		return SystemUpdateRuntimeTokenRotation{}, err
	}
	rotation.LocalStageReceiptID = localStageReceiptID.String
	rotation.stagedTokenCiphertext = stagedTokenCiphertext.String
	rotation.stagedTokenNonce = stagedTokenNonce.String
	rotation.credentialClaimIDHash = credentialClaimIDHash.String
	if credentialClaimRevision.Valid {
		rotation.credentialClaimRevision = credentialClaimRevision.Int64
	}
	rotation.EmergencyRevokedTokenID = emergencyRevokedTokenID.String
	if credentialClaimedAt.Valid {
		rotation.CredentialClaimedAt = cloneTimePtr(&credentialClaimedAt.Time)
	}
	if localStageAcknowledgedAt.Valid {
		rotation.LocalStageAcknowledgedAt = cloneTimePtr(&localStageAcknowledgedAt.Time)
	}
	if localStagedAt.Valid {
		rotation.LocalStagedAt = cloneTimePtr(&localStagedAt.Time)
	}
	if heartbeatProvedAt.Valid {
		rotation.HeartbeatProvedAt = cloneTimePtr(&heartbeatProvedAt.Time)
	}
	if activatedAt.Valid {
		rotation.ActivatedAt = cloneTimePtr(&activatedAt.Time)
	}
	if cancelRequestedAt.Valid {
		rotation.CancelRequestedAt = cloneTimePtr(&cancelRequestedAt.Time)
	}
	if cancelAcknowledgedAt.Valid {
		rotation.CancelAcknowledgedAt = cloneTimePtr(&cancelAcknowledgedAt.Time)
	}
	if canceledAt.Valid {
		rotation.CanceledAt = cloneTimePtr(&canceledAt.Time)
	}
	if emergencyRevokedAt.Valid {
		rotation.EmergencyRevokedAt = cloneTimePtr(&emergencyRevokedAt.Time)
	}
	return rotation, nil
}
