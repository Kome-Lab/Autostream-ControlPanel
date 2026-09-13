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

func (s MariaDBAuthStore) SetServiceConfigureToken(ctx context.Context, serviceID, tokenHash string, expiresAt time.Time) (RegisteredService, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE services SET configure_token_hash = ?, configure_token_expires_at = ?, configure_token_used_at = NULL, staged_node_previous_token_id = NULL, staged_node_token_id = CASE WHEN staged_node_token_id IS NOT NULL AND staged_node_token_id <> token_id THEN staged_node_token_id ELSE NULL END, staged_node_token_hash = NULL, staged_node_token_scopes = NULL, staged_node_token_ciphertext = NULL, staged_node_token_nonce = NULL, staged_node_activation_token_hash = NULL, staged_node_token_at = NULL, updated_at = ? WHERE service_id = ?`, tokenHash, expiresAt, now, serviceID)
	if err != nil {
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrNotFound
	}
	return s.getService(ctx, serviceID)
}

func (s MariaDBAuthStore) ValidateServiceConfigureToken(ctx context.Context, serviceID, rawToken string, now time.Time) (bool, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return false, ErrNotFound
	}
	var tokenHash string
	var expiresAt sql.NullTime
	var usedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(configure_token_hash, ''), configure_token_expires_at, configure_token_used_at FROM services WHERE service_id = ?`, serviceID).Scan(&tokenHash, &expiresAt, &usedAt)
	if err == sql.ErrNoRows {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	now = now.UTC()
	return tokenHash != "" &&
		expiresAt.Valid &&
		!usedAt.Valid &&
		now.Before(expiresAt.Time.UTC()) &&
		security.VerifyTokenHash(rawToken, tokenHash), nil
}

func (s MariaDBAuthStore) ConsumeServiceConfigureToken(ctx context.Context, serviceID, rawToken string, now time.Time) (RegisteredService, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegisteredService{}, err
	}
	defer tx.Rollback()
	var tokenHash string
	var expiresAt sql.NullTime
	var usedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(configure_token_hash, ''), configure_token_expires_at, configure_token_used_at FROM services WHERE service_id = ? FOR UPDATE`, serviceID).Scan(&tokenHash, &expiresAt, &usedAt)
	if err == sql.ErrNoRows {
		return RegisteredService{}, ErrNotFound
	}
	if err != nil {
		return RegisteredService{}, err
	}
	if tokenHash == "" || !expiresAt.Valid || usedAt.Valid || !now.Before(expiresAt.Time) || !security.VerifyTokenHash(rawToken, tokenHash) {
		return RegisteredService{}, ErrUnauthorized
	}
	if _, err := tx.ExecContext(ctx, `UPDATE services SET configure_token_used_at = ?, updated_at = ? WHERE service_id = ?`, now, now, serviceID); err != nil {
		return RegisteredService{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegisteredService{}, err
	}
	return s.getService(ctx, serviceID)
}

func (s MariaDBAuthStore) ConfigureServiceNode(ctx context.Context, serviceID, rawConfigureToken string, now time.Time, report ServiceRuntimeReport, seal NodeTokenSealer) (ServiceToken, RegisteredService, error) {
	if seal == nil {
		return ServiceToken{}, RegisteredService{}, errNodeTokenSealerRequired
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	now = now.UTC()
	report.ServiceID = serviceID
	report = normalizeServiceRuntimeReport(report)
	discoveredService, err := s.getService(ctx, serviceID)
	if errors.Is(err, ErrNotFound) {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	discovered, err := discoverMariaDBServiceTokenReferences(ctx, s.db, []string{discoveredService.TokenID})
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	defer tx.Rollback()

	lockedServices, lockedTokens, err := lockMariaDBServiceTokenMutation(
		ctx, tx, "configure_service_node", discovered, []string{discoveredService.TokenID}, serviceID,
	)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	service, ok := lockedServices[serviceID]
	if !ok || service.TokenID != discoveredService.TokenID {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if service.ServiceType == "update_agent" {
		return ServiceToken{}, RegisteredService{}, ErrTwoPhaseConfigureRequired
	}
	var configureTokenHash string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(configure_token_hash, '') FROM services WHERE service_id = ?`, serviceID).Scan(&configureTokenHash); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	if configureTokenHash == "" || service.ConfigureTokenExpiresAt == nil || service.ConfigureTokenUsedAt != nil || !now.Before(*service.ConfigureTokenExpiresAt) || !security.VerifyTokenHash(rawConfigureToken, configureTokenHash) {
		return ServiceToken{}, RegisteredService{}, ErrUnauthorized
	}

	oldToken, ok := lockedTokens[service.TokenID]
	if !ok || oldToken.RevokedAt != nil {
		return ServiceToken{}, RegisteredService{}, ErrNotFound
	}
	if service.ServiceType != oldToken.ServiceType ||
		!mariaDBServiceTokenReferenceTypesMatch(discovered, lockedServices, lockedTokens) {
		return ServiceToken{}, RegisteredService{}, ErrForbidden
	}
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
	result, err := tx.ExecContext(ctx, `UPDATE services SET status = CASE WHEN status = 'pending' THEN 'registered' ELSE status END, version = CASE WHEN ? = '' THEN version ELSE ? END, reported_version = CASE WHEN ? = '' THEN reported_version ELSE ? END, reported_commit = CASE WHEN ? = '' THEN reported_commit ELSE ? END, reported_build_date = CASE WHEN ? = '' THEN reported_build_date ELSE ? END, reported_hostname = CASE WHEN ? = '' THEN reported_hostname ELSE ? END, reported_os = CASE WHEN ? = '' THEN reported_os ELSE ? END, reported_arch = CASE WHEN ? = '' THEN reported_arch ELSE ? END, last_reported_at = CASE WHEN ? = '' AND ? = '' AND ? = '' AND ? = '' AND ? = '' AND ? = '' THEN last_reported_at ELSE ? END, token_id = ?, node_token_ciphertext = ?, node_token_nonce = ?, configure_token_used_at = ?, last_heartbeat_at = NULL, reported_capabilities = '{}', node_token_rotated_at = ?, updated_at = ? WHERE service_id = ? AND token_id = ?`,
		report.Version, report.Version, report.Version, report.Version, report.Commit, report.Commit, report.BuildDate, report.BuildDate, report.Hostname, report.Hostname, report.OS, report.OS, report.Arch, report.Arch, report.Version, report.Commit, report.BuildDate, report.Hostname, report.OS, report.Arch, now, token.ID, ciphertext, nonce, now, now, now, serviceID, oldToken.ID)
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

	service = applyServiceRuntimeReport(service, report, now)
	service.TokenID = token.ID
	service.NodeTokenCiphertext = ciphertext
	service.NodeTokenNonce = nonce
	service.ConfigureTokenUsedAt = &now
	service.LastHeartbeatAt = nil
	service.ReportedCapabilities = map[string]any{}
	service.NodeTokenRotatedAt = &now
	service.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return ServiceToken{}, RegisteredService{}, err
	}
	return token, service, nil
}

func (s MariaDBAuthStore) StageServiceNodeConfiguration(ctx context.Context, serviceID, rawConfigureToken string, now time.Time, seal NodeTokenSealer) (StagedServiceNodeConfiguration, error) {
	if seal == nil {
		return StagedServiceNodeConfiguration{}, errNodeTokenSealerRequired
	}
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	now = now.UTC()
	for attempt := 0; attempt < mariaDBServiceTokenReferenceRetryLimit; attempt++ {
		discoveredService, err := s.getService(ctx, serviceID)
		if errors.Is(err, ErrNotFound) {
			return StagedServiceNodeConfiguration{}, ErrNotFound
		}
		if err != nil {
			return StagedServiceNodeConfiguration{}, err
		}
		discovered, err := discoverMariaDBServiceTokenReferences(ctx, s.db, []string{discoveredService.TokenID})
		if err != nil {
			return StagedServiceNodeConfiguration{}, err
		}
		observeMariaDBServiceTokenLockPhase(ctx, "stage_service_node_configuration", mariaDBServiceTokenReferenceDiscoveryComplete)
		staged, err := s.stageServiceNodeConfigurationWithReferences(
			ctx,
			serviceID,
			rawConfigureToken,
			now,
			seal,
			discoveredService,
			discovered,
		)
		if !errors.Is(err, errMariaDBServiceTokenReferenceSetChanged) {
			return staged, err
		}
		observeMariaDBServiceTokenLockPhase(ctx, "stage_service_node_configuration", mariaDBServiceTokenReferenceSetMismatch)
		if attempt+1 < mariaDBServiceTokenReferenceRetryLimit {
			observeMariaDBServiceTokenLockPhase(ctx, "stage_service_node_configuration", mariaDBServiceTokenReferenceRetryStart)
		}
	}
	return StagedServiceNodeConfiguration{}, ErrConflict
}

func (s MariaDBAuthStore) stageServiceNodeConfigurationWithReferences(
	ctx context.Context,
	serviceID, rawConfigureToken string,
	now time.Time,
	seal NodeTokenSealer,
	discoveredService RegisteredService,
	discovered []mariaDBServiceTokenReference,
) (StagedServiceNodeConfiguration, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	defer tx.Rollback()

	lockedServices, lockedTokens, err := lockMariaDBServiceTokenMutationRetryable(
		ctx, tx, "stage_service_node_configuration", discovered, []string{discoveredService.TokenID},
		serviceID,
	)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	observeMariaDBServiceTokenLockPhase(ctx, "stage_service_node_configuration", mariaDBServiceTokenStableAuthReplayConflict)
	service, ok := lockedServices[serviceID]
	if !ok || service.TokenID != discoveredService.TokenID {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	if service.ServiceType != "update_agent" {
		return StagedServiceNodeConfiguration{}, ErrForbidden
	}
	var configureTokenHash string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(configure_token_hash, '') FROM services WHERE service_id = ?`, serviceID).Scan(&configureTokenHash); err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	if configureTokenHash == "" || service.ConfigureTokenExpiresAt == nil || service.ConfigureTokenUsedAt != nil || !now.Before(*service.ConfigureTokenExpiresAt) || !security.VerifyTokenHash(rawConfigureToken, configureTokenHash) {
		return StagedServiceNodeConfiguration{}, ErrUnauthorized
	}
	oldToken, ok := lockedTokens[service.TokenID]
	if !ok {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	if oldToken.RevokedAt != nil &&
		(!isEmergencyRevokedNodeConfigurationAnchor(service, oldToken) ||
			(hasStagedServiceNodeConfiguration(service) &&
				!hasOnlyStagedNodeConfigurationTombstone(service))) {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	if oldToken.ServiceType != "update_agent" {
		return StagedServiceNodeConfiguration{}, ErrForbidden
	}
	if err := validateRequiredUpdateAgentScopes(oldToken.ServiceType, oldToken.Scopes); err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	for _, reference := range discovered {
		if !updateAgentStageTokenReferenceAllowed(
			reference.ServiceID,
			reference.TokenID,
			reference.StagedPreviousTokenID,
			reference.StagedTokenID,
			serviceID,
			oldToken.ID,
		) {
			return StagedServiceNodeConfiguration{}, ErrSystemUpdateRuntimeTokenRotationSharedToken
		}
	}
	if !mariaDBServiceTokenReferenceTypesMatch(discovered, lockedServices, lockedTokens) {
		return StagedServiceNodeConfiguration{}, ErrForbidden
	}
	token, scopesJSON, err := newRotatedServiceToken(oldToken, now)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	ciphertext, nonce, err := seal(token.RawToken)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	activationRandom, err := security.RandomToken(32)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	activationToken := "ast_act_" + activationRandom
	result, err := tx.ExecContext(ctx, `UPDATE services SET staged_node_previous_token_id = ?, staged_node_token_id = ?, staged_node_token_hash = ?, staged_node_token_scopes = ?, staged_node_token_ciphertext = ?, staged_node_token_nonce = ?, staged_node_activation_token_hash = ?, staged_node_token_at = ?, configure_token_used_at = ?, updated_at = ? WHERE service_id = ? AND token_id = ?`, oldToken.ID, token.ID, token.TokenHash, scopesJSON, ciphertext, nonce, security.HashToken(activationToken), now, now, now, serviceID, oldToken.ID)
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	if affected != 1 {
		return StagedServiceNodeConfiguration{}, ErrNotFound
	}
	service.StagedNodePreviousTokenID = oldToken.ID
	service.StagedNodeTokenID = token.ID
	service.StagedNodeTokenHash = token.TokenHash
	service.StagedNodeTokenScopes = append([]string(nil), token.Scopes...)
	service.StagedNodeTokenCiphertext = ciphertext
	service.StagedNodeTokenNonce = nonce
	service.StagedNodeActivationTokenHash = security.HashToken(activationToken)
	service.StagedNodeTokenAt = &now
	service.ConfigureTokenUsedAt = &now
	service.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return StagedServiceNodeConfiguration{}, err
	}
	return StagedServiceNodeConfiguration{Token: token, Service: service, ActivationToken: activationToken, ActivationExpiresAt: *service.ConfigureTokenExpiresAt}, nil
}

func (s MariaDBAuthStore) ActivateServiceNodeConfiguration(ctx context.Context, serviceID, configurationID, rawActivationToken string, now time.Time, report ServiceRuntimeReport) (ServiceToken, RegisteredService, bool, error) {
	serviceID = strings.TrimSpace(serviceID)
	configurationID = strings.TrimSpace(configurationID)
	rawActivationToken = strings.TrimSpace(rawActivationToken)
	if serviceID == "" || configurationID == "" || rawActivationToken == "" {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	now = now.UTC()
	report.ServiceID = serviceID
	report = normalizeServiceRuntimeReport(report)
	for attempt := 0; attempt < mariaDBServiceTokenReferenceRetryLimit; attempt++ {
		discoveredService, err := s.getService(ctx, serviceID)
		if errors.Is(err, ErrNotFound) {
			return ServiceToken{}, RegisteredService{}, false, ErrNotFound
		}
		if err != nil {
			return ServiceToken{}, RegisteredService{}, false, err
		}
		tokenIDs := []string{discoveredService.TokenID, configurationID}
		discovered, err := discoverMariaDBServiceTokenReferences(ctx, s.db, tokenIDs)
		if err != nil {
			return ServiceToken{}, RegisteredService{}, false, err
		}
		observeMariaDBServiceTokenLockPhase(ctx, "activate_service_node_configuration", mariaDBServiceTokenReferenceDiscoveryComplete)
		token, service, alreadyActivated, err := s.activateServiceNodeConfigurationWithReferences(
			ctx,
			serviceID,
			configurationID,
			rawActivationToken,
			now,
			report,
			discoveredService,
			tokenIDs,
			discovered,
		)
		if !errors.Is(err, errMariaDBServiceTokenReferenceSetChanged) {
			return token, service, alreadyActivated, err
		}
		observeMariaDBServiceTokenLockPhase(ctx, "activate_service_node_configuration", mariaDBServiceTokenReferenceSetMismatch)
		if attempt+1 < mariaDBServiceTokenReferenceRetryLimit {
			observeMariaDBServiceTokenLockPhase(ctx, "activate_service_node_configuration", mariaDBServiceTokenReferenceRetryStart)
		}
	}
	return ServiceToken{}, RegisteredService{}, false, ErrConflict
}

func (s MariaDBAuthStore) activateServiceNodeConfigurationWithReferences(
	ctx context.Context,
	serviceID, configurationID, rawActivationToken string,
	now time.Time,
	report ServiceRuntimeReport,
	discoveredService RegisteredService,
	tokenIDs []string,
	discovered []mariaDBServiceTokenReference,
) (ServiceToken, RegisteredService, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	defer tx.Rollback()
	lockedServices, lockedTokens, err := lockMariaDBServiceTokenMutationRetryable(
		ctx, tx, "activate_service_node_configuration", discovered, tokenIDs,
		serviceID,
	)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	observeMariaDBServiceTokenLockPhase(ctx, "activate_service_node_configuration", mariaDBServiceTokenStableAuthReplayConflict)
	service, ok := lockedServices[serviceID]
	if !ok || service.TokenID != discoveredService.TokenID {
		return ServiceToken{}, RegisteredService{}, false, ErrNotFound
	}
	if service.ServiceType != "update_agent" {
		return ServiceToken{}, RegisteredService{}, false, ErrForbidden
	}
	if service.TokenID == configurationID && service.StagedNodeTokenID == "" {
		var activationTokenHash string
		if err := tx.QueryRowContext(
			ctx,
			`SELECT COALESCE(configure_token_hash, '') FROM services WHERE service_id = ?`,
			serviceID,
		).Scan(&activationTokenHash); err != nil {
			return ServiceToken{}, RegisteredService{}, false, err
		}
		if service.ConfigureTokenUsedAt == nil ||
			service.ConfigureTokenExpiresAt != nil ||
			activationTokenHash == "" ||
			!security.VerifyTokenHash(rawActivationToken, activationTokenHash) {
			return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
		}
		token, ok := lockedTokens[service.TokenID]
		if !ok || token.RevokedAt != nil || token.ServiceType != service.ServiceType ||
			!mariaDBServiceTokenReferenceTypesMatch(discovered, lockedServices, lockedTokens) {
			return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
		}
		if err := validateRequiredUpdateAgentScopes(token.ServiceType, token.Scopes); err != nil {
			return ServiceToken{}, RegisteredService{}, false, err
		}
		return token, service, true, nil
	}
	if service.StagedNodeTokenID != configurationID || service.StagedNodeTokenHash == "" || service.StagedNodeActivationTokenHash == "" || !security.VerifyTokenHash(rawActivationToken, service.StagedNodeActivationTokenHash) {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	if service.TokenID == service.StagedNodeTokenID {
		token, ok := lockedTokens[service.TokenID]
		if !ok || token.RevokedAt != nil {
			return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
		}
		if !mariaDBServiceTokenReferenceTypesMatch(discovered, lockedServices, lockedTokens) {
			return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
		}
		if err := validateRequiredUpdateAgentScopes(token.ServiceType, token.Scopes); err != nil {
			return ServiceToken{}, RegisteredService{}, false, err
		}
		token.TokenHash = service.StagedNodeTokenHash
		return token, service, true, nil
	}
	if service.ConfigureTokenExpiresAt == nil || !now.Before(*service.ConfigureTokenExpiresAt) || service.StagedNodeTokenAt == nil || service.TokenID != service.StagedNodePreviousTokenID {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	oldToken, ok := lockedTokens[service.TokenID]
	if !ok ||
		oldToken.ServiceType != "update_agent" ||
		(oldToken.RevokedAt != nil &&
			!isEmergencyRevokedNodeConfigurationAnchor(service, oldToken)) {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	for _, reference := range discovered {
		if !updateAgentActivationTokenReferenceAllowed(
			reference.ServiceID,
			reference.TokenID,
			reference.StagedPreviousTokenID,
			reference.StagedTokenID,
			serviceID,
			oldToken.ID,
			service.StagedNodeTokenID,
		) {
			return ServiceToken{}, RegisteredService{}, false, ErrSystemUpdateRuntimeTokenRotationSharedToken
		}
	}
	if !mariaDBServiceTokenReferenceTypesMatch(discovered, lockedServices, lockedTokens) {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	if err := validateRequiredUpdateAgentScopes(oldToken.ServiceType, oldToken.Scopes); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	if err := validateRequiredUpdateAgentScopes(service.ServiceType, service.StagedNodeTokenScopes); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	token := ServiceToken{ID: service.StagedNodeTokenID, ServiceType: "update_agent", Scopes: append([]string(nil), service.StagedNodeTokenScopes...), TokenHash: service.StagedNodeTokenHash, CreatedAt: service.StagedNodeTokenAt.UTC()}
	if _, exists := lockedTokens[token.ID]; exists {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	if err := validateServiceScopes(token.Scopes); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	scopesJSON, err := json.Marshal(token.Scopes)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO service_tokens (id, service_type, token_hash, scopes, created_at) VALUES (?, ?, ?, ?, ?)`, token.ID, token.ServiceType, token.TokenHash, string(scopesJSON), token.CreatedAt); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE services SET status = CASE WHEN status = 'pending' THEN 'registered' ELSE status END, version = CASE WHEN ? = '' THEN version ELSE ? END, reported_version = CASE WHEN ? = '' THEN reported_version ELSE ? END, reported_commit = CASE WHEN ? = '' THEN reported_commit ELSE ? END, reported_build_date = CASE WHEN ? = '' THEN reported_build_date ELSE ? END, reported_hostname = CASE WHEN ? = '' THEN reported_hostname ELSE ? END, reported_os = CASE WHEN ? = '' THEN reported_os ELSE ? END, reported_arch = CASE WHEN ? = '' THEN reported_arch ELSE ? END, last_reported_at = CASE WHEN ? = '' AND ? = '' AND ? = '' AND ? = '' AND ? = '' AND ? = '' THEN last_reported_at ELSE ? END, token_id = ?, node_token_ciphertext = ?, node_token_nonce = ?, configure_token_hash = ?, configure_token_expires_at = NULL, staged_node_previous_token_id = NULL, staged_node_token_id = NULL, staged_node_token_hash = NULL, staged_node_token_scopes = NULL, staged_node_token_ciphertext = NULL, staged_node_token_nonce = NULL, staged_node_activation_token_hash = NULL, staged_node_token_at = NULL, last_heartbeat_at = NULL, reported_capabilities = '{}', node_token_rotated_at = ?, updated_at = ? WHERE service_id = ? AND token_id = ? AND staged_node_token_id = ?`, report.Version, report.Version, report.Version, report.Version, report.Commit, report.Commit, report.BuildDate, report.BuildDate, report.Hostname, report.Hostname, report.OS, report.OS, report.Arch, report.Arch, report.Version, report.Commit, report.BuildDate, report.Hostname, report.OS, report.Arch, now, token.ID, service.StagedNodeTokenCiphertext, service.StagedNodeTokenNonce, service.StagedNodeActivationTokenHash, now, now, serviceID, oldToken.ID, token.ID)
	if err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	if affected != 1 {
		return ServiceToken{}, RegisteredService{}, false, ErrUnauthorized
	}
	if oldToken.RevokedAt == nil {
		if err := revokeServiceTokenIfUnreferencedInTx(ctx, tx, oldToken.ID, now); err != nil {
			return ServiceToken{}, RegisteredService{}, false, err
		}
	}
	service = applyServiceRuntimeReport(service, report, now)
	service.TokenID = token.ID
	service.NodeTokenCiphertext = service.StagedNodeTokenCiphertext
	service.NodeTokenNonce = service.StagedNodeTokenNonce
	service.ConfigureTokenHash = service.StagedNodeActivationTokenHash
	service.ConfigureTokenExpiresAt = nil
	clearStagedNodeConfiguration(&service)
	service.LastHeartbeatAt = nil
	service.ReportedCapabilities = map[string]any{}
	service.NodeTokenRotatedAt = &now
	service.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return ServiceToken{}, RegisteredService{}, false, err
	}
	return token, service, false, nil
}

func (s MariaDBAuthStore) SetServiceNodeTokenSecret(ctx context.Context, serviceID, ciphertext, nonce string) (RegisteredService, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `UPDATE services SET node_token_ciphertext = ?, node_token_nonce = ?, updated_at = ? WHERE service_id = ?`, ciphertext, nonce, now, serviceID)
	if err != nil {
		return RegisteredService{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RegisteredService{}, err
	}
	if affected == 0 {
		return RegisteredService{}, ErrNotFound
	}
	return s.getService(ctx, serviceID)
}
