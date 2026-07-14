package store

import (
	"strings"
	"testing"
)

func TestOAuthAccountDisplayNameDoesNotExposeEmailAsPrimaryLabel(t *testing.T) {
	integrations := NewMemoryIntegrationStore()
	account, err := integrations.CreateOAuthAccount(t.Context(), OAuthAccount{
		ProviderID:   "google-main",
		ProviderType: "google",
		AccountLabel: "archive@example.com",
		Subject:      "google-subject-01",
		Email:        "archive@example.com",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.DisplayName == "archive@example.com" || !strings.HasPrefix(account.DisplayName, "Googleアカウント (") {
		t.Fatalf("display name should be a distinct provider fallback instead of email: %#v", account)
	}
}

func TestOAuthAccountDisplayNameUsesConfiguredAccountLabel(t *testing.T) {
	integrations := NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), OAuthProvider{
		ProviderType: "google",
		Name:         "配信用Google OAuth",
		Enabled:      true,
		ClientID:     "client-id",
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := integrations.CreateOAuthAccount(t.Context(), OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: provider.ProviderType,
		AccountLabel: "第1スタジオ YouTube",
		Subject:      "google-subject-01",
		Email:        "studio-1@example.com",
		Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
		RefreshToken: "raw-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.DisplayName != "第1スタジオ YouTube" || account.ProviderName != provider.Name {
		t.Fatalf("configured account label should be the public display name: %#v", account)
	}
}

func TestOAuthAccountDisplayNameUsesProviderNameForLegacyEmailLabel(t *testing.T) {
	integrations := NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), OAuthProvider{
		ProviderType: "google",
		Name:         "録画保管用Google",
		Enabled:      true,
		ClientID:     "client-id",
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := integrations.CreateOAuthAccount(t.Context(), OAuthAccount{
		ProviderID:   provider.ID,
		ProviderType: provider.ProviderType,
		AccountLabel: "archive@example.com",
		Subject:      "google-subject-01",
		Email:        "archive@example.com",
		Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
		RefreshToken: "raw-refresh-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.DisplayName, provider.Name+" (") || created.ProviderName != provider.Name {
		t.Fatalf("legacy email label should fall back to a distinct configured provider name: %#v", created)
	}
	accounts, err := integrations.ListOAuthAccounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].DisplayName != created.DisplayName || accounts[0].AccountLabel != "archive@example.com" {
		t.Fatalf("listed legacy account should preserve identity but expose provider display name: %#v", accounts)
	}
}

func TestOAuthAccountDisplayNameKeepsLegacyAccountsDistinct(t *testing.T) {
	integrations := NewMemoryIntegrationStore()
	provider, err := integrations.CreateOAuthProvider(t.Context(), OAuthProvider{
		ProviderType: "google",
		Name:         "配信用Google OAuth",
		Enabled:      true,
		ClientID:     "client-id",
		RedirectURI:  "https://control.example.com/auth/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"studio-1@example.com", "studio-2@example.com"} {
		if _, err := integrations.CreateOAuthAccount(t.Context(), OAuthAccount{
			ProviderID:   provider.ID,
			ProviderType: provider.ProviderType,
			AccountLabel: email,
			Subject:      email,
			Email:        email,
			Scopes:       []string{"https://www.googleapis.com/auth/youtube"},
			RefreshToken: "raw-refresh-token",
		}); err != nil {
			t.Fatal(err)
		}
	}
	accounts, err := integrations.ListOAuthAccounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 || accounts[0].DisplayName == accounts[1].DisplayName {
		t.Fatalf("legacy account fallbacks must remain distinguishable: %#v", accounts)
	}
	for _, account := range accounts {
		if !strings.HasPrefix(account.DisplayName, provider.Name+" (") || strings.Contains(account.DisplayName, account.Email) {
			t.Fatalf("legacy account fallback should be provider based and must not expose email: %#v", account)
		}
	}
}

func TestOAuthAccountPurposeIsDerivedFromGrantedScopes(t *testing.T) {
	tests := []struct {
		name          string
		scopes        []string
		purpose       string
		allowsDrive   bool
		allowsYouTube bool
	}{
		{name: "drive", scopes: []string{"https://www.googleapis.com/auth/drive.file"}, purpose: OAuthAccountPurposeDrive, allowsDrive: true},
		{name: "youtube", scopes: []string{"https://www.googleapis.com/auth/youtube.force-ssl"}, purpose: OAuthAccountPurposeYouTube, allowsYouTube: true},
		{name: "both", scopes: []string{"https://www.googleapis.com/auth/drive", "https://www.googleapis.com/auth/youtube"}, purpose: OAuthAccountPurposeDriveYouTube, allowsDrive: true, allowsYouTube: true},
		{name: "login only", scopes: []string{"openid", "email", "profile"}, purpose: OAuthAccountPurposeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := OAuthAccount{Scopes: tt.scopes}
			if got := OAuthAccountPurposeFromScopes(account.Scopes); got != tt.purpose {
				t.Fatalf("purpose = %q, want %q", got, tt.purpose)
			}
			if got := OAuthAccountAllowsPurpose(account, OAuthAccountPurposeDrive); got != tt.allowsDrive {
				t.Fatalf("drive eligibility = %v, want %v", got, tt.allowsDrive)
			}
			if got := OAuthAccountAllowsPurpose(account, OAuthAccountPurposeYouTube); got != tt.allowsYouTube {
				t.Fatalf("youtube eligibility = %v, want %v", got, tt.allowsYouTube)
			}
		})
	}
}
