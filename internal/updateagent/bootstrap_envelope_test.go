package updateagent

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestBootstrapEnvelopeIdentityIsStableAndPrivate(t *testing.T) {
	stateDir := t.TempDir()
	first, err := EnsureBootstrapEnvelopeIdentity(stateDir)
	if err != nil {
		t.Fatalf("ensure first identity: %v", err)
	}
	second, err := EnsureBootstrapEnvelopeIdentity(stateDir)
	if err != nil {
		t.Fatalf("ensure second identity: %v", err)
	}
	if first != second {
		t.Fatalf("identity changed across ensure: first=%#v second=%#v", first, second)
	}

	publicKey, err := base64.RawURLEncoding.DecodeString(first.PublicKey)
	if err != nil || len(publicKey) != 65 {
		t.Fatalf("public key is not raw P-256 SEC1 data: len=%d err=%v", len(publicKey), err)
	}
	if _, err := ecdh.P256().NewPublicKey(publicKey); err != nil {
		t.Fatalf("public key rejected by P-256: %v", err)
	}
	publicDigest := sha256.Sum256(publicKey)
	wantFingerprint := "SHA256:" + base64.RawStdEncoding.EncodeToString(publicDigest[:])
	if first.Fingerprint != wantFingerprint {
		t.Fatalf("fingerprint=%q want=%q", first.Fingerprint, wantFingerprint)
	}

	privatePath := filepath.Join(stateDir, "bootstrap-envelope", "p256-private-key")
	info, err := os.Lstat(privatePath)
	if err != nil {
		t.Fatalf("stat private key: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != 32 {
		t.Fatalf("unsafe private key info: mode=%v size=%d", info.Mode(), info.Size())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode=%o want=600", info.Mode().Perm())
	}
}

func TestBootstrapEnvelopeRoundTripBindsCanonicalContext(t *testing.T) {
	stateDir := t.TempDir()
	identity, err := EnsureBootstrapEnvelopeIdentity(stateDir)
	if err != nil {
		t.Fatalf("ensure identity: %v", err)
	}
	binding := BootstrapEnvelopeBinding{
		Version: BootstrapEnvelopeVersion, UpdaterID: "updater-01", PolicyRevision: 12,
		JobID: "bootstrap-job-01", HostIDs: []string{"host-b", "host-a"},
	}
	credential := BootstrapAdministratorCredential{
		AdministratorUser: "deploy",
		PrivateKey:        []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nprivate-secret\n-----END OPENSSH PRIVATE KEY-----\n"),
		Passphrase:        []byte("passphrase-secret"),
	}
	envelope, err := EncryptBootstrapCredentialEnvelope(identity.PublicKey, binding, credential)
	if err != nil {
		t.Fatalf("encrypt credential: %v", err)
	}
	for _, secret := range []string{"private-secret", "passphrase-secret"} {
		if bytes.Contains(envelope, []byte(secret)) {
			t.Fatalf("envelope exposed plaintext secret %q", secret)
		}
	}

	sortedBinding := binding
	sortedBinding.HostIDs = []string{"host-a", "host-b"}
	decrypted, err := DecryptBootstrapCredentialEnvelope(stateDir, sortedBinding, envelope)
	if err != nil {
		t.Fatalf("decrypt credential: %v", err)
	}
	if decrypted.AdministratorUser != credential.AdministratorUser ||
		!bytes.Equal(decrypted.PrivateKey, credential.PrivateKey) ||
		!bytes.Equal(decrypted.Passphrase, credential.Passphrase) {
		t.Fatalf("decrypted credential mismatch: %#v", decrypted)
	}

	wrongBinding := sortedBinding
	wrongBinding.PolicyRevision++
	_, err = DecryptBootstrapCredentialEnvelope(stateDir, wrongBinding, envelope)
	if err == nil {
		t.Fatal("credential decrypted under a different policy revision")
	}
	for _, secret := range []string{"private-secret", "passphrase-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("decryption error exposed %q: %v", secret, err)
		}
	}
}

func TestBootstrapEnvelopeDecryptsWebCryptoFixedVector(t *testing.T) {
	// Generated with Node WebCrypto using:
	// recipient P-256 scalar 1, ephemeral scalar 2, nonce 00..0b,
	// ECDH deriveBits(256), HKDF-SHA256, then AES-256-GCM.
	const (
		recipientPrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE"
		recipientPublicKey  = "BGsX0fLhLEJH-Lzm5WOkQPJ3A32BLeszoPShOUXYmMKWT-NC4v4af5uO5-tKfA-eFivOM1drMV7Oy7ZAaDe_UfU"
		envelopeJSON        = `{"version":1,"ephemeral_public_key":"BHzyexiNA09-ilI4AwS1GsPAiWnid_IbNaYLSPxHZpl4B3dVENuO0EApPZrGn3Qw27p9reY86YIpngS3nSJ4c9E","nonce":"AAECAwQFBgcICQoL","ciphertext":"2KTE0tK-dlqNjJhoI4r7bcqaKhpQksriceJVF6BZYOGFQfKoOJEiSzNIJVCzxYmwMLD9ozGPidtYQA9R1aOJndP3rJQ4ViWbW8wc4KIWmG6iPwe6nQsATMHRef1y2XFVuY5KmQM3d2etdodnItFv8vZAsuXEgSgaje8"}`
	)

	stateDir := t.TempDir()
	identityDir := filepath.Join(stateDir, bootstrapEnvelopeDirectoryName)
	if err := os.Mkdir(identityDir, 0o700); err != nil {
		t.Fatalf("create fixed-vector identity directory: %v", err)
	}
	privateBytes, err := base64.RawURLEncoding.DecodeString(recipientPrivateKey)
	if err != nil {
		t.Fatalf("decode fixed recipient private key: %v", err)
	}
	privateKey, err := ecdh.P256().NewPrivateKey(privateBytes)
	if err != nil {
		t.Fatalf("parse fixed recipient private key: %v", err)
	}
	if got := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()); got != recipientPublicKey {
		t.Fatalf("fixed recipient public key=%q want=%q", got, recipientPublicKey)
	}
	privatePath := filepath.Join(identityDir, bootstrapEnvelopePrivateKeyName)
	if err := os.WriteFile(privatePath, privateBytes, 0o600); err != nil {
		t.Fatalf("write fixed recipient private key: %v", err)
	}

	credential, err := DecryptBootstrapCredentialEnvelope(stateDir, BootstrapEnvelopeBinding{
		Version:        BootstrapEnvelopeVersion,
		UpdaterID:      "updater-01",
		PolicyRevision: 7,
		JobID:          "bootstrap-job-01",
		HostIDs:        []string{"host-b", "host-a"},
	}, []byte(envelopeJSON))
	if err != nil {
		t.Fatalf("decrypt WebCrypto fixed vector: %v", err)
	}
	if credential.AdministratorUser != "deploy" ||
		!bytes.Equal(credential.PrivateKey, []byte("test-private-key")) ||
		!bytes.Equal(credential.Passphrase, []byte("test-passphrase")) {
		t.Fatalf("fixed-vector credential mismatch: %#v", credential)
	}
}

func TestBootstrapEnvelopeStrictlyRejectsMalformedInput(t *testing.T) {
	stateDir := t.TempDir()
	identity, err := EnsureBootstrapEnvelopeIdentity(stateDir)
	if err != nil {
		t.Fatalf("ensure identity: %v", err)
	}
	binding := bootstrapEnvelopeTestBinding()
	valid, err := EncryptBootstrapCredentialEnvelope(identity.PublicKey, binding, BootstrapAdministratorCredential{
		AdministratorUser: "deploy",
		PrivateKey:        []byte("secret-key"),
	})
	if err != nil {
		t.Fatalf("encrypt valid envelope: %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(valid, &fields); err != nil {
		t.Fatalf("decode test envelope: %v", err)
	}
	fields["unexpected"] = true
	withUnknown, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode unknown-field envelope: %v", err)
	}
	var wire BootstrapCredentialEnvelope
	if err := json.Unmarshal(valid, &wire); err != nil {
		t.Fatalf("decode duplicate-field fixture: %v", err)
	}
	withDuplicate := []byte(fmt.Sprintf(
		`{"version":1,"version":1,"ephemeral_public_key":"%s","nonce":"%s","ciphertext":"%s"}`,
		wire.EphemeralPublicKey,
		wire.Nonce,
		wire.Ciphertext,
	))
	withCaseAlias := []byte(fmt.Sprintf(
		`{"Version":1,"ephemeral_public_key":"%s","nonce":"%s","ciphertext":"%s"}`,
		wire.EphemeralPublicKey,
		wire.Nonce,
		wire.Ciphertext,
	))
	withCaseAliasDuplicate := []byte(fmt.Sprintf(
		`{"version":1,"Version":1,"ephemeral_public_key":"%s","nonce":"%s","ciphertext":"%s"}`,
		wire.EphemeralPublicKey,
		wire.Nonce,
		wire.Ciphertext,
	))

	tests := map[string][]byte{
		"unknown field":        withUnknown,
		"duplicate field":      withDuplicate,
		"case alias":           withCaseAlias,
		"case alias duplicate": withCaseAliasDuplicate,
		"trailing data":        append(append([]byte(nil), valid...), []byte("\n{}")...),
		"oversized":            bytes.Repeat([]byte("x"), (256<<10)+1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := DecryptBootstrapCredentialEnvelope(stateDir, binding, input)
			if err == nil {
				t.Fatal("malformed envelope was accepted")
			}
			if strings.Contains(err.Error(), "secret-key") {
				t.Fatalf("error exposed credential: %v", err)
			}
		})
	}
}

func TestBootstrapEnvelopeStrictlyRejectsCredentialPayload(t *testing.T) {
	stateDir := t.TempDir()
	identity, err := EnsureBootstrapEnvelopeIdentity(stateDir)
	if err != nil {
		t.Fatalf("ensure identity: %v", err)
	}
	binding := bootstrapEnvelopeTestBinding()

	tests := map[string]string{
		"unknown field":        `{"administrator_user":"deploy","private_key":"c2VjcmV0","unexpected":true}`,
		"duplicate field":      `{"administrator_user":"deploy","administrator_user":"deploy","private_key":"c2VjcmV0"}`,
		"case alias":           `{"Administrator_User":"deploy","private_key":"c2VjcmV0"}`,
		"case alias duplicate": `{"administrator_user":"deploy","Administrator_User":"deploy","private_key":"c2VjcmV0"}`,
		"trailing data":        `{"administrator_user":"deploy","private_key":"c2VjcmV0"} {}`,
		"root user":            `{"administrator_user":"root","private_key":"c2VjcmV0"}`,
		"missing key":          `{"administrator_user":"deploy","private_key":""}`,
		"padded base64":        `{"administrator_user":"deploy","private_key":"c2VjcmV0="}`,
	}
	for name, plaintext := range tests {
		t.Run(name, func(t *testing.T) {
			envelope := sealBootstrapEnvelopeForTest(t, identity.PublicKey, binding, []byte(plaintext))
			_, err := DecryptBootstrapCredentialEnvelope(stateDir, binding, envelope)
			if err == nil {
				t.Fatal("invalid credential payload was accepted")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error exposed credential material: %v", err)
			}
		})
	}
}

func TestBootstrapEnvelopeCredentialFormattingIsRedacted(t *testing.T) {
	credential := BootstrapAdministratorCredential{
		AdministratorUser: "deploy",
		PrivateKey:        []byte("private-secret"),
		Passphrase:        []byte("passphrase-secret"),
	}
	formatted := fmt.Sprintf("%v / %#v", credential, credential)
	encoded, err := json.Marshal(credential)
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	for _, output := range []string{formatted, string(encoded)} {
		for _, secret := range []string{"private-secret", "passphrase-secret"} {
			if strings.Contains(output, secret) {
				t.Fatalf("credential formatting exposed %q: %s", secret, output)
			}
		}
	}
	if !strings.Contains(formatted, "[REDACTED]") || !bytes.Contains(encoded, []byte("[REDACTED]")) {
		t.Fatalf("credential formatting did not make redaction explicit: %s / %s", formatted, encoded)
	}
}

func TestBootstrapEnvelopeRejectsInvalidBindingAndRecipient(t *testing.T) {
	stateDir := t.TempDir()
	identity, err := EnsureBootstrapEnvelopeIdentity(stateDir)
	if err != nil {
		t.Fatalf("ensure identity: %v", err)
	}
	credential := BootstrapAdministratorCredential{AdministratorUser: "deploy", PrivateKey: []byte("key")}

	duplicateHosts := bootstrapEnvelopeTestBinding()
	duplicateHosts.HostIDs = []string{"host-a", "host-a"}
	if _, err := EncryptBootstrapCredentialEnvelope(identity.PublicKey, duplicateHosts, credential); err == nil {
		t.Fatal("duplicate host binding was accepted")
	}
	if _, err := EncryptBootstrapCredentialEnvelope("not-base64url", bootstrapEnvelopeTestBinding(), credential); err == nil {
		t.Fatal("invalid recipient public key was accepted")
	}
}

func bootstrapEnvelopeTestBinding() BootstrapEnvelopeBinding {
	return BootstrapEnvelopeBinding{
		Version: BootstrapEnvelopeVersion, UpdaterID: "updater-01", PolicyRevision: 7,
		JobID: "bootstrap-job-01", HostIDs: []string{"host-a"},
	}
}

func sealBootstrapEnvelopeForTest(t *testing.T, recipientEncoded string, binding BootstrapEnvelopeBinding, plaintext []byte) []byte {
	t.Helper()
	curve := ecdh.P256()
	recipientBytes, err := base64.RawURLEncoding.DecodeString(recipientEncoded)
	if err != nil {
		t.Fatalf("decode recipient key: %v", err)
	}
	recipient, err := curve.NewPublicKey(recipientBytes)
	if err != nil {
		t.Fatalf("parse recipient key: %v", err)
	}
	ephemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ephemeral key: %v", err)
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		t.Fatalf("derive shared key: %v", err)
	}
	key, err := hkdf.Key(sha256.New, shared, nil, "autostream-bootstrap-envelope-v1", 32)
	if err != nil {
		t.Fatalf("derive AES key: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("create AES cipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("create AES-GCM: %v", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}
	aad := bootstrapEnvelopeAADForTest(t, binding)
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	wire := struct {
		Version            int    `json:"version"`
		EphemeralPublicKey string `json:"ephemeral_public_key"`
		Nonce              string `json:"nonce"`
		Ciphertext         string `json:"ciphertext"`
	}{
		Version:            BootstrapEnvelopeVersion,
		EphemeralPublicKey: base64.RawURLEncoding.EncodeToString(ephemeral.PublicKey().Bytes()),
		Nonce:              base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:         base64.RawURLEncoding.EncodeToString(ciphertext),
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("encode test envelope: %v", err)
	}
	return encoded
}

func bootstrapEnvelopeAADForTest(t *testing.T, binding BootstrapEnvelopeBinding) []byte {
	t.Helper()
	hostIDs := append([]string(nil), binding.HostIDs...)
	sort.Strings(hostIDs)
	aad, err := json.Marshal(struct {
		Version        int      `json:"version"`
		UpdaterID      string   `json:"updater_id"`
		PolicyRevision int64    `json:"policy_revision"`
		JobID          string   `json:"job_id"`
		HostIDs        []string `json:"host_ids"`
	}{
		Version: binding.Version, UpdaterID: binding.UpdaterID, PolicyRevision: binding.PolicyRevision,
		JobID: binding.JobID, HostIDs: hostIDs,
	})
	if err != nil {
		t.Fatalf("encode test AAD: %v", err)
	}
	return aad
}
