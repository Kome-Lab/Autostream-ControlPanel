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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	BootstrapEnvelopeVersion = 1

	bootstrapEnvelopeDirectoryName  = "bootstrap-envelope"
	bootstrapEnvelopePrivateKeyName = "p256-private-key"
	bootstrapEnvelopeHKDFInfo       = "autostream-bootstrap-envelope-v1"

	bootstrapEnvelopePrivateKeyBytes = 32
	bootstrapEnvelopePublicKeyBytes  = 65
	bootstrapEnvelopeNonceBytes      = 12
	bootstrapEnvelopeMaxBytes        = 256 << 10
	bootstrapCredentialMaxBytes      = 128 << 10
	bootstrapPrivateKeyMaxBytes      = 64 << 10
	bootstrapPassphraseMaxBytes      = 8 << 10
	bootstrapEnvelopeMaxHostIDs      = 256
)

// BootstrapEnvelopeBinding is authenticated, but not encrypted. Its canonical
// JSON form is shared with browser clients as AES-GCM additional data. HostIDs
// are sorted before encoding so callers cannot create different bindings for
// the same host set by changing input order.
type BootstrapEnvelopeBinding struct {
	Version        int      `json:"version"`
	UpdaterID      string   `json:"updater_id"`
	PolicyRevision int64    `json:"policy_revision"`
	JobID          string   `json:"job_id"`
	HostIDs        []string `json:"host_ids"`
}

// BootstrapEnvelopeIdentity is safe to report to the Control Panel. The
// private key remains in the updater state directory and is never returned.
type BootstrapEnvelopeIdentity struct {
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
}

// BootstrapAdministratorCredential is the decrypted, short-lived SSH
// credential. Formatting and JSON encoding deliberately redact its secret
// fields so an accidental structured log cannot expose them.
type BootstrapAdministratorCredential struct {
	AdministratorUser string
	PrivateKey        []byte
	Passphrase        []byte
}

func (BootstrapAdministratorCredential) String() string {
	return "BootstrapAdministratorCredential{administrator_user:[REDACTED] private_key:[REDACTED] passphrase:[REDACTED]}"
}

func (c BootstrapAdministratorCredential) GoString() string {
	return c.String()
}

func (c BootstrapAdministratorCredential) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, c.String())
}

func (c BootstrapAdministratorCredential) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		AdministratorUser string `json:"administrator_user"`
		PrivateKey        string `json:"private_key"`
		Passphrase        string `json:"passphrase"`
	}{
		AdministratorUser: safeBootstrapAdministratorUser(c.AdministratorUser),
		PrivateKey:        "[REDACTED]",
		Passphrase:        "[REDACTED]",
	})
}

// BootstrapCredentialEnvelope is the opaque JSON object transported through
// the Control Panel. Its binary fields are canonical unpadded base64url.
type BootstrapCredentialEnvelope struct {
	Version            int    `json:"version"`
	EphemeralPublicKey string `json:"ephemeral_public_key"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

type bootstrapCredentialEnvelopeWire = BootstrapCredentialEnvelope

type bootstrapAdministratorCredentialWire struct {
	AdministratorUser string  `json:"administrator_user"`
	PrivateKey        string  `json:"private_key"`
	Passphrase        *string `json:"passphrase,omitempty"`
}

// EnsureBootstrapEnvelopeIdentity creates or reuses one updater-owned P-256
// ECDH identity. The raw 32-byte private key is stored beneath stateDir as a
// daemon-owned 0600 regular file. Public keys use SEC1 uncompressed encoding
// and unpadded base64url so Web Crypto can import them directly.
func EnsureBootstrapEnvelopeIdentity(stateDir string) (BootstrapEnvelopeIdentity, error) {
	privatePath, err := bootstrapEnvelopePrivateKeyPath(stateDir)
	if err != nil {
		return BootstrapEnvelopeIdentity{}, err
	}
	if !managedSSHIdentityCallerAllowed() {
		return BootstrapEnvelopeIdentity{}, errors.New("bootstrap envelope identity must be initialized by the non-root updater service user")
	}
	cleanStateDir := filepath.Clean(stateDir)
	if err := validateManagedDirectoryChain(cleanStateDir); err != nil {
		return BootstrapEnvelopeIdentity{}, errors.New("bootstrap envelope state directory is unsafe")
	}
	identityDir := filepath.Dir(privatePath)
	if err := ensureManagedSSHDirectory(identityDir); err != nil {
		return BootstrapEnvelopeIdentity{}, errors.New("initialize bootstrap envelope identity directory")
	}

	exists, err := bootstrapEnvelopePrivateKeyExists(privatePath)
	if err != nil {
		return BootstrapEnvelopeIdentity{}, err
	}
	if !exists {
		privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return BootstrapEnvelopeIdentity{}, errors.New("generate bootstrap envelope identity")
		}
		privateBytes := privateKey.Bytes()
		defer clear(privateBytes)
		if _, err := publishManagedSSHFile(privatePath, privateBytes, 0o600); err != nil {
			return BootstrapEnvelopeIdentity{}, errors.New("install bootstrap envelope identity")
		}
	}

	privateKey, err := loadBootstrapEnvelopePrivateKey(privatePath)
	if err != nil {
		return BootstrapEnvelopeIdentity{}, err
	}
	if err := validateManagedDirectoryChain(cleanStateDir); err != nil {
		return BootstrapEnvelopeIdentity{}, errors.New("bootstrap envelope state directory changed during identity load")
	}
	return bootstrapEnvelopePublicIdentity(privateKey.PublicKey()), nil
}

// EncryptBootstrapCredentialEnvelope is the Go-side interoperability helper
// for browser-produced envelopes. The wire algorithm is:
//
//   - P-256 ECDH with a fresh ephemeral key, SEC1 uncompressed public keys
//   - HKDF-SHA256 with an empty salt and bootstrapEnvelopeHKDFInfo
//   - AES-256-GCM with a random 12-byte nonce
//   - canonical BootstrapEnvelopeBinding JSON as additional authenticated data
//   - unpadded base64url for every binary envelope field
func EncryptBootstrapCredentialEnvelope(recipientPublicKey string, binding BootstrapEnvelopeBinding, credential BootstrapAdministratorCredential) ([]byte, error) {
	aad, err := canonicalBootstrapEnvelopeAAD(binding)
	if err != nil {
		return nil, err
	}
	if err := validateBootstrapAdministratorCredential(credential); err != nil {
		return nil, err
	}
	recipientBytes, err := decodeCanonicalBase64URL(recipientPublicKey, bootstrapEnvelopePublicKeyBytes, bootstrapEnvelopePublicKeyBytes, false)
	if err != nil {
		return nil, errors.New("bootstrap envelope recipient public key is invalid")
	}
	recipient, err := ecdh.P256().NewPublicKey(recipientBytes)
	if err != nil {
		return nil, errors.New("bootstrap envelope recipient public key is invalid")
	}

	privateKeyText := base64.RawURLEncoding.EncodeToString(credential.PrivateKey)
	wireCredential := bootstrapAdministratorCredentialWire{
		AdministratorUser: credential.AdministratorUser,
		PrivateKey:        privateKeyText,
	}
	if len(credential.Passphrase) > 0 {
		passphrase := base64.RawURLEncoding.EncodeToString(credential.Passphrase)
		wireCredential.Passphrase = &passphrase
	}
	plaintext, err := json.Marshal(wireCredential)
	if err != nil || len(plaintext) == 0 || len(plaintext) > bootstrapCredentialMaxBytes {
		return nil, errors.New("encode bootstrap administrator credential")
	}
	defer clear(plaintext)

	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, errors.New("generate bootstrap envelope ephemeral key")
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		return nil, errors.New("derive bootstrap envelope shared key")
	}
	key, err := deriveBootstrapEnvelopeAESKey(shared)
	clear(shared)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	aead, err := newBootstrapEnvelopeAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("generate bootstrap envelope nonce")
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	wire := bootstrapCredentialEnvelopeWire{
		Version:            BootstrapEnvelopeVersion,
		EphemeralPublicKey: base64.RawURLEncoding.EncodeToString(ephemeral.PublicKey().Bytes()),
		Nonce:              base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext:         base64.RawURLEncoding.EncodeToString(ciphertext),
	}
	envelope, err := json.Marshal(wire)
	if err != nil || len(envelope) == 0 || len(envelope) > bootstrapEnvelopeMaxBytes {
		return nil, errors.New("encode bootstrap credential envelope")
	}
	return envelope, nil
}

// DecryptBootstrapCredentialEnvelope opens one bounded, strict envelope using
// the fixed private-key path beneath stateDir. All parse and authentication
// failures use fixed messages that never include attacker-controlled JSON,
// private-key bytes, or passphrase bytes.
func DecryptBootstrapCredentialEnvelope(stateDir string, binding BootstrapEnvelopeBinding, envelopeJSON []byte) (BootstrapAdministratorCredential, error) {
	aad, err := canonicalBootstrapEnvelopeAAD(binding)
	if err != nil {
		return BootstrapAdministratorCredential{}, err
	}
	if !managedSSHIdentityCallerAllowed() {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap credential envelope must be decrypted by the non-root updater service user")
	}
	var wire bootstrapCredentialEnvelopeWire
	if err := decodeStrictBootstrapJSON(
		envelopeJSON,
		bootstrapEnvelopeMaxBytes,
		&wire,
		"version",
		"ephemeral_public_key",
		"nonce",
		"ciphertext",
	); err != nil {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap credential envelope is invalid")
	}
	if wire.Version != BootstrapEnvelopeVersion || wire.Version != binding.Version {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap credential envelope version is invalid")
	}
	ephemeralBytes, err := decodeCanonicalBase64URL(wire.EphemeralPublicKey, bootstrapEnvelopePublicKeyBytes, bootstrapEnvelopePublicKeyBytes, false)
	if err != nil {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap credential envelope public key is invalid")
	}
	ephemeral, err := ecdh.P256().NewPublicKey(ephemeralBytes)
	if err != nil {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap credential envelope public key is invalid")
	}
	nonce, err := decodeCanonicalBase64URL(wire.Nonce, bootstrapEnvelopeNonceBytes, bootstrapEnvelopeNonceBytes, false)
	if err != nil {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap credential envelope nonce is invalid")
	}
	ciphertext, err := decodeCanonicalBase64URL(wire.Ciphertext, 17, bootstrapCredentialMaxBytes+32, false)
	if err != nil {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap credential envelope ciphertext is invalid")
	}

	privatePath, err := bootstrapEnvelopePrivateKeyPath(stateDir)
	if err != nil {
		return BootstrapAdministratorCredential{}, err
	}
	cleanStateDir := filepath.Clean(stateDir)
	if err := validateManagedDirectoryChain(cleanStateDir); err != nil {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap envelope state directory is unsafe")
	}
	privateKey, err := loadBootstrapEnvelopePrivateKey(privatePath)
	if err != nil {
		return BootstrapAdministratorCredential{}, err
	}
	if err := validateManagedDirectoryChain(cleanStateDir); err != nil {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap envelope state directory changed during identity load")
	}
	shared, err := privateKey.ECDH(ephemeral)
	if err != nil {
		return BootstrapAdministratorCredential{}, errors.New("derive bootstrap envelope shared key")
	}
	key, err := deriveBootstrapEnvelopeAESKey(shared)
	clear(shared)
	if err != nil {
		return BootstrapAdministratorCredential{}, err
	}
	defer clear(key)
	aead, err := newBootstrapEnvelopeAEAD(key)
	if err != nil {
		return BootstrapAdministratorCredential{}, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap credential envelope authentication failed")
	}
	defer clear(plaintext)
	if len(plaintext) == 0 || len(plaintext) > bootstrapCredentialMaxBytes {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap administrator credential is invalid")
	}

	var credentialWire bootstrapAdministratorCredentialWire
	if err := decodeStrictBootstrapJSON(
		plaintext,
		bootstrapCredentialMaxBytes,
		&credentialWire,
		"administrator_user",
		"private_key",
		"passphrase",
	); err != nil {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap administrator credential is invalid")
	}
	privateKeyBytes, err := decodeCanonicalBase64URL(credentialWire.PrivateKey, 1, bootstrapPrivateKeyMaxBytes, false)
	if err != nil {
		return BootstrapAdministratorCredential{}, errors.New("bootstrap administrator credential is invalid")
	}
	var passphrase []byte
	if credentialWire.Passphrase != nil {
		passphrase, err = decodeCanonicalBase64URL(*credentialWire.Passphrase, 0, bootstrapPassphraseMaxBytes, true)
		if err != nil {
			clear(privateKeyBytes)
			return BootstrapAdministratorCredential{}, errors.New("bootstrap administrator credential is invalid")
		}
	}
	credential := BootstrapAdministratorCredential{
		AdministratorUser: credentialWire.AdministratorUser,
		PrivateKey:        privateKeyBytes,
		Passphrase:        passphrase,
	}
	if err := validateBootstrapAdministratorCredential(credential); err != nil {
		clear(credential.PrivateKey)
		clear(credential.Passphrase)
		return BootstrapAdministratorCredential{}, err
	}
	return credential, nil
}

func bootstrapEnvelopePrivateKeyPath(stateDir string) (string, error) {
	if stateDir == "" || stateDir != strings.TrimSpace(stateDir) {
		return "", errors.New("bootstrap envelope state directory is invalid")
	}
	cleanStateDir := filepath.Clean(stateDir)
	if !filepath.IsAbs(cleanStateDir) || filepath.Dir(cleanStateDir) == cleanStateDir {
		return "", errors.New("bootstrap envelope state directory must be a non-root absolute path")
	}
	privatePath := filepath.Join(cleanStateDir, bootstrapEnvelopeDirectoryName, bootstrapEnvelopePrivateKeyName)
	relative, err := filepath.Rel(cleanStateDir, privatePath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("bootstrap envelope identity path escapes state directory")
	}
	return privatePath, nil
}

func bootstrapEnvelopePrivateKeyExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("inspect bootstrap envelope identity")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("bootstrap envelope identity must be a regular non-symlink file")
	}
	return true, nil
}

func loadBootstrapEnvelopePrivateKey(path string) (*ecdh.PrivateKey, error) {
	if !filepath.IsAbs(path) || filepath.Dir(filepath.Clean(path)) == filepath.Clean(path) {
		return nil, errors.New("bootstrap envelope identity path is invalid")
	}
	if err := validateManagedSSHDirectory(filepath.Dir(path)); err != nil {
		return nil, errors.New("bootstrap envelope identity directory is unsafe")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || pathInfo.Size() != bootstrapEnvelopePrivateKeyBytes {
		return nil, errors.New("bootstrap envelope identity is unavailable")
	}
	if !managedSSHOwnedByCurrentUser(pathInfo) {
		return nil, errors.New("bootstrap envelope identity must be owned by the updater service user")
	}
	if runtime.GOOS != "windows" && pathInfo.Mode().Perm() != 0o600 {
		return nil, errors.New("bootstrap envelope identity must have mode 0600")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open bootstrap envelope identity")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(pathInfo, openedInfo) || pathInfo.Mode() != openedInfo.Mode() ||
		pathInfo.Size() != openedInfo.Size() || !pathInfo.ModTime().Equal(openedInfo.ModTime()) {
		return nil, errors.New("bootstrap envelope identity changed during secure open")
	}
	privateBytes, err := io.ReadAll(io.LimitReader(file, bootstrapEnvelopePrivateKeyBytes+1))
	if err != nil || len(privateBytes) != bootstrapEnvelopePrivateKeyBytes {
		clear(privateBytes)
		return nil, errors.New("read bootstrap envelope identity")
	}
	defer clear(privateBytes)
	privateKey, err := ecdh.P256().NewPrivateKey(privateBytes)
	if err != nil {
		return nil, errors.New("bootstrap envelope identity is invalid")
	}
	return privateKey, nil
}

func bootstrapEnvelopePublicIdentity(publicKey *ecdh.PublicKey) BootstrapEnvelopeIdentity {
	publicBytes := publicKey.Bytes()
	digest := sha256.Sum256(publicBytes)
	return BootstrapEnvelopeIdentity{
		PublicKey:   base64.RawURLEncoding.EncodeToString(publicBytes),
		Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:]),
	}
}

func canonicalBootstrapEnvelopeAAD(binding BootstrapEnvelopeBinding) ([]byte, error) {
	if binding.Version != BootstrapEnvelopeVersion ||
		binding.UpdaterID != strings.TrimSpace(binding.UpdaterID) ||
		!identifierPattern.MatchString(binding.UpdaterID) ||
		binding.PolicyRevision <= 0 ||
		binding.JobID != strings.TrimSpace(binding.JobID) ||
		!identifierPattern.MatchString(binding.JobID) ||
		len(binding.HostIDs) == 0 || len(binding.HostIDs) > bootstrapEnvelopeMaxHostIDs {
		return nil, errors.New("bootstrap envelope binding is invalid")
	}
	hostIDs := append([]string(nil), binding.HostIDs...)
	seen := make(map[string]struct{}, len(hostIDs))
	for _, hostID := range hostIDs {
		if hostID != strings.TrimSpace(hostID) || !managedSSHHostIDPattern.MatchString(hostID) {
			return nil, errors.New("bootstrap envelope binding is invalid")
		}
		if _, exists := seen[hostID]; exists {
			return nil, errors.New("bootstrap envelope binding is invalid")
		}
		seen[hostID] = struct{}{}
	}
	sort.Strings(hostIDs)
	canonical := struct {
		Version        int      `json:"version"`
		UpdaterID      string   `json:"updater_id"`
		PolicyRevision int64    `json:"policy_revision"`
		JobID          string   `json:"job_id"`
		HostIDs        []string `json:"host_ids"`
	}{
		Version: binding.Version, UpdaterID: binding.UpdaterID, PolicyRevision: binding.PolicyRevision,
		JobID: binding.JobID, HostIDs: hostIDs,
	}
	aad, err := json.Marshal(canonical)
	if err != nil {
		return nil, errors.New("encode bootstrap envelope binding")
	}
	return aad, nil
}

func validateBootstrapAdministratorCredential(credential BootstrapAdministratorCredential) error {
	if credential.AdministratorUser != strings.TrimSpace(credential.AdministratorUser) ||
		!sshUserPattern.MatchString(credential.AdministratorUser) ||
		credential.AdministratorUser == "root" ||
		len(credential.PrivateKey) == 0 || len(credential.PrivateKey) > bootstrapPrivateKeyMaxBytes ||
		len(credential.Passphrase) > bootstrapPassphraseMaxBytes {
		return errors.New("bootstrap administrator credential is invalid")
	}
	return nil
}

func safeBootstrapAdministratorUser(value string) string {
	if value == strings.TrimSpace(value) && sshUserPattern.MatchString(value) && value != "root" {
		return value
	}
	return "[REDACTED]"
}

func deriveBootstrapEnvelopeAESKey(shared []byte) ([]byte, error) {
	if len(shared) == 0 {
		return nil, errors.New("bootstrap envelope shared key is invalid")
	}
	key, err := hkdf.Key(sha256.New, shared, nil, bootstrapEnvelopeHKDFInfo, 32)
	if err != nil || len(key) != 32 {
		clear(key)
		return nil, errors.New("derive bootstrap envelope encryption key")
	}
	return key, nil
}

func newBootstrapEnvelopeAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("initialize bootstrap envelope encryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || aead.NonceSize() != bootstrapEnvelopeNonceBytes {
		return nil, errors.New("initialize bootstrap envelope authentication")
	}
	return aead, nil
}

func decodeCanonicalBase64URL(value string, minimumBytes, maximumBytes int, allowEmpty bool) ([]byte, error) {
	if maximumBytes < minimumBytes || value != strings.TrimSpace(value) {
		return nil, errors.New("invalid base64url value")
	}
	if value == "" {
		if allowEmpty && minimumBytes == 0 {
			return []byte{}, nil
		}
		return nil, errors.New("invalid base64url value")
	}
	if len(value) > base64.RawURLEncoding.EncodedLen(maximumBytes) {
		return nil, errors.New("invalid base64url value")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) < minimumBytes || len(decoded) > maximumBytes ||
		base64.RawURLEncoding.EncodeToString(decoded) != value {
		clear(decoded)
		return nil, errors.New("invalid base64url value")
	}
	return decoded, nil
}

func decodeStrictBootstrapJSON(data []byte, maximumBytes int, destination any, allowedFields ...string) error {
	if len(data) == 0 || len(data) > maximumBytes || len(allowedFields) == 0 {
		return errors.New("bounded JSON input is invalid")
	}
	allowed := make(map[string]struct{}, len(allowedFields))
	for _, field := range allowedFields {
		if field == "" {
			return errors.New("strict JSON field configuration is invalid")
		}
		allowed[field] = struct{}{}
	}
	if err := rejectDuplicateBootstrapJSONFields(data, allowed); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("strict JSON input is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("strict JSON input contains trailing data")
	}
	return nil
}

func rejectDuplicateBootstrapJSONFields(data []byte, allowed map[string]struct{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	open, ok := token.(json.Delim)
	if err != nil || !ok || open != '{' {
		return errors.New("strict JSON input must be an object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return errors.New("strict JSON object field is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("strict JSON object contains a duplicate field")
		}
		if _, permitted := allowed[name]; !permitted {
			return errors.New("strict JSON object contains an unknown field")
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return errors.New("strict JSON object value is invalid")
		}
	}
	token, err = decoder.Token()
	close, ok := token.(json.Delim)
	if err != nil || !ok || close != '}' {
		return errors.New("strict JSON object is incomplete")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("strict JSON input contains trailing data")
	}
	return nil
}
