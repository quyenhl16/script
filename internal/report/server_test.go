package report

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserListsAndServesHTMLReports(t *testing.T) {
	directory := t.TempDir()
	finished := time.Date(2026, 9, 9, 14, 30, 0, 0, time.FixedZone("ICT", 7*60*60))
	document := Document{
		Title: "Post Deployment Validation", Operation: "post-deployment", System: "01HTX",
		Profile: "validation", Kind: "local workflow", Status: "failed", FinishedAt: finished,
	}
	path, err := Write(document, Options{Directory: directory, Format: HTML})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Write(document, Options{Directory: directory, Format: Markdown}); err != nil {
		t.Fatal(err)
	}

	handler, err := NewBrowserHandler(directory)
	if err != nil {
		t.Fatal(err)
	}
	indexRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	indexResponse := httptest.NewRecorder()
	handler.ServeHTTP(indexResponse, indexRequest)
	if indexResponse.Code != http.StatusOK {
		t.Fatalf("index status = %d", indexResponse.Code)
	}
	index := indexResponse.Body.String()
	for _, expected := range []string{"SYSSETUP Reports", "Post Deployment Validation", "FAILED", "01HTX", "validation"} {
		if !strings.Contains(index, expected) {
			t.Fatalf("index does not contain %q: %s", expected, index)
		}
	}
	if strings.Contains(index, ".md") {
		t.Fatalf("index unexpectedly lists a Markdown report: %s", index)
	}

	relative, err := filepath.Rel(filepath.Join(directory, string(HTML)), path)
	if err != nil {
		t.Fatal(err)
	}
	reportRequest := httptest.NewRequest(http.MethodGet, "/reports/"+filepath.ToSlash(relative), nil)
	reportResponse := httptest.NewRecorder()
	handler.ServeHTTP(reportResponse, reportRequest)
	if reportResponse.Code != http.StatusOK {
		t.Fatalf("report status = %d, body = %s", reportResponse.Code, reportResponse.Body.String())
	}
	if !strings.Contains(reportResponse.Body.String(), "Post Deployment Validation") {
		t.Fatal("served report has unexpected content")
	}
	if reportResponse.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers were not applied")
	}
}

func TestBrowserFiltersReports(t *testing.T) {
	directory := t.TempDir()
	documents := []Document{
		{Title: "Healthy Cluster", Operation: "healthy", System: "site-a", Status: "pass"},
		{Title: "Broken Cluster", Operation: "broken", System: "site-b", Status: "failed"},
	}
	for _, document := range documents {
		if _, err := Write(document, Options{Directory: directory, Format: HTML}); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := NewBrowserHandler(directory)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/?q=healthy&status=PASS", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if !strings.Contains(body, "Healthy Cluster") || strings.Contains(body, "Broken Cluster") {
		t.Fatalf("filtered index has unexpected reports: %s", body)
	}
}

func TestBrowserRejectsNonHTMLFilesAndTraversal(t *testing.T) {
	directory := t.TempDir()
	htmlDirectory := filepath.Join(directory, string(HTML))
	if err := os.MkdirAll(htmlDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(htmlDirectory, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := NewBrowserHandler(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/reports/secret.txt", "/reports/%2e%2e/secret.html"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", target, response.Code)
		}
	}
}
