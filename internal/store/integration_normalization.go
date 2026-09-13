package store

import (
	"errors"
	"strings"
)

func normalizeOAuthProvider(provider OAuthProvider, creating bool) (OAuthProvider, error) {
	provider.ProviderType = strings.TrimSpace(strings.ToLower(provider.ProviderType))
	provider.Name = strings.TrimSpace(provider.Name)
	provider.ClientID = strings.TrimSpace(provider.ClientID)
	provider.RedirectURI = strings.TrimSpace(provider.RedirectURI)
	provider.ClientSecret = strings.TrimSpace(provider.ClientSecret)
	provider.Scopes = cleanStringSlice(provider.Scopes)
	provider.AllowedDomains = cleanStringSlice(provider.AllowedDomains)
	provider.DefaultRoleIDs = cleanStringSlice(provider.DefaultRoleIDs)
	if !creating && strings.TrimSpace(provider.ID) == "" {
		return OAuthProvider{}, errors.New("oauth provider id is required")
	}
	if provider.ProviderType != "google" && provider.ProviderType != "github" && provider.ProviderType != "discord" {
		return OAuthProvider{}, errors.New("invalid oauth provider type")
	}
	if provider.Name == "" || provider.ClientID == "" || provider.RedirectURI == "" {
		return OAuthProvider{}, errors.New("oauth provider name, client_id, and redirect_uri are required")
	}
	return provider, nil
}

func normalizeOAuthAccount(account OAuthAccount, creating bool) (OAuthAccount, error) {
	account.ProviderID = strings.TrimSpace(account.ProviderID)
	account.ProviderType = strings.TrimSpace(strings.ToLower(account.ProviderType))
	account.AccountLabel = strings.TrimSpace(account.AccountLabel)
	account.Subject = strings.TrimSpace(account.Subject)
	account.Email = strings.TrimSpace(account.Email)
	account.RefreshToken = strings.TrimSpace(account.RefreshToken)
	account.Scopes = cleanStringSlice(account.Scopes)
	account.AccountPurpose = OAuthAccountPurposeFromScopes(account.Scopes)
	if !creating && strings.TrimSpace(account.ID) == "" {
		return OAuthAccount{}, errors.New("oauth account id is required")
	}
	if account.ProviderID == "" || account.AccountLabel == "" {
		return OAuthAccount{}, errors.New("oauth account provider_id and account_label are required")
	}
	if account.ProviderType != "google" && account.ProviderType != "github" && account.ProviderType != "discord" {
		return OAuthAccount{}, errors.New("invalid oauth account provider type")
	}
	return account, nil
}

func normalizeDriveDestination(destination DriveDestination, creating bool) (DriveDestination, error) {
	destination.ID = strings.TrimSpace(destination.ID)
	destination.Name = strings.TrimSpace(destination.Name)
	destination.AuthMode = strings.TrimSpace(strings.ToLower(destination.AuthMode))
	destination.OAuthAccountID = strings.TrimSpace(destination.OAuthAccountID)
	destination.FolderID = strings.TrimSpace(destination.FolderID)
	if !creating && destination.ID == "" {
		return DriveDestination{}, errors.New("drive destination id is required")
	}
	if destination.Name == "" {
		return DriveDestination{}, errors.New("drive destination name is required")
	}
	if destination.AuthMode == "" {
		destination.AuthMode = "oauth2"
	}
	if destination.AuthMode != "oauth2" {
		return DriveDestination{}, errors.New("invalid drive destination auth_mode")
	}
	if destination.OAuthAccountID == "" {
		return DriveDestination{}, errors.New("oauth2 drive destination requires oauth_account_id")
	}
	if creating && destination.FolderID == "" {
		return DriveDestination{}, errors.New("drive destination folder_id is required")
	}
	return destination, nil
}

func publicOAuthProvider(provider OAuthProvider) OAuthProvider {
	provider.ClientSecret = ""
	return provider
}

func publicOAuthAccount(account OAuthAccount) OAuthAccount {
	account.RefreshToken = ""
	account.AccountPurpose = OAuthAccountPurposeFromScopes(account.Scopes)
	account.DisplayName = oauthAccountDisplayName(account)
	return account
}
