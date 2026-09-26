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
		"SYSSETUP WORKFLOW REPORT", "Summary metrics", "Checklist summary", "Check", "Status",
		"Expected", "Actual", "namespace", "memory limit",
	} {
		if !strings.Contains(allXML, expected) {
			t.Errorf("Excel report does not contain %q", expected)
		}
	}
	if strings.Contains(allXML, ">Output<") || strings.Contains(allXML, ">Line<") {
		t.Error("Excel checklist sheets still use the raw Line / Output layout")
	}
}

func TestParseChecklistOutputGroupsChecksAndComparisonValues(t *testing.T) {
	output := `=========================================================
KUBERNETES SERVICE CHECK: namespace [pramf01]
=========================================================

Checking service: aerospike
[PASS] Service [aerospike] exists (ClusterIP: None, External: None)
[PASS] Service [aerospike]: External IP matches expected=[192.168.5.90]
[PASS] Service [aerospike]: TCP 172.16.118.178:3000 is reachable (curl)

Checking service: dns
[SKIP] Service [dns] has no TCP port
[FAIL] [MISMATCH] dns/limits.memory expected=4Gi actual=2Gi source=Resources!H16`

	parsed := parseChecklistOutput(output)
	if len(parsed.Sections) != 3 {
		t.Fatalf("section count = %d, want 3: %#v", len(parsed.Sections), parsed.Sections)
	}
	for status, expected := range map[string]int{"PASS": 3, "FAIL": 1, "SKIP": 1, "WARN": 0} {
		if parsed.Counts[status] != expected {
			t.Errorf("count[%s] = %d, want %d", status, parsed.Counts[status], expected)
		}
	}
	first := parsed.Sections[0]
	if first.Title != "Service: aerospike" || len(first.Items) != 3 {
		t.Fatalf("first section = %#v", first)
	}
	if first.Items[0].Check != "Exists" || first.Items[0].Expected != "Exists" || first.Items[0].Actual != "Exists" {
		t.Errorf("exists check = %#v", first.Items[0])
	}
	if first.Items[1].Expected != "192.168.5.90" || first.Items[1].Actual != "192.168.5.90" {
		t.Errorf("external IP check = %#v", first.Items[1])
	}
	if len(parsed.Messages) != 0 {
		t.Errorf("unexpected additional messages: %#v", parsed.Messages)
	}
	comparison := parsed.Sections[2].Items[0]
	if comparison.Check != "limits.memory" || comparison.Expected != "4Gi" || comparison.Actual != "2Gi" || comparison.Source != "Resources!H16" {
		t.Errorf("comparison check = %#v", comparison)
	}
}

func TestExcelChecklistReportContainsStructuredSectionsAndCounts(t *testing.T) {
	document := Document{Entries: []Entry{{
		Title: "services - k8s-service-check", Status: "done",
		Output: "Checking service: dns\n[PASS] Service [dns] exists (ClusterIP: 10.0.0.1)\n[SKIP] Service [dns] has no TCP port",
	}}}
	content, err := renderExcel(document)
	if err != nil {
		t.Fatal(err)
	}
	allXML := readExcelXML(t, content)
	for _, expected := range []string{"Service: dns", "Checklist summary", "Checks", "Pass", "Fail", "Skip", "Expected", "Actual", "Exists", "TCP port", "Not configured", "COUNTIF"} {
		if !strings.Contains(allXML, expected) {
			t.Errorf("structured Excel report does not contain %q", expected)
		}
	}
}

func TestParseChecklistOutputPreservesPhaseSubsectionAndLegacyCurlLines(t *testing.T) {
	output := `[PHASE 3] VERIFYING NETWORK CONNECTIVITY (PING TESTS)
3.4 SBI-Client: MM pods -> peer NFs
  Pod: [mm-0]
[PASS] Pod [mm-0]: path to NRF (10.0.0.1) is reachable
[PHASE 4] VERIFYING PEER NF HTTP CONNECTIVITY (CURL)
  Pod: [mm-0]
  NF: AUSF       [PASS] Pod [mm-0]: https://10.0.0.2/health is reachable (HTTP 200)`

	parsed := parseChecklistOutput(output)
	if parsed.Counts["PASS"] != 2 {
		t.Fatalf("pass count = %d, want 2", parsed.Counts["PASS"])
	}
	if len(parsed.Sections) != 2 {
		t.Fatalf("section count = %d, want 2: %#v", len(parsed.Sections), parsed.Sections)
	}
	if !strings.HasPrefix(parsed.Sections[0].Phase, "PHASE 3") || parsed.Sections[0].Subsection != "3.4 SBI-Client: MM pods -> peer NFs" {
		t.Errorf("phase 3 hierarchy = %#v", parsed.Sections[0])
	}
	if !strings.HasPrefix(parsed.Sections[1].Phase, "PHASE 4") || !strings.Contains(parsed.Sections[1].Items[0].Check, "AUSF") {
		t.Errorf("legacy curl item = %#v", parsed.Sections[1])
	}
}

func TestComponentSummaryIsAuthoritativeAndRenderedWithFormulas(t *testing.T) {
	output := `Component [redis]
[FAIL] [MISMATCH] redis/limits.memory expected=4Gi actual=2Gi source=Resources!H16
SYSSETUP_REPORT {"type":"component-summary","component":"redis","workload":"StatefulSet/redis container=redis","status":"FAIL","checks":4,"pass":3,"fail":1,"skip":0,"warn":0,"missing":0,"mismatch":1,"extra":0,"resolution_errors":0}`
	parsed := parseChecklistOutput(output)
	if parsed.Counts["PASS"] != 3 || parsed.Counts["FAIL"] != 1 || len(parsed.Components) != 1 {
		t.Fatalf("component counts = %#v, components = %#v", parsed.Counts, parsed.Components)
	}
	document := Document{Entries: []Entry{{Title: "k8s-resource-check", Status: "failed", Output: output}}}
	content, err := renderExcel(document)
	if err != nil {
		t.Fatal(err)
	}
	xmlText := readExcelXML(t, content)
	for _, expected := range []string{"Component overview", "StatefulSet/redis", "Mismatch", "<f>SUM(D12:D12)</f>", "01-k8s-resource-check", "!B7</f>"} {
		if !strings.Contains(xmlText, expected) {
			t.Errorf("component Excel report does not contain %q", expected)
		}
	}
}

func TestFeatureRunAttachesOutputToFeatureWithoutExecutionOutputSheet(t *testing.T) {
	document := FeatureRun(
		domain.Profile{Name: "k8s-connectivity-check"},
		[]domain.Result{{FeatureID: "k8s-connectivity-check", Status: domain.StatusDone, Output: "[PASS] Pod [mm-0]: reachable"}},
		"==> heading\n[PASS] Pod [mm-0]: reachable", nil, time.Now(), time.Now(),
	)
	if len(document.Entries) != 1 || document.Entries[0].Title != "k8s-connectivity-check" || document.Entries[0].Output == "" {
		t.Fatalf("feature entries = %#v", document.Entries)
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
