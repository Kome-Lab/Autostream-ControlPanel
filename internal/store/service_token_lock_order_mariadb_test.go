package store

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type safeServiceTokenDiagnostic struct {
	ID          string
	ServiceType string
	Revoked     bool
	RefCount    int
	Category    string
}

func formatSafeServiceTokenDiagnostic(operation string, serviceToken ServiceToken, refCount int, category string) string {
	diagnostic := safeServiceTokenDiagnostic{
		ID:          serviceToken.ID,
		ServiceType: serviceToken.ServiceType,
		Revoked:     serviceToken.RevokedAt != nil,
		RefCount:    refCount,
		Category:    category,
	}
	return fmt.Sprintf(
		"operation=%q token_id=%q service_type=%q revoked=%t ref_count=%d category=%q",
		operation,
		diagnostic.ID,
		diagnostic.ServiceType,
		diagnostic.Revoked,
		diagnostic.RefCount,
		diagnostic.Category,
	)
}

func formatSafeRegisteredServiceDiagnostic(service RegisteredService) string {
	return fmt.Sprintf(
		"service_id=%q service_type=%q token_id=%q staged_previous_token_id=%q staged_token_id=%q status=%q current_stream_id=%q configure_used=%t staged_at_present=%t",
		service.ServiceID,
		service.ServiceType,
		service.TokenID,
		service.StagedNodePreviousTokenID,
		service.StagedNodeTokenID,
		service.Status,
		service.CurrentStreamID,
		service.ConfigureTokenUsedAt != nil,
		service.StagedNodeTokenAt != nil,
	)
}

func formatSafeStagedServiceNodeConfigurationDiagnostic(staged StagedServiceNodeConfiguration) string {
	return fmt.Sprintf(
		"token=(%s) service=(%s) activation_present=%t activation_expires_at=%s",
		formatSafeServiceTokenDiagnostic("stage", staged.Token, 0, "staged"),
		formatSafeRegisteredServiceDiagnostic(staged.Service),
		staged.ActivationToken != "",
		staged.ActivationExpiresAt.UTC().Format(time.RFC3339Nano),
	)
}

func formatSafeSensitiveCompositeDiagnostic(value any) string {
	return fmt.Sprintf("type=%T details=redacted", value)
}

func TestFIX010SafeServiceTokenDiagnosticOmitsSecrets(t *testing.T) {
	now := time.Now().UTC()
	const rawMarker = "FIX010_RAW_TOKEN_MARKER"
	const hashMarker = "FIX010_TOKEN_HASH_MARKER"
	const ciphertextMarker = "FIX010_CIPHERTEXT_MARKER"
	const nonceMarker = "FIX010_NONCE_MARKER"
	const activationMarker = "FIX010_ACTIVATION_MARKER"
	const configureMarker = "FIX010_CONFIGURE_MARKER"
	const scopesMarker = "FIX010_SCOPES_MARKER"
	diagnostic := formatSafeServiceTokenDiagnostic(
		"rotate",
		ServiceToken{
			ID:          "token-safe-id",
			ServiceType: "update_agent",
			RawToken:    rawMarker,
			TokenHash:   hashMarker,
			RevokedAt:   &now,
		},
		2,
		"conflict",
	)
	if strings.Contains(diagnostic, rawMarker) || strings.Contains(diagnostic, hashMarker) {
		t.Fatal("safe token diagnostic included a secret marker")
	}
	if !strings.Contains(diagnostic, `token_id="token-safe-id"`) ||
		!strings.Contains(diagnostic, "revoked=true") {
		t.Fatalf("safe token diagnostic omitted bounded state: %s", diagnostic)
	}
	serviceDiagnostic := formatSafeRegisteredServiceDiagnostic(RegisteredService{
		ServiceID:                     "service-safe-id",
		TokenID:                       "token-safe-id",
		NodeTokenCiphertext:           ciphertextMarker,
		NodeTokenNonce:                nonceMarker,
		StagedNodeTokenHash:           hashMarker,
		StagedNodeTokenScopes:         []string{scopesMarker},
		StagedNodeTokenCiphertext:     ciphertextMarker,
		StagedNodeTokenNonce:          nonceMarker,
		StagedNodeActivationTokenHash: activationMarker,
		ConfigureTokenHash:            configureMarker,
	})
	stagedDiagnostic := formatSafeStagedServiceNodeConfigurationDiagnostic(StagedServiceNodeConfiguration{
		Token: ServiceToken{ID: "token-safe-id", RawToken: rawMarker, TokenHash: hashMarker},
		Service: RegisteredService{
			ServiceID:                     "service-safe-id",
			NodeTokenCiphertext:           ciphertextMarker,
			NodeTokenNonce:                nonceMarker,
			StagedNodeTokenHash:           hashMarker,
			StagedNodeTokenScopes:         []string{scopesMarker},
			StagedNodeTokenCiphertext:     ciphertextMarker,
			StagedNodeTokenNonce:          nonceMarker,
			StagedNodeActivationTokenHash: activationMarker,
			ConfigureTokenHash:            configureMarker,
		},
		ActivationToken:     activationMarker,
		ActivationExpiresAt: now.Add(time.Hour),
	})
	for _, marker := range []string{
		rawMarker,
		hashMarker,
		ciphertextMarker,
		nonceMarker,
		activationMarker,
		configureMarker,
		scopesMarker,
	} {
		if strings.Contains(serviceDiagnostic, marker) || strings.Contains(stagedDiagnostic, marker) {
			t.Fatalf("safe service diagnostic included secret marker category %q", marker)
		}
	}
}

func TestFIX010SafeServiceTokenDiagnosticFailurePathProbe(t *testing.T) {
	now := time.Now().UTC()
	const rawMarker = "FIX010_FAILURE_RAW_TOKEN_MARKER"
	const hashMarker = "FIX010_FAILURE_TOKEN_HASH_MARKER"
	probe, err := os.CreateTemp(t.TempDir(), "fix010-token-diagnostic-*.log")
	if err != nil {
		t.Fatal(err)
	}
	probePath := probe.Name()
	if _, err := fmt.Fprintln(probe, formatSafeServiceTokenDiagnostic(
		"activate",
		ServiceToken{
			ID:          "token-failure-path-id",
			ServiceType: "update_agent",
			RawToken:    rawMarker,
			TokenHash:   hashMarker,
			RevokedAt:   &now,
		},
		1,
		"unexpected_result",
	)); err != nil {
		probe.Close()
		t.Fatal(err)
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(probePath)
	if err != nil {
		t.Fatal(err)
	}
	output := string(contents)
	if !strings.Contains(output, `token_id="token-failure-path-id"`) ||
		!strings.Contains(output, "revoked=true") {
		t.Fatal("failure-path diagnostic omitted bounded token state")
	}
	if strings.Contains(output, rawMarker) || strings.Contains(output, hashMarker) {
		t.Fatal("failure-path diagnostic included a secret marker")
	}
}

func TestFIX010IncidentOwnedDiagnosticsRejectSensitiveWholeValues(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	relativePaths := []string{
		"internal/httpapi/host_agent_configure_policy_e2e_test.go",
		"internal/httpapi/stream_assignment_guard_test.go",
		"internal/httpapi/stream_create_isolation_test.go",
		"internal/httpapi/streams_management_test.go",
		"internal/store/service_assignment_guard_mariadb_test.go",
		"internal/store/service_assignment_guard_test.go",
		"internal/store/service_assignment_lock_order_mariadb_test.go",
		"internal/store/streams_test.go",
		"internal/store/system_updates_mariadb_test.go",
	}

	// Follow every extracted test/helper owner exactly once. These families
	// contain the same original diagnostics and shared fixture types.
	for _, pattern := range []string{"memory_services*_test.go", "service_token_lock_order*_test.go"} {
		paths, err := filepath.Glob(filepath.Join(repositoryRoot, "internal", "store", pattern))
		if err != nil || len(paths) == 0 {
			t.Fatalf("resolve store diagnostic inventory: count=%d err=%v", len(paths), err)
		}
		for _, path := range paths {
			relativePath, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				t.Fatal(err)
			}
			relativePaths = append(relativePaths, filepath.ToSlash(relativePath))
		}
	}

	// The original server_test.go diagnostics now live with their HTTP domains.
	// Parse every extracted matching test and shared helper, so moving a test
	// never removes its sensitive-value flow from this oracle.
	serverTestPaths, err := filepath.Glob(filepath.Join(repositoryRoot, "internal", "httpapi", "server*_test.go"))
	if err != nil || len(serverTestPaths) == 0 {
		t.Fatalf("resolve extracted HTTP diagnostic inventory: count=%d err=%v", len(serverTestPaths), err)
	}
	for _, path := range serverTestPaths {
		relativePath, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			t.Fatal(err)
		}
		relativePaths = append(relativePaths, filepath.ToSlash(relativePath))
	}

	fileSet := token.NewFileSet()
	parsedFiles := make(map[string]*ast.File, len(relativePaths))
	for _, relativePath := range relativePaths {
		parsed, err := parser.ParseFile(fileSet, filepath.Join(repositoryRoot, filepath.FromSlash(relativePath)), nil, 0)
		if err != nil {
			t.Fatalf("parse diagnostic inventory %s: %v", relativePath, err)
		}
		parsedFiles[relativePath] = parsed
	}

	sensitiveTypeNames := map[string]struct{}{
		"ServiceToken":                      {},
		"RegisteredService":                 {},
		"StagedServiceNodeConfiguration":    {},
		"UpdaterStagedConfiguration":        {},
		"mariaDBServiceTokenMutationResult": {},
	}
	for changed := true; changed; {
		changed = false
		for _, parsed := range parsedFiles {
			for _, declaration := range parsed.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || general.Tok != token.TYPE {
					continue
				}
				for _, specification := range general.Specs {
					typeSpec := specification.(*ast.TypeSpec)
					if _, exists := sensitiveTypeNames[typeSpec.Name.Name]; exists {
						continue
					}
					if fix010SensitiveTypeExpression(typeSpec.Type, sensitiveTypeNames) {
						sensitiveTypeNames[typeSpec.Name.Name] = struct{}{}
						changed = true
					}
				}
			}
		}
	}

	for _, relativePath := range relativePaths {
		parsed := parsedFiles[relativePath]
		sensitiveIdentifiers := fix010SensitiveIdentifiers(parsed, sensitiveTypeNames)
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			position := fileSet.Position(call.Pos())
			if formatIndex, formatValue, recognized := fix010FormattingCall(call); recognized {
				arguments := call.Args[formatIndex+1:]
				if formatValue == "" {
					for _, argument := range arguments {
						if fix010SensitiveDiagnosticExpression(argument, sensitiveIdentifiers, sensitiveTypeNames) {
							t.Errorf("%s:%d: non-literal formatting of a sensitive diagnostic value", relativePath, position.Line)
						}
					}
				} else {
					for argumentIndex := range fix010FormattedValueArguments(formatValue) {
						if argumentIndex < len(arguments) && fix010SensitiveDiagnosticExpression(arguments[argumentIndex], sensitiveIdentifiers, sensitiveTypeNames) {
							t.Errorf("%s:%d: sensitive diagnostic value reaches direct formatting", relativePath, position.Line)
						}
					}
				}
			}
			if fix010SensitiveSerializationCall(call) {
				for _, argument := range call.Args {
					if fix010SensitiveDiagnosticExpression(argument, sensitiveIdentifiers, sensitiveTypeNames) {
						t.Errorf("%s:%d: sensitive diagnostic value reaches JSON/diff/spew output", relativePath, position.Line)
					}
				}
			}
			return true
		})
	}
}
