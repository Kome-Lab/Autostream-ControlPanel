package store

import (
	"sort"
	"strings"
)

const (
	OAuthAccountPurposeDrive        = "drive"
	OAuthAccountPurposeYouTube      = "youtube"
	OAuthAccountPurposeDriveYouTube = "drive_youtube"
	OAuthAccountPurposeUnknown      = "unknown"
)

func OAuthAccountPurposeFromScopes(scopes []string) string {
	drive, youtube := false, false
	for _, scope := range scopes {
		switch strings.TrimSpace(scope) {
		case "https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/drive":
			drive = true
		case "https://www.googleapis.com/auth/youtube",
			"https://www.googleapis.com/auth/youtube.force-ssl",
			"https://www.googleapis.com/auth/youtube.upload":
			youtube = true
		}
	}
	switch {
	case drive && youtube:
		return OAuthAccountPurposeDriveYouTube
	case drive:
		return OAuthAccountPurposeDrive
	case youtube:
		return OAuthAccountPurposeYouTube
	default:
		return OAuthAccountPurposeUnknown
	}
}

func OAuthAccountAllowsPurpose(account OAuthAccount, purpose string) bool {
	actual := OAuthAccountPurposeFromScopes(account.Scopes)
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case OAuthAccountPurposeDrive:
		return actual == OAuthAccountPurposeDrive || actual == OAuthAccountPurposeDriveYouTube
	case OAuthAccountPurposeYouTube:
		return actual == OAuthAccountPurposeYouTube || actual == OAuthAccountPurposeDriveYouTube
	default:
		return false
	}
}

func oauthAccountDisplayName(account OAuthAccount) string {
	email := strings.TrimSpace(strings.ToLower(account.Email))
	if label := strings.TrimSpace(account.AccountLabel); label != "" {
		if strings.ToLower(label) != email && !generatedOAuthAccountLabel(label, account) {
			return label
		}
	}
	base := strings.TrimSpace(account.ProviderName)
	if base == "" || genericOAuthProviderName(base, account.ProviderType) {
		if providerLabel := oauthAccountProviderLabel(account.ProviderType); providerLabel != "" {
			base = providerLabel + "アカウント"
		} else {
			base = "OAuthアカウント"
		}
	}
	if reference := oauthAccountDisplayReference(account.ID); reference != "" {
		return base + " (" + reference + ")"
	}
	return base + " (表示名未設定)"
}

func oauthAccountDisplayReference(id string) string {
	id = strings.ReplaceAll(strings.TrimSpace(id), "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func generatedOAuthAccountLabel(label string, account OAuthAccount) bool {
	normalized := compactOAuthLabel(label)
	for _, base := range []string{oauthAccountProviderLabel(account.ProviderType), account.ProviderName} {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		if normalized == compactOAuthLabel(base) || normalized == compactOAuthLabel(base+" 接続アカウント") || normalized == compactOAuthLabel(base+" connected account") {
			return true
		}
	}
	return false
}

func genericOAuthProviderName(name, providerType string) bool {
	base := oauthAccountProviderLabel(providerType)
	if base == "" {
		return false
	}
	normalized := compactOAuthLabel(name)
	return normalized == compactOAuthLabel(base) || normalized == compactOAuthLabel(base+" 接続アカウント") || normalized == compactOAuthLabel(base+" connected account")
}

func compactOAuthLabel(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), ""))
}

func sortOAuthAccounts(accounts []OAuthAccount) {
	sort.SliceStable(accounts, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(accounts[i].DisplayName))
		right := strings.ToLower(strings.TrimSpace(accounts[j].DisplayName))
		if left == right {
			return accounts[i].ID < accounts[j].ID
		}
		return left < right
	})
}

func oauthAccountProviderLabel(providerType string) string {
	switch strings.TrimSpace(strings.ToLower(providerType)) {
	case "google":
		return "Google"
	case "github":
		return "GitHub"
	case "discord":
		return "Discord"
	default:
		return strings.TrimSpace(providerType)
	}
}
