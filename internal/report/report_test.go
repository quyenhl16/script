package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteMarkdownAndHTML(t *testing.T) {
	started := time.Date(2026, 9, 4, 15, 30, 0, 0, time.FixedZone("ICT", 7*60*60))
	document := Document{
		Title: "SYSSETUP REPORT", Operation: "prepare setup", System: "01HTX", Profile: "base-server",
		Kind: "workflow", Status: "pass", StartedAt: started, FinishedAt: started.Add(2 * time.Second),
		Summary: []SummaryItem{{Label: "Passed", Value: "1"}},
		Entries: []Entry{{Title: "verify-network", Target: "10.0.0.1:22", Status: "pass", Output: "\x1b[32m[PASS]\x1b[0m bond2.306"}},
	}
	for _, format := range []Format{Markdown, HTML} {
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
	for _, value := range []string{"", "md", "MD", "html", "HTML"} {
		if _, err := ParseFormat(value); err != nil {
			t.Fatalf("ParseFormat(%q): %v", value, err)
		}
	}
	if _, err := ParseFormat("pdf"); err == nil {
		t.Fatal("expected unsupported format error")
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
