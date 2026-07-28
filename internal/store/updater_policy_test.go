package store

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

const testUpdaterHostPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8g"

func TestMemoryUpdaterPolicyStoreNormalizesAndRevisions(t *testing.T) {
	t.Parallel()

	policies := NewMemoryUpdaterPolicyStore()
	if _, err := policies.GetUpdaterPolicy(t.Context(), "updater-01"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing policy error = %v, want ErrNotFound", err)
	}

	created, err := policies.SaveUpdaterPolicy(t.Context(), " updater-01 ", 0, UpdaterPolicy{
		API: UpdaterPolicyAPI{},
		Hosts: []UpdaterPolicyHost{{
			HostID:        " host-a ",
			Name:          " Studio A ",
			Address:       " host-a.example.com ",
			Port:          55850,
			User:          " autostream-update-host ",
			Arch:          " amd64 ",
			HostPublicKey: " " + testUpdaterHostPublicKey + " ",
		}},
		Targets: []UpdaterPolicyTarget{{
			TargetID:       " worker-a ",
			HostID:         " host-a ",
			ServiceType:    " worker ",
			DeploymentMode: " systemd ",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.UpdaterID != "updater-01" || created.Revision != 1 || created.UpdatedAt.IsZero() {
		t.Fatalf("created metadata = %#v", created)
	}
	if created.API != (UpdaterPolicyAPI{BindHost: "127.0.0.1", Host: "127.0.0.1", Port: 8090}) {
		t.Fatalf("default API = %#v", created.API)
	}
	if created.PollIntervalSeconds != 15 || created.HeartbeatIntervalSeconds != 30 {
		t.Fatalf("default intervals = poll %d heartbeat %d", created.PollIntervalSeconds, created.HeartbeatIntervalSeconds)
	}
	if created.Hosts[0].HostID != "host-a" || created.Hosts[0].Name != "Studio A" || created.Targets[0].TargetID != "worker-a" {
		t.Fatalf("policy was not normalized: %#v", created)
	}
	if created.Hosts[0].HostPublicKey != testUpdaterHostPublicKey {
		t.Fatalf("host public key was not canonicalized: %q", created.Hosts[0].HostPublicKey)
	}

	// Returned values must not let callers mutate the stored policy.
	created.Hosts[0].Name = "mutated"
	got, err := policies.GetUpdaterPolicy(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hosts[0].Name != "Studio A" {
		t.Fatalf("stored policy was mutated through a returned slice: %#v", got)
	}

	got.PollIntervalSeconds = 20
	updated, err := policies.SaveUpdaterPolicy(t.Context(), "updater-01", got.Revision, got)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.PollIntervalSeconds != 20 || !updated.UpdatedAt.After(got.UpdatedAt) {
		t.Fatalf("updated policy = %#v", updated)
	}
	if _, err := policies.SaveUpdaterPolicy(t.Context(), "updater-01", 1, got); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save error = %v, want ErrConflict", err)
	}
	got.UpdaterID = "missing-updater"
	if _, err := policies.SaveUpdaterPolicy(t.Context(), "missing-updater", 2, got); !errors.Is(err, ErrConflict) {
		t.Fatalf("nonexistent revision save error = %v, want ErrConflict", err)
	}
}

func TestMemoryUpdaterPolicyStoreListsByUpdaterID(t *testing.T) {
	t.Parallel()

	policies := NewMemoryUpdaterPolicyStore()
	for _, updaterID := range []string{"updater-z", "updater-a"} {
		input := validUpdaterPolicy()
		input.UpdaterID = updaterID
		if _, err := policies.SaveUpdaterPolicy(t.Context(), updaterID, 0, input); err != nil {
			t.Fatal(err)
		}
	}
	got, err := policies.ListUpdaterPolicies(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if ids := []string{got[0].UpdaterID, got[1].UpdaterID}; !reflect.DeepEqual(ids, []string{"updater-a", "updater-z"}) {
		t.Fatalf("policy order = %#v", ids)
	}
}

func TestNormalizeUpdaterPolicyCanonicalizesTargetServiceID(t *testing.T) {
	t.Parallel()

	legacy := validUpdaterPolicy()
	normalizedLegacy, err := normalizeUpdaterPolicy("updater-01", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if normalizedLegacy.Targets[0].ServiceID != normalizedLegacy.Targets[0].TargetID {
		t.Fatalf("legacy target service_id = %q, want target_id %q", normalizedLegacy.Targets[0].ServiceID, normalizedLegacy.Targets[0].TargetID)
	}

	explicit := validUpdaterPolicy()
	explicit.Targets[0].ServiceID = " worker-service-a "
	normalizedExplicit, err := normalizeUpdaterPolicy("updater-01", explicit)
	if err != nil {
		t.Fatal(err)
	}
	if normalizedExplicit.Targets[0].ServiceID != "worker-service-a" {
		t.Fatalf("explicit target service_id = %q, want worker-service-a", normalizedExplicit.Targets[0].ServiceID)
	}
}

func TestDecodeUpdaterPolicyDefaultsMissingTargetServiceID(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(validUpdaterPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(body, &legacy); err != nil {
		t.Fatal(err)
	}
	targets, ok := legacy["targets"].([]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("legacy targets = %#v", legacy["targets"])
	}
	target, ok := targets[0].(map[string]any)
	if !ok {
		t.Fatalf("legacy target = %#v", targets[0])
	}
	delete(target, "service_id")
	body, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := decodeUpdaterPolicy("updater-01", 1, body, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Targets[0].ServiceID != decoded.Targets[0].TargetID {
		t.Fatalf("decoded legacy target service_id = %q, want target_id %q", decoded.Targets[0].ServiceID, decoded.Targets[0].TargetID)
	}
}

func TestMemoryUpdaterPolicyStoreAllowsOnlyOneSavePerRevision(t *testing.T) {
	t.Parallel()

	policies := NewMemoryUpdaterPolicyStore()
	created, err := policies.SaveUpdaterPolicy(t.Context(), "updater-01", 0, validUpdaterPolicy())
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, interval := range []int{20, 25} {
		interval := interval
		go func() {
			<-start
			input := validUpdaterPolicy()
			input.PollIntervalSeconds = interval
			_, err := policies.SaveUpdaterPolicy(t.Context(), "updater-01", created.Revision, input)
			results <- err
		}()
	}
	close(start)
	saved, conflicted := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			saved++
		case errors.Is(err, ErrConflict):
			conflicted++
		default:
			t.Fatalf("parallel save error = %v", err)
		}
	}
	if saved != 1 || conflicted != 1 {
		t.Fatalf("parallel saves = saved %d conflicted %d, want 1 each", saved, conflicted)
	}
	got, err := policies.GetUpdaterPolicy(t.Context(), "updater-01")
	if err != nil || got.Revision != 2 {
		t.Fatalf("policy after parallel saves = %#v, %v", got, err)
	}
}

func TestNormalizeUpdaterPolicyRejectsUnsafeOrIncompleteInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*UpdaterPolicy)
	}{
		{name: "missing hosts", edit: func(p *UpdaterPolicy) { p.Hosts = nil }},
		{name: "missing targets", edit: func(p *UpdaterPolicy) { p.Targets = nil }},
		{name: "invalid updater id", edit: func(p *UpdaterPolicy) { p.UpdaterID = "../updater" }},
		{name: "mismatched updater id", edit: func(p *UpdaterPolicy) { p.UpdaterID = "other-updater" }},
		{name: "duplicate host", edit: func(p *UpdaterPolicy) { p.Hosts = append(p.Hosts, p.Hosts[0]) }},
		{name: "duplicate target", edit: func(p *UpdaterPolicy) { p.Targets = append(p.Targets, p.Targets[0]) }},
		{name: "host ID with colon", edit: func(p *UpdaterPolicy) {
			p.Hosts[0].HostID = "host:a"
			p.Targets[0].HostID = "host:a"
		}},
		{name: "host ID with slash", edit: func(p *UpdaterPolicy) {
			p.Hosts[0].HostID = "host/a"
			p.Targets[0].HostID = "host/a"
		}},
		{name: "URL address", edit: func(p *UpdaterPolicy) { p.Hosts[0].Address = "ssh://host-a.example.com" }},
		{name: "address control character", edit: func(p *UpdaterPolicy) { p.Hosts[0].Address = "host-a.example.com\nother" }},
		{name: "bad port", edit: func(p *UpdaterPolicy) { p.Hosts[0].Port = 0 }},
		{name: "root user", edit: func(p *UpdaterPolicy) { p.Hosts[0].User = "root" }},
		{name: "bad Linux user", edit: func(p *UpdaterPolicy) { p.Hosts[0].User = "Update Host" }},
		{name: "unsupported arch", edit: func(p *UpdaterPolicy) { p.Hosts[0].Arch = "x86_64" }},
		{name: "missing host key", edit: func(p *UpdaterPolicy) { p.Hosts[0].HostPublicKey = "" }},
		{name: "multiline host key", edit: func(p *UpdaterPolicy) { p.Hosts[0].HostPublicKey += "\nssh-rsa AAAA" }},
		{name: "unstructured host key", edit: func(p *UpdaterPolicy) { p.Hosts[0].HostPublicKey = "not-a-key" }},
		{name: "RSA host key", edit: func(p *UpdaterPolicy) { p.Hosts[0].HostPublicKey = testRSAUpdaterHostPublicKey(t) }},
		{name: "host key comment", edit: func(p *UpdaterPolicy) { p.Hosts[0].HostPublicKey = testUpdaterHostPublicKey + " host-a" }},
		{name: "host key options", edit: func(p *UpdaterPolicy) {
			p.Hosts[0].HostPublicKey = `from="10.0.0.1" ` + testUpdaterHostPublicKey
		}},
		{name: "noncanonical host key spacing", edit: func(p *UpdaterPolicy) {
			p.Hosts[0].HostPublicKey = strings.Replace(testUpdaterHostPublicKey, " ", "  ", 1)
		}},
		{name: "unknown target host", edit: func(p *UpdaterPolicy) { p.Targets[0].HostID = "host-b" }},
		{name: "invalid target service ID", edit: func(p *UpdaterPolicy) { p.Targets[0].ServiceID = "../worker" }},
		{name: "unreferenced host", edit: func(p *UpdaterPolicy) {
			host := p.Hosts[0]
			host.HostID = "host-b"
			host.Name = "Studio B"
			p.Hosts = append(p.Hosts, host)
		}},
		{name: "unsupported service", edit: func(p *UpdaterPolicy) { p.Targets[0].ServiceType = "shell" }},
		{name: "unsupported deployment", edit: func(p *UpdaterPolicy) { p.Targets[0].DeploymentMode = "command" }},
		{name: "short poll", edit: func(p *UpdaterPolicy) { p.PollIntervalSeconds = 4 }},
		{name: "short heartbeat", edit: func(p *UpdaterPolicy) { p.HeartbeatIntervalSeconds = 4 }},
		{name: "heartbeat above offline-safe maximum", edit: func(p *UpdaterPolicy) { p.HeartbeatIntervalSeconds = 61 }},
		{name: "long heartbeat", edit: func(p *UpdaterPolicy) { p.HeartbeatIntervalSeconds = 3601 }},
		{name: "non TLS public host", edit: func(p *UpdaterPolicy) { p.API.Host = "updater.example.com" }},
		{name: "non TLS public bind", edit: func(p *UpdaterPolicy) { p.API.BindHost = "0.0.0.0" }},
		{name: "TLS relative cert", edit: func(p *UpdaterPolicy) {
			p.API.SSLEnabled = true
			p.API.Host = "updater.example.com"
			p.API.TLSCertFile = "updater.crt"
			p.API.TLSKeyFile = "/etc/autostream/updater.key"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validUpdaterPolicy()
			tt.edit(&input)
			if _, err := normalizeUpdaterPolicy("updater-01", input); !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("normalize error = %v, want ErrInvalidSettings", err)
			}
		})
	}
}

func TestNormalizeUpdaterPolicyAcceptsIPv6AndTLS(t *testing.T) {
	t.Parallel()

	input := validUpdaterPolicy()
	input.Hosts[0].Address = "2001:db8::20"
	input.API = UpdaterPolicyAPI{
		BindHost:    "0.0.0.0",
		Host:        "updater.example.com",
		Port:        8443,
		SSLEnabled:  true,
		TLSCertFile: "/etc/autostream/tls/updater.crt",
		TLSKeyFile:  "/etc/autostream/tls/updater.key",
	}
	input.HeartbeatIntervalSeconds = 60
	got, err := normalizeUpdaterPolicy("updater-01", input)
	if err != nil {
		t.Fatal(err)
	}
	if got.API != input.API || got.Hosts[0].Address != input.Hosts[0].Address || got.HeartbeatIntervalSeconds != 60 {
		t.Fatalf("normalized TLS/IPv6 policy = %#v", got)
	}
}

func TestUpdaterPolicyJSONContainsOnlyDeclarativePublicFields(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(validUpdaterPolicy())
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		`"updater_id"`, `"revision"`, `"api"`, `"bind_host"`, `"poll_interval_seconds"`,
		`"heartbeat_interval_seconds"`, `"hosts"`, `"host_public_key"`, `"targets"`, `"service_id"`, `"deployment_mode"`, `"updated_at"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("policy JSON %s does not contain %s", text, required)
		}
	}
	for _, forbidden := range []string{"identity_file", "known_hosts_file", "helper_argv", "backup_argv", "systemctl_path", "docker_path", "github_token"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("policy JSON contains privileged field %q: %s", forbidden, text)
		}
	}
}

func TestDecodeUpdaterPolicyRejectsNoncanonicalHostKey(t *testing.T) {
	t.Parallel()

	input := validUpdaterPolicy()
	input.Hosts[0].HostPublicKey += " database-comment"
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeUpdaterPolicy("updater-01", 1, body, time.Now().UTC()); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("decode error = %v, want ErrInvalidSettings", err)
	}
}

func TestMemoryUpdaterPolicyAdminStoreSavesPolicyAndReleaseTokenAtomically(t *testing.T) {
	t.Parallel()

	policies := NewMemoryUpdaterPolicyStore()
	releaseToken := " github-release-token "
	created, status, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		0,
		validUpdaterPolicy(),
		&releaseToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || !status.Configured || status.Fingerprint == "" {
		t.Fatalf("created policy/status = %#v / %#v", created, status)
	}
	value, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || value != "github-release-token" {
		t.Fatalf("stored release token = %q, %v", value, err)
	}

	updatedInput := validUpdaterPolicy()
	updatedInput.PollIntervalSeconds = 20
	updated, preservedStatus, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		created.Revision,
		updatedInput,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.PollIntervalSeconds != 20 || !preservedStatus.Configured ||
		preservedStatus.Fingerprint != status.Fingerprint {
		t.Fatalf("updated policy/status = %#v / %#v", updated, preservedStatus)
	}
	value, err = policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || value != "github-release-token" {
		t.Fatalf("omitted token was not preserved = %q, %v", value, err)
	}

	deleteToken := " "
	deleted, deletedStatus, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		updated.Revision,
		updatedInput,
		&deleteToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Revision != 3 || deletedStatus.Configured {
		t.Fatalf("deleted policy/status = %#v / %#v", deleted, deletedStatus)
	}
	if _, err := policies.GetUpdaterReleaseTokenValue(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("explicit empty token was not deleted: %v", err)
	}
}

func TestMemoryUpdaterPolicyAdminStoreConflictDoesNotMutateReleaseToken(t *testing.T) {
	t.Parallel()

	policies := NewMemoryUpdaterPolicyStore()
	originalToken := "original-token"
	created, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		0,
		validUpdaterPolicy(),
		&originalToken,
	)
	if err != nil {
		t.Fatal(err)
	}

	replacementToken := "replacement-token"
	replacementPolicy := validUpdaterPolicy()
	replacementPolicy.PollIntervalSeconds = 20
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		0,
		replacementPolicy,
		&replacementToken,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save error = %v, want ErrConflict", err)
	}
	got, err := policies.GetUpdaterPolicy(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != created.Revision || got.PollIntervalSeconds != created.PollIntervalSeconds {
		t.Fatalf("conflict mutated policy: %#v", got)
	}
	value, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || value != originalToken {
		t.Fatalf("conflict mutated token = %q, %v", value, err)
	}
}

func TestMemoryUpdaterPolicyAdminStoreCanceledSaveDoesNotMutate(t *testing.T) {
	t.Parallel()

	policies := NewMemoryUpdaterPolicyStore()
	originalToken := "original-token"
	created, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		t.Context(),
		"updater-01",
		0,
		validUpdaterPolicy(),
		&originalToken,
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	replacementToken := "replacement-token"
	replacementPolicy := validUpdaterPolicy()
	replacementPolicy.PollIntervalSeconds = 20
	if _, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
		ctx,
		"updater-01",
		created.Revision,
		replacementPolicy,
		&replacementToken,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save error = %v, want context.Canceled", err)
	}
	got, err := policies.GetUpdaterPolicy(t.Context(), "updater-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != created.Revision || got.PollIntervalSeconds != created.PollIntervalSeconds {
		t.Fatalf("canceled save mutated policy: %#v", got)
	}
	value, err := policies.GetUpdaterReleaseTokenValue(t.Context())
	if err != nil || value != originalToken {
		t.Fatalf("canceled save mutated token = %q, %v", value, err)
	}
}

func TestMemoryUpdaterPolicyAdminStoreRejectsUnsafeReleaseTokensWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
	}{
		{name: "embedded newline", token: "github_pat_first\nsecond"},
		{name: "embedded space", token: "github_pat_first second"},
		{name: "non ASCII", token: "github_pat_\U0001f4a5"},
		{name: "oversized", token: strings.Repeat("a", maxUpdaterReleaseTokenBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policies := NewMemoryUpdaterPolicyStore()
			_, _, err := policies.SaveUpdaterPolicyAndReleaseToken(
				t.Context(),
				"updater-01",
				0,
				validUpdaterPolicy(),
				&tt.token,
			)
			if !errors.Is(err, ErrInvalidSettings) {
				t.Fatalf("unsafe token error = %v, want ErrInvalidSettings", err)
			}
			if strings.Contains(err.Error(), tt.token) {
				t.Fatalf("unsafe token escaped in error: %v", err)
			}
			if _, err := policies.GetUpdaterPolicy(t.Context(), "updater-01"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unsafe token mutated policy: %v", err)
			}
			if _, err := policies.GetUpdaterReleaseTokenValue(t.Context()); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unsafe token was stored: %v", err)
			}
		})
	}
}

func TestStoredUpdaterReleaseTokenValidationMatchesClaimContract(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		value string
		valid bool
	}{
		"printable ASCII":  {value: "github_pat_valid-._~", valid: true},
		"empty":            {value: "", valid: false},
		"outer space":      {value: " github_pat_value", valid: false},
		"embedded newline": {value: "github_pat_first\nsecond", valid: false},
		"non ASCII":        {value: "github_pat_\U0001f4a5", valid: false},
		"oversized":        {value: strings.Repeat("a", maxUpdaterReleaseTokenBytes+1), valid: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := validStoredUpdaterReleaseToken(test.value); got != test.valid {
				t.Fatalf("validStoredUpdaterReleaseToken(%q) = %v, want %v", test.value, got, test.valid)
			}
		})
	}
}

func TestUpdaterGitHubReleaseTokenSecretIsNotGeneric(t *testing.T) {
	t.Parallel()

	secrets := NewMemorySecretStore()
	if _, err := secrets.UpdateSecret(t.Context(), UpdaterGitHubReleaseTokenSecretName, "github-release-token"); !errors.Is(err, ErrUnknownSecret) {
		t.Fatalf("generic update error = %v, want ErrUnknownSecret", err)
	}
	if _, err := secrets.GetSecretValue(t.Context(), UpdaterGitHubReleaseTokenSecretName); !errors.Is(err, ErrUnknownSecret) {
		t.Fatalf("generic get error = %v, want ErrUnknownSecret", err)
	}
	statuses, err := secrets.ListSecretStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range statuses {
		if status.Name == UpdaterGitHubReleaseTokenSecretName {
			t.Fatalf("generic status listed updater release token: %#v", status)
		}
	}
}

func validUpdaterPolicy() UpdaterPolicy {
	return UpdaterPolicy{
		UpdaterID:                "updater-01",
		API:                      UpdaterPolicyAPI{BindHost: "127.0.0.1", Host: "127.0.0.1", Port: 8090},
		PollIntervalSeconds:      15,
		HeartbeatIntervalSeconds: 30,
		Hosts: []UpdaterPolicyHost{{
			HostID:        "host-a",
			Name:          "Studio A",
			Address:       "host-a.example.com",
			Port:          55850,
			User:          "autostream-update-host",
			Arch:          "amd64",
			HostPublicKey: testUpdaterHostPublicKey,
		}},
		Targets: []UpdaterPolicyTarget{{
			TargetID:       "worker-a",
			HostID:         "host-a",
			ServiceType:    "worker",
			DeploymentMode: "systemd",
		}},
	}
}

func testRSAUpdaterHostPublicKey(t *testing.T) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey)))
}
