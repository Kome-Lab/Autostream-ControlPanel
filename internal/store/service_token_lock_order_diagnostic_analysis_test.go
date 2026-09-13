package store

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

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
