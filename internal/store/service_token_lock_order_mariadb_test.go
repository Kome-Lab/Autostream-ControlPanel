package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/autostream-control-panel/internal/database"
	"github.com/example/autostream-control-panel/internal/security"
	"github.com/go-sql-driver/mysql"
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
		"internal/httpapi/server_test.go",
		"internal/httpapi/stream_assignment_guard_test.go",
		"internal/httpapi/stream_create_isolation_test.go",
		"internal/httpapi/streams_management_test.go",
		"internal/store/memory_services_test.go",
		"internal/store/service_assignment_guard_mariadb_test.go",
		"internal/store/service_assignment_guard_test.go",
		"internal/store/service_assignment_lock_order_mariadb_test.go",
		"internal/store/service_token_lock_order_fix005_mariadb_test.go",
		"internal/store/service_token_lock_order_fix007_oracle_test.go",
		"internal/store/service_token_lock_order_mariadb_test.go",
		"internal/store/streams_test.go",
		"internal/store/system_updates_mariadb_test.go",
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

func fix010SensitiveTypeExpression(expression ast.Expr, sensitiveTypeNames map[string]struct{}) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		_, sensitive := sensitiveTypeNames[value.Name]
		return sensitive
	case *ast.SelectorExpr:
		_, sensitive := sensitiveTypeNames[value.Sel.Name]
		return sensitive
	case *ast.ArrayType:
		return fix010SensitiveTypeExpression(value.Elt, sensitiveTypeNames)
	case *ast.MapType:
		return fix010SensitiveTypeExpression(value.Key, sensitiveTypeNames) ||
			fix010SensitiveTypeExpression(value.Value, sensitiveTypeNames)
	case *ast.StarExpr:
		return fix010SensitiveTypeExpression(value.X, sensitiveTypeNames)
	case *ast.StructType:
		for _, field := range value.Fields.List {
			if fix010SensitiveTypeExpression(field.Type, sensitiveTypeNames) {
				return true
			}
		}
	}
	return false
}

func fix010SensitiveIdentifiers(parsed *ast.File, sensitiveTypeNames map[string]struct{}) map[*ast.Object]struct{} {
	identifiers := make(map[*ast.Object]struct{})
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.ValueSpec:
			if value.Type != nil && fix010SensitiveTypeExpression(value.Type, sensitiveTypeNames) {
				for _, name := range value.Names {
					if name.Obj != nil {
						identifiers[name.Obj] = struct{}{}
					}
				}
			}
		case *ast.Field:
			if fix010SensitiveTypeExpression(value.Type, sensitiveTypeNames) {
				for _, name := range value.Names {
					if name.Obj != nil {
						identifiers[name.Obj] = struct{}{}
					}
				}
			}
		}
		return true
	})
	for changed := true; changed; {
		changed = false
		ast.Inspect(parsed, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			if len(assignment.Rhs) == 1 {
				if call, ok := assignment.Rhs[0].(*ast.CallExpr); ok {
					for resultIndex := range fix010SensitiveCallResultPositions(call) {
						if resultIndex < len(assignment.Lhs) && fix010MarkSensitiveIdentifier(assignment.Lhs[resultIndex], identifiers) {
							changed = true
						}
					}
				}
			}
			for index, right := range assignment.Rhs {
				if index < len(assignment.Lhs) &&
					fix010SensitiveDiagnosticExpression(right, identifiers, sensitiveTypeNames) &&
					fix010MarkSensitiveIdentifier(assignment.Lhs[index], identifiers) {
					changed = true
				}
			}
			return true
		})
	}
	return identifiers
}

func fix010MarkSensitiveIdentifier(expression ast.Expr, identifiers map[*ast.Object]struct{}) bool {
	name, ok := expression.(*ast.Ident)
	if !ok || name.Name == "_" || name.Obj == nil {
		return false
	}
	if _, exists := identifiers[name.Obj]; exists {
		return false
	}
	identifiers[name.Obj] = struct{}{}
	return true
}

func fix010SensitiveCallResultPositions(call *ast.CallExpr) map[int]struct{} {
	name := ""
	switch function := call.Fun.(type) {
	case *ast.Ident:
		name = function.Name
	case *ast.SelectorExpr:
		name = function.Sel.Name
	}
	positions := make(map[int]struct{})
	switch name {
	case "CreateServiceToken", "RotateServiceToken", "AuthenticateServiceToken", "ListServiceTokens",
		"StageServiceNodeConfiguration",
		"createMariaDBServiceTokenPairService", "receiveMariaDBServiceTokenMutation",
		"registerPullSystemUpdateAgentForOwnershipTest":
		positions[0] = struct{}{}
	case "RotateServiceNodeToken", "ConfigureServiceNode", "ActivateServiceNodeConfiguration":
		positions[0] = struct{}{}
		positions[1] = struct{}{}
	case "PrecreateService", "RegisterService", "Heartbeat", "UpdateServiceRuntimeReport",
		"SetServiceConfigureToken", "ConsumeServiceConfigureToken", "SetServiceNodeTokenSecret",
		"ListServices", "ListWorkers", "GetService", "UpdateServiceMetadata",
		"AssignServiceToStream", "AssignServiceToStreamWithRole", "UnassignServiceFromStream",
		"AssignServiceToStreamGuarded", "UnassignServiceFromStreamGuarded",
		"ListStreamAssignments", "RequestServiceRestart":
		positions[0] = struct{}{}
	}
	return positions
}

func fix010SensitiveDiagnosticExpression(expression ast.Expr, sensitiveIdentifiers map[*ast.Object]struct{}, sensitiveTypeNames map[string]struct{}) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		if value.Obj != nil {
			if _, sensitive := sensitiveIdentifiers[value.Obj]; sensitive {
				return true
			}
		}
		return fix010SensitiveDiagnosticName(value.Name)
	case *ast.SelectorExpr:
		if fix010SafeDiagnosticScalarName(value.Sel.Name) {
			return false
		}
		if value.Sel.Name == "MutationOutcome" || fix010SensitiveDiagnosticName(value.Sel.Name) {
			return true
		}
		if value.Sel.Name == "Config" {
			if base, ok := value.X.(*ast.Ident); ok {
				return fix010SensitiveDiagnosticExpression(base, sensitiveIdentifiers, sensitiveTypeNames)
			}
		}
		return false
	case *ast.IndexExpr:
		return fix010SensitiveDiagnosticExpression(value.X, sensitiveIdentifiers, sensitiveTypeNames)
	case *ast.IndexListExpr:
		return fix010SensitiveDiagnosticExpression(value.X, sensitiveIdentifiers, sensitiveTypeNames)
	case *ast.ParenExpr:
		return fix010SensitiveDiagnosticExpression(value.X, sensitiveIdentifiers, sensitiveTypeNames)
	case *ast.StarExpr:
		return fix010SensitiveDiagnosticExpression(value.X, sensitiveIdentifiers, sensitiveTypeNames)
	case *ast.UnaryExpr:
		return fix010SensitiveDiagnosticExpression(value.X, sensitiveIdentifiers, sensitiveTypeNames)
	case *ast.CompositeLit:
		return fix010SensitiveTypeExpression(value.Type, sensitiveTypeNames)
	}
	return false
}

func fix010SensitiveDiagnosticName(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "valid") && strings.HasSuffix(lower, "token") {
		return false
	}
	if strings.HasPrefix(lower, "has") {
		return false
	}
	for _, safeSuffix := range []string{"id", "ids", "count", "counts", "revoked", "type", "types"} {
		if strings.HasSuffix(lower, safeSuffix) {
			return false
		}
	}
	if lower == "token" || strings.HasSuffix(lower, "token") || strings.HasSuffix(lower, "tokens") ||
		lower == "staged" || lower == "mutationoutcome" || lower == "rotation" {
		return true
	}
	return strings.Contains(lower, "rawtoken") ||
		strings.Contains(lower, "tokenhash") ||
		strings.Contains(lower, "ciphertext") ||
		strings.Contains(lower, "nonce") ||
		strings.Contains(lower, "activationtoken") ||
		strings.Contains(lower, "configuretoken")
}

func fix010SafeDiagnosticScalarName(name string) bool {
	switch name {
	case "ID", "ServiceID", "TokenID", "StagedNodePreviousTokenID", "StagedNodeTokenID",
		"ServiceType", "Status", "CurrentStreamID", "RevokedAt", "CreatedAt", "UpdatedAt":
		return true
	}
	return false
}

func fix010FormattingCall(call *ast.CallExpr) (int, string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || strings.HasPrefix(selector.Sel.Name, "formatSafe") {
		return 0, "", false
	}
	formatIndex := 0
	switch selector.Sel.Name {
	case "Fatalf", "Errorf", "Logf", "Sprintf", "Printf":
	case "Fprintf":
		formatIndex = 1
	default:
		return 0, "", false
	}
	if len(call.Args) <= formatIndex {
		return 0, "", false
	}
	literal, ok := call.Args[formatIndex].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return formatIndex, "", true
	}
	formatValue, err := strconv.Unquote(literal.Value)
	if err != nil {
		return formatIndex, "", true
	}
	return formatIndex, formatValue, true
}

func fix010FormattedValueArguments(format string) map[int]struct{} {
	arguments := make(map[int]struct{})
	nextArgument := 0
	for index := 0; index < len(format); index++ {
		if format[index] != '%' {
			continue
		}
		index++
		if index >= len(format) || format[index] == '%' {
			continue
		}
		if format[index] == '[' {
			if explicit, end, ok := fix010PrintfIndex(format, index); ok {
				nextArgument = explicit
				index = end
			}
		}
		for index < len(format) && strings.ContainsRune("#0+- '", rune(format[index])) {
			index++
		}
		for index < len(format) && format[index] >= '0' && format[index] <= '9' {
			index++
		}
		if index < len(format) && format[index] == '*' {
			nextArgument++
			index++
		}
		if index < len(format) && format[index] == '.' {
			index++
			for index < len(format) && format[index] >= '0' && format[index] <= '9' {
				index++
			}
			if index < len(format) && format[index] == '*' {
				nextArgument++
				index++
			}
		}
		if index < len(format) && format[index] == '[' {
			if explicit, end, ok := fix010PrintfIndex(format, index); ok {
				nextArgument = explicit
				index = end
			}
		}
		arguments[nextArgument] = struct{}{}
		nextArgument++
	}
	return arguments
}

func fix010PrintfIndex(format string, start int) (int, int, bool) {
	end := start + 1
	for end < len(format) && format[end] >= '0' && format[end] <= '9' {
		end++
	}
	if end == start+1 || end >= len(format) || format[end] != ']' {
		return 0, start, false
	}
	value, err := strconv.Atoi(format[start+1 : end])
	if err != nil || value < 1 {
		return 0, start, false
	}
	return value - 1, end + 1, true
}

func fix010SensitiveSerializationCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, _ := selector.X.(*ast.Ident)
	if packageName == nil {
		return false
	}
	switch packageName.Name {
	case "json":
		return selector.Sel.Name == "Marshal" || selector.Sel.Name == "MarshalIndent"
	case "spew":
		return true
	case "cmp":
		return selector.Sel.Name == "Diff"
	case "assert", "require":
		return selector.Sel.Name == "Equal" || selector.Sel.Name == "ElementsMatch"
	}
	return false
}

func TestMariaDBServiceTokenMutationsLockServiceBeforeToken(t *testing.T) {
	sourceBytes, err := os.ReadFile("services.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)

	for _, test := range []struct {
		name           string
		next           string
		lockHelper     string
		mutationPhases []string
	}{
		{name: "RevokeServiceToken", next: "RotateServiceToken", lockHelper: "lockMariaDBServiceTokenMutationRetryable(", mutationPhases: []string{"revokeServiceTokenInTx("}},
		{name: "RotateServiceToken", next: "RotateServiceNodeToken", lockHelper: "lockMariaDBServiceTokenMutationRetryable(", mutationPhases: []string{"INSERT INTO service_tokens"}},
		{name: "RotateServiceNodeToken", next: "requiresStagedNodeTokenRotation", mutationPhases: []string{"INSERT INTO service_tokens"}},
		{name: "ConfigureServiceNode", next: "StageServiceNodeConfiguration", mutationPhases: []string{"INSERT INTO service_tokens"}},
		{name: "stageServiceNodeConfigurationWithReferences", next: "ActivateServiceNodeConfiguration", lockHelper: "lockMariaDBServiceTokenMutationRetryable("},
		{name: "activateServiceNodeConfigurationWithReferences", next: "SetServiceNodeTokenSecret", lockHelper: "lockMariaDBServiceTokenMutationRetryable(", mutationPhases: []string{"INSERT INTO service_tokens"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := mariaDBServiceTokenFunctionSource(t, source, test.name, test.next)
			lockHelper := test.lockHelper
			if lockHelper == "" {
				lockHelper = "lockMariaDBServiceTokenMutation("
			}
			lockPhase := strings.Index(body, lockHelper)
			if lockPhase < 0 {
				t.Fatalf("%s does not use the canonical service-token lock helper", test.name)
			}
			if directTokenLock := firstSourceIndex(body,
				"FROM service_tokens WHERE id = ? FOR UPDATE",
				"selectActiveServiceTokenForUpdate(",
				"selectServiceTokenForNodeConfiguration(",
			); directTokenLock >= 0 {
				t.Fatalf("%s bypasses the canonical service-token lock helper", test.name)
			}
			if mutationPhase := firstSourceIndex(body, test.mutationPhases...); mutationPhase >= 0 && lockPhase >= mutationPhase {
				t.Fatalf("%s mutates a token before the canonical lock helper", test.name)
			}
		})
	}

	helper := mariaDBServiceTokenFunctionSource(
		t,
		source,
		"lockMariaDBServiceTokenMutation",
		"mariaDBServiceTokenReferenceContains",
	)
	servicePhase := strings.Index(helper, "lockMariaDBServicesSorted(")
	tokenPhase := strings.Index(helper, "lockMariaDBServiceTokensSorted(")
	revalidationPhase := strings.Index(helper, "discoverMariaDBServiceTokenReferences(ctx, tx, tokenIDs)")
	if servicePhase < 0 || tokenPhase < 0 || revalidationPhase < 0 ||
		servicePhase >= tokenPhase || tokenPhase >= revalidationPhase {
		t.Fatalf("canonical helper order service=%d token=%d revalidation=%d", servicePhase, tokenPhase, revalidationPhase)
	}
}

func TestFIX009GenericTokenMutationsUseOnlyDiscoveredServiceRows(t *testing.T) {
	sourceBytes, err := os.ReadFile("services.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)

	for _, test := range []struct {
		name string
		next string
	}{
		{name: "RevokeServiceToken", next: "RotateServiceToken"},
		{name: "RotateServiceToken", next: "RotateServiceNodeToken"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := mariaDBServiceTokenFunctionSource(t, source, test.name, test.next)
			if strings.Contains(body, "WHERE token_id = ?") {
				t.Fatalf("%s still scans or mutates future service bindings after locking the token", test.name)
			}
			if !strings.Contains(body, "WHERE service_id = ? AND token_id = ?") {
				t.Fatalf("%s does not constrain service mutation to an exact discovered service ID", test.name)
			}
			if !strings.Contains(body, "errMariaDBServiceTokenReferenceSetChanged") {
				t.Fatalf("%s does not retry a committed binding-set change", test.name)
			}
		})
	}
}

func TestMariaDBRuntimeTokenMutationsLockServiceBeforeToken(t *testing.T) {
	sourceBytes, err := os.ReadFile("system_update_runtime_token_rotations_mariadb.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)

	for _, test := range []struct {
		name string
		next string
	}{
		{name: "stageSystemUpdateRuntimeTokenRotationOnce", next: "isMariaDBRuntimeTokenRotationDeadlock"},
		{name: "ClaimSystemUpdateRuntimeTokenRotationStagedCredential", next: "mariaDBRuntimeTokenRotationPolicyForUpdate"},
		{name: "MarkSystemUpdateRuntimeTokenRotationLocalStaged", next: "ProveSystemUpdateRuntimeTokenRotationHeartbeat"},
		{name: "ProveSystemUpdateRuntimeTokenRotationHeartbeat", next: "ActivateSystemUpdateRuntimeTokenRotation"},
		{name: "ActivateSystemUpdateRuntimeTokenRotation", next: "CancelSystemUpdateRuntimeTokenRotation"},
		{name: "CancelSystemUpdateRuntimeTokenRotation", next: "AcknowledgeSystemUpdateRuntimeTokenRotationCancel"},
		{name: "AcknowledgeSystemUpdateRuntimeTokenRotationCancel", next: "EmergencyRevokeSystemUpdateRuntimeToken"},
		{name: "EmergencyRevokeSystemUpdateRuntimeToken", next: "mariaDBRuntimeTokenRotationForTransition"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := mariaDBRuntimeTokenFunctionSource(t, source, test.name, test.next)
			lockPhase := strings.Index(body, "lockMariaDBRuntimeTokenRotationPlan(")
			if lockPhase < 0 {
				t.Fatalf("%s does not use the canonical runtime service-token lock plan", test.name)
			}
			if tokenPhase := firstSourceIndex(
				body,
				"selectActiveServiceTokenForUpdate(",
				"mariaDBRuntimeServiceTokenForUpdate(",
				"mariaDBRuntimeTokenServiceReferencesForUpdate(",
				"INSERT INTO service_tokens",
				"UPDATE service_tokens",
			); tokenPhase >= 0 && lockPhase >= tokenPhase {
				t.Fatalf("%s reaches a token phase before the canonical runtime lock plan", test.name)
			}
		})
	}
}

func TestMariaDBPrecreateServiceEstablishesServiceBeforeTokenAndRevalidatesBinding(t *testing.T) {
	sourceBytes, err := os.ReadFile("services.go")
	if err != nil {
		t.Fatal(err)
	}
	body := mariaDBServiceTokenFunctionSource(
		t,
		string(sourceBytes),
		"PrecreateService",
		"RegisterService",
	)
	transactionPhase := strings.Index(body, "BeginTx(")
	insertPhase := strings.Index(body, "INSERT INTO services")
	tokenPhase := strings.Index(body, "lockMariaDBServiceTokensSorted(")
	revalidationPhase := strings.Index(body, "discoverMariaDBServiceTokenReferences(ctx, tx")
	commitPhase := strings.Index(body, "tx.Commit()")
	if transactionPhase < 0 || insertPhase < 0 || tokenPhase < 0 ||
		revalidationPhase < 0 || commitPhase < 0 ||
		transactionPhase >= insertPhase || insertPhase >= tokenPhase ||
		tokenPhase >= revalidationPhase || revalidationPhase >= commitPhase {
		t.Fatalf(
			"precreate lock order transaction=%d insert=%d token=%d revalidation=%d commit=%d",
			transactionPhase, insertPhase, tokenPhase, revalidationPhase, commitPhase,
		)
	}
	if strings.Contains(body, "s.db.ExecContext(ctx, `INSERT INTO services") {
		t.Fatal("PrecreateService still inserts the token binding outside its transaction")
	}
}

func TestMariaDBDeactivatePullUpdaterOwnershipLocksAllServicesBeforeTokens(t *testing.T) {
	sourceBytes, err := os.ReadFile("updater_policy.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func (s MariaDBUpdaterPolicyStore) DeactivatePullUpdaterOwnership("
	nextMarker := "func uniqueActiveMariaDBLegacyUpdaterForHostLocked("
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatal("DeactivatePullUpdaterOwnership not found")
	}
	endOffset := strings.Index(source[start+len(startMarker):], nextMarker)
	if endOffset < 0 {
		t.Fatal("function following DeactivatePullUpdaterOwnership not found")
	}
	body := source[start : start+len(startMarker)+endOffset]
	if !strings.Contains(body, "lockMariaDBServiceTokenMutation(") {
		t.Fatal("DeactivatePullUpdaterOwnership does not lock the complete service set before token rows")
	}
	if strings.Contains(body, "selectActiveServiceTokenForUpdate(") {
		t.Fatal("DeactivatePullUpdaterOwnership retains a token lock before its later legacy service lock")
	}
}

func TestMariaDBActivatePullUpdaterOwnershipLocksAllServicesBeforeTokens(t *testing.T) {
	sourceBytes, err := os.ReadFile("updater_policy.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func (s MariaDBUpdaterPolicyStore) ActivatePullUpdaterOwnership("
	nextMarker := "func (s MariaDBUpdaterPolicyStore) DeactivatePullUpdaterOwnership("
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatal("ActivatePullUpdaterOwnership not found")
	}
	endOffset := strings.Index(source[start+len(startMarker):], nextMarker)
	if endOffset < 0 {
		t.Fatal("function following ActivatePullUpdaterOwnership not found")
	}
	body := source[start : start+len(startMarker)+endOffset]
	if strings.Count(body, "lockMariaDBServiceTokenMutation(") != 1 {
		t.Fatalf(
			"ActivatePullUpdaterOwnership canonical lock helper count = %d, want 1",
			strings.Count(body, "lockMariaDBServiceTokenMutation("),
		)
	}
	if strings.Contains(body, "serviceSelectColumns+` FROM services WHERE service_id = ? FOR UPDATE`") {
		t.Fatal("ActivatePullUpdaterOwnership locks a service outside the global sorted service set")
	}
	discoveryPhase := strings.Index(body, "discoverMariaDBPullUpdaterOwnershipLockPlan(")
	transactionPhase := strings.Index(body, "BeginTx(")
	policyLockPhase := strings.Index(body, "lockMariaDBPullUpdaterOwnershipPolicies(")
	lockPhase := strings.Index(body, "lockMariaDBServiceTokenMutation(")
	if discoveryPhase < 0 || transactionPhase < 0 || policyLockPhase < 0 || lockPhase < 0 ||
		discoveryPhase >= transactionPhase || transactionPhase >= policyLockPhase ||
		policyLockPhase >= lockPhase {
		t.Fatalf(
			"ActivatePullUpdaterOwnership discovery=%d transaction=%d policy_lock=%d service_token_lock=%d",
			discoveryPhase, transactionPhase, policyLockPhase, lockPhase,
		)
	}
	legacyStartMarker := "func uniqueActiveMariaDBLegacyUpdaterForHostLocked("
	legacyNextMarker := "func (s MariaDBUpdaterPolicyStore) GetUpdaterReleaseTokenStatus("
	legacyStart := strings.Index(source, legacyStartMarker)
	if legacyStart < 0 {
		t.Fatal("uniqueActiveMariaDBLegacyUpdaterForHost not found")
	}
	legacyEndOffset := strings.Index(source[legacyStart+len(legacyStartMarker):], legacyNextMarker)
	if legacyEndOffset < 0 {
		t.Fatal("function following uniqueActiveMariaDBLegacyUpdaterForHost not found")
	}
	legacyHelper := source[legacyStart : legacyStart+len(legacyStartMarker)+legacyEndOffset]
	for _, forbidden := range []string{
		"QueryContext(",
		"FOR UPDATE",
		"INNER JOIN services",
		"INNER JOIN service_tokens",
		"lockMariaDBServicesSorted(",
		"lockMariaDBServiceTokenMutation(",
		"selectActiveServiceTokenForUpdate(",
	} {
		if strings.Contains(legacyHelper, forbidden) {
			t.Fatalf("locked legacy updater revalidation acquires a new service/token lock via %q", forbidden)
		}
	}
}

func TestMariaDBDeactivatePullUpdaterOwnershipLocksGlobalPoliciesBeforeServices(t *testing.T) {
	sourceBytes, err := os.ReadFile("updater_policy.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func (s MariaDBUpdaterPolicyStore) DeactivatePullUpdaterOwnership("
	nextMarker := "func uniqueActiveMariaDBLegacyUpdaterForHostLocked("
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatal("DeactivatePullUpdaterOwnership not found")
	}
	endOffset := strings.Index(source[start+len(startMarker):], nextMarker)
	if endOffset < 0 {
		t.Fatal("function following DeactivatePullUpdaterOwnership not found")
	}
	body := source[start : start+len(startMarker)+endOffset]
	policyLockPhase := strings.Index(body, "lockMariaDBPullUpdaterOwnershipPolicies(")
	serviceLockPhase := strings.Index(body, "lockMariaDBServiceTokenMutation(")
	if policyLockPhase < 0 || serviceLockPhase < 0 || policyLockPhase >= serviceLockPhase {
		t.Fatalf(
			"DeactivatePullUpdaterOwnership policy_lock=%d service_token_lock=%d",
			policyLockPhase, serviceLockPhase,
		)
	}
	if strings.Contains(body, "mariaDBUpdaterPolicyForUpdate(") {
		t.Fatal("DeactivatePullUpdaterOwnership takes an individual policy lock outside the global policy set")
	}
}

func TestMariaDBPullUpdaterOwnershipPolicyServiceClosureIsTransitiveAndSorted(t *testing.T) {
	policies := []UpdaterPolicy{
		{UpdaterID: "policy-b", Targets: []UpdaterPolicyTarget{{ServiceID: "service-c"}}},
		{UpdaterID: "policy-a", Targets: []UpdaterPolicyTarget{{ServiceID: "policy-b"}}},
	}
	got := expandMariaDBUpdaterPolicyServiceClosure(policies, []string{"policy-a"})
	want := []string{"policy-a", "policy-b", "service-c"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("policy service closure = %#v, want %#v", got, want)
	}
}

func TestMariaDBUpdaterPolicyLockObserverNilIsNoOp(t *testing.T) {
	observeMariaDBUpdaterPolicyLockPhase(
		context.Background(),
		"activate_pull_updater_ownership",
		mariaDBUpdaterPolicyBeforePolicyLocks,
	)
}

func TestMariaDBFIX006CanonicalPairUsesLeadingFixtureNamespace(t *testing.T) {
	sourceBytes, err := os.ReadFile("service_token_lock_order_mariadb_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	startMarker := "func " + "newMariaDBServiceTokenPairFixture("
	nextMarker := "type " + "mariaDBServiceTokenMutationResult struct"
	start := strings.Index(source, startMarker)
	end := strings.Index(source, nextMarker)
	if start < 0 || end <= start {
		t.Fatal("canonical pair fixture source was not found")
	}
	if strings.Contains(source[start:end], `serviceID := serviceType + "-" + suffix`) {
		t.Fatal("canonical pair service ID places the service type before the cleanup namespace")
	}
	if !strings.Contains(source[start:end], `serviceID := suffix + "-" + serviceType`) {
		t.Fatal("canonical pair service ID does not begin with the cleanup namespace")
	}
}

func TestMariaDBUnreferencedTokenCleanupDoesNotLockServiceAfterToken(t *testing.T) {
	sourceBytes, err := os.ReadFile("services.go")
	if err != nil {
		t.Fatal(err)
	}
	body := mariaDBServiceTokenFunctionSource(
		t,
		string(sourceBytes),
		"revokeServiceTokenIfUnreferencedInTx",
		"AuthenticateServiceToken",
	)
	if strings.Contains(body, "FROM services") && strings.Contains(body, "FOR UPDATE") {
		t.Fatal("unreferenced-token cleanup locks a service row after callers already locked the token row")
	}
}

func TestMariaDBDeleteServiceRevalidatesAllTokenReferencesBeforeRevocation(t *testing.T) {
	sourceBytes, err := os.ReadFile("services.go")
	if err != nil {
		t.Fatal(err)
	}
	body := mariaDBServiceTokenFunctionSource(
		t,
		string(sourceBytes),
		"DeleteService",
		"AssignServiceToStream",
	)
	const referenceDiscoveryCall = "discoverMariaDBServiceTokenReferences("
	discoveryPhase := strings.Index(body, referenceDiscoveryCall)
	servicePhase := strings.Index(body, "lockMariaDBServicesSorted(")
	assignmentPhase := strings.Index(body, "lockMariaDBAssignmentRowsSorted(")
	tokenPhase := strings.Index(body, "lockMariaDBServiceTokensSorted(")
	revalidationPhase := -1
	if discoveryPhase >= 0 {
		if offset := strings.Index(body[discoveryPhase+len(referenceDiscoveryCall):], referenceDiscoveryCall); offset >= 0 {
			revalidationPhase = discoveryPhase + len(referenceDiscoveryCall) + offset
		}
	}
	if discoveryPhase < 0 || servicePhase < 0 || assignmentPhase < 0 || tokenPhase < 0 || revalidationPhase < 0 ||
		servicePhase >= assignmentPhase || assignmentPhase >= tokenPhase || tokenPhase >= revalidationPhase {
		t.Fatalf(
			"delete lock order discovery=%d service=%d assignment=%d token=%d revalidation=%d",
			discoveryPhase, servicePhase, assignmentPhase, tokenPhase, revalidationPhase,
		)
	}
}

func TestMariaDBServiceTokenReferencePlanIsSortedAndFailClosed(t *testing.T) {
	references := []mariaDBServiceTokenReference{
		{ServiceID: "service-b", TokenID: "token-current"},
		{ServiceID: "service-a", StagedPreviousTokenID: "token-current", StagedTokenID: "token-staged"},
	}
	serviceIDs := mariaDBServiceTokenReferenceServiceIDs(references, " service-c ", "service-a")
	wantServiceIDs := []string{"service-a", "service-b", "service-c"}
	if len(serviceIDs) != len(wantServiceIDs) {
		t.Fatalf("service IDs = %#v, want %#v", serviceIDs, wantServiceIDs)
	}
	for index := range wantServiceIDs {
		if serviceIDs[index] != wantServiceIDs[index] {
			t.Fatalf("service IDs = %#v, want %#v", serviceIDs, wantServiceIDs)
		}
	}
	services := map[string]RegisteredService{
		"service-a": {
			ServiceID: "service-a", ServiceType: "worker",
			StagedNodePreviousTokenID: "token-current", StagedNodeTokenID: "token-staged",
		},
		"service-b": {ServiceID: "service-b", ServiceType: "worker", TokenID: "token-current"},
	}
	tokens := map[string]ServiceToken{
		"token-current": {ID: "token-current", ServiceType: "worker"},
		"token-staged":  {ID: "token-staged", ServiceType: "worker"},
	}
	if !mariaDBServiceTokenReferenceTypesMatch(references, services, tokens) {
		t.Fatal("valid current/staged reference plan was rejected")
	}
	changed := append([]mariaDBServiceTokenReference(nil), references...)
	changed[1].StagedTokenID = "token-raced"
	if mariaDBServiceTokenReferencesEqual(references, changed) {
		t.Fatal("staged token binding change was accepted")
	}
	tokens["token-current"] = ServiceToken{ID: "token-current", ServiceType: "encoder_recorder"}
	if mariaDBServiceTokenReferenceTypesMatch(references, services, tokens) {
		t.Fatal("service/token type mismatch was accepted")
	}
	if mariaDBServiceTokenReferencesUseCurrentToken(references, "token-current") {
		t.Fatal("staged-only token reference was accepted as a current-token rotation target")
	}
	if !mariaDBServiceTokenReferencesUseCurrentToken(
		[]mariaDBServiceTokenReference{{ServiceID: "service-b", TokenID: "token-current"}},
		"token-current",
	) {
		t.Fatal("current token reference was rejected")
	}
	if !mariaDBServiceTokenReferencesUseCurrentToken(nil, "token-unbound") {
		t.Fatal("unbound token was rejected")
	}
	if ids := mariaDBServiceTokenReferenceServiceIDs(nil); len(ids) != 0 {
		t.Fatalf("unbound token service IDs = %#v, want none", ids)
	}
}

func mariaDBServiceTokenFunctionSource(t *testing.T, source, name, next string) string {
	t.Helper()
	startMarker := "func (s MariaDBAuthStore) " + name + "("
	if strings.HasPrefix(name, "revokeServiceToken") || strings.HasPrefix(name, "lockMariaDBServiceToken") {
		startMarker = "func " + name + "("
	}
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatalf("function %s not found", name)
	}
	nextMarker := "func (s MariaDBAuthStore) " + next + "("
	if next == "requiresStagedNodeTokenRotation" || next == "mariaDBServiceTokenReferenceContains" {
		nextMarker = "func " + next + "("
	}
	endOffset := strings.Index(source[start+len(startMarker):], nextMarker)
	if endOffset < 0 {
		t.Fatalf("function following %s (%s) not found", name, next)
	}
	return source[start : start+len(startMarker)+endOffset]
}

func mariaDBRuntimeTokenFunctionSource(t *testing.T, source, name, next string) string {
	t.Helper()
	startMarkers := []string{
		"func (s *MariaDBSystemUpdateStore) " + name + "(",
		"func " + name + "(",
	}
	nextMarkers := []string{
		"func (s *MariaDBSystemUpdateStore) " + next + "(",
		"func " + next + "(",
	}
	start := firstSourceIndex(source, startMarkers...)
	if start < 0 {
		t.Fatalf("runtime token function %s not found", name)
	}
	nextOffset := firstSourceIndex(source[start+1:], nextMarkers...)
	if nextOffset < 0 {
		t.Fatalf("runtime token function following %s (%s) not found", name, next)
	}
	return source[start : start+1+nextOffset]
}

func firstSourceIndex(source string, needles ...string) int {
	first := -1
	for _, needle := range needles {
		if index := strings.Index(source, needle); index >= 0 && (first < 0 || index < first) {
			first = index
		}
	}
	return first
}

func TestMariaDBServiceTokenCanonicalLockOrderPairs(t *testing.T) {
	dsn := os.Getenv("AUTOSTREAM_MARIADB_TEST_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("AUTOSTREAM_MARIADB_TEST_DSN is not configured")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := database.OpenFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.RunEmbeddedMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	auth := NewMariaDBAuthStore(db)
	streams := NewMariaDBStreamStore(db)

	for _, pair := range []struct {
		serviceOperation string
		tokenMutation    string
	}{
		{serviceOperation: "heartbeat", tokenMutation: "rotate"},
		{serviceOperation: "heartbeat", tokenMutation: "revoke"},
		{serviceOperation: "artifact_report", tokenMutation: "rotate"},
		{serviceOperation: "artifact_report", tokenMutation: "revoke"},
		{serviceOperation: "service_delete", tokenMutation: "rotate"},
		{serviceOperation: "service_delete", tokenMutation: "revoke"},
	} {
		pair := pair
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("%s_vs_%s/%d", pair.serviceOperation, pair.tokenMutation, iteration), func(t *testing.T) {
				fixture := newMariaDBServiceTokenPairFixture(t, ctx, auth, streams, pair.serviceOperation)
				runMariaDBServiceTokenPair(t, ctx, db, fixture, pair.serviceOperation, pair.tokenMutation)
			})
		}
	}

	for iteration := 1; iteration <= 3; iteration++ {
		t.Run(fmt.Sprintf("heartbeat_vs_rotate_service_node_token_shared/%d", iteration), func(t *testing.T) {
			runMariaDBSharedServiceTokenNodeRotationPair(t, ctx, db, auth)
		})
		t.Run(fmt.Sprintf("heartbeat_vs_configure_service_node_shared/%d", iteration), func(t *testing.T) {
			runMariaDBSharedServiceTokenNodeConfigurePair(t, ctx, db, auth)
		})
		t.Run(fmt.Sprintf("heartbeat_vs_activate_service_node_configuration_shared/%d", iteration), func(t *testing.T) {
			runMariaDBSharedServiceTokenNodeActivationPair(t, ctx, db, auth)
		})
		t.Run(fmt.Sprintf("service_delete_shared_token_fails_closed/%d", iteration), func(t *testing.T) {
			assertMariaDBSharedTokenDeleteFailsClosed(t, ctx, db, auth)
		})
	}
}

func TestMariaDBFIX010UpdateAgentStageRejectsSharedReferences(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for _, referenceColumn := range []string{
		"token_id",
		"staged_node_previous_token_id",
		"staged_node_token_id",
	} {
		referenceColumn := referenceColumn
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("%s/%d", referenceColumn, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()
				cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
				targetID := cleanup.prefix + "stage-target"
				otherID := cleanup.prefix + "stage-other"
				oldToken := createMariaDBServiceTokenPairService(
					t, ctx, auth, targetID, "update_agent", nil, cleanup,
				)
				createMariaDBServiceTokenPairService(
					t, ctx, auth, otherID, "update_agent", nil, cleanup,
				)
				setMariaDBFIX010ServiceTokenReference(
					t, ctx, db, otherID, referenceColumn, oldToken.ID,
				)
				now := time.Now().UTC()
				configureToken := cleanup.prefix + "stage-configure"
				if _, err := auth.SetServiceConfigureToken(
					ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
				); err != nil {
					t.Fatal(err)
				}
				beforeTarget, err := auth.GetService(ctx, targetID)
				if err != nil {
					t.Fatal(err)
				}
				beforeOther, err := auth.GetService(ctx, otherID)
				if err != nil {
					t.Fatal(err)
				}

				sealerCalled := false
				_, err = auth.StageServiceNodeConfiguration(
					ctx,
					targetID,
					configureToken,
					now,
					func(string) (string, string, error) {
						sealerCalled = true
						return "fix010-stage-ciphertext", "fix010-stage-nonce", nil
					},
				)
				if !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
					t.Fatalf("stage error = %v, want shared-token conflict", err)
				}
				if sealerCalled {
					t.Fatal("stage sealed a token before rejecting the shared reference")
				}
				afterTarget, err := auth.GetService(ctx, targetID)
				if err != nil {
					t.Fatal(err)
				}
				afterOther, err := auth.GetService(ctx, otherID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(afterTarget, beforeTarget) ||
					!reflect.DeepEqual(afterOther, beforeOther) ||
					mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
					t.Fatalf(
						"shared stage changed state: target_current=%q target_previous=%q target_staged=%q other_current=%q other_previous=%q other_staged=%q old_revoked=%t",
						afterTarget.TokenID,
						afterTarget.StagedNodePreviousTokenID,
						afterTarget.StagedNodeTokenID,
						afterOther.TokenID,
						afterOther.StagedNodePreviousTokenID,
						afterOther.StagedNodeTokenID,
						mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
					)
				}
			})
		}
	}
}

func TestMariaDBFIX010UpdateAgentActivationRejectsSharedReferences(t *testing.T) {
	tests := []struct {
		name            string
		referenceColumn string
		useStagedToken  bool
	}{
		{name: "old_current", referenceColumn: "token_id"},
		{name: "old_staged_previous", referenceColumn: "staged_node_previous_token_id"},
		{name: "old_staged_token", referenceColumn: "staged_node_token_id"},
		{name: "new_current", referenceColumn: "token_id", useStagedToken: true},
		{name: "new_staged_previous", referenceColumn: "staged_node_previous_token_id", useStagedToken: true},
		{name: "new_staged_token", referenceColumn: "staged_node_token_id", useStagedToken: true},
	}
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	for _, test := range tests {
		test := test
		for iteration := 1; iteration <= 3; iteration++ {
			t.Run(fmt.Sprintf("%s/%d", test.name, iteration), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(parent, 20*time.Second)
				defer cancel()
				cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
				targetID := cleanup.prefix + "activation-target"
				otherID := cleanup.prefix + "activation-other"
				oldToken := createMariaDBServiceTokenPairService(
					t, ctx, auth, targetID, "update_agent", nil, cleanup,
				)
				now := time.Now().UTC()
				configureToken := cleanup.prefix + "activation-configure"
				if _, err := auth.SetServiceConfigureToken(
					ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
				); err != nil {
					t.Fatal(err)
				}
				staged, err := auth.StageServiceNodeConfiguration(
					ctx,
					targetID,
					configureToken,
					now,
					func(string) (string, string, error) {
						return "fix010-activation-ciphertext", "fix010-activation-nonce", nil
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				cleanup.trackToken(staged.Token)
				createMariaDBServiceTokenPairService(
					t, ctx, auth, otherID, "update_agent", nil, cleanup,
				)
				referenceTokenID := oldToken.ID
				if test.useStagedToken {
					referenceTokenID = staged.Token.ID
				}
				setMariaDBFIX010ServiceTokenReference(
					t, ctx, db, otherID, test.referenceColumn, referenceTokenID,
				)
				beforeTarget, err := auth.GetService(ctx, targetID)
				if err != nil {
					t.Fatal(err)
				}
				beforeOther, err := auth.GetService(ctx, otherID)
				if err != nil {
					t.Fatal(err)
				}

				activatedToken, activatedService, alreadyActivated, err :=
					auth.ActivateServiceNodeConfiguration(
						ctx,
						targetID,
						staged.Token.ID,
						staged.ActivationToken,
						now.Add(time.Minute),
						ServiceRuntimeReport{},
					)
				if !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
					t.Fatalf("activation error = %v, want shared-token conflict", err)
				}
				if activatedToken.ID != "" || activatedService.ServiceID != "" || alreadyActivated {
					t.Fatalf(
						"shared activation returned success values: token_id=%q service_id=%q already=%t",
						activatedToken.ID,
						activatedService.ServiceID,
						alreadyActivated,
					)
				}
				afterTarget, err := auth.GetService(ctx, targetID)
				if err != nil {
					t.Fatal(err)
				}
				afterOther, err := auth.GetService(ctx, otherID)
				if err != nil {
					t.Fatal(err)
				}
				var stagedRows int
				if err := db.QueryRowContext(
					ctx, `SELECT COUNT(*) FROM service_tokens WHERE id = ?`, staged.Token.ID,
				).Scan(&stagedRows); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(afterTarget, beforeTarget) ||
					!reflect.DeepEqual(afterOther, beforeOther) ||
					mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
					stagedRows != 0 {
					t.Fatalf(
						"shared activation changed state: target_current=%q target_previous=%q target_staged=%q other_current=%q other_previous=%q other_staged=%q old_revoked=%t staged_rows=%d",
						afterTarget.TokenID,
						afterTarget.StagedNodePreviousTokenID,
						afterTarget.StagedNodeTokenID,
						afterOther.TokenID,
						afterOther.StagedNodePreviousTokenID,
						afterOther.StagedNodeTokenID,
						mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
						stagedRows,
					)
				}
			})
		}
	}
}

func TestMariaDBFIX010ExternalBindingWinsAgainstStageAndActivation(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)

	type concurrentResult struct {
		tokenID          string
		serviceID        string
		alreadyActivated bool
		err              error
	}
	for _, operation := range []string{"stage", "activate"} {
		operation := operation
		t.Run(operation, func(t *testing.T) {
			for iteration := 1; iteration <= 3; iteration++ {
				t.Run(strconv.Itoa(iteration), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(parent, 20*time.Second)
					defer cancel()
					cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
					targetServiceID := cleanup.prefix + operation + "-target"
					externalServiceID := cleanup.prefix + operation + "-external"
					oldToken := createMariaDBServiceTokenPairService(
						t, ctx, auth, targetServiceID, "update_agent", nil, cleanup,
					)
					now := time.Now().UTC()
					configureToken := cleanup.prefix + "configure"
					if _, err := auth.SetServiceConfigureToken(
						ctx,
						targetServiceID,
						security.HashToken(configureToken),
						now.Add(time.Hour),
					); err != nil {
						t.Fatal(err)
					}

					var staged StagedServiceNodeConfiguration
					if operation == "activate" {
						var err error
						staged, err = auth.StageServiceNodeConfiguration(
							ctx,
							targetServiceID,
							configureToken,
							now,
							func(string) (string, string, error) {
								return cleanup.prefix + "ciphertext", cleanup.prefix + "nonce", nil
							},
						)
						if err != nil {
							t.Fatal(err)
						}
						cleanup.trackToken(staged.Token)
					}
					beforeTarget, err := auth.GetService(ctx, targetServiceID)
					if err != nil {
						t.Fatal(err)
					}

					observedOperation := "stage_service_node_configuration"
					if operation == "activate" {
						observedOperation = "activate_service_node_configuration"
					}
					phases := make(chan mariaDBServiceTokenLockPhase, 16)
					release := make(chan struct{})
					observer := mariaDBServiceTokenLockObserver(func(name string, phase mariaDBServiceTokenLockPhase) {
						if name != observedOperation {
							return
						}
						if !isMariaDBServiceTokenCanonicalLockPhase(phase) {
							return
						}
						phases <- phase
						if phase == mariaDBServiceTokenBeforeServiceLocks {
							<-release
						}
					})
					operationContext := context.WithValue(
						ctx,
						mariaDBServiceTokenLockObserverContextKey{},
						observer,
					)
					sealerCalled := make(chan struct{}, 1)
					result := make(chan concurrentResult, 1)
					go func() {
						if operation == "stage" {
							configuration, err := auth.StageServiceNodeConfiguration(
								operationContext,
								targetServiceID,
								configureToken,
								now,
								func(string) (string, string, error) {
									sealerCalled <- struct{}{}
									return cleanup.prefix + "unexpected-ciphertext", cleanup.prefix + "unexpected-nonce", nil
								},
							)
							result <- concurrentResult{
								tokenID:   configuration.Token.ID,
								serviceID: configuration.Service.ServiceID,
								err:       err,
							}
							return
						}
						token, service, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(
							operationContext,
							targetServiceID,
							staged.Token.ID,
							staged.ActivationToken,
							now.Add(time.Second),
							ServiceRuntimeReport{Version: "v1.0.1"},
						)
						result <- concurrentResult{
							tokenID:          token.ID,
							serviceID:        service.ServiceID,
							alreadyActivated: alreadyActivated,
							err:              err,
						}
					}()

					select {
					case phase := <-phases:
						if phase != mariaDBServiceTokenBeforeServiceLocks {
							t.Fatalf("first observed lock phase = %q, want %q", phase, mariaDBServiceTokenBeforeServiceLocks)
						}
					case <-ctx.Done():
						t.Fatal("node configuration did not reach the service-lock barrier")
					}

					cleanup.trackServiceID(externalServiceID)
					if _, err := auth.PrecreateService(ctx, oldToken, ServiceRegistration{
						ServiceID:   externalServiceID,
						ServiceType: "update_agent",
						ServiceName: externalServiceID,
						PublicURL:   "https://" + externalServiceID + ".example.com",
						Port:        443,
						SSLEnabled:  true,
					}); err != nil {
						close(release)
						t.Fatal(err)
					}
					close(release)

					var outcome concurrentResult
					select {
					case outcome = <-result:
					case <-ctx.Done():
						t.Fatal("node configuration did not finish after releasing the barrier")
					}
					if !errors.Is(outcome.err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
						t.Fatalf("%s error = %v, want shared-token conflict", operation, outcome.err)
					}
					if outcome.tokenID != "" || outcome.serviceID != "" || outcome.alreadyActivated {
						t.Fatalf("%s returned success state: token_id=%q service_id=%q already=%t", operation, outcome.tokenID, outcome.serviceID, outcome.alreadyActivated)
					}
					select {
					case <-sealerCalled:
						t.Fatal("stage sealer ran after the reference inventory changed")
					default:
					}

					afterTarget, err := auth.GetService(ctx, targetServiceID)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(afterTarget, beforeTarget) {
						t.Fatalf(
							"%s partially mutated target: before=%s after=%s",
							operation,
							formatSafeRegisteredServiceDiagnostic(beforeTarget),
							formatSafeRegisteredServiceDiagnostic(afterTarget),
						)
					}
					external, err := auth.GetService(ctx, externalServiceID)
					if err != nil {
						t.Fatal(err)
					}
					if external.TokenID != oldToken.ID || mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
						t.Fatalf(
							"%s external binding or old-token state changed: external=%s old_revoked=%t",
							operation,
							formatSafeRegisteredServiceDiagnostic(external),
							mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
						)
					}
					if operation == "activate" {
						var stagedTokenRows int
						if err := db.QueryRowContext(
							ctx,
							`SELECT COUNT(*) FROM service_tokens WHERE id = ?`,
							staged.Token.ID,
						).Scan(&stagedTokenRows); err != nil {
							t.Fatal(err)
						}
						if stagedTokenRows != 0 {
							t.Fatalf("activation inserted %d staged token rows after conflict", stagedTokenRows)
						}
					}
				})
			}
		})
	}
}

func TestMariaDBFIX011InvalidConfigureCredentialWinsAfterReferenceSetMismatch(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "fix011-stage-target"
	externalID := cleanup.prefix + "fix011-stage-external"
	oldToken := createMariaDBServiceTokenPairService(
		t, ctx, auth, targetID, "update_agent", nil, cleanup,
	)
	createMariaDBServiceTokenPairService(
		t, ctx, auth, externalID, "update_agent", nil, cleanup,
	)
	now := time.Now().UTC()
	configureToken := cleanup.prefix + "fix011-stage-configure"
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	beforeTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	beforeExternal, err := auth.GetService(ctx, externalID)
	if err != nil {
		t.Fatal(err)
	}
	recorder := newMariaDBFIX011ReferenceRaceRecorder(
		t,
		ctx,
		db,
		"stage_service_node_configuration",
		[]mariaDBFIX011ReferenceMutation{{
			serviceID: externalID,
			column:    "staged_node_previous_token_id",
			tokenID:   oldToken.ID,
		}},
	)
	operationCtx := context.WithValue(
		ctx,
		mariaDBServiceTokenLockObserverContextKey{},
		mariaDBServiceTokenLockObserver(recorder.observe),
	)
	sealerCalled := false
	_, err = auth.StageServiceNodeConfiguration(
		operationCtx,
		targetID,
		configureToken+"-invalid",
		now,
		func(string) (string, string, error) {
			sealerCalled = true
			return cleanup.prefix + "unexpected-ciphertext", cleanup.prefix + "unexpected-nonce", nil
		},
	)
	if !errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
		t.Fatalf("invalid configure error = %v, want ErrUnauthorized only", err)
	}
	if sealerCalled {
		t.Fatal("invalid configure credential reached the sealer")
	}
	if recorder.commitCount != 1 {
		t.Fatalf("external reference commits = %d, want 1", recorder.commitCount)
	}
	assertMariaDBFIX011RetryPhaseOrder(t, recorder.events)
	clearMariaDBFIX011ServiceTokenReference(
		t, ctx, db, externalID, "staged_node_previous_token_id",
	)
	assertMariaDBFIX011ServicesUnchanged(
		t, ctx, auth, beforeTarget, beforeExternal,
	)
	if mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
		t.Fatal("invalid configure credential revoked the active token")
	}
}

func TestMariaDBFIX011InvalidActivationCredentialWinsAfterReferenceSetMismatch(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "fix011-activation-target"
	externalID := cleanup.prefix + "fix011-activation-external"
	oldToken := createMariaDBServiceTokenPairService(
		t, ctx, auth, targetID, "update_agent", nil, cleanup,
	)
	now := time.Now().UTC()
	configureToken := cleanup.prefix + "fix011-activation-configure"
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(
		ctx,
		targetID,
		configureToken,
		now,
		func(string) (string, string, error) {
			return cleanup.prefix + "activation-ciphertext", cleanup.prefix + "activation-nonce", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackToken(staged.Token)
	createMariaDBServiceTokenPairService(
		t, ctx, auth, externalID, "update_agent", nil, cleanup,
	)
	beforeTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	beforeExternal, err := auth.GetService(ctx, externalID)
	if err != nil {
		t.Fatal(err)
	}
	recorder := newMariaDBFIX011ReferenceRaceRecorder(
		t,
		ctx,
		db,
		"activate_service_node_configuration",
		[]mariaDBFIX011ReferenceMutation{{
			serviceID: externalID,
			column:    "staged_node_token_id",
			tokenID:   staged.Token.ID,
		}},
	)
	operationCtx := context.WithValue(
		ctx,
		mariaDBServiceTokenLockObserverContextKey{},
		mariaDBServiceTokenLockObserver(recorder.observe),
	)
	activatedToken, activatedService, alreadyActivated, err :=
		auth.ActivateServiceNodeConfiguration(
			operationCtx,
			targetID,
			staged.Token.ID,
			staged.ActivationToken+"-invalid",
			now.Add(time.Minute),
			ServiceRuntimeReport{},
		)
	if !errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
		t.Fatalf("invalid activation error = %v, want ErrUnauthorized only", err)
	}
	if activatedToken.ID != "" || activatedService.ServiceID != "" || alreadyActivated {
		t.Fatalf(
			"invalid activation returned success values: token_id=%q service_id=%q already=%t",
			activatedToken.ID,
			activatedService.ServiceID,
			alreadyActivated,
		)
	}
	if recorder.commitCount != 1 {
		t.Fatalf("external reference commits = %d, want 1", recorder.commitCount)
	}
	assertMariaDBFIX011RetryPhaseOrder(t, recorder.events)
	clearMariaDBFIX011ServiceTokenReference(
		t, ctx, db, externalID, "staged_node_token_id",
	)
	assertMariaDBFIX011ServicesUnchanged(
		t, ctx, auth, beforeTarget, beforeExternal,
	)
	var stagedTokenRows int
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM service_tokens WHERE id = ?`, staged.Token.ID,
	).Scan(&stagedTokenRows); err != nil {
		t.Fatal(err)
	}
	if stagedTokenRows != 0 || mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
		t.Fatalf(
			"invalid activation changed token state: staged_rows=%d old_revoked=%t",
			stagedTokenRows,
			mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
		)
	}
}

func TestMariaDBFIX011DuplicateActivationReplayWinsAfterReferenceSetMismatch(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "fix011-replay-target"
	externalID := cleanup.prefix + "fix011-replay-external"
	oldToken := createMariaDBServiceTokenPairService(
		t, ctx, auth, targetID, "update_agent", nil, cleanup,
	)
	now := time.Now().UTC()
	configureToken := cleanup.prefix + "fix011-replay-configure"
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(
		ctx,
		targetID,
		configureToken,
		now,
		func(string) (string, string, error) {
			return cleanup.prefix + "replay-ciphertext", cleanup.prefix + "replay-nonce", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackToken(staged.Token)
	if _, _, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(
		ctx,
		targetID,
		staged.Token.ID,
		staged.ActivationToken,
		now.Add(time.Minute),
		ServiceRuntimeReport{Version: "v1.0.0"},
	); err != nil || alreadyActivated {
		t.Fatalf("initial activation already=%t err=%v", alreadyActivated, err)
	}
	createMariaDBServiceTokenPairService(
		t, ctx, auth, externalID, "update_agent", nil, cleanup,
	)
	beforeTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	beforeExternal, err := auth.GetService(ctx, externalID)
	if err != nil {
		t.Fatal(err)
	}
	recorder := newMariaDBFIX011ReferenceRaceRecorder(
		t,
		ctx,
		db,
		"activate_service_node_configuration",
		[]mariaDBFIX011ReferenceMutation{{
			serviceID: externalID,
			column:    "staged_node_token_id",
			tokenID:   staged.Token.ID,
		}},
	)
	operationCtx := context.WithValue(
		ctx,
		mariaDBServiceTokenLockObserverContextKey{},
		mariaDBServiceTokenLockObserver(recorder.observe),
	)
	replayedToken, replayedService, alreadyActivated, err :=
		auth.ActivateServiceNodeConfiguration(
			operationCtx,
			targetID,
			staged.Token.ID,
			staged.ActivationToken,
			now.Add(2*time.Minute),
			ServiceRuntimeReport{Version: "v9.9.9"},
		)
	if err != nil || !alreadyActivated {
		t.Fatalf("duplicate activation replay already=%t err=%v", alreadyActivated, err)
	}
	if replayedToken.ID != staged.Token.ID || replayedService.ServiceID != targetID {
		t.Fatalf(
			"duplicate replay returned unexpected identity: token_id=%q service_id=%q",
			replayedToken.ID,
			replayedService.ServiceID,
		)
	}
	if recorder.commitCount != 1 {
		t.Fatalf("external reference commits = %d, want 1", recorder.commitCount)
	}
	assertMariaDBFIX011RetryPhaseOrder(t, recorder.events)
	clearMariaDBFIX011ServiceTokenReference(
		t, ctx, db, externalID, "staged_node_token_id",
	)
	assertMariaDBFIX011ServicesUnchanged(
		t, ctx, auth, beforeTarget, beforeExternal,
	)
	if !mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
		mariaDBServiceTokenRevoked(t, ctx, db, staged.Token.ID) {
		t.Fatalf(
			"duplicate replay changed revocation state: old_revoked=%t new_revoked=%t",
			mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
			mariaDBServiceTokenRevoked(t, ctx, db, staged.Token.ID),
		)
	}
}

func TestMariaDBFIX011ReplayRevalidatesTargetAfterReferenceSetRetry(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "fix011-stale-replay-target"
	oldToken := createMariaDBServiceTokenPairService(
		t, ctx, auth, targetID, "update_agent", nil, cleanup,
	)
	now := time.Now().UTC()
	configureToken := cleanup.prefix + "fix011-stale-replay-configure"
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(
		ctx,
		targetID,
		configureToken,
		now,
		func(string) (string, string, error) {
			return cleanup.prefix + "stale-replay-ciphertext", cleanup.prefix + "stale-replay-nonce", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackToken(staged.Token)
	if _, _, alreadyActivated, err := auth.ActivateServiceNodeConfiguration(
		ctx,
		targetID,
		staged.Token.ID,
		staged.ActivationToken,
		now.Add(time.Minute),
		ServiceRuntimeReport{Version: "v1.0.0"},
	); err != nil || alreadyActivated {
		t.Fatalf("initial activation already=%t err=%v", alreadyActivated, err)
	}

	var rotatedToken ServiceToken
	var postRotationService RegisteredService
	rotationCommitted := false
	events := make([]string, 0, 24)
	observer := mariaDBServiceTokenLockObserver(func(operation string, phase mariaDBServiceTokenLockPhase) {
		if operation != "activate_service_node_configuration" {
			return
		}
		events = append(events, string(phase))
		if phase != mariaDBServiceTokenReferenceDiscoveryComplete || rotationCommitted {
			return
		}
		var rotationErr error
		rotatedToken, rotationErr = auth.RotateServiceToken(ctx, staged.Token.ID)
		if rotationErr != nil {
			t.Fatal(rotationErr)
		}
		cleanup.trackToken(rotatedToken)
		postRotationService, rotationErr = auth.GetService(ctx, targetID)
		if rotationErr != nil {
			t.Fatal(rotationErr)
		}
		rotationCommitted = true
		events = append(events, "concurrent_target_rotation_committed")
	})
	operationCtx := context.WithValue(
		ctx,
		mariaDBServiceTokenLockObserverContextKey{},
		observer,
	)
	replayedToken, replayedService, alreadyActivated, err :=
		auth.ActivateServiceNodeConfiguration(
			operationCtx,
			targetID,
			staged.Token.ID,
			staged.ActivationToken,
			now.Add(2*time.Minute),
			ServiceRuntimeReport{Version: "v9.9.9"},
		)
	if !errors.Is(err, ErrUnauthorized) ||
		errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) ||
		alreadyActivated {
		t.Fatalf("stale replay already=%t err=%v, want ErrUnauthorized only", alreadyActivated, err)
	}
	if replayedToken.ID != "" || replayedService.ServiceID != "" {
		t.Fatalf(
			"stale replay returned success identity: token_id=%q service_id=%q",
			replayedToken.ID,
			replayedService.ServiceID,
		)
	}
	if !rotationCommitted || rotatedToken.ID == "" {
		t.Fatal("concurrent target rotation was not committed")
	}
	assertMariaDBFIX011PhaseSubsequence(t, events, []string{
		"reference_discovery_complete",
		"concurrent_target_rotation_committed",
		string(mariaDBServiceTokenServiceLocksHeld),
		string(mariaDBServiceTokenTokenLocksHeld),
		"reference_set_mismatch",
		"reference_retry_start",
		"reference_discovery_complete",
		string(mariaDBServiceTokenServiceLocksHeld),
		string(mariaDBServiceTokenTokenLocksHeld),
		string(mariaDBServiceTokenBindingsValidated),
		"stable_auth_replay_conflict",
	})
	for event, want := range map[string]int{
		"reference_discovery_complete": 2,
		"reference_set_mismatch":       1,
		"reference_retry_start":        1,
		"stable_auth_replay_conflict":  1,
	} {
		if got := countMariaDBFIX011Event(events, event); got != want {
			t.Fatalf("%s phases = %d, want %d; events=%v", event, got, want, events)
		}
	}
	afterReplay, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterReplay, postRotationService) ||
		afterReplay.TokenID != rotatedToken.ID ||
		!mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
		!mariaDBServiceTokenRevoked(t, ctx, db, staged.Token.ID) ||
		mariaDBServiceTokenRevoked(t, ctx, db, rotatedToken.ID) {
		t.Fatalf(
			"stale replay changed post-rotation state: service=%s old_revoked=%t replay_token_revoked=%t rotated_revoked=%t",
			formatSafeRegisteredServiceDiagnostic(afterReplay),
			mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
			mariaDBServiceTokenRevoked(t, ctx, db, staged.Token.ID),
			mariaDBServiceTokenRevoked(t, ctx, db, rotatedToken.ID),
		)
	}
}

func TestMariaDBFIX011ReferenceSetRetryExhaustionUsesConflict(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "fix011-exhaustion-target"
	oldToken := createMariaDBServiceTokenPairService(
		t, ctx, auth, targetID, "update_agent", nil, cleanup,
	)
	now := time.Now().UTC()
	configureToken := cleanup.prefix + "fix011-exhaustion-configure"
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	beforeTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	mutations := make([]mariaDBFIX011ReferenceMutation, 0, mariaDBServiceTokenReferenceRetryLimit)
	beforeExternal := make([]RegisteredService, 0, mariaDBServiceTokenReferenceRetryLimit)
	for attempt := 1; attempt <= mariaDBServiceTokenReferenceRetryLimit; attempt++ {
		externalID := fmt.Sprintf("%sfix011-exhaustion-external-%d", cleanup.prefix, attempt)
		createMariaDBServiceTokenPairService(
			t, ctx, auth, externalID, "update_agent", nil, cleanup,
		)
		external, err := auth.GetService(ctx, externalID)
		if err != nil {
			t.Fatal(err)
		}
		beforeExternal = append(beforeExternal, external)
		mutations = append(mutations, mariaDBFIX011ReferenceMutation{
			serviceID: externalID,
			column:    "staged_node_previous_token_id",
			tokenID:   oldToken.ID,
		})
	}
	recorder := newMariaDBFIX011ReferenceRaceRecorder(
		t,
		ctx,
		db,
		"stage_service_node_configuration",
		mutations,
	)
	operationCtx := context.WithValue(
		ctx,
		mariaDBServiceTokenLockObserverContextKey{},
		mariaDBServiceTokenLockObserver(recorder.observe),
	)
	sealerCalled := false
	_, err = auth.StageServiceNodeConfiguration(
		operationCtx,
		targetID,
		configureToken,
		now,
		func(string) (string, string, error) {
			sealerCalled = true
			return cleanup.prefix + "unexpected-ciphertext", cleanup.prefix + "unexpected-nonce", nil
		},
	)
	if !errors.Is(err, ErrConflict) || errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
		t.Fatalf("retry exhaustion error = %v, want ErrConflict only", err)
	}
	if sealerCalled {
		t.Fatal("retry exhaustion reached the sealer")
	}
	if recorder.commitCount != mariaDBServiceTokenReferenceRetryLimit {
		t.Fatalf(
			"external reference commits = %d, want %d",
			recorder.commitCount,
			mariaDBServiceTokenReferenceRetryLimit,
		)
	}
	assertMariaDBFIX011ExhaustionPhases(t, recorder.events)
	for _, mutation := range mutations {
		clearMariaDBFIX011ServiceTokenReference(
			t, ctx, db, mutation.serviceID, mutation.column,
		)
	}
	services := append([]RegisteredService{beforeTarget}, beforeExternal...)
	assertMariaDBFIX011ServicesUnchanged(t, ctx, auth, services...)
	if mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
		t.Fatal("retry exhaustion revoked the active token")
	}
}

func TestMariaDBFIX011StableExternalOldAndNewReferencesRemainConflicts(t *testing.T) {
	db, parent := openMariaDBFIX005Test(t)
	auth := NewMariaDBAuthStore(db)

	t.Run("old token", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
		targetID := cleanup.prefix + "fix011-stable-old-target"
		externalID := cleanup.prefix + "fix011-stable-old-external"
		oldToken := createMariaDBServiceTokenPairService(
			t, ctx, auth, targetID, "update_agent", nil, cleanup,
		)
		createMariaDBServiceTokenPairService(
			t, ctx, auth, externalID, "update_agent", nil, cleanup,
		)
		setMariaDBFIX010ServiceTokenReference(
			t, ctx, db, externalID, "staged_node_previous_token_id", oldToken.ID,
		)
		now := time.Now().UTC()
		configureToken := cleanup.prefix + "fix011-stable-old-configure"
		if _, err := auth.SetServiceConfigureToken(
			ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}
		beforeTarget, err := auth.GetService(ctx, targetID)
		if err != nil {
			t.Fatal(err)
		}
		beforeExternal, err := auth.GetService(ctx, externalID)
		if err != nil {
			t.Fatal(err)
		}
		recorder := newMariaDBFIX011ReferenceRaceRecorder(
			t, ctx, db, "stage_service_node_configuration", nil,
		)
		operationCtx := context.WithValue(
			ctx,
			mariaDBServiceTokenLockObserverContextKey{},
			mariaDBServiceTokenLockObserver(recorder.observe),
		)
		sealerCalled := false
		_, err = auth.StageServiceNodeConfiguration(
			operationCtx,
			targetID,
			configureToken,
			now,
			func(string) (string, string, error) {
				sealerCalled = true
				return cleanup.prefix + "unexpected-ciphertext", cleanup.prefix + "unexpected-nonce", nil
			},
		)
		if !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
			t.Fatalf("stable old-token reference error = %v, want shared-token conflict", err)
		}
		if sealerCalled {
			t.Fatal("stable old-token reference reached the sealer")
		}
		assertMariaDBFIX011StableConflictPhases(t, recorder.events)
		assertMariaDBFIX011ServicesUnchanged(t, ctx, auth, beforeTarget, beforeExternal)
		if mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
			t.Fatal("stable old-token conflict revoked the active token")
		}
	})

	t.Run("new token", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
		targetID := cleanup.prefix + "fix011-stable-new-target"
		externalID := cleanup.prefix + "fix011-stable-new-external"
		oldToken := createMariaDBServiceTokenPairService(
			t, ctx, auth, targetID, "update_agent", nil, cleanup,
		)
		now := time.Now().UTC()
		configureToken := cleanup.prefix + "fix011-stable-new-configure"
		if _, err := auth.SetServiceConfigureToken(
			ctx, targetID, security.HashToken(configureToken), now.Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}
		staged, err := auth.StageServiceNodeConfiguration(
			ctx,
			targetID,
			configureToken,
			now,
			func(string) (string, string, error) {
				return cleanup.prefix + "stable-new-ciphertext", cleanup.prefix + "stable-new-nonce", nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		cleanup.trackToken(staged.Token)
		createMariaDBServiceTokenPairService(
			t, ctx, auth, externalID, "update_agent", nil, cleanup,
		)
		setMariaDBFIX010ServiceTokenReference(
			t, ctx, db, externalID, "staged_node_token_id", staged.Token.ID,
		)
		beforeTarget, err := auth.GetService(ctx, targetID)
		if err != nil {
			t.Fatal(err)
		}
		beforeExternal, err := auth.GetService(ctx, externalID)
		if err != nil {
			t.Fatal(err)
		}
		recorder := newMariaDBFIX011ReferenceRaceRecorder(
			t, ctx, db, "activate_service_node_configuration", nil,
		)
		operationCtx := context.WithValue(
			ctx,
			mariaDBServiceTokenLockObserverContextKey{},
			mariaDBServiceTokenLockObserver(recorder.observe),
		)
		activatedToken, activatedService, alreadyActivated, err :=
			auth.ActivateServiceNodeConfiguration(
				operationCtx,
				targetID,
				staged.Token.ID,
				staged.ActivationToken,
				now.Add(time.Minute),
				ServiceRuntimeReport{},
			)
		if !errors.Is(err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
			t.Fatalf("stable new-token reference error = %v, want shared-token conflict", err)
		}
		if activatedToken.ID != "" || activatedService.ServiceID != "" || alreadyActivated {
			t.Fatalf(
				"stable new-token conflict returned success: token_id=%q service_id=%q already=%t",
				activatedToken.ID,
				activatedService.ServiceID,
				alreadyActivated,
			)
		}
		assertMariaDBFIX011StableConflictPhases(t, recorder.events)
		assertMariaDBFIX011ServicesUnchanged(t, ctx, auth, beforeTarget, beforeExternal)
		var stagedTokenRows int
		if err := db.QueryRowContext(
			ctx, `SELECT COUNT(*) FROM service_tokens WHERE id = ?`, staged.Token.ID,
		).Scan(&stagedTokenRows); err != nil {
			t.Fatal(err)
		}
		if stagedTokenRows != 0 || mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) {
			t.Fatalf(
				"stable new-token conflict changed token state: staged_rows=%d old_revoked=%t",
				stagedTokenRows,
				mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
			)
		}
	})
}

type mariaDBFIX011ReferenceMutation struct {
	serviceID string
	column    string
	tokenID   string
}

type mariaDBFIX011ReferenceRaceRecorder struct {
	t           *testing.T
	ctx         context.Context
	db          *sql.DB
	operation   string
	mutations   []mariaDBFIX011ReferenceMutation
	commitCount int
	events      []string
}

func newMariaDBFIX011ReferenceRaceRecorder(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	operation string,
	mutations []mariaDBFIX011ReferenceMutation,
) *mariaDBFIX011ReferenceRaceRecorder {
	t.Helper()
	return &mariaDBFIX011ReferenceRaceRecorder{
		t:         t,
		ctx:       ctx,
		db:        db,
		operation: operation,
		mutations: append([]mariaDBFIX011ReferenceMutation(nil), mutations...),
	}
}

func (recorder *mariaDBFIX011ReferenceRaceRecorder) observe(
	operation string,
	phase mariaDBServiceTokenLockPhase,
) {
	if operation != recorder.operation {
		return
	}
	recorder.events = append(recorder.events, string(phase))
	if phase != mariaDBServiceTokenTokenLocksHeld ||
		recorder.commitCount >= len(recorder.mutations) {
		return
	}
	mutation := recorder.mutations[recorder.commitCount]
	setMariaDBFIX010ServiceTokenReference(
		recorder.t,
		recorder.ctx,
		recorder.db,
		mutation.serviceID,
		mutation.column,
		mutation.tokenID,
	)
	recorder.commitCount++
	recorder.events = append(recorder.events, "external_reference_committed")
}

func clearMariaDBFIX011ServiceTokenReference(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	serviceID, column string,
) {
	t.Helper()
	var query string
	switch column {
	case "staged_node_previous_token_id":
		query = `UPDATE services SET staged_node_previous_token_id = NULL WHERE service_id = ?`
	case "staged_node_token_id":
		query = `UPDATE services SET staged_node_token_id = NULL WHERE service_id = ?`
	default:
		t.Fatalf("unsupported FIX-011 reference column %q", column)
	}
	result, err := db.ExecContext(ctx, query, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("clear FIX-011 reference affected=%d err=%v", affected, err)
	}
}

func assertMariaDBFIX011ServicesUnchanged(
	t *testing.T,
	ctx context.Context,
	auth MariaDBAuthStore,
	before ...RegisteredService,
) {
	t.Helper()
	for _, expected := range before {
		actual, err := auth.GetService(ctx, expected.ServiceID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf(
				"FIX-011 operation changed service state: before=%s after=%s",
				formatSafeRegisteredServiceDiagnostic(expected),
				formatSafeRegisteredServiceDiagnostic(actual),
			)
		}
	}
}

func assertMariaDBFIX011RetryPhaseOrder(t *testing.T, events []string) {
	t.Helper()
	assertMariaDBFIX011PhaseSubsequence(t, events, []string{
		"reference_discovery_complete",
		string(mariaDBServiceTokenServiceLocksHeld),
		string(mariaDBServiceTokenTokenLocksHeld),
		"external_reference_committed",
		"reference_set_mismatch",
		"reference_retry_start",
		"reference_discovery_complete",
		string(mariaDBServiceTokenServiceLocksHeld),
		string(mariaDBServiceTokenTokenLocksHeld),
		string(mariaDBServiceTokenBindingsValidated),
		"stable_auth_replay_conflict",
	})
	for event, want := range map[string]int{
		"reference_discovery_complete": 2,
		"reference_set_mismatch":       1,
		"reference_retry_start":        1,
		"stable_auth_replay_conflict":  1,
	} {
		if got := countMariaDBFIX011Event(events, event); got != want {
			t.Fatalf("%s phases = %d, want %d; events=%v", event, got, want, events)
		}
	}
}

func assertMariaDBFIX011StableConflictPhases(t *testing.T, events []string) {
	t.Helper()
	for event, want := range map[string]int{
		"reference_discovery_complete": 1,
		"reference_set_mismatch":       0,
		"reference_retry_start":        0,
		"stable_auth_replay_conflict":  1,
	} {
		if got := countMariaDBFIX011Event(events, event); got != want {
			t.Fatalf("%s phases = %d, want %d; events=%v", event, got, want, events)
		}
	}
}

func assertMariaDBFIX011ExhaustionPhases(t *testing.T, events []string) {
	t.Helper()
	if got := countMariaDBFIX011Event(events, "reference_discovery_complete"); got != mariaDBServiceTokenReferenceRetryLimit {
		t.Fatalf("reference discovery phases = %d, want %d; events=%v", got, mariaDBServiceTokenReferenceRetryLimit, events)
	}
	if got := countMariaDBFIX011Event(events, "reference_set_mismatch"); got != mariaDBServiceTokenReferenceRetryLimit {
		t.Fatalf("reference mismatch phases = %d, want %d; events=%v", got, mariaDBServiceTokenReferenceRetryLimit, events)
	}
	if got := countMariaDBFIX011Event(events, "reference_retry_start"); got != mariaDBServiceTokenReferenceRetryLimit-1 {
		t.Fatalf("reference retry phases = %d, want %d; events=%v", got, mariaDBServiceTokenReferenceRetryLimit-1, events)
	}
	if got := countMariaDBFIX011Event(events, "stable_auth_replay_conflict"); got != 0 {
		t.Fatalf("retry exhaustion reached stable classification %d times; events=%v", got, events)
	}
}

func countMariaDBFIX011Event(events []string, expected string) int {
	count := 0
	for _, event := range events {
		if event == expected {
			count++
		}
	}
	return count
}

func assertMariaDBFIX011PhaseSubsequence(t *testing.T, events, expected []string) {
	t.Helper()
	next := 0
	for _, event := range events {
		if next < len(expected) && event == expected[next] {
			next++
		}
	}
	if next != len(expected) {
		t.Fatalf("lock-phase subsequence stopped at %d/%d; events=%v", next, len(expected), events)
	}
}

func setMariaDBFIX010ServiceTokenReference(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	serviceID, column, tokenID string,
) {
	t.Helper()
	var query string
	switch column {
	case "token_id":
		query = `UPDATE services SET token_id = ? WHERE service_id = ?`
	case "staged_node_previous_token_id":
		query = `UPDATE services SET staged_node_previous_token_id = ? WHERE service_id = ?`
	case "staged_node_token_id":
		query = `UPDATE services SET staged_node_token_id = ? WHERE service_id = ?`
	default:
		t.Fatalf("unsupported FIX-010 reference column %q", column)
	}
	result, err := db.ExecContext(ctx, query, tokenID, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("set FIX-010 reference affected=%d err=%v", affected, err)
	}
}

type mariaDBServiceTokenPairFixture struct {
	auth      MariaDBAuthStore
	streams   MariaDBStreamStore
	cleanup   *mariaDBFIX005Cleanup
	serviceID string
	streamID  string
	token     ServiceToken
}

func newMariaDBServiceTokenPairFixture(
	t *testing.T,
	ctx context.Context,
	auth MariaDBAuthStore,
	streams MariaDBStreamStore,
	serviceOperation string,
) mariaDBServiceTokenPairFixture {
	t.Helper()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, auth.db)
	suffix := cleanup.prefix + "pair"
	serviceType := "worker"
	if serviceOperation == "artifact_report" {
		serviceType = "encoder_recorder"
	}
	serviceID := suffix + "-" + serviceType
	token := createMariaDBServiceTokenPairService(t, ctx, auth, serviceID, serviceType, nil, cleanup)
	stream, err := streams.CreateStream(ctx, cleanup.prefix+"token lock order")
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackStreamID(stream.ID)
	if _, err := auth.AssignServiceToStreamGuarded(ctx, ServiceAssignmentMutation{
		ServiceID: serviceID, StreamID: stream.ID, AssignmentRole: "primary",
	}); err != nil {
		t.Fatal(err)
	}
	return mariaDBServiceTokenPairFixture{
		auth: auth, streams: streams, cleanup: cleanup,
		serviceID: serviceID, streamID: stream.ID, token: token,
	}
}

type mariaDBServiceTokenMutationResult struct {
	token ServiceToken
	err   error
}

func runMariaDBServiceTokenPair(
	t *testing.T,
	parent context.Context,
	db *sql.DB,
	fixture mariaDBServiceTokenPairFixture,
	serviceOperation,
	tokenMutation string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	blocker := lockMariaDBServiceTokenForTest(t, ctx, db, fixture.token.ID)
	defer blocker.Rollback()

	serviceResult := make(chan error, 1)
	go func() {
		serviceResult <- runMariaDBServiceTokenPairOperation(ctx, fixture, serviceOperation)
	}()
	waitForMariaDBServiceRowLock(t, ctx, db, fixture.serviceID)

	operation := tokenMutation + "_service_token"
	phases := make(chan mariaDBServiceTokenLockPhase, 8)
	observer := mariaDBServiceTokenLockObserver(func(observedOperation string, phase mariaDBServiceTokenLockPhase) {
		if observedOperation == operation {
			phases <- phase
		}
	})
	mutationCtx := context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, observer)
	mutationResult := make(chan mariaDBServiceTokenMutationResult, 1)
	go func() {
		if tokenMutation == "rotate" {
			token, err := fixture.auth.RotateServiceToken(mutationCtx, fixture.token.ID)
			mutationResult <- mariaDBServiceTokenMutationResult{token: token, err: err}
			return
		}
		mutationResult <- mariaDBServiceTokenMutationResult{err: fixture.auth.RevokeServiceToken(mutationCtx, fixture.token.ID)}
	}()
	if phase := receiveMariaDBServiceTokenPhase(t, phases, "token mutation service-lock attempt"); phase != mariaDBServiceTokenBeforeServiceLocks {
		t.Fatalf("first token mutation phase = %q, want %q", phase, mariaDBServiceTokenBeforeServiceLocks)
	}
	select {
	case phase := <-phases:
		t.Fatalf("token mutation advanced to %q while the canonical service row was held", phase)
	case <-time.After(75 * time.Millisecond):
	}

	if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	serviceErr := receiveMariaDBServiceTokenError(t, serviceResult, "service operation")
	mutation := receiveMariaDBServiceTokenMutation(t, mutationResult)
	fixture.cleanup.trackToken(mutation.token)
	assertMariaDBServiceTokenOperationError(t, serviceOperation, serviceErr)
	if serviceOperation == "service_delete" {
		if !errors.Is(mutation.err, ErrNotFound) {
			t.Fatalf("%s after service delete error = %v, want ErrNotFound", tokenMutation, mutation.err)
		}
	} else {
		assertMariaDBServiceTokenOperationError(t, tokenMutation, mutation.err)
	}
	assertMariaDBServiceTokenPhaseOrder(t, phases)
	assertMariaDBServiceTokenPairFinalState(t, ctx, db, fixture, serviceOperation, tokenMutation, mutation)
}

func runMariaDBServiceTokenPairOperation(
	ctx context.Context,
	fixture mariaDBServiceTokenPairFixture,
	operation string,
) error {
	switch operation {
	case "heartbeat":
		_, err := fixture.auth.Heartbeat(ctx, fixture.token, ServiceHeartbeat{
			ServiceID: fixture.serviceID, Status: "online", CurrentStreamID: fixture.streamID,
		})
		return err
	case "artifact_report":
		return fixture.streams.WriteStreamArtifactReport(
			ctx,
			fixture.token,
			ServiceStreamEvent{
				ServiceID: fixture.serviceID,
				StreamID:  fixture.streamID,
				EventType: "archive.artifacts.reported",
			},
			[]StreamArtifact{{
				Kind: "archive", Name: "final.mp4",
				RelativePath: "final/" + fixture.streamID + "/final.mp4",
				SizeBytes:    1,
			}},
		)
	case "service_delete":
		return fixture.auth.DeleteService(ctx, fixture.serviceID)
	default:
		return fmt.Errorf("unknown service operation %q", operation)
	}
}

func lockMariaDBServiceTokenForTest(t *testing.T, ctx context.Context, db *sql.DB, tokenID string) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lockedID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM service_tokens WHERE id = ? FOR UPDATE`, tokenID).Scan(&lockedID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	return tx
}

func waitForMariaDBServiceRowLock(t *testing.T, ctx context.Context, db *sql.DB, serviceID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		probe, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var lockedID string
		err = probe.QueryRowContext(ctx, `SELECT service_id FROM services WHERE service_id = ? FOR UPDATE NOWAIT`, serviceID).Scan(&lockedID)
		_ = probe.Rollback()
		if err == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && (mysqlErr.Number == 1205 || mysqlErr.Number == 3572) {
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("service %s disappeared before the lock barrier", serviceID)
		}
		t.Fatalf("probe service row lock: %v", err)
	}
	t.Fatalf("service operation did not acquire the service row before the lock barrier")
}

func receiveMariaDBServiceTokenPhase(
	t *testing.T,
	phases <-chan mariaDBServiceTokenLockPhase,
	label string,
) mariaDBServiceTokenLockPhase {
	t.Helper()
	select {
	case phase := <-phases:
		return phase
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not reach its bounded barrier", label)
		return ""
	}
}

func receiveMariaDBServiceTokenError(t *testing.T, result <-chan error, label string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not complete before timeout", label)
		return nil
	}
}

func receiveMariaDBServiceTokenMutation(
	t *testing.T,
	result <-chan mariaDBServiceTokenMutationResult,
) mariaDBServiceTokenMutationResult {
	t.Helper()
	select {
	case mutation := <-result:
		return mutation
	case <-time.After(10 * time.Second):
		t.Fatal("token mutation did not complete before timeout")
		return mariaDBServiceTokenMutationResult{}
	}
}

func assertMariaDBServiceTokenOperationError(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && (mysqlErr.Number == 1213 || mysqlErr.Number == 1205) {
		t.Fatalf("%s hit MariaDB lock failure %d: %v", label, mysqlErr.Number, err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("%s timed out: %v", label, err)
	}
	t.Fatalf("%s returned unexpected store error: %v", label, err)
}

func assertMariaDBServiceTokenPhaseOrder(t *testing.T, phases <-chan mariaDBServiceTokenLockPhase) {
	t.Helper()
	seenServiceLocks := false
	seenTokenLocks := false
	for {
		select {
		case phase := <-phases:
			switch phase {
			case mariaDBServiceTokenServiceLocksHeld:
				seenServiceLocks = true
			case mariaDBServiceTokenBeforeTokenLocks, mariaDBServiceTokenTokenLocksHeld:
				if !seenServiceLocks {
					t.Fatalf("token phase %q occurred before service locks", phase)
				}
				if phase == mariaDBServiceTokenTokenLocksHeld {
					seenTokenLocks = true
				}
			case mariaDBServiceTokenBindingsValidated:
				if !seenServiceLocks || !seenTokenLocks {
					t.Fatalf("binding validation preceded canonical locks: service=%v token=%v", seenServiceLocks, seenTokenLocks)
				}
			}
		default:
			return
		}
	}
}

func assertMariaDBServiceTokenPairFinalState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture mariaDBServiceTokenPairFixture,
	serviceOperation,
	tokenMutation string,
	mutation mariaDBServiceTokenMutationResult,
) {
	t.Helper()
	oldRevoked := mariaDBServiceTokenRevoked(t, ctx, db, fixture.token.ID)
	if serviceOperation == "service_delete" {
		if _, err := fixture.auth.GetService(ctx, fixture.serviceID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted service lookup error = %v, want ErrNotFound", err)
		}
		assignments, err := fixture.auth.ListStreamAssignments(ctx, fixture.streamID)
		if err != nil {
			t.Fatal(err)
		}
		if len(assignments) != 0 || !oldRevoked {
			t.Fatalf("service delete left partial state: assignments=%s old_revoked=%v", formatSafeSensitiveCompositeDiagnostic(assignments), oldRevoked)
		}
		return
	}
	service, err := fixture.auth.GetService(ctx, fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	assignments, err := fixture.auth.ListServiceAssignmentsForService(ctx, fixture.serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].StreamID != fixture.streamID || service.CurrentStreamID != fixture.streamID {
		t.Fatalf("assignment/current_stream_id split: service=%s assignments=%#v", formatSafeRegisteredServiceDiagnostic(service), assignments)
	}
	if !oldRevoked || service.LastHeartbeatAt != nil || len(service.ReportedCapabilities) != 0 {
		t.Fatalf("token mutation left runtime readiness active: service=%s old_revoked=%v", formatSafeRegisteredServiceDiagnostic(service), oldRevoked)
	}
	if tokenMutation == "rotate" {
		if mutation.token.ID == "" || service.TokenID != mutation.token.ID || mariaDBServiceTokenRevoked(t, ctx, db, mutation.token.ID) {
			t.Fatalf("rotation binding mismatch: service=%s token=%s", formatSafeRegisteredServiceDiagnostic(service), formatSafeServiceTokenDiagnostic("rotate", mutation.token, 0, "unexpected_result"))
		}
	} else if service.TokenID != fixture.token.ID {
		t.Fatalf("revoke changed service token binding: service=%s old=%s", formatSafeRegisteredServiceDiagnostic(service), fixture.token.ID)
	}
	if serviceOperation == "artifact_report" {
		artifacts, err := fixture.streams.ListStreamArtifacts(ctx, fixture.streamID)
		if err != nil {
			t.Fatal(err)
		}
		if len(artifacts) == 0 {
			t.Fatal("artifact report committed no artifact")
		}
	}
}

func mariaDBServiceTokenRevoked(t *testing.T, ctx context.Context, db *sql.DB, tokenID string) bool {
	t.Helper()
	var revoked bool
	if err := db.QueryRowContext(ctx, `SELECT revoked_at IS NOT NULL FROM service_tokens WHERE id = ?`, tokenID).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	return revoked
}

func createMariaDBServiceTokenPairService(
	t *testing.T,
	ctx context.Context,
	auth MariaDBAuthStore,
	serviceID,
	serviceType string,
	existing *ServiceToken,
	cleanup *mariaDBFIX005Cleanup,
) ServiceToken {
	t.Helper()
	cleanup.trackServiceID(serviceID)
	var token ServiceToken
	if existing == nil {
		scopes := []string{"service.register", "service.heartbeat"}
		if serviceType == "encoder_recorder" {
			scopes = append(scopes, "encoder.status.write")
		}
		if serviceType == "update_agent" {
			scopes = append(scopes, "updates.claim", "updates.report", "updates.authorize")
		}
		var err error
		token, err = auth.CreateServiceToken(ctx, serviceType, scopes)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		token = *existing
	}
	cleanup.trackToken(token)
	registration := ServiceRegistration{
		ServiceID: serviceID, ServiceType: serviceType, ServiceName: serviceID,
		PublicURL: "https://" + serviceID + ".example.com", Port: 443, SSLEnabled: true,
	}
	if _, err := auth.PrecreateService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.RegisterService(ctx, token, registration); err != nil {
		t.Fatal(err)
	}
	return token
}

func runMariaDBSharedServiceTokenNodeRotationPair(
	t *testing.T,
	parent context.Context,
	db *sql.DB,
	auth MariaDBAuthStore,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "worker-a"
	heartbeatID := cleanup.prefix + "worker-b"
	oldToken := createMariaDBServiceTokenPairService(t, ctx, auth, targetID, "worker", nil, cleanup)
	createMariaDBServiceTokenPairService(t, ctx, auth, heartbeatID, "worker", &oldToken, cleanup)
	blocker := lockMariaDBServiceTokenForTest(t, ctx, db, oldToken.ID)
	defer blocker.Rollback()
	heartbeatResult := make(chan error, 1)
	go func() {
		_, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{ServiceID: heartbeatID, Status: "online"})
		heartbeatResult <- err
	}()
	waitForMariaDBServiceRowLock(t, ctx, db, heartbeatID)

	phases := make(chan mariaDBServiceTokenLockPhase, 8)
	observer := mariaDBServiceTokenLockObserver(func(operation string, phase mariaDBServiceTokenLockPhase) {
		if operation == "rotate_service_node_token" {
			phases <- phase
		}
	})
	mutationCtx := context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, observer)
	rotationResult := make(chan mariaDBServiceTokenMutationResult, 1)
	go func() {
		token, _, err := auth.RotateServiceNodeToken(mutationCtx, targetID, oldToken.ID, func(string) (string, string, error) {
			return "test-ciphertext", "test-nonce", nil
		})
		rotationResult <- mariaDBServiceTokenMutationResult{token: token, err: err}
	}()
	if phase := receiveMariaDBServiceTokenPhase(t, phases, "shared node token service-lock attempt"); phase != mariaDBServiceTokenBeforeServiceLocks {
		t.Fatalf("first shared rotation phase = %q", phase)
	}
	select {
	case phase := <-phases:
		t.Fatalf("shared rotation advanced to %q while referenced service was locked", phase)
	case <-time.After(75 * time.Millisecond):
	}
	if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	assertMariaDBServiceTokenOperationError(t, "shared heartbeat", receiveMariaDBServiceTokenError(t, heartbeatResult, "shared heartbeat"))
	rotation := receiveMariaDBServiceTokenMutation(t, rotationResult)
	cleanup.trackToken(rotation.token)
	assertMariaDBServiceTokenOperationError(t, "shared node rotation", rotation.err)
	assertMariaDBServiceTokenPhaseOrder(t, phases)
	target, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	heartbeatService, err := auth.GetService(ctx, heartbeatID)
	if err != nil {
		t.Fatal(err)
	}
	if target.TokenID != rotation.token.ID || heartbeatService.TokenID != oldToken.ID ||
		mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
		mariaDBServiceTokenRevoked(t, ctx, db, rotation.token.ID) {
		t.Fatalf("shared token rotation split state: target=%s heartbeat=%s token=%s", formatSafeRegisteredServiceDiagnostic(target), formatSafeRegisteredServiceDiagnostic(heartbeatService), formatSafeServiceTokenDiagnostic("rotate", rotation.token, 0, "unexpected_result"))
	}
}

func runMariaDBSharedServiceTokenNodeConfigurePair(
	t *testing.T,
	parent context.Context,
	db *sql.DB,
	auth MariaDBAuthStore,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	suffix := cleanup.prefix + "configure"
	targetID := cleanup.prefix + "worker-a"
	heartbeatID := cleanup.prefix + "worker-b"
	oldToken := createMariaDBServiceTokenPairService(t, ctx, auth, targetID, "worker", nil, cleanup)
	createMariaDBServiceTokenPairService(t, ctx, auth, heartbeatID, "worker", &oldToken, cleanup)
	now := time.Now().UTC()
	rawConfigureToken := "configure-node-" + suffix
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(rawConfigureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}

	blocker := lockMariaDBServiceTokenForTest(t, ctx, db, oldToken.ID)
	defer blocker.Rollback()
	heartbeatResult := make(chan error, 1)
	go func() {
		_, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{ServiceID: heartbeatID, Status: "online"})
		heartbeatResult <- err
	}()
	waitForMariaDBServiceRowLock(t, ctx, db, heartbeatID)

	phases := make(chan mariaDBServiceTokenLockPhase, 8)
	observer := mariaDBServiceTokenLockObserver(func(operation string, phase mariaDBServiceTokenLockPhase) {
		if operation == "configure_service_node" {
			phases <- phase
		}
	})
	mutationCtx := context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, observer)
	configurationResult := make(chan mariaDBServiceTokenMutationResult, 1)
	go func() {
		token, _, err := auth.ConfigureServiceNode(
			mutationCtx,
			targetID,
			rawConfigureToken,
			now,
			ServiceRuntimeReport{},
			func(string) (string, string, error) { return "test-ciphertext", "test-nonce", nil },
		)
		configurationResult <- mariaDBServiceTokenMutationResult{token: token, err: err}
	}()
	assertMariaDBSharedNodeMutationBlocksOnService(t, phases, blocker, "configure service node")
	assertMariaDBServiceTokenOperationError(
		t, "shared configure heartbeat", receiveMariaDBServiceTokenError(t, heartbeatResult, "shared configure heartbeat"),
	)
	configured := receiveMariaDBServiceTokenMutation(t, configurationResult)
	cleanup.trackToken(configured.token)
	assertMariaDBServiceTokenOperationError(t, "shared node configure", configured.err)
	assertMariaDBServiceTokenPhaseOrder(t, phases)
	assertMariaDBSharedNodeMutationFinalState(t, ctx, db, auth, targetID, heartbeatID, oldToken.ID, configured.token)
}

func runMariaDBSharedServiceTokenNodeActivationPair(
	t *testing.T,
	parent context.Context,
	db *sql.DB,
	auth MariaDBAuthStore,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	suffix := cleanup.prefix + "activate"
	targetID := cleanup.prefix + "updater-a"
	heartbeatID := cleanup.prefix + "updater-b"
	oldToken := createMariaDBServiceTokenPairService(t, ctx, auth, targetID, "update_agent", nil, cleanup)
	now := time.Now().UTC()
	rawConfigureToken := "activate-node-" + suffix
	if _, err := auth.SetServiceConfigureToken(
		ctx, targetID, security.HashToken(rawConfigureToken), now.Add(time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	staged, err := auth.StageServiceNodeConfiguration(
		ctx,
		targetID,
		rawConfigureToken,
		now,
		func(string) (string, string, error) { return "staged-ciphertext", "staged-nonce", nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	cleanup.trackToken(staged.Token)
	createMariaDBServiceTokenPairService(t, ctx, auth, heartbeatID, "update_agent", &oldToken, cleanup)
	beforeTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	beforeHeartbeat, err := auth.GetService(ctx, heartbeatID)
	if err != nil {
		t.Fatal(err)
	}

	blocker := lockMariaDBServiceTokenForTest(t, ctx, db, oldToken.ID)
	defer blocker.Rollback()
	heartbeatResult := make(chan error, 1)
	go func() {
		_, err := auth.Heartbeat(ctx, oldToken, ServiceHeartbeat{ServiceID: heartbeatID, Status: "online"})
		heartbeatResult <- err
	}()
	waitForMariaDBServiceRowLock(t, ctx, db, heartbeatID)

	phases := make(chan mariaDBServiceTokenLockPhase, 8)
	observer := mariaDBServiceTokenLockObserver(func(operation string, phase mariaDBServiceTokenLockPhase) {
		if operation == "activate_service_node_configuration" &&
			isMariaDBServiceTokenCanonicalLockPhase(phase) {
			phases <- phase
		}
	})
	mutationCtx := context.WithValue(ctx, mariaDBServiceTokenLockObserverContextKey{}, observer)
	activationResult := make(chan mariaDBServiceTokenMutationResult, 1)
	go func() {
		token, _, _, err := auth.ActivateServiceNodeConfiguration(
			mutationCtx,
			targetID,
			staged.Token.ID,
			staged.ActivationToken,
			now.Add(time.Minute),
			ServiceRuntimeReport{},
		)
		activationResult <- mariaDBServiceTokenMutationResult{token: token, err: err}
	}()
	assertMariaDBSharedNodeMutationBlocksOnService(t, phases, blocker, "activate service node configuration")
	assertMariaDBServiceTokenOperationError(
		t, "shared activation heartbeat", receiveMariaDBServiceTokenError(t, heartbeatResult, "shared activation heartbeat"),
	)
	activated := receiveMariaDBServiceTokenMutation(t, activationResult)
	if !errors.Is(activated.err, ErrSystemUpdateRuntimeTokenRotationSharedToken) {
		t.Fatalf("shared node activation error = %v, want shared-token conflict", activated.err)
	}
	if activated.token.ID != "" {
		t.Fatalf("shared node activation returned token = %s", formatSafeServiceTokenDiagnostic("activate", activated.token, 0, "unexpected_success"))
	}
	assertMariaDBServiceTokenPhaseOrder(t, phases)
	afterTarget, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	afterHeartbeat, err := auth.GetService(ctx, heartbeatID)
	if err != nil {
		t.Fatal(err)
	}
	var stagedTokenRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM service_tokens WHERE id = ?`, staged.Token.ID).Scan(&stagedTokenRows); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterTarget, beforeTarget) ||
		afterHeartbeat.TokenID != beforeHeartbeat.TokenID ||
		afterHeartbeat.StagedNodePreviousTokenID != beforeHeartbeat.StagedNodePreviousTokenID ||
		afterHeartbeat.StagedNodeTokenID != beforeHeartbeat.StagedNodeTokenID ||
		mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID) ||
		stagedTokenRows != 0 {
		t.Fatalf(
			"shared node activation partially mutated state: target=%s heartbeat=%s old_revoked=%t staged_rows=%d",
			formatSafeRegisteredServiceDiagnostic(afterTarget),
			formatSafeRegisteredServiceDiagnostic(afterHeartbeat),
			mariaDBServiceTokenRevoked(t, ctx, db, oldToken.ID),
			stagedTokenRows,
		)
	}
}

func assertMariaDBSharedNodeMutationBlocksOnService(
	t *testing.T,
	phases <-chan mariaDBServiceTokenLockPhase,
	blocker *sql.Tx,
	label string,
) {
	t.Helper()
	if phase := receiveMariaDBServiceTokenPhase(t, phases, label+" service-lock attempt"); phase != mariaDBServiceTokenBeforeServiceLocks {
		t.Fatalf("first %s phase = %q", label, phase)
	}
	select {
	case phase := <-phases:
		t.Fatalf("%s advanced to %q while referenced service was locked", label, phase)
	case <-time.After(75 * time.Millisecond):
	}
	if err := blocker.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
}

func isMariaDBServiceTokenCanonicalLockPhase(phase mariaDBServiceTokenLockPhase) bool {
	switch phase {
	case mariaDBServiceTokenBeforeServiceLocks,
		mariaDBServiceTokenServiceLocksHeld,
		mariaDBServiceTokenBeforeTokenLocks,
		mariaDBServiceTokenTokenLocksHeld,
		mariaDBServiceTokenBindingsValidated:
		return true
	default:
		return false
	}
}

func assertMariaDBSharedNodeMutationFinalState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	auth MariaDBAuthStore,
	targetID,
	heartbeatID,
	oldTokenID string,
	newToken ServiceToken,
) {
	t.Helper()
	target, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	heartbeatService, err := auth.GetService(ctx, heartbeatID)
	if err != nil {
		t.Fatal(err)
	}
	if newToken.ID == "" || target.TokenID != newToken.ID || heartbeatService.TokenID != oldTokenID ||
		mariaDBServiceTokenRevoked(t, ctx, db, oldTokenID) ||
		mariaDBServiceTokenRevoked(t, ctx, db, newToken.ID) {
		t.Fatalf("shared node mutation split state: target=%s heartbeat=%s token=%s", formatSafeRegisteredServiceDiagnostic(target), formatSafeRegisteredServiceDiagnostic(heartbeatService), formatSafeServiceTokenDiagnostic("node_mutation", newToken, 0, "unexpected_result"))
	}
}

func assertMariaDBSharedTokenDeleteFailsClosed(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	auth MariaDBAuthStore,
) {
	t.Helper()
	cleanup := newMariaDBFIX005Cleanup(t, ctx, db)
	targetID := cleanup.prefix + "worker-a"
	otherID := cleanup.prefix + "worker-b"
	token := createMariaDBServiceTokenPairService(t, ctx, auth, targetID, "worker", nil, cleanup)
	createMariaDBServiceTokenPairService(t, ctx, auth, otherID, "worker", &token, cleanup)
	if err := auth.DeleteService(ctx, targetID); !errors.Is(err, ErrServiceAssignmentConflict) {
		t.Fatalf("shared-token delete error = %v, want ErrServiceAssignmentConflict", err)
	}
	target, err := auth.GetService(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.GetService(ctx, otherID)
	if err != nil {
		t.Fatal(err)
	}
	if target.TokenID != token.ID || other.TokenID != token.ID || mariaDBServiceTokenRevoked(t, ctx, db, token.ID) {
		t.Fatalf(
			"shared-token delete partially mutated state: target=%s other=%s",
			formatSafeRegisteredServiceDiagnostic(target),
			formatSafeRegisteredServiceDiagnostic(other),
		)
	}
}
