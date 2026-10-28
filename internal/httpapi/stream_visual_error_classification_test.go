package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestP2UnknownVisualRepositoryFailureIsNotInputValidation(t *testing.T) {
	sentinel := "private SQL and commit transport details"
	response := httptest.NewRecorder()
	writeStreamVisualError(response, errors.New(sentinel))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("unknown repository/commit failure became retryable input validation: status=%d", response.Code)
	}
	if strings.Contains(response.Body.String(), sentinel) {
		t.Fatal("raw internal error leaked")
	}
}
