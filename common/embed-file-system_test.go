package common

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

//go:embed embed-file-system-testdata/index.html
var embedFileSystemTestData embed.FS

func TestEmbedFolderWithIndexServesRootIndex(t *testing.T) {
	fs := EmbedFolderWithIndex(embedFileSystemTestData, "embed-file-system-testdata")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	http.FileServer(fs).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != "docs test" {
		t.Fatalf("body = %q, want %q", got, "docs test")
	}
}
