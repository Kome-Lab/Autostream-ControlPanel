package store

import (
	"strings"
	"testing"
)

func TestNewUUIDShape(t *testing.T) {
	id := newUUID()
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Fatalf("bad uuid: %s", id)
	}
}
