package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"strings"
	"time"
)

func (s MariaDBAuthStore) CreateServiceToken(ctx context.Context, serviceType string, scopes []string) (ServiceToken, error) {
	if err := validateServiceType(serviceType); err != nil {
		return ServiceToken{}, err
	}
	if err := validateServiceScopes(scopes); err != nil {
		return ServiceToken{}, err
	}
	raw, err := security.RandomToken(32)
	if err != nil {
		return ServiceToken{}, err
	}
	token := ServiceToken{ID: newUUID(), ServiceType: serviceType, Scopes: scopes, RawToken: "ast_svc_" + raw, CreatedAt: time.Now().UTC()}
	token.TokenHash = security.HashToken(token.RawToken)
	body, err := json.Marshal(scopes)
	if err != nil {
		return ServiceToken{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO service_tokens (id, service_type, token_hash, scopes, created_at) VALUES (?, ?, ?, ?, ?)`, token.ID, token.ServiceType, token.TokenHash, string(body), token.CreatedAt)
	if err != nil {
		return ServiceToken{}, err
	}
	return token, nil
}

func (s MariaDBAuthStore) ListServiceTokens(ctx context.Context) ([]ServiceToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, service_type, scopes, revoked_at, created_at FROM service_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []ServiceToken
	for rows.Next() {
		var token ServiceToken
		var scopes string
		var revoked sql.NullTime
		if err := rows.Scan(&token.ID, &token.ServiceType, &scopes, &revoked, &token.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopes), &token.Scopes)
		if revoked.Valid {
			token.RevokedAt = &revoked.Time
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (s MariaDBAuthStore) RevokeServiceToken(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrNotFound
	}
	for range mariaDBServiceTokenReferenceRetryLimit {
		discovered, err := discoverMariaDBServiceTokenReferences(ctx, s.db, []string{id})
		if err != nil {
			return err
		}
		err = func() error {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			lockedServices, lockedTokens, err := lockMariaDBServiceTokenMutationRetryable(
				ctx, tx, "revoke_service_token", discovered, []string{id},
			)
			if err != nil {
				return err
			}
			token, ok := lockedTokens[id]
			if !ok || token.RevokedAt != nil ||
				!mariaDBServiceTokenReferenceTypesMatch(discovered, lockedServices, lockedTokens) {
				return ErrNotFound
			}
			now := time.Now().UTC()
			if err := revokeServiceTokenInTx(ctx, tx, id, now); err != nil {
				return err
			}
			for _, reference := range discovered {
				if reference.TokenID != id {
					continue
				}
				if _, err := tx.ExecContext(ctx, `UPDATE services SET last_heartbeat_at = NULL, reported_capabilities = '{}', updated_at = ? WHERE service_id = ? AND token_id = ?`, now, reference.ServiceID, id); err != nil {
					return err
				}
			}
			return tx.Commit()
		}()
		if errors.Is(err, errMariaDBServiceTokenReferenceSetChanged) {
			continue
		}
		return err
	}
	return ErrNotFound
}

func (s MariaDBAuthStore) RotateServiceToken(ctx context.Context, id string) (ServiceToken, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ServiceToken{}, ErrNotFound
	}
	for range mariaDBServiceTokenReferenceRetryLimit {
		discovered, err := discoverMariaDBServiceTokenReferences(ctx, s.db, []string{id})
		if err != nil {
			return ServiceToken{}, err
		}
		var rotated ServiceToken
		err = func() error {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()

			lockedServices, lockedTokens, err := lockMariaDBServiceTokenMutationRetryable(
				ctx, tx, "rotate_service_token", discovered, []string{id},
			)
			if err != nil {
				return err
			}
			oldToken, ok := lockedTokens[id]
			if !ok || oldToken.RevokedAt != nil {
				return ErrNotFound
			}
			if !mariaDBServiceTokenReferenceTypesMatch(discovered, lockedServices, lockedTokens) {
				return ErrNotFound
			}
			if !mariaDBServiceTokenReferencesUseCurrentToken(discovered, oldToken.ID) {
				return ErrNotFound
			}

			raw, err := security.RandomToken(32)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			token := ServiceToken{
				ID:          newUUID(),
				ServiceType: oldToken.ServiceType,
				Scopes:      serviceTokenScopesForRotation(oldToken),
				RawToken:    "ast_svc_" + raw,
				CreatedAt:   now,
			}
			token.TokenHash = security.HashToken(token.RawToken)
			body, err := json.Marshal(token.Scopes)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO service_tokens (id, service_type, token_hash, scopes, created_at) VALUES (?, ?, ?, ?, ?)`, token.ID, token.ServiceType, token.TokenHash, string(body), token.CreatedAt); err != nil {
				return err
			}
			for _, reference := range discovered {
				result, err := tx.ExecContext(ctx, `UPDATE services SET token_id = ?, last_heartbeat_at = NULL, reported_capabilities = '{}', staged_node_previous_token_id = NULL, staged_node_token_id = NULL, staged_node_token_hash = NULL, staged_node_token_scopes = NULL, staged_node_token_ciphertext = NULL, staged_node_token_nonce = NULL, staged_node_activation_token_hash = NULL, staged_node_token_at = NULL, node_token_rotated_at = ?, updated_at = ? WHERE service_id = ? AND token_id = ?`, token.ID, now, now, reference.ServiceID, oldToken.ID)
				if err != nil {
					return err
				}
				if affected, err := result.RowsAffected(); err != nil {
					return err
				} else if affected != 1 {
					return ErrNotFound
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE service_tokens SET revoked_at = ? WHERE id = ?`, now, oldToken.ID); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			rotated = token
			return nil
		}()
		if errors.Is(err, errMariaDBServiceTokenReferenceSetChanged) {
			continue
		}
		return rotated, err
	}
	return ServiceToken{}, ErrNotFound
}

func (s MariaDBAuthStore) RotateServiceNodeToken(ctx context.Context, serviceID, expectedTokenID string, seal NodeTokenSealer) (ServiceToken, RegisteredService, error) {
	if seal == nil {
		return ServiceToken{}, RegisteredService{}, errNodeTokenSealerRequired
	}
	serviceID = strings.TrimSpace(serviceID)
	expectedTokenID = strings.TrimSpace(expectedTokenID)
	if serviceID == "" || expectedTokenID == "" {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	discovered, err := discoverMariaDBServiceTokenReferences(ctx, s.db, []string{expectedTokenID})
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	defer tx.Rollback()

	lockedServices, lockedTokens, err := lockMariaDBServiceTokenMutation(
		ctx, tx, "rotate_service_node_token", discovered, []string{expectedTokenID}, serviceID,
	)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	service, ok := lockedServices[serviceID]
	if !ok {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if service.TokenID != expectedTokenID {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if requiresStagedNodeTokenRotation(service) {
		return ServiceToken{}, RegisteredService{}, ErrConflict
	}

	oldToken, ok := lockedTokens[expectedTokenID]
	if !ok || oldToken.RevokedAt != nil {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if service.ServiceType != oldToken.ServiceType ||
		!mariaDBServiceTokenReferenceTypesMatch(discovered, lockedServices, lockedTokens) {
		return ServiceToken{}, RegisteredService{}, ErrForbidden
	}

	now := time.Now().UTC()
	token, scopesJSON, err := newRotatedServiceToken(oldToken, now)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	ciphertext, nonce, err := seal(token.RawToken)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO service_tokens (id, service_type, token_hash, scopes, created_at) VALUES (?, ?, ?, ?, ?)`, token.ID, token.ServiceType, token.TokenHash, scopesJSON, token.CreatedAt); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE services SET token_id = ?, node_token_ciphertext = ?, node_token_nonce = ?, last_heartbeat_at = NULL, reported_capabilities = '{}', staged_node_previous_token_id = NULL, staged_node_token_id = NULL, staged_node_token_hash = NULL, staged_node_token_scopes = NULL, staged_node_token_ciphertext = NULL, staged_node_token_nonce = NULL, staged_node_activation_token_hash = NULL, staged_node_token_at = NULL, configure_token_hash = NULL, configure_token_expires_at = NULL, configure_token_used_at = NULL, node_token_rotated_at = ?, updated_at = ? WHERE service_id = ? AND token_id = ?`, token.ID, ciphertext, nonce, now, now, service.ServiceID, oldToken.ID)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	if affected != 1 {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if err := revokeServiceTokenIfUnreferencedInTx(ctx, tx, oldToken.ID, now); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}

	service.TokenID = token.ID
	service.NodeTokenCiphertext = ciphertext
	service.NodeTokenNonce = nonce
	service.ConfigureTokenHash = ""
	service.ConfigureTokenExpiresAt = nil
	service.ConfigureTokenUsedAt = nil
	clearStagedNodeConfiguration(&service)
	service.LastHeartbeatAt = nil
	service.ReportedCapabilities = map[string]any{}
	service.NodeTokenRotatedAt = &now
	service.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	return token, service, nil
}

func requiresStagedNodeTokenRotation(service RegisteredService) bool {
	return service.ServiceType == "update_agent" &&
		service.TransportMode == SystemUpdateTransportPullV2
}

func isEmergencyRevokedNodeConfigurationAnchor(service RegisteredService, token ServiceToken) bool {
	return requiresStagedNodeTokenRotation(service) &&
		service.TokenID == token.ID &&
		service.Status == "offline" &&
		service.LastHeartbeatAt == nil &&
		service.NodeTokenCiphertext == "" &&
		service.NodeTokenNonce == "" &&
		len(service.ReportedCapabilities) == 0 &&
		token.ServiceType == "update_agent" &&
		token.RevokedAt != nil
}

func hasOnlyStagedNodeConfigurationTombstone(service RegisteredService) bool {
	return service.StagedNodePreviousTokenID == "" &&
		service.StagedNodeTokenID != "" &&
		service.StagedNodeTokenID != service.TokenID &&
		service.StagedNodeTokenHash == "" &&
		len(service.StagedNodeTokenScopes) == 0 &&
		service.StagedNodeTokenCiphertext == "" &&
		service.StagedNodeTokenNonce == "" &&
		service.StagedNodeActivationTokenHash == "" &&
		service.StagedNodeTokenAt == nil
}

func selectActiveServiceTokenForUpdate(ctx context.Context, tx *sql.Tx, id string) (ServiceToken, error) {
	var token ServiceToken
	var scopesJSON string
	var revoked sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT id, service_type, scopes, revoked_at, created_at FROM service_tokens WHERE id = ? FOR UPDATE`, id).Scan(&token.ID, &token.ServiceType, &scopesJSON, &revoked, &token.CreatedAt)
	if err == sql.ErrNoRows || revoked.Valid {
		return ServiceToken{}, ErrNotFound
	}
	if err != nil {
		return ServiceToken{}, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &token.Scopes); err != nil {
		return ServiceToken{}, err
	}
	return token, nil
}

func selectServiceTokenForNodeConfiguration(
	ctx context.Context,
	tx *sql.Tx,
	id string,
) (ServiceToken, error) {
	var token ServiceToken
	var scopesJSON string
	var revoked sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT id, service_type, token_hash, scopes, revoked_at, created_at FROM service_tokens WHERE id = ? FOR UPDATE`, id).Scan(&token.ID, &token.ServiceType, &token.TokenHash, &scopesJSON, &revoked, &token.CreatedAt)
	if err == sql.ErrNoRows {
		return ServiceToken{}, ErrNotFound
	}
	if err != nil {
		return ServiceToken{}, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &token.Scopes); err != nil {
		return ServiceToken{}, err
	}
	if revoked.Valid {
		token.RevokedAt = cloneTimePtr(&revoked.Time)
	}
	return token, nil
}

func newRotatedServiceToken(oldToken ServiceToken, now time.Time) (ServiceToken, string, error) {
	raw, err := security.RandomToken(32)
	if err != nil {
		return ServiceToken{}, "", err
	}
	token := ServiceToken{
		ID:          newUUID(),
		ServiceType: oldToken.ServiceType,
		Scopes:      serviceTokenScopesForRotation(oldToken),
		RawToken:    "ast_svc_" + raw,
		CreatedAt:   now,
	}
	token.TokenHash = security.HashToken(token.RawToken)
	scopesJSON, err := json.Marshal(token.Scopes)
	if err != nil {
		return ServiceToken{}, "", err
	}
	return token, string(scopesJSON), nil
}

func serviceTokenScopesForRotation(oldToken ServiceToken) []string {
	return ProjectedServiceTokenScopesForRotation(oldToken)
}

// ProjectedServiceTokenScopesForRotation returns the scopes a freshly issued
// replacement Node Runtime Token would receive. Callers that authorize an
// operator-triggered rotation must validate this projected set rather than the
// old token alone, so an operator cannot use a rotation to grant a permission
// they do not hold.
func ProjectedServiceTokenScopesForRotation(oldToken ServiceToken) []string {
	scopes := append([]string(nil), oldToken.Scopes...)
	if oldToken.ServiceType == "observability" && !hasString(scopes, "notifications.email.send") {
		scopes = append(scopes, "notifications.email.send")
	}
	// Voice-triggered streams use streams.start and streams.stop as one paired
	// capability. Keep a legacy Bot token unchanged until an operator performs
	// the existing explicit Node Configure/rotation flow; that flow then grants
	// only the missing paired stop scope, never a broader Discord authority.
	if oldToken.ServiceType == "discord_bot" &&
		hasString(scopes, "streams.start") &&
		!hasString(scopes, "streams.stop") {
		scopes = append(scopes, "streams.stop")
	}
	return scopes
}

func revokeServiceTokenInTx(ctx context.Context, tx *sql.Tx, id string, now time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE service_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

func revokeServiceTokenIfUnreferencedInTx(ctx context.Context, tx *sql.Tx, id string, now time.Time) error {
	var references int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM services
WHERE token_id = ? OR staged_node_previous_token_id = ? OR staged_node_token_id = ?`, id, id, id).Scan(&references)
	if err != nil {
		return err
	}
	if references > 0 {
		return nil
	}
	return revokeServiceTokenInTx(ctx, tx, id, now)
}

func (s MariaDBAuthStore) AuthenticateServiceToken(ctx context.Context, rawToken, requiredScope string) (ServiceToken, error) {
	if rawToken == "" {
		return ServiceToken{}, ErrUnauthorized
	}
	var token ServiceToken
	var scopes string
	var revoked sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT id, service_type, token_hash, scopes, revoked_at, created_at FROM service_tokens WHERE token_hash = ?`, security.HashToken(rawToken)).Scan(&token.ID, &token.ServiceType, &token.TokenHash, &scopes, &revoked, &token.CreatedAt)
	if err == sql.ErrNoRows {
		return ServiceToken{}, ErrUnauthorized
	}
	if err != nil {
		return ServiceToken{}, err
	}
	if revoked.Valid {
		return ServiceToken{}, ErrUnauthorized
	}
	_ = json.Unmarshal([]byte(scopes), &token.Scopes)
	if requiredScope != "" && !hasString(token.Scopes, requiredScope) {
		return ServiceToken{}, ErrForbidden
	}
	return token, nil
}
