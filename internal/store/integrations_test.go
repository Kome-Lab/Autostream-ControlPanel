package store

import "testing"

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
	if account.DisplayName == "archive@example.com" || account.DisplayName != "Google接続アカウント" {
		t.Fatalf("display name should be provider label instead of email: %#v", account)
	}
}
