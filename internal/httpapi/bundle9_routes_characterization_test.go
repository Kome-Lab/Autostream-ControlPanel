package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/store"
)

type bundle9RouteFixture struct {
	SchemaVersion  int    `json:"schemaVersion"`
	BaselineCommit string `json:"baselineCommit"`
	Registrations  []struct {
		Statement string `json:"statement"`
		Pattern   string `json:"pattern"`
	} `json:"registrations"`
}

func loadBundle9RouteFixture(t *testing.T) bundle9RouteFixture {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "bundle9-routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture bundle9RouteFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SchemaVersion != 1 || fixture.BaselineCommit != "792c6c56506c26ffd81b18c1793211e8e78be44d" || len(fixture.Registrations) == 0 {
		t.Fatal("route fixture must retain the accepted, nonempty Bundle 8B baseline")
	}
	return fixture
}

// This is a source-connected contract check, not a file-location oracle. It
// follows the actual Server.routes composition through domain registration
// methods and retains every ordered route, handler, permission and wrapper.
func TestBundle9RouteRegistrationCharacterization(t *testing.T) {
	fixture := loadBundle9RouteFixture(t)
	fileSet, methods := bundle9ServerMethods(t)
	got := bundle9RegistrationStatements(t, fileSet, methods, "routes", map[string]bool{})
	want := make([]string, 0, len(fixture.Registrations))
	for _, registration := range fixture.Registrations {
		want = append(want, bundle9GoTokens(t, registration.Statement))
	}
	if !reflect.DeepEqual(got, want) {
		limit := len(got)
		if len(want) < limit {
			limit = len(want)
		}
		for i := 0; i < limit; i++ {
			if got[i] != want[i] {
				t.Errorf("registration %d changed:\n got %s\nwant %s", i, got[i], want[i])
			}
		}
		t.Fatalf("ordered route registration contract differs: got %d statements, want %d", len(got), len(want))
	}
}

func TestBundle9RegisteredRoutesResolveThroughActualMux(t *testing.T) {
	fixture := loadBundle9RouteFixture(t)
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()))
	wildcard := regexp.MustCompile(`\{[^}]+\}`)
	profileBase := regexp.MustCompile(`^s\.registerProfileRoutes\("([^"]+)"`)
	patterns := make([]string, 0, len(fixture.Registrations)+20)
	for _, registration := range fixture.Registrations {
		if registration.Pattern != "" {
			patterns = append(patterns, registration.Pattern)
			continue
		}
		match := profileBase.FindStringSubmatch(registration.Statement)
		if len(match) != 2 {
			t.Fatalf("fixture has an unrecognized registration: %s", registration.Statement)
		}
		patterns = append(patterns, "GET "+match[1], "POST "+match[1], "GET "+match[1]+"/{id}", "PUT "+match[1]+"/{id}", "DELETE "+match[1]+"/{id}")
	}
	for _, pattern := range patterns {
		t.Run(pattern, func(t *testing.T) {
			method, path, ok := strings.Cut(pattern, " ")
			if !ok {
				t.Fatal("fixture route has no method")
			}
			path = strings.ReplaceAll(path, "{$}", "")
			path = wildcard.ReplaceAllString(path, "bundle9-probe")
			request := httptest.NewRequest(method, path, nil)
			_, actual := server.mux.Handler(request)
			if actual != pattern {
				t.Fatalf("mux selected %q, want %q", actual, pattern)
			}
		})
	}
}

func TestBundle9UnauthenticatedDomainResponsesRetainSecurityMiddleware(t *testing.T) {
	server := NewServer(store.NewMemoryStreamStore(), WithAuthStore(store.NewMemoryAuthStore()))
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/streams", ""},
		{http.MethodPost, "/streams", `{"name":"probe"}`},
		{http.MethodPost, "/streams", strings.Repeat("x", maxControlRequestBytes+1)},
		{http.MethodGet, "/profiles/encoder", ""},
		{http.MethodGet, "/nodes", ""},
		{http.MethodPut, "/nodes/probe", `{}`},
		{http.MethodGet, "/observability/incidents", ""},
		{http.MethodPost, "/auth/logout", ""},
	} {
		t.Run(fmt.Sprintf("%s_%s_body_%d", tc.method, tc.path, len(tc.body)), func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Body.String() != "{\"code\":\"unauthorized\"}\n" {
				t.Fatalf("unauthenticated domain response changed: status=%d body=%q", response.Code, response.Body.String())
			}
			for header, value := range map[string]string{
				"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY",
				"Referrer-Policy": "no-referrer", "Content-Security-Policy": "default-src 'self'",
			} {
				if response.Header().Get(header) != value {
					t.Errorf("%s=%q, want %q", header, response.Header().Get(header), value)
				}
			}
		})
	}
}

func bundle9ServerMethods(t *testing.T) (*token.FileSet, map[string]*ast.FuncDecl) {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	methods := map[string]*ast.FuncDecl{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			pointer, ok := function.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			owner, ok := pointer.X.(*ast.Ident)
			if !ok || owner.Name != "Server" {
				continue
			}
			if _, duplicate := methods[function.Name.Name]; duplicate {
				t.Fatalf("Server method has multiple owners: %s", function.Name.Name)
			}
			methods[function.Name.Name] = function
		}
	}
	return fileSet, methods
}

func bundle9RegistrationStatements(t *testing.T, fileSet *token.FileSet, methods map[string]*ast.FuncDecl, name string, visiting map[string]bool) []string {
	t.Helper()
	method := methods[name]
	if method == nil || visiting[name] {
		t.Fatalf("route composition is missing or recursive at %s", name)
	}
	visiting[name] = true
	defer delete(visiting, name)
	var out []string
	for _, statement := range method.Body.List {
		expression, ok := statement.(*ast.ExprStmt)
		if !ok {
			t.Fatalf("route composition %s contains an unsupported statement", name)
		}
		call, ok := expression.X.(*ast.CallExpr)
		if !ok {
			t.Fatalf("route composition %s contains a non-call", name)
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && len(call.Args) == 0 && strings.HasPrefix(selector.Sel.Name, "register") {
			if receiver, ok := selector.X.(*ast.Ident); !ok || receiver.Name != "s" {
				t.Fatalf("domain registration receiver changed in %s", name)
			}
			out = append(out, bundle9RegistrationStatements(t, fileSet, methods, selector.Sel.Name, visiting)...)
			continue
		}
		var rendered bytes.Buffer
		if err := format.Node(&rendered, fileSet, statement); err != nil {
			t.Fatal(err)
		}
		out = append(out, bundle9GoTokens(t, rendered.String()))
	}
	return out
}

func bundle9GoTokens(t *testing.T, source string) string {
	t.Helper()
	fileSet := token.NewFileSet()
	file := fileSet.AddFile("route", fileSet.Base(), len(source))
	var lexer scanner.Scanner
	lexer.Init(file, []byte(source), func(position token.Position, message string) {
		t.Fatalf("route token error at %s: %s", position, message)
	}, 0)
	var out []string
	for {
		_, kind, literal := lexer.Scan()
		if kind == token.EOF {
			break
		}
		if kind == token.SEMICOLON {
			continue
		}
		if literal == "" {
			literal = kind.String()
		}
		out = append(out, literal)
	}
	return strings.Join(out, " ")
}
