package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/example/autostream-control-panel/internal/security"
	"time"
)

func (s MariaDBIntegrationStore) ListDriveDestinations(ctx context.Context) ([]DriveDestination, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, auth_mode, oauth_account_id, folder_id_fingerprint, masked_folder_id, shared_drive, created_at, updated_at FROM drive_destinations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DriveDestination
	for rows.Next() {
		destination, err := scanDriveDestination(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, destination)
	}
	return out, rows.Err()
}

func (s MariaDBIntegrationStore) CreateDriveDestination(ctx context.Context, destination DriveDestination) (DriveDestination, error) {
	destination, err := normalizeDriveDestination(destination, true)
	if err != nil {
		return DriveDestination{}, err
	}
	destination.ID = newUUID()
	now := time.Now().UTC()
	ciphertext, nonce, _, err := s.encryptRequired(destination.FolderID)
	if err != nil {
		return DriveDestination{}, err
	}
	fingerprint := security.SecretFingerprint(destination.FolderID)
	masked := maskIdentifier(destination.FolderID)
	_, err = s.db.ExecContext(ctx, `INSERT INTO drive_destinations (id, name, auth_mode, oauth_account_id, folder_id_ciphertext, folder_id_nonce, folder_id_fingerprint, masked_folder_id, shared_drive, created_at, updated_at) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)`, destination.ID, destination.Name, destination.AuthMode, destination.OAuthAccountID, ciphertext, nonce, fingerprint, masked, destination.SharedDrive, now, now)
	if err != nil {
		return DriveDestination{}, err
	}
	destination.FolderID = ""
	destination.FolderIDConfigured = true
	destination.FolderIDFingerprint = fingerprint
	destination.MaskedFolderID = masked
	destination.CreatedAt = now.Format(time.RFC3339)
	destination.UpdatedAt = now.Format(time.RFC3339)
	return destination, nil
}

func (s MariaDBIntegrationStore) GetDriveDestination(ctx context.Context, id string) (DriveDestination, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, auth_mode, oauth_account_id, folder_id_fingerprint, masked_folder_id, shared_drive, created_at, updated_at FROM drive_destinations WHERE id = ?`, id)
	destination, err := scanDriveDestination(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DriveDestination{}, ErrNotFound
	}
	return destination, err
}

func (s MariaDBIntegrationStore) GetDriveDestinationForDispatch(ctx context.Context, id string) (DriveDestination, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, auth_mode, oauth_account_id, folder_id_ciphertext, folder_id_nonce, folder_id_fingerprint, masked_folder_id, shared_drive, created_at, updated_at FROM drive_destinations WHERE id = ?`, id)
	var destination DriveDestination
	var oauthAccountID sql.NullString
	var ciphertext, nonce string
	var createdAt, updatedAt time.Time
	if err := row.Scan(&destination.ID, &destination.Name, &destination.AuthMode, &oauthAccountID, &ciphertext, &nonce, &destination.FolderIDFingerprint, &destination.MaskedFolderID, &destination.SharedDrive, &createdAt, &updatedAt); errors.Is(err, sql.ErrNoRows) {
		return DriveDestination{}, ErrNotFound
	} else if err != nil {
		return DriveDestination{}, err
	}
	if s.keyMaterial == "" {
		return DriveDestination{}, ErrSecretKeyRequired
	}
	folderID, err := security.DecryptSecret(ciphertext, nonce, s.keyMaterial)
	if err != nil {
		return DriveDestination{}, err
	}
	destination.OAuthAccountID = oauthAccountID.String
	destination.FolderID = folderID
	destination.FolderIDConfigured = folderID != ""
	destination.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	destination.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return destination, nil
}

func (s MariaDBIntegrationStore) UpdateDriveDestination(ctx context.Context, destination DriveDestination) (DriveDestination, error) {
	destination, err := normalizeDriveDestination(destination, false)
	if err != nil {
		return DriveDestination{}, err
	}
	now := time.Now().UTC()
	if destination.FolderID != "" {
		ciphertext, nonce, _, err := s.encryptRequired(destination.FolderID)
		if err != nil {
			return DriveDestination{}, err
		}
		result, err := s.db.ExecContext(ctx, `UPDATE drive_destinations SET name = ?, auth_mode = ?, oauth_account_id = NULLIF(?, ''), folder_id_ciphertext = ?, folder_id_nonce = ?, folder_id_fingerprint = ?, masked_folder_id = ?, shared_drive = ?, updated_at = ? WHERE id = ?`, destination.Name, destination.AuthMode, destination.OAuthAccountID, ciphertext, nonce, security.SecretFingerprint(destination.FolderID), maskIdentifier(destination.FolderID), destination.SharedDrive, now, destination.ID)
		if err != nil {
			return DriveDestination{}, err
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			return DriveDestination{}, ErrNotFound
		}
	} else {
		result, err := s.db.ExecContext(ctx, `UPDATE drive_destinations SET name = ?, auth_mode = ?, oauth_account_id = NULLIF(?, ''), shared_drive = ?, updated_at = ? WHERE id = ?`, destination.Name, destination.AuthMode, destination.OAuthAccountID, destination.SharedDrive, now, destination.ID)
		if err != nil {
			return DriveDestination{}, err
		}
		if affected, err := result.RowsAffected(); err != nil || affected == 0 {
			return DriveDestination{}, ErrNotFound
		}
	}
	return s.GetDriveDestination(ctx, destination.ID)
}

func (s MariaDBIntegrationStore) DeleteDriveDestination(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM drive_destinations WHERE id = ?`, id)
	return notFoundOnNoRows(result, err)
}
