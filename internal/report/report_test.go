package report

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/workflow"
)

func TestWriteMarkdownAndHTML(t *testing.T) {
	started := time.Date(2026, 9, 4, 15, 30, 0, 0, time.FixedZone("ICT", 7*60*60))
	document := Document{
		Title: "SYSSETUP REPORT", Operation: "prepare setup", System: "01HTX", Profile: "base-server",
		Kind: "workflow", Status: "pass", StartedAt: started, FinishedAt: started.Add(2 * time.Second),
		Summary: []SummaryItem{{Label: "Passed", Value: "1"}},
		Entries: []Entry{{Title: "verify-network", Target: "10.0.0.1:22", Status: "pass", Output: "\x1b[32m[PASS]\x1b[0m bond2.306"}},
	}
	for _, format := range []Format{Markdown, HTML, Excel} {
		directory := t.TempDir()
		path, err := Write(document, Options{Directory: directory, Format: format})
		if err != nil {
			t.Fatal(err)
		}
		wantDirectory := filepath.Join(directory, string(format), "01HTX")
		if filepath.Dir(path) != wantDirectory {
			t.Fatalf("path directory = %q, want %q", filepath.Dir(path), wantDirectory)
		}
		if filepath.Ext(path) != format.Extension() {
			t.Fatalf("path = %q, want extension %q", path, format.Extension())
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		if format == Excel {
			text = readExcelXML(t, content)
		}
		for _, expected := range []string{"SYSSETUP REPORT", "01HTX", "verify-network", "bond2.306"} {
			if !strings.Contains(text, expected) {
				t.Fatalf("%s report does not contain %q: %s", format, expected, text)
			}
		}
		if strings.Contains(text, "\x1b") {
			t.Fatalf("%s report contains ANSI escape sequences", format)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for _, value := range []string{"", "md", "MD", "html", "HTML", "xlsx", "XLSX"} {
		if _, err := ParseFormat(value); err != nil {
			t.Fatalf("ParseFormat(%q): %v", value, err)
		}
	}
	if _, err := ParseFormat("pdf"); err == nil {
		t.Fatal("expected unsupported format error")
	}
}

func TestExcelWorkflowReportHasSummaryAndOneSheetPerFeatureResult(t *testing.T) {
	started := time.Date(2026, 9, 17, 9, 30, 0, 0, time.FixedZone("ICT", 7*60*60))
	document := Document{
		Title: "SYSSETUP WORKFLOW REPORT", Operation: "validation", System: "01HTX", Profile: "post-deployment",
		Kind: "local workflow", Status: "partial", StartedAt: started, FinishedAt: started.Add(3 * time.Second),
		Summary: []SummaryItem{{Label: "Passed", Value: "1"}, {Label: "Failed", Value: "1"}, {Label: "Steps", Value: "2"}},
		Entries: []Entry{
			{Title: "environment.1 - k8s-env-check", Target: "local", Status: "done", Duration: time.Second, Output: "[PASS] namespace\n[PASS] log level"},
			{Title: "resources.1 - k8s-resource-check", Target: "local", Status: "failed", Duration: 2 * time.Second, Message: "exit status 1", Output: "[FAIL] memory limit"},
		},
	}

	content, err := renderExcel(document)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := map[string]bool{
		"xl/worksheets/sheet1.xml": false,
		"xl/worksheets/sheet2.xml": false,
		"xl/worksheets/sheet3.xml": false,
	}
	for _, file := range reader.File {
		if _, found := wantFiles[file.Name]; found {
			wantFiles[file.Name] = true
		}
	}
	for name, found := range wantFiles {
		if !found {
			t.Errorf("Excel report is missing %s", name)
		}
	}
	allXML := readExcelXML(t, content)
	for _, expected := range []string{
		`name="Summary"`, `name="01-k8s-env-check"`, `name="02-k8s-resource-check"`,
		"SYSSETUP WORKFLOW REPORT", "Summary metrics", "[PASS] namespace", "[FAIL] memory limit",
	} {
		if !strings.Contains(allXML, expected) {
			t.Errorf("Excel report does not contain %q", expected)
		}
	}
}

func TestWorkflowRunBuildsExecutionSummaryForExcel(t *testing.T) {
	started := time.Now().Add(-time.Second)
	document := WorkflowRun(
		domain.Profile{Name: "validation", System: "01HTX"},
		workflow.Definition{ID: "post-deployment", ExecutionMode: "local"},
		workflow.Execution{
			Servers: []workflow.ServerResult{{Address: "local", Success: false}},
			Results: []workflow.Result{
				{StepID: "environment", FeatureID: "k8s-env-check", Status: workflow.StatusDone},
				{StepID: "resources", FeatureID: "k8s-resource-check", Status: workflow.StatusFailed},
				{StepID: "services", FeatureID: "k8s-service-check", Status: workflow.StatusSkipped},
			},
		},
		fmt.Errorf("workflow failed"), started, time.Now(),
	)

	summary := make(map[string]string, len(document.Summary))
	for _, item := range document.Summary {
		summary[item.Label] = item.Value
	}
	for label, expected := range map[string]string{
		"Feature executions": "3", "Done executions": "1", "Failed executions": "1", "Skipped executions": "1",
	} {
		if summary[label] != expected {
			t.Errorf("summary[%q] = %q, want %q", label, summary[label], expected)
		}
	}
	if len(document.Entries) != 3 {
		t.Fatalf("entry count = %d, want 3", len(document.Entries))
	}
}

func TestWriteDoesNotOverwriteReportFromSameTimestamp(t *testing.T) {
	directory := t.TempDir()
	finished := time.Date(2026, 9, 4, 15, 30, 0, 123000000, time.Local)
	document := Document{Title: "Report", Operation: "test", Status: "pass", FinishedAt: finished}
	first, err := Write(document, Options{Directory: directory, Format: Markdown})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Write(document, Options{Directory: directory, Format: Markdown})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("second report overwrote %q", first)
	}
}

func readExcelXML(t *testing.T, content []byte) string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	var combined strings.Builder
	for _, file := range reader.File {
		if !strings.HasSuffix(file.Name, ".xml") {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read %s: %v / %v", file.Name, readErr, closeErr)
		}
		decoder := xml.NewDecoder(bytes.NewReader(data))
		for {
			if _, err := decoder.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("invalid XML in %s: %v", file.Name, err)
			}
		}
		combined.Write(data)
	}
	return combined.String()
}
