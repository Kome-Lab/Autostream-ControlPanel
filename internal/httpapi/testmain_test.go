package httpapi

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS", "*.example.com")
	os.Exit(m.Run())
}
