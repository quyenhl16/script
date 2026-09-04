package report

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Format string

const (
	Markdown Format = "md"
	HTML     Format = "html"
)

type SummaryItem struct {
	Label string
	Value string
}

type Entry struct {
	Title    string
	Target   string
	Status   string
	Duration time.Duration
	Message  string
	Output   string
}

type Document struct {
	Title      string
	Operation  string
	System     string
	Profile    string
	Kind       string
	Status     string
	StartedAt  time.Time
	FinishedAt time.Time
	Summary    []SummaryItem
	Entries    []Entry
}

type Options struct {
	Directory string
	Format    Format
}

func ParseFormat(value string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(value))) {
	case "", Markdown:
		return Markdown, nil
	case HTML:
		return HTML, nil
	default:
		return "", fmt.Errorf("unsupported report format %q; expected md or html", value)
	}
}

func (f Format) Extension() string {
	if f == HTML {
		return ".html"
	}
	return ".md"
}

func Write(document Document, options Options) (string, error) {
	format, err := ParseFormat(string(options.Format))
	if err != nil {
		return "", err
	}
	if options.Directory == "" {
		options.Directory = "reports"
	}
	if document.FinishedAt.IsZero() {
		document.FinishedAt = time.Now()
	}
	if document.StartedAt.IsZero() {
		document.StartedAt = document.FinishedAt
	}
	document.Status = strings.ToUpper(document.Status)
	for index := range document.Entries {
		document.Entries[index].Status = strings.ToUpper(document.Entries[index].Status)
		document.Entries[index].Output = cleanOutput(document.Entries[index].Output)
	}

	system := slug(document.System)
	if system == "" {
		system = "interactive"
	}
	directory := filepath.Join(options.Directory, system)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("create report directory: %w", err)
	}
	operation := slug(document.Operation)
	if operation == "" {
		operation = "run"
	}
	baseName := document.FinishedAt.Format("20060102_150405.000") + "_" + operation

	var content []byte
	if format == HTML {
		content, err = renderHTML(document)
	} else {
		content = []byte(renderMarkdown(document))
	}
	if err != nil {
		return "", fmt.Errorf("render report: %w", err)
	}
	for sequence := 1; sequence <= 1000; sequence++ {
		suffix := ""
		if sequence > 1 {
			suffix = fmt.Sprintf("_%d", sequence)
		}
		path := filepath.Join(directory, baseName+suffix+format.Extension())
		file, openErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(openErr, os.ErrExist) {
			continue
		}
		if openErr != nil {
			return "", fmt.Errorf("create report: %w", openErr)
		}
		_, writeErr := file.Write(content)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("write report: %w", err)
		}
		return path, nil
	}
	return "", errors.New("could not allocate a unique report filename")
}

func renderMarkdown(document Document) string {
	var output strings.Builder
	fmt.Fprintf(&output, "# %s\n\n", markdownText(document.Title))
	fmt.Fprintf(&output, "- **Status:** %s\n", markdownText(document.Status))
	fmt.Fprintf(&output, "- **System:** %s\n", markdownText(valueOrDash(document.System)))
	fmt.Fprintf(&output, "- **Profile:** %s\n", markdownText(valueOrDash(document.Profile)))
	fmt.Fprintf(&output, "- **Type:** %s\n", markdownText(valueOrDash(document.Kind)))
	fmt.Fprintf(&output, "- **Started:** %s\n", document.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&output, "- **Finished:** %s\n", document.FinishedAt.Format(time.RFC3339))
	fmt.Fprintf(&output, "- **Duration:** %s\n", document.FinishedAt.Sub(document.StartedAt).Round(time.Millisecond))
	if len(document.Summary) > 0 {
		output.WriteString("\n## Summary\n\n| Item | Value |\n|---|---:|\n")
		for _, item := range document.Summary {
			fmt.Fprintf(&output, "| %s | %s |\n", markdownText(item.Label), markdownText(item.Value))
		}
	}
	for _, entry := range document.Entries {
		fmt.Fprintf(&output, "\n## %s - %s\n\n", markdownText(entry.Title), markdownText(entry.Status))
		if entry.Target != "" {
			fmt.Fprintf(&output, "- **Target:** %s\n", markdownText(entry.Target))
		}
		if entry.Duration > 0 {
			fmt.Fprintf(&output, "- **Duration:** %s\n", entry.Duration.Round(time.Millisecond))
		}
		if entry.Message != "" {
			fmt.Fprintf(&output, "- **Message:** %s\n", markdownText(entry.Message))
		}
		if entry.Output != "" {
			output.WriteString("\n### Output\n\n")
			for _, line := range strings.Split(entry.Output, "\n") {
				fmt.Fprintf(&output, "    %s\n", line)
			}
		}
	}
	return output.String()
}

var htmlReport = template.Must(template.New("report").Funcs(template.FuncMap{
	"duration": func(start, finish time.Time) string { return finish.Sub(start).Round(time.Millisecond).String() },
	"round":    func(value time.Duration) string { return value.Round(time.Millisecond).String() },
	"time":     func(value time.Time) string { return value.Format(time.RFC3339) },
	"status":   func(value string) string { return strings.ToLower(value) },
	"dash":     valueOrDash,
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title><style>
:root{color-scheme:light dark;--bg:#f5f7fb;--card:#fff;--text:#172033;--muted:#657086;--line:#dce2ec;--pass:#137333;--fail:#b3261e;--warn:#9a6700}
@media(prefers-color-scheme:dark){:root{--bg:#10141d;--card:#191f2b;--text:#eef2fa;--muted:#aab4c8;--line:#30394a;--pass:#6dd58c;--fail:#ff897d;--warn:#f6c453}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.55 system-ui,sans-serif}.page{max-width:1080px;margin:auto;padding:32px 20px}h1{margin:0 0 8px}.meta,.summary,.entry{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:18px;margin:16px 0}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px}.label{color:var(--muted);font-size:12px;text-transform:uppercase}.value{font-weight:650}.pass,.done,.planned{color:var(--pass)}.fail,.failed,.cancelled{color:var(--fail)}.partial,.skipped{color:var(--warn)}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:var(--bg);border-radius:8px;padding:14px}details summary{cursor:pointer;font-weight:650}.badge{font-weight:750;text-transform:uppercase}
</style></head><body><main class="page"><h1>{{.Title}}</h1><div class="badge {{status .Status}}">{{.Status}}</div>
<section class="meta grid"><div><div class="label">System</div><div class="value">{{dash .System}}</div></div><div><div class="label">Profile</div><div class="value">{{dash .Profile}}</div></div><div><div class="label">Type</div><div class="value">{{dash .Kind}}</div></div><div><div class="label">Duration</div><div class="value">{{duration .StartedAt .FinishedAt}}</div></div><div><div class="label">Started</div><div class="value">{{time .StartedAt}}</div></div><div><div class="label">Finished</div><div class="value">{{time .FinishedAt}}</div></div></section>
{{if .Summary}}<section class="summary grid">{{range .Summary}}<div><div class="label">{{.Label}}</div><div class="value">{{.Value}}</div></div>{{end}}</section>{{end}}
{{range .Entries}}<section class="entry"><h2>{{.Title}} - <span class="{{status .Status}}">{{.Status}}</span></h2>{{if .Target}}<div><span class="label">Target:</span> {{.Target}}</div>{{end}}{{if .Duration}}<div><span class="label">Duration:</span> {{round .Duration}}</div>{{end}}{{if .Message}}<p>{{.Message}}</p>{{end}}{{if .Output}}<details open><summary>Output</summary><pre>{{.Output}}</pre></details>{{end}}</section>{{end}}
</main></body></html>`))

func renderHTML(document Document) ([]byte, error) {
	var output bytes.Buffer
	if err := htmlReport.Execute(&output, document); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
var slugPattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func cleanOutput(value string) string {
	value = ansiPattern.ReplaceAllString(value, "")
	value = strings.ReplaceAll(value, "\x00", "")
	return strings.TrimSpace(value)
}

func slug(value string) string {
	value = strings.TrimSpace(value)
	value = slugPattern.ReplaceAllString(value, "-")
	return strings.Trim(value, ".-_")
}

func markdownText(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func JoinRunAndReportErrors(runErr, reportErr error) error {
	if reportErr == nil {
		return runErr
	}
	return errors.Join(runErr, fmt.Errorf("report: %w", reportErr))
}
