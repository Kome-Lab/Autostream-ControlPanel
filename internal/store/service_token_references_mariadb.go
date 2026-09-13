package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type mariaDBServiceTokenReference struct {
	ServiceID             string
	TokenID               string
	StagedPreviousTokenID string
	StagedTokenID         string
}

func updateAgentStageTokenReferenceAllowed(serviceID, tokenID, stagedPreviousTokenID, stagedTokenID, targetServiceID, oldTokenID string) bool {
	if tokenID != oldTokenID && stagedPreviousTokenID != oldTokenID && stagedTokenID != oldTokenID {
		return true
	}
	return serviceID == targetServiceID &&
		tokenID == oldTokenID &&
		stagedPreviousTokenID != oldTokenID &&
		stagedTokenID != oldTokenID
}

func updateAgentActivationTokenReferenceAllowed(serviceID, tokenID, stagedPreviousTokenID, stagedTokenID, targetServiceID, oldTokenID, newTokenID string) bool {
	if tokenID != oldTokenID && tokenID != newTokenID &&
		stagedPreviousTokenID != oldTokenID && stagedPreviousTokenID != newTokenID &&
		stagedTokenID != oldTokenID && stagedTokenID != newTokenID {
		return true
	}
	return serviceID == targetServiceID &&
		tokenID == oldTokenID &&
		stagedPreviousTokenID == oldTokenID &&
		stagedTokenID == newTokenID
}

type mariaDBServiceTokenReferenceQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type mariaDBServiceTokenLockPhase string

const (
	mariaDBServiceTokenReferenceDiscoveryComplete mariaDBServiceTokenLockPhase = "reference_discovery_complete"
	mariaDBServiceTokenBeforeServiceLocks         mariaDBServiceTokenLockPhase = "before_service_locks"
	mariaDBServiceTokenServiceLocksHeld           mariaDBServiceTokenLockPhase = "service_locks_held"
	mariaDBServiceTokenBeforeTokenLocks           mariaDBServiceTokenLockPhase = "before_token_locks"
	mariaDBServiceTokenTokenLocksHeld             mariaDBServiceTokenLockPhase = "token_locks_held"
	mariaDBServiceTokenBindingsValidated          mariaDBServiceTokenLockPhase = "bindings_validated"
	mariaDBServiceTokenReferenceSetMismatch       mariaDBServiceTokenLockPhase = "reference_set_mismatch"
	mariaDBServiceTokenReferenceRetryStart        mariaDBServiceTokenLockPhase = "reference_retry_start"
	mariaDBServiceTokenStableAuthReplayConflict   mariaDBServiceTokenLockPhase = "stable_auth_replay_conflict"
	mariaDBServiceTokenReferenceRetryLimit                                     = 3
)

var errMariaDBServiceTokenReferenceSetChanged = errors.New("mariadb service token reference set changed")

type mariaDBServiceTokenLockObserver func(string, mariaDBServiceTokenLockPhase)
type mariaDBServiceTokenLockObserverContextKey struct{}

func observeMariaDBServiceTokenLockPhase(ctx context.Context, operation string, phase mariaDBServiceTokenLockPhase) {
	observer, _ := ctx.Value(mariaDBServiceTokenLockObserverContextKey{}).(mariaDBServiceTokenLockObserver)
	if observer != nil {
		observer(operation, phase)
	}
}

func discoverMariaDBServiceTokenReferences(
	ctx context.Context,
	queryer mariaDBServiceTokenReferenceQueryer,
	tokenIDs []string,
) ([]mariaDBServiceTokenReference, error) {
	tokenIDs = sortedUniqueStrings(tokenIDs)
	if len(tokenIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(tokenIDs)), ",")
	args := make([]any, 0, len(tokenIDs)*3)
	for range 3 {
		for _, tokenID := range tokenIDs {
			args = append(args, tokenID)
		}
	}
	rows, err := queryer.QueryContext(ctx, `SELECT service_id, token_id,
COALESCE(staged_node_previous_token_id, ''), COALESCE(staged_node_token_id, '')
FROM services
WHERE token_id IN (`+placeholders+`)
   OR staged_node_previous_token_id IN (`+placeholders+`)
   OR staged_node_token_id IN (`+placeholders+`)
ORDER BY service_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byServiceID := make(map[string]mariaDBServiceTokenReference)
	for rows.Next() {
		var reference mariaDBServiceTokenReference
		if err := rows.Scan(
			&reference.ServiceID,
			&reference.TokenID,
			&reference.StagedPreviousTokenID,
			&reference.StagedTokenID,
		); err != nil {
			return nil, err
		}
		reference.ServiceID = strings.TrimSpace(reference.ServiceID)
		reference.TokenID = strings.TrimSpace(reference.TokenID)
		reference.StagedPreviousTokenID = strings.TrimSpace(reference.StagedPreviousTokenID)
		reference.StagedTokenID = strings.TrimSpace(reference.StagedTokenID)
		if reference.ServiceID != "" {
			byServiceID[reference.ServiceID] = reference
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	serviceIDs := make([]string, 0, len(byServiceID))
	for serviceID := range byServiceID {
		serviceIDs = append(serviceIDs, serviceID)
	}
	serviceIDs = sortedUniqueStrings(serviceIDs)
	references := make([]mariaDBServiceTokenReference, 0, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		references = append(references, byServiceID[serviceID])
	}
	return references, nil
}

func mariaDBServiceTokenReferencesEqual(left, right []mariaDBServiceTokenReference) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func mariaDBServiceTokenReferenceServiceIDs(
	references []mariaDBServiceTokenReference,
	additional ...string,
) []string {
	serviceIDs := append([]string(nil), additional...)
	for _, reference := range references {
		serviceIDs = append(serviceIDs, reference.ServiceID)
	}
	return sortedUniqueStrings(serviceIDs)
}

func lockMariaDBServiceTokensSorted(
	ctx context.Context,
	tx *sql.Tx,
	tokenIDs []string,
) (map[string]ServiceToken, error) {
	locked := make(map[string]ServiceToken)
	for _, tokenID := range sortedUniqueStrings(tokenIDs) {
		token, err := selectServiceTokenForNodeConfiguration(ctx, tx, tokenID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		locked[token.ID] = token
	}
	return locked, nil
}

func lockMariaDBServiceTokenMutation(
	ctx context.Context,
	tx *sql.Tx,
	operation string,
	discovered []mariaDBServiceTokenReference,
	tokenIDs []string,
	additionalServiceIDs ...string,
) (map[string]RegisteredService, map[string]ServiceToken, error) {
	return lockMariaDBServiceTokenMutationWithReferenceSetError(
		ctx,
		tx,
		operation,
		discovered,
		tokenIDs,
		ErrNotFound,
		additionalServiceIDs...,
	)
}

func lockMariaDBServiceTokenMutationRetryable(
	ctx context.Context,
	tx *sql.Tx,
	operation string,
	discovered []mariaDBServiceTokenReference,
	tokenIDs []string,
	additionalServiceIDs ...string,
) (map[string]RegisteredService, map[string]ServiceToken, error) {
	return lockMariaDBServiceTokenMutationWithReferenceSetError(
		ctx,
		tx,
		operation,
		discovered,
		tokenIDs,
		errMariaDBServiceTokenReferenceSetChanged,
		additionalServiceIDs...,
	)
}

func lockMariaDBServiceTokenMutationWithReferenceSetError(
	ctx context.Context,
	tx *sql.Tx,
	operation string,
	discovered []mariaDBServiceTokenReference,
	tokenIDs []string,
	referenceSetError error,
	additionalServiceIDs ...string,
) (map[string]RegisteredService, map[string]ServiceToken, error) {
	observeMariaDBServiceTokenLockPhase(ctx, operation, mariaDBServiceTokenBeforeServiceLocks)
	lockedServices, err := lockMariaDBServicesSorted(
		ctx,
		tx,
		mariaDBServiceTokenReferenceServiceIDs(discovered, additionalServiceIDs...),
	)
	if err != nil {
		return nil, nil, err
	}
	observeMariaDBServiceTokenLockPhase(ctx, operation, mariaDBServiceTokenServiceLocksHeld)
	observeMariaDBServiceTokenLockPhase(ctx, operation, mariaDBServiceTokenBeforeTokenLocks)
	lockedTokens, err := lockMariaDBServiceTokensSorted(ctx, tx, tokenIDs)
	if err != nil {
		return nil, nil, err
	}
	observeMariaDBServiceTokenLockPhase(ctx, operation, mariaDBServiceTokenTokenLocksHeld)
	revalidated, err := discoverMariaDBServiceTokenReferences(ctx, tx, tokenIDs)
	if err != nil {
		return nil, nil, err
	}
	if !mariaDBServiceTokenReferencesEqual(discovered, revalidated) {
		return nil, nil, referenceSetError
	}
	observeMariaDBServiceTokenLockPhase(ctx, operation, mariaDBServiceTokenBindingsValidated)
	return lockedServices, lockedTokens, nil
}

func lockMariaDBPrecreateServiceAuthority(
	ctx context.Context,
	tx *sql.Tx,
	serviceID string,
	discovered []mariaDBServiceTokenReference,
) (map[string]RegisteredService, error) {
	serviceIDs := mariaDBServiceTokenReferenceServiceIDs(discovered, serviceID)
	locked := make(map[string]RegisteredService, len(serviceIDs))
	for _, candidateID := range serviceIDs {
		service, err := scanService(tx.QueryRowContext(
			ctx,
			serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`,
			candidateID,
		))
		if errors.Is(err, sql.ErrNoRows) && candidateID == serviceID {
			// Under InnoDB's default REPEATABLE READ isolation this unique-key
			// miss establishes the insertion gap authority for serviceID. The
			// caller inserts only after every pre-existing reference was locked
			// in the same lexical service order used by token mutations.
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if candidateID == serviceID {
			return nil, ErrAlreadyExists
		}
		locked[service.ServiceID] = service
	}
	return locked, nil
}

func mariaDBServiceTokenReferencesWithCurrentBinding(
	references []mariaDBServiceTokenReference,
	serviceID, tokenID string,
) []mariaDBServiceTokenReference {
	byServiceID := make(map[string]mariaDBServiceTokenReference, len(references)+1)
	for _, reference := range references {
		byServiceID[reference.ServiceID] = reference
	}
	byServiceID[serviceID] = mariaDBServiceTokenReference{
		ServiceID: serviceID,
		TokenID:   tokenID,
	}
	serviceIDs := make([]string, 0, len(byServiceID))
	for candidateID := range byServiceID {
		serviceIDs = append(serviceIDs, candidateID)
	}
	serviceIDs = sortedUniqueStrings(serviceIDs)
	result := make([]mariaDBServiceTokenReference, 0, len(serviceIDs))
	for _, candidateID := range serviceIDs {
		result = append(result, byServiceID[candidateID])
	}
	return result
}

func mariaDBServiceTokenSnapshotMatches(input, locked ServiceToken) bool {
	if strings.TrimSpace(input.ID) != locked.ID ||
		strings.TrimSpace(input.ServiceType) != locked.ServiceType ||
		input.RevokedAt != nil || locked.RevokedAt != nil ||
		(len(input.Scopes) != len(locked.Scopes)) {
		return false
	}
	if input.TokenHash != "" && input.TokenHash != locked.TokenHash {
		return false
	}
	for index := range input.Scopes {
		if strings.TrimSpace(input.Scopes[index]) != locked.Scopes[index] {
			return false
		}
	}
	return true
}

func mariaDBServiceTokenReferenceContains(reference mariaDBServiceTokenReference, tokenID string) bool {
	tokenID = strings.TrimSpace(tokenID)
	return tokenID != "" && (reference.TokenID == tokenID ||
		reference.StagedPreviousTokenID == tokenID ||
		reference.StagedTokenID == tokenID)
}

func mariaDBServiceTokenReferenceTypesMatch(
	references []mariaDBServiceTokenReference,
	services map[string]RegisteredService,
	tokens map[string]ServiceToken,
) bool {
	for _, reference := range references {
		service, ok := services[reference.ServiceID]
		if !ok || service.TokenID != reference.TokenID ||
			service.StagedNodePreviousTokenID != reference.StagedPreviousTokenID ||
			service.StagedNodeTokenID != reference.StagedTokenID {
			return false
		}
		for tokenID, token := range tokens {
			if mariaDBServiceTokenReferenceContains(reference, tokenID) && service.ServiceType != token.ServiceType {
				return false
			}
		}
	}
	return true
}

func mariaDBServiceTokenReferencesUseCurrentToken(
	references []mariaDBServiceTokenReference,
	tokenID string,
) bool {
	tokenID = strings.TrimSpace(tokenID)
	for _, reference := range references {
		if reference.TokenID != tokenID {
			return false
		}
	}
	return true
}
