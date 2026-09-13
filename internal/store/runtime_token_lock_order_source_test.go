package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestRuntimeTokenLockOrderSourceReadsActualOwners(t *testing.T) {
	bodies, err := readRuntimeTokenLockOrderSource(os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 8 {
		t.Fatalf("method count=%d, want 8", len(bodies))
	}
	for _, name := range runtimeTokenLockOrderMethods {
		if err := runtimeTokenLockOrderError(name, bodies[name]); err != nil {
			t.Error(err)
		}
	}
}

func TestRuntimeTokenLockOrderSourceRejectsMutants(t *testing.T) {
	const owner = "runtime_token_rotation_stage_mariadb.go"
	const name = "stageSystemUpdateRuntimeTokenRotationOnce"
	const declaration = "func (s *MariaDBSystemUpdateStore) " + name
	sources := runtimeTokenOwnerSources(t)
	positions := token.NewFileSet()
	parsed, err := parser.ParseFile(positions, owner, sources[owner], 0)
	if err != nil {
		t.Fatal(err)
	}
	var methodSource string
	for _, declaration := range parsed.Decls {
		if method, ok := declaration.(*ast.FuncDecl); ok && method.Name.Name == name {
			file := positions.File(method.Pos())
			methodSource = string(sources[owner][file.Offset(method.Pos()):file.Offset(method.End())])
		}
	}
	if methodSource == "" {
		t.Fatal("actual stage method is missing")
	}
	for _, test := range []struct {
		name, old, replacement, want string
	}{
		{name: "missing_owner", want: "runtime token owner " + owner},
		{name: "invalid_source", old: "package store", replacement: "package", want: "runtime token owner " + owner},
		{name: "wrong_package", old: "package store", replacement: "package other", want: "want store"},
		{name: "missing_method", old: declaration, replacement: declaration + "Removed", want: name + " not found"},
		{name: "duplicate_method", old: methodSource, replacement: methodSource + "\n" + methodSource, want: "duplicate runtime token method " + name},
		{name: "wrong_receiver", old: declaration, replacement: "func (s *OtherStore) " + name, want: "must have receiver *MariaDBSystemUpdateStore"},
		{name: "value_receiver", old: declaration, replacement: "func (s MariaDBSystemUpdateStore) " + name, want: "must have receiver *MariaDBSystemUpdateStore"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := readRuntimeTokenLockOrderSource(func(path string) ([]byte, error) {
				if path != owner {
					return sources[path], nil
				}
				if test.name == "missing_owner" {
					return nil, os.ErrNotExist
				}
				if strings.Count(string(sources[path]), test.old) != 1 {
					t.Fatal("mutation must change exactly one actual declaration")
				}
				return []byte(strings.Replace(string(sources[path]), test.old, test.replacement, 1)), nil
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mutant error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestRuntimeTokenLockOrderChecksRejectMutants(t *testing.T) {
	sources := runtimeTokenOwnerSources(t)
	bodies, err := readRuntimeTokenLockOrderSource(os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range runtimeTokenLockOrderMethods {
		body := bodies[name]
		for _, test := range []struct {
			name, body, want string
		}{
			{"missing_lock", strings.ReplaceAll(body, "lockMariaDBRuntimeTokenRotationPlan(", "removedRuntimeLockPlan("), "does not use the canonical runtime service-token lock plan"},
			{"token_before_lock", "{\nselectActiveServiceTokenForUpdate()\n" + body[1:], "reaches a token phase before the canonical runtime lock plan"},
		} {
			t.Run(name+"/"+test.name, func(t *testing.T) {
				changed := 0
				mutant, err := readRuntimeTokenLockOrderSource(func(path string) ([]byte, error) {
					source := string(sources[path])
					if strings.Contains(source, body) {
						changed++
						source = strings.Replace(source, body, test.body, 1)
					}
					return []byte(source), nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if changed != 1 {
					t.Fatalf("mutated %d owners, want 1", changed)
				}
				if err := runtimeTokenLockOrderError(name, mutant[name]); err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("mutant error=%v, want %q", err, test.want)
				}
			})
		}
	}
}

func runtimeTokenOwnerSources(t *testing.T) map[string][]byte {
	t.Helper()
	sources := make(map[string][]byte, len(runtimeTokenLockOrderOwners))
	for _, path := range runtimeTokenLockOrderOwners {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sources[path] = source
	}
	return sources
}
