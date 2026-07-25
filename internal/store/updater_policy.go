package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/example/autostream-control-panel/internal/security"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/ssh"
)

const (
	UpdaterGitHubReleaseTokenSecretName = "updater_github_release_token"
	maxUpdaterReleaseTokenBytes         = 4096
)

var (
	ErrConflict                     = errors.New("conflict")
	errUpdaterReleaseTokenIntegrity = errors.New("updater release token integrity check failed")
	updaterPolicyIdentifierPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	updaterPolicyHostIDPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	updaterPolicyLinuxUserPattern   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

type UpdaterPolicy struct {
	UpdaterID                string                `json:"updater_id"`
	Revision                 int64                 `json:"revision"`
	API                      UpdaterPolicyAPI      `json:"api"`
	PollIntervalSeconds      int                   `json:"poll_interval_seconds"`
	HeartbeatIntervalSeconds int                   `json:"heartbeat_interval_seconds"`
	Hosts                    []UpdaterPolicyHost   `json:"hosts"`
	Targets                  []UpdaterPolicyTarget `json:"targets"`
	UpdatedAt                time.Time             `json:"updated_at"`
}

type UpdaterPolicyAPI struct {
	BindHost    string `json:"bind_host"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	SSLEnabled  bool   `json:"ssl_enabled"`
	TLSCertFile string `json:"tls_cert_file,omitempty"`
	TLSKeyFile  string `json:"tls_key_file,omitempty"`
}

type UpdaterPolicyHost struct {
	HostID        string `json:"host_id"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Arch          string `json:"arch"`
	HostPublicKey string `json:"host_public_key"`
}

type UpdaterPolicyTarget struct {
	TargetID       string `json:"target_id"`
	HostID         string `json:"host_id"`
	ServiceType    string `json:"service_type"`
	DeploymentMode string `json:"deployment_mode"`
}

type UpdaterPolicyStore interface {
	GetUpdaterPolicy(ctx context.Context, serviceID string) (UpdaterPolicy, error)
	ListUpdaterPolicies(ctx context.Context) ([]UpdaterPolicy, error)
	SaveUpdaterPolicy(ctx context.Context, serviceID string, expectedRevision int64, input UpdaterPolicy) (UpdaterPolicy, error)
}

type UpdaterPolicyAdminStore interface {
	UpdaterPolicyStore
	SaveUpdaterPolicyAndReleaseToken(ctx context.Context, serviceID string, expectedRevision int64, input UpdaterPolicy, releaseToken *string) (UpdaterPolicy, SecretStatus, error)
	GetUpdaterReleaseTokenStatus(ctx context.Context) (SecretStatus, error)
	GetUpdaterReleaseTokenValue(ctx context.Context) (string, error)
}

type MemoryUpdaterPolicyStore struct {
	mu                 sync.Mutex
	policies           map[string]UpdaterPolicy
	releaseToken       string
	releaseTokenStatus SecretStatus
}

func NewMemoryUpdaterPolicyStore() *MemoryUpdaterPolicyStore {
	return &MemoryUpdaterPolicyStore{policies: map[string]UpdaterPolicy{}}
}

func (s *MemoryUpdaterPolicyStore) GetUpdaterPolicy(ctx context.Context, serviceID string) (UpdaterPolicy, error) {
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, err
	}
	serviceID = strings.TrimSpace(serviceID)
	if !updaterPolicyIdentifierPattern.MatchString(serviceID) {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.policies[serviceID]
	if !ok {
		return UpdaterPolicy{}, ErrNotFound
	}
	return cloneUpdaterPolicy(policy), nil
}

func (s *MemoryUpdaterPolicyStore) ListUpdaterPolicies(ctx context.Context) ([]UpdaterPolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.policies))
	for id := range s.policies {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	policies := make([]UpdaterPolicy, 0, len(ids))
	for _, id := range ids {
		policies = append(policies, cloneUpdaterPolicy(s.policies[id]))
	}
	return policies, nil
}

func (s *MemoryUpdaterPolicyStore) SaveUpdaterPolicy(ctx context.Context, serviceID string, expectedRevision int64, input UpdaterPolicy) (UpdaterPolicy, error) {
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, err
	}
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return UpdaterPolicy{}, ErrConflict
	}
	normalized, err := normalizeUpdaterPolicy(serviceID, input)
	if err != nil {
		return UpdaterPolicy{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.policies[normalized.UpdaterID]
	if (!exists && expectedRevision != 0) || (exists && current.Revision != expectedRevision) {
		return UpdaterPolicy{}, ErrConflict
	}
	now := time.Now().UTC()
	if exists && !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
	}
	normalized.Revision = expectedRevision + 1
	normalized.UpdatedAt = now
	s.policies[normalized.UpdaterID] = cloneUpdaterPolicy(normalized)
	return cloneUpdaterPolicy(normalized), nil
}

func (s *MemoryUpdaterPolicyStore) SaveUpdaterPolicyAndReleaseToken(
	ctx context.Context,
	serviceID string,
	expectedRevision int64,
	input UpdaterPolicy,
	releaseToken *string,
) (UpdaterPolicy, SecretStatus, error) {
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return UpdaterPolicy{}, SecretStatus{}, ErrConflict
	}
	normalized, err := normalizeUpdaterPolicy(serviceID, input)
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	normalizedToken, err := normalizeUpdaterReleaseToken(releaseToken)
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	current, exists := s.policies[normalized.UpdaterID]
	if (!exists && expectedRevision != 0) || (exists && current.Revision != expectedRevision) {
		return UpdaterPolicy{}, SecretStatus{}, ErrConflict
	}
	now := time.Now().UTC()
	if exists && !now.After(current.UpdatedAt) {
		now = current.UpdatedAt.Add(time.Nanosecond)
	}
	normalized.Revision = expectedRevision + 1
	normalized.UpdatedAt = now

	tokenStatus := s.releaseTokenStatus
	tokenStatus.Name = UpdaterGitHubReleaseTokenSecretName
	if normalizedToken != nil {
		if *normalizedToken == "" {
			s.releaseToken = ""
			s.releaseTokenStatus = SecretStatus{}
			tokenStatus = SecretStatus{
				Name:      UpdaterGitHubReleaseTokenSecretName,
				UpdatedAt: now.Format(time.RFC3339),
			}
		} else {
			s.releaseToken = *normalizedToken
			s.releaseTokenStatus = SecretStatus{
				Name:        UpdaterGitHubReleaseTokenSecretName,
				Configured:  true,
				Fingerprint: security.SecretFingerprint(*normalizedToken),
				UpdatedAt:   now.Format(time.RFC3339),
			}
			tokenStatus = s.releaseTokenStatus
		}
	}
	s.policies[normalized.UpdaterID] = cloneUpdaterPolicy(normalized)
	return cloneUpdaterPolicy(normalized), tokenStatus, nil
}

func (s *MemoryUpdaterPolicyStore) GetUpdaterReleaseTokenStatus(ctx context.Context) (SecretStatus, error) {
	if err := ctx.Err(); err != nil {
		return SecretStatus{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.releaseTokenStatus
	status.Name = UpdaterGitHubReleaseTokenSecretName
	return status, nil
}

func (s *MemoryUpdaterPolicyStore) GetUpdaterReleaseTokenValue(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.releaseToken == "" {
		return "", ErrNotFound
	}
	return s.releaseToken, nil
}

type MariaDBUpdaterPolicyStore struct {
	db          *sql.DB
	keyMaterial string
}

func NewMariaDBUpdaterPolicyStore(db *sql.DB) MariaDBUpdaterPolicyStore {
	return MariaDBUpdaterPolicyStore{db: db}
}

func NewMariaDBUpdaterPolicyAdminStore(db *sql.DB, keyMaterial string) MariaDBUpdaterPolicyStore {
	return MariaDBUpdaterPolicyStore{db: db, keyMaterial: keyMaterial}
}

func (s MariaDBUpdaterPolicyStore) GetUpdaterPolicy(ctx context.Context, serviceID string) (UpdaterPolicy, error) {
	serviceID = strings.TrimSpace(serviceID)
	if !updaterPolicyIdentifierPattern.MatchString(serviceID) {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	var (
		revision  int64
		body      []byte
		updatedAt time.Time
	)
	err := s.db.QueryRowContext(ctx, `SELECT revision, policy_json, updated_at FROM update_agent_policies WHERE service_id = ?`, serviceID).Scan(&revision, &body, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdaterPolicy{}, ErrNotFound
	}
	if err != nil {
		return UpdaterPolicy{}, err
	}
	return decodeUpdaterPolicy(serviceID, revision, body, updatedAt)
}

func (s MariaDBUpdaterPolicyStore) ListUpdaterPolicies(ctx context.Context) ([]UpdaterPolicy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT service_id, revision, policy_json, updated_at FROM update_agent_policies ORDER BY service_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	policies := []UpdaterPolicy{}
	for rows.Next() {
		var (
			serviceID string
			revision  int64
			body      []byte
			updatedAt time.Time
		)
		if err := rows.Scan(&serviceID, &revision, &body, &updatedAt); err != nil {
			return nil, err
		}
		policy, err := decodeUpdaterPolicy(serviceID, revision, body, updatedAt)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].UpdaterID < policies[j].UpdaterID
	})
	return policies, nil
}

func (s MariaDBUpdaterPolicyStore) SaveUpdaterPolicy(ctx context.Context, serviceID string, expectedRevision int64, input UpdaterPolicy) (UpdaterPolicy, error) {
	normalized, body, err := prepareUpdaterPolicySave(serviceID, expectedRevision, input)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if err := saveUpdaterPolicyCAS(ctx, s.db, expectedRevision, normalized, body); err != nil {
		return UpdaterPolicy{}, err
	}
	return normalized, nil
}

func (s MariaDBUpdaterPolicyStore) SaveUpdaterPolicyAndReleaseToken(
	ctx context.Context,
	serviceID string,
	expectedRevision int64,
	input UpdaterPolicy,
	releaseToken *string,
) (UpdaterPolicy, SecretStatus, error) {
	normalized, body, err := prepareUpdaterPolicySave(serviceID, expectedRevision, input)
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	normalizedToken, err := normalizeUpdaterReleaseToken(releaseToken)
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}

	var (
		tokenCiphertext string
		tokenNonce      string
	)
	if normalizedToken != nil && *normalizedToken != "" {
		if s.keyMaterial == "" {
			return UpdaterPolicy{}, SecretStatus{}, ErrSecretKeyRequired
		}
		tokenCiphertext, tokenNonce, err = security.EncryptSecret(*normalizedToken, s.keyMaterial)
		if err != nil {
			return UpdaterPolicy{}, SecretStatus{}, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	defer tx.Rollback()

	if err := saveUpdaterPolicyCAS(ctx, tx, expectedRevision, normalized, body); err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}

	var tokenStatus SecretStatus
	switch {
	case normalizedToken == nil:
		_, tokenStatus, err = readUpdaterReleaseToken(ctx, tx, s.keyMaterial)
		if errors.Is(err, ErrNotFound) {
			err = nil
			tokenStatus = SecretStatus{Name: UpdaterGitHubReleaseTokenSecretName}
		}
	case *normalizedToken == "":
		_, err = tx.ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, UpdaterGitHubReleaseTokenSecretName)
		tokenStatus = SecretStatus{
			Name:      UpdaterGitHubReleaseTokenSecretName,
			UpdatedAt: normalized.UpdatedAt.Format(time.RFC3339),
		}
	default:
		tokenStatus = SecretStatus{
			Name:        UpdaterGitHubReleaseTokenSecretName,
			Configured:  true,
			Fingerprint: security.SecretFingerprint(*normalizedToken),
			UpdatedAt:   normalized.UpdatedAt.Format(time.RFC3339),
		}
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO secrets (name, ciphertext, nonce, value_hash, updated_at) VALUES (?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE ciphertext = VALUES(ciphertext), nonce = VALUES(nonce), value_hash = VALUES(value_hash), updated_at = VALUES(updated_at)`,
			UpdaterGitHubReleaseTokenSecretName,
			tokenCiphertext,
			tokenNonce,
			tokenStatus.Fingerprint,
			normalized.UpdatedAt,
		)
	}
	if err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	if err := tx.Commit(); err != nil {
		return UpdaterPolicy{}, SecretStatus{}, err
	}
	return normalized, tokenStatus, nil
}

func (s MariaDBUpdaterPolicyStore) GetUpdaterReleaseTokenStatus(ctx context.Context) (SecretStatus, error) {
	_, status, err := readUpdaterReleaseToken(ctx, s.db, s.keyMaterial)
	if errors.Is(err, ErrNotFound) {
		return SecretStatus{Name: UpdaterGitHubReleaseTokenSecretName}, nil
	}
	return status, err
}

func (s MariaDBUpdaterPolicyStore) GetUpdaterReleaseTokenValue(ctx context.Context) (string, error) {
	value, _, err := readUpdaterReleaseToken(ctx, s.db, s.keyMaterial)
	return value, err
}

type updaterPolicyExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type updaterPolicyQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func prepareUpdaterPolicySave(serviceID string, expectedRevision int64, input UpdaterPolicy) (UpdaterPolicy, []byte, error) {
	if expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return UpdaterPolicy{}, nil, ErrConflict
	}
	normalized, err := normalizeUpdaterPolicy(serviceID, input)
	if err != nil {
		return UpdaterPolicy{}, nil, err
	}
	normalized.Revision = expectedRevision + 1
	normalized.UpdatedAt = time.Now().UTC()
	body, err := json.Marshal(normalized)
	if err != nil {
		return UpdaterPolicy{}, nil, err
	}
	return normalized, body, nil
}

func saveUpdaterPolicyCAS(ctx context.Context, execer updaterPolicyExecer, expectedRevision int64, policy UpdaterPolicy, body []byte) error {
	if expectedRevision == 0 {
		_, err := execer.ExecContext(
			ctx,
			`INSERT INTO update_agent_policies (service_id, revision, policy_json, updated_at) VALUES (?, ?, ?, ?)`,
			policy.UpdaterID,
			policy.Revision,
			body,
			policy.UpdatedAt,
		)
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return ErrConflict
			}
			return err
		}
		return nil
	}
	result, err := execer.ExecContext(
		ctx,
		`UPDATE update_agent_policies SET revision = ?, policy_json = ?, updated_at = ? WHERE service_id = ? AND revision = ?`,
		policy.Revision,
		body,
		policy.UpdatedAt,
		policy.UpdaterID,
		expectedRevision,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrConflict
	}
	return nil
}

func readUpdaterReleaseToken(ctx context.Context, queryer updaterPolicyQueryer, keyMaterial string) (string, SecretStatus, error) {
	var (
		ciphertext string
		nonce      string
		status     = SecretStatus{Name: UpdaterGitHubReleaseTokenSecretName}
		updatedAt  time.Time
	)
	err := queryer.QueryRowContext(
		ctx,
		`SELECT ciphertext, nonce, value_hash, updated_at FROM secrets WHERE name = ?`,
		UpdaterGitHubReleaseTokenSecretName,
	).Scan(&ciphertext, &nonce, &status.Fingerprint, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", status, ErrNotFound
	}
	if err != nil {
		return "", SecretStatus{}, err
	}
	if keyMaterial == "" {
		return "", SecretStatus{}, ErrSecretKeyRequired
	}
	value, err := security.DecryptSecret(ciphertext, nonce, keyMaterial)
	if err != nil {
		return "", SecretStatus{}, err
	}
	if !validStoredUpdaterReleaseToken(value) || security.SecretFingerprint(value) != status.Fingerprint {
		return "", SecretStatus{}, errUpdaterReleaseTokenIntegrity
	}
	status.Configured = true
	status.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return value, status, nil
}

func normalizeUpdaterReleaseToken(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if len(normalized) > maxUpdaterReleaseTokenBytes {
		return nil, ErrInvalidSettings
	}
	for _, char := range []byte(normalized) {
		if char < 0x21 || char > 0x7e {
			return nil, ErrInvalidSettings
		}
	}
	return &normalized, nil
}

func validStoredUpdaterReleaseToken(value string) bool {
	normalized, err := normalizeUpdaterReleaseToken(&value)
	return err == nil && normalized != nil && *normalized != "" && *normalized == value
}

func normalizeUpdaterPolicy(serviceID string, input UpdaterPolicy) (UpdaterPolicy, error) {
	serviceID = strings.TrimSpace(serviceID)
	input.UpdaterID = strings.TrimSpace(input.UpdaterID)
	if !updaterPolicyIdentifierPattern.MatchString(serviceID) ||
		(input.UpdaterID != "" && input.UpdaterID != serviceID) {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	input.UpdaterID = serviceID

	input.API.BindHost = strings.Trim(strings.TrimSpace(input.API.BindHost), "[]")
	input.API.Host = strings.Trim(strings.TrimSpace(input.API.Host), "[]")
	input.API.TLSCertFile = strings.TrimSpace(input.API.TLSCertFile)
	input.API.TLSKeyFile = strings.TrimSpace(input.API.TLSKeyFile)
	if input.API.BindHost == "" {
		input.API.BindHost = "127.0.0.1"
	}
	if input.API.Host == "" {
		input.API.Host = "127.0.0.1"
	}
	if input.API.Port == 0 {
		input.API.Port = 8090
	}
	if !validUpdaterPolicyHost(input.API.BindHost) || !validUpdaterPolicyHost(input.API.Host) || input.API.Port < 1 || input.API.Port > 65535 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	if input.API.SSLEnabled {
		if !safeUpdaterPolicyAbsolutePath(input.API.TLSCertFile) || !safeUpdaterPolicyAbsolutePath(input.API.TLSKeyFile) {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
	} else {
		if !updaterPolicyLoopbackHost(input.API.BindHost) || !updaterPolicyLoopbackHost(input.API.Host) {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		input.API.TLSCertFile = ""
		input.API.TLSKeyFile = ""
	}

	if input.PollIntervalSeconds == 0 {
		input.PollIntervalSeconds = 15
	}
	if input.HeartbeatIntervalSeconds == 0 {
		input.HeartbeatIntervalSeconds = 30
	}
	if input.PollIntervalSeconds < 5 || input.PollIntervalSeconds > 3600 ||
		input.HeartbeatIntervalSeconds < 5 || input.HeartbeatIntervalSeconds > 60 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	if len(input.Hosts) == 0 || len(input.Hosts) > 128 || len(input.Targets) == 0 || len(input.Targets) > 1024 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}

	hosts := make(map[string]bool, len(input.Hosts))
	hostReferences := make(map[string]int, len(input.Hosts))
	for i := range input.Hosts {
		host := &input.Hosts[i]
		host.HostID = strings.TrimSpace(host.HostID)
		host.Name = strings.TrimSpace(host.Name)
		host.Address = strings.Trim(strings.TrimSpace(host.Address), "[]")
		host.User = strings.TrimSpace(host.User)
		host.Arch = strings.TrimSpace(host.Arch)
		host.HostPublicKey = strings.TrimSpace(host.HostPublicKey)
		canonicalHostPublicKey, validHostPublicKey := normalizeUpdaterPolicyHostPublicKey(host.HostPublicKey)
		if !updaterPolicyHostIDPattern.MatchString(host.HostID) || hosts[host.HostID] ||
			host.Name == "" || len([]rune(host.Name)) > 128 || updaterPolicyContainsControl(host.Name) ||
			!validUpdaterPolicyHost(host.Address) || host.Port < 1 || host.Port > 65535 ||
			!updaterPolicyLinuxUserPattern.MatchString(host.User) || host.User == "root" ||
			(host.Arch != "amd64" && host.Arch != "arm64") ||
			!validHostPublicKey {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		host.HostPublicKey = canonicalHostPublicKey
		hosts[host.HostID] = true
	}

	targets := make(map[string]bool, len(input.Targets))
	for i := range input.Targets {
		target := &input.Targets[i]
		target.TargetID = strings.TrimSpace(target.TargetID)
		target.HostID = strings.TrimSpace(target.HostID)
		target.ServiceType = strings.TrimSpace(target.ServiceType)
		target.DeploymentMode = strings.TrimSpace(target.DeploymentMode)
		if !updaterPolicyIdentifierPattern.MatchString(target.TargetID) || targets[target.TargetID] ||
			!updaterPolicyHostIDPattern.MatchString(target.HostID) || !hosts[target.HostID] ||
			!validUpdaterPolicyServiceType(target.ServiceType) ||
			(target.DeploymentMode != "systemd" && target.DeploymentMode != "docker") {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
		targets[target.TargetID] = true
		hostReferences[target.HostID]++
	}
	for hostID := range hosts {
		if hostReferences[hostID] == 0 {
			return UpdaterPolicy{}, ErrInvalidSettings
		}
	}

	input.Revision = 0
	input.UpdatedAt = time.Time{}
	return cloneUpdaterPolicy(input), nil
}

// NormalizeUpdaterPolicy validates and canonicalizes the declarative policy
// without assigning a database revision or update timestamp.
func NormalizeUpdaterPolicy(serviceID string, input UpdaterPolicy) (UpdaterPolicy, error) {
	return normalizeUpdaterPolicy(serviceID, input)
}

func decodeUpdaterPolicy(serviceID string, revision int64, body []byte, updatedAt time.Time) (UpdaterPolicy, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var policy UpdaterPolicy
	if err := decoder.Decode(&policy); err != nil {
		return UpdaterPolicy{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return UpdaterPolicy{}, errors.New("updater policy contains trailing data")
	}
	normalized, err := normalizeUpdaterPolicy(serviceID, policy)
	if err != nil {
		return UpdaterPolicy{}, err
	}
	if revision < 1 {
		return UpdaterPolicy{}, ErrInvalidSettings
	}
	normalized.Revision = revision
	normalized.UpdatedAt = updatedAt.UTC()
	return normalized, nil
}

func cloneUpdaterPolicy(policy UpdaterPolicy) UpdaterPolicy {
	policy.Hosts = append([]UpdaterPolicyHost(nil), policy.Hosts...)
	policy.Targets = append([]UpdaterPolicyTarget(nil), policy.Targets...)
	return policy
}

func validUpdaterPolicyHost(value string) bool {
	if value == "" || len(value) > 253 || updaterPolicyContainsControl(value) || strings.ContainsAny(value, " /\\@?#[]") {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, char := range label {
			if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func updaterPolicyLoopbackHost(value string) bool {
	if strings.EqualFold(value, "localhost") {
		return true
	}
	ip := net.ParseIP(value)
	return ip != nil && ip.IsLoopback()
}

func safeUpdaterPolicyAbsolutePath(value string) bool {
	return path.IsAbs(value) && path.Clean(value) != "/" && !updaterPolicyContainsControl(value) && !strings.ContainsRune(value, '\\')
}

func updaterPolicyContainsControl(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

func normalizeUpdaterPolicyHostPublicKey(value string) (string, bool) {
	if value == "" || len(value) > 16*1024 || updaterPolicyContainsControl(value) {
		return "", false
	}
	publicKey, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(value))
	if err != nil || publicKey.Type() != ssh.KeyAlgoED25519 || comment != "" || len(options) != 0 || len(rest) != 0 {
		return "", false
	}
	canonical := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(publicKey)), "\n")
	if value != canonical {
		return "", false
	}
	return canonical, true
}

func validUpdaterPolicyServiceType(value string) bool {
	switch value {
	case "control_panel", "worker", "encoder_recorder", "discord_bot", "observability":
		return true
	default:
		return false
	}
}

var (
	_ UpdaterPolicyStore      = (*MemoryUpdaterPolicyStore)(nil)
	_ UpdaterPolicyAdminStore = (*MemoryUpdaterPolicyStore)(nil)
	_ UpdaterPolicyStore      = MariaDBUpdaterPolicyStore{}
	_ UpdaterPolicyAdminStore = MariaDBUpdaterPolicyStore{}
)
