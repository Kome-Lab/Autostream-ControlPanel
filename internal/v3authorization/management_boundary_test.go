package v3authorization

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// This supplements the existing source boundaries; it does not replace them.
func TestSQLAuthorityManagementSourceBoundary(t *testing.T) {
	names, err := filepath.Glob("management_*.go")
	if err != nil || len(names) == 0 {
		t.Fatal("management source inventory", err)
	}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range file.Imports {
			pkg, err := strconv.Unquote(entry.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if pkg == "os/exec" || pkg == "syscall" || pkg == "unsafe" || pkg == "net" || pkg == "net/http" ||
				strings.Contains(pkg, "/provider") || strings.Contains(pkg, "/media") || strings.Contains(pkg, "/ssh") {
				t.Fatal("management crossed the CP data boundary", name, pkg)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			lower := strings.ToLower(value)
			for _, forbidden := range []string{"user_roles", "role_permissions", "update_agent_policies", "super_admin"} {
				if strings.Contains(lower, forbidden) {
					t.Fatal("management acquired v2 authority", name, forbidden)
				}
			}
			return true
		})
	}
	grant := reflect.TypeOf(ManagementGrant{})
	for i := 0; i < grant.NumField(); i++ {
		name := strings.ToLower(grant.Field(i).Name)
		for _, forbidden := range []string{"issue", "readhistory", "password", "token", "secret", "session"} {
			if strings.Contains(name, forbidden) {
				t.Fatal("management grant mixed business authority or credentials", name)
			}
		}
	}
	handler, err := os.ReadFile("../httpapi/server_v3_authorization_management.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(handler), ".Bootstrap(") || strings.Contains(string(handler), "SQLAuthority.Save(") {
		t.Fatal("HTTP bypassed independent bootstrap/management service")
	}
}
