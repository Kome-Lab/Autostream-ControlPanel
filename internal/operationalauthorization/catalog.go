package operationalauthorization

import (
	"github.com/example/autostream-control-panel/internal/security"
	"sort"
)

var operationCatalog = func() map[string]struct{} {
	out := make(map[string]struct{}, len(security.DefaultPermissions))
	for _, operation := range security.DefaultPermissions {
		out[operation] = struct{}{}
	}
	return out
}()

func KnownOperation(operation string) bool { _, ok := operationCatalog[operation]; return ok }
func AllOperations() []string {
	out := make([]string, 0, len(operationCatalog))
	for operation := range operationCatalog {
		out = append(out, operation)
	}
	sort.Strings(out)
	return out
}
