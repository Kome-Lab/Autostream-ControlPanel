package operationalauthorization

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestOperationalCatalogMatchesFixedV2(t *testing.T) {
	sum := sha256.Sum256([]byte(strings.Join(AllOperations(), "\n")))
	if hex.EncodeToString(sum[:]) != "bc4e06d45fcea5d4137d4014e55df34095173caee1e1e49dd048af7f6716d89d" {
		t.Fatal("catalog differs from fixed v2 Permission inventory")
	}
}
