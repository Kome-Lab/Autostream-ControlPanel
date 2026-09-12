package httpapi

import (
	"context"
	"errors"
	"strings"

	"github.com/example/autostream-control-panel/internal/servicecall"
	"github.com/example/autostream-control-panel/internal/store"
)

func (s *Server) applyArchiveConfig(ctx context.Context, req *servicecall.StartRequest) error {
	profileID := strings.TrimSpace(req.ArchiveProfileID)
	if profileID == "" {
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileArchive, profileID)
	if errors.Is(err, store.ErrNotFound) {
		return errArchiveProfileNotFound
	}
	if err != nil {
		return err
	}
	archiveConfig := map[string]any{
		"archive_profile_id": profile.ID,
	}
	if retentionDays := configInt(profile.Config, "retention_days"); retentionDays > 0 {
		archiveConfig["retention_days"] = normalizeArchiveRetentionDays(retentionDays)
	}
	destinationID := strings.TrimSpace(configString(profile.Config, "drive_destination_id"))
	if destinationID == "" {
		if len(archiveConfig) > 1 {
			req.ArchiveConfig = archiveConfig
		}
		return nil
	}
	destination, err := s.integrations.GetDriveDestinationForDispatch(ctx, destinationID)
	if errors.Is(err, store.ErrNotFound) {
		return errDriveDestinationNotFound
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(destination.FolderID) == "" {
		return errDriveDestinationUnavailable
	}
	if !strings.EqualFold(strings.TrimSpace(destination.AuthMode), "oauth2") {
		return errArchiveProfileInvalidConfig
	}
	req.ArchiveConfig = archiveConfig
	req.ArchiveConfig["drive_destination_id"] = destination.ID
	req.ArchiveConfig["auth_mode"] = "oauth2"
	req.ArchiveConfig["oauth_account_id"] = destination.OAuthAccountID
	req.ArchiveConfig["folder_id_secret_name"] = driveDestinationFolderIDSecretName(destination.ID)
	req.ArchiveConfig["shared_drive"] = destination.SharedDrive
	account, err := s.integrations.GetOAuthAccountForDispatch(ctx, destination.OAuthAccountID)
	if errors.Is(err, store.ErrNotFound) || strings.TrimSpace(account.RefreshToken) == "" {
		return errDriveOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive) {
		return errDriveOAuthAccountUnavailable
	}
	provider, err := s.integrations.GetOAuthProviderForDispatch(ctx, account.ProviderID)
	if errors.Is(err, store.ErrNotFound) || strings.TrimSpace(provider.ClientSecret) == "" {
		return errDriveOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	req.ArchiveConfig["oauth_provider_id"] = provider.ID
	req.ArchiveConfig["client_id"] = provider.ClientID
	req.ArchiveConfig["client_secret_secret_name"] = oauthProviderClientSecretSecretName(provider.ID)
	req.ArchiveConfig["refresh_token_secret_name"] = oauthAccountRefreshTokenSecretName(account.ID)
	if fileName := archiveSafeFileName(configString(profile.Config, "archive_file_name")); fileName != "" {
		req.ArchiveConfig["archive_file_name"] = fileName
	}
	if sharedDriveID := strings.TrimSpace(configString(profile.Config, "shared_drive_id")); sharedDriveID != "" {
		req.ArchiveConfig["shared_drive_id"] = sharedDriveID
	}
	return nil
}

func (s *Server) archiveConfigReadinessIssues(ctx context.Context, req *servicecall.StartRequest) []servicecall.ReadinessIssue {
	if err := s.validateArchiveConfigReadiness(ctx, req); err != nil {
		return []servicecall.ReadinessIssue{{
			ServiceType: "encoder_recorder",
			Code:        archiveConfigCode(err),
			Message:     archiveConfigReadinessMessage(err),
		}}
	}
	return nil
}

func (s *Server) validateArchiveConfigReadiness(ctx context.Context, req *servicecall.StartRequest) error {
	profileID := strings.TrimSpace(req.ArchiveProfileID)
	if profileID == "" {
		return nil
	}
	profile, err := s.profiles.GetProfile(ctx, store.ProfileArchive, profileID)
	if errors.Is(err, store.ErrNotFound) {
		return errArchiveProfileNotFound
	}
	if err != nil {
		return err
	}
	destinationID := strings.TrimSpace(configString(profile.Config, "drive_destination_id"))
	if destinationID == "" {
		return nil
	}
	destination, err := s.integrations.GetDriveDestination(ctx, destinationID)
	if errors.Is(err, store.ErrNotFound) {
		return errDriveDestinationNotFound
	}
	if err != nil {
		return err
	}
	if !destination.FolderIDConfigured {
		return errDriveDestinationUnavailable
	}
	if !strings.EqualFold(strings.TrimSpace(destination.AuthMode), "oauth2") {
		return errArchiveProfileInvalidConfig
	}
	return s.validateDriveOAuthReadiness(ctx, destination)
}

func (s *Server) validateDriveOAuthReadiness(ctx context.Context, destination store.DriveDestination) error {
	if strings.TrimSpace(destination.OAuthAccountID) == "" {
		return errDriveOAuthAccountUnavailable
	}
	account, err := s.integrations.GetOAuthAccount(ctx, destination.OAuthAccountID)
	if errors.Is(err, store.ErrNotFound) || !account.RefreshTokenConfigured {
		return errDriveOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	provider, err := s.integrations.GetOAuthProvider(ctx, account.ProviderID)
	if errors.Is(err, store.ErrNotFound) || !provider.Enabled || strings.TrimSpace(provider.ClientID) == "" || !provider.ClientSecretConfigured {
		return errDriveOAuthAccountUnavailable
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(provider.ProviderType, "google") || !strings.EqualFold(account.ProviderType, "google") {
		return errDriveOAuthAccountUnavailable
	}
	if !store.OAuthAccountAllowsPurpose(account, store.OAuthAccountPurposeDrive) {
		return errDriveOAuthAccountUnavailable
	}
	return nil
}

func archiveConfigReadinessMessage(err error) string {
	switch {
	case errors.Is(err, errArchiveProfileNotFound):
		return "selected archive profile was not found."
	case errors.Is(err, errArchiveProfileInvalidConfig):
		return "selected archive profile or Drive destination has invalid settings."
	case errors.Is(err, errDriveDestinationNotFound):
		return "selected Drive destination was not found."
	case errors.Is(err, errDriveDestinationUnavailable):
		return "selected Drive destination folder ID is not configured."
	case errors.Is(err, errDriveOAuthAccountUnavailable):
		return "selected Drive OAuth connected account is not ready."
	default:
		return "selected archive configuration could not be validated."
	}
}

func (s *Server) retryArchiveConfig(ctx context.Context, stream store.Stream) (map[string]any, error) {
	archiveProfileID := strings.TrimSpace(stream.ArchiveProfileID)
	if archiveProfileID == "" {
		return nil, nil
	}
	req := servicecall.StartRequest{ArchiveProfileID: archiveProfileID}
	if err := s.applyArchiveConfig(ctx, &req); err != nil {
		return nil, err
	}
	return req.ArchiveConfig, nil
}

func driveDestinationFolderIDSecretName(id string) string {
	return "drive_destination:" + strings.TrimSpace(id) + ":folder_id"
}

func oauthProviderClientSecretSecretName(id string) string {
	return "oauth_provider:" + strings.TrimSpace(id) + ":client_secret"
}

func oauthAccountRefreshTokenSecretName(id string) string {
	return "oauth_account:" + strings.TrimSpace(id) + ":refresh_token"
}
