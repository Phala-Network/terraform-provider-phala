package provider

import (
	"io"
	"net/http"
	"testing"
)

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err := io.WriteString(w, body)
	if err != nil {
		t.Fatalf("write response: %v", err)
	}
}
