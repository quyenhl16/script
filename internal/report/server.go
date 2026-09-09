package report

import (
	"context"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const defaultReportListenAddress = "127.0.0.1:8080"

type ServerOptions struct {
	Directory string
	Listen    string
	Output    io.Writer
}

type BrowserServer struct {
	server    *http.Server
	listener  net.Listener
	directory string
}

type browserReport struct {
	Title        string
	Status       string
	StatusClass  string
	System       string
	Profile      string
	Kind         string
	Finished     string
	ModifiedTime time.Time
	Size         string
	RelativePath string
	URL          string
}

type statusOption struct {
	Value    string
	Label    string
	Selected bool
}

type browserPage struct {
	Reports       []browserReport
	Query         string
	Status        string
	StatusOptions []statusOption
	Total         int
	Passed        int
	Issues        int
	Other         int
	GeneratedAt   string
}

type reportBrowser struct {
	htmlRoot string
}

// Serve starts a small read-only web server for HTML reports. By default it
// only listens on localhost so reports are not accidentally exposed.
func Serve(ctx context.Context, options ServerOptions) error {
	browser, err := StartBrowser(options)
	if err != nil {
		return err
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	fmt.Fprintf(options.Output, "SYSSETUP report browser: %s\n", browser.URL())
	fmt.Fprintf(options.Output, "Serving HTML reports from: %s\n", browser.Directory())
	fmt.Fprintln(options.Output, "Press Ctrl+C to stop.")
	return browser.Run(ctx)
}

// StartBrowser binds the configured address and prepares a report browser.
// Call Run to serve requests and Close if the prepared server is not used.
func StartBrowser(options ServerOptions) (*BrowserServer, error) {
	if options.Directory == "" {
		options.Directory = "reports"
	}
	if options.Listen == "" {
		options.Listen = defaultReportListenAddress
	}

	handler, err := NewBrowserHandler(options.Directory)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", options.Listen)
	if err != nil {
		return nil, fmt.Errorf("listen for report browser on %q: %w", options.Listen, err)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return &BrowserServer{
		server: server, listener: listener,
		directory: filepath.Join(options.Directory, string(HTML)),
	}, nil
}

// Run serves requests until the context is cancelled or the listener fails.
func (s *BrowserServer) Run(ctx context.Context) error {
	errChannel := make(chan error, 1)
	go func() {
		errChannel <- s.server.Serve(s.listener)
	}()

	select {
	case serveErr := <-errChannel:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return serveErr
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := s.server.Shutdown(shutdownContext)
		serveErr := <-errChannel
		if !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(ctx.Err(), serveErr, shutdownErr)
		}
		return errors.Join(ctx.Err(), shutdownErr)
	}
}

func (s *BrowserServer) URL() string {
	return "http://" + browserAddress(s.listener.Addr())
}

func (s *BrowserServer) Directory() string {
	return s.directory
}

// Close releases a listener that was prepared but has not been handed to Run.
func (s *BrowserServer) Close() error {
	return errors.Join(s.server.Close(), s.listener.Close())
}

// NewBrowserHandler returns the report browser HTTP handler. It is exported so
// an embedding application can mount the browser on its own HTTP server.
func NewBrowserHandler(directory string) (http.Handler, error) {
	if directory == "" {
		directory = "reports"
	}
	root, err := filepath.Abs(filepath.Clean(directory))
	if err != nil {
		return nil, fmt.Errorf("resolve reports directory: %w", err)
	}
	htmlRoot := filepath.Join(root, string(HTML))
	if err := os.MkdirAll(htmlRoot, 0o750); err != nil {
		return nil, fmt.Errorf("create HTML report directory: %w", err)
	}
	htmlRoot, err = filepath.EvalSymlinks(htmlRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve HTML report directory: %w", err)
	}

	browser := &reportBrowser{htmlRoot: htmlRoot}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", browser.handleIndex)
	mux.HandleFunc("GET /reports/", browser.handleReport)
	return securityHeaders(mux), nil
}

func (b *reportBrowser) handleIndex(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(response, request)
		return
	}
	reports, err := b.listReports()
	if err != nil {
		http.Error(response, "Could not read the report directory.", http.StatusInternalServerError)
		return
	}

	query := strings.TrimSpace(request.URL.Query().Get("q"))
	status := strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("status")))
	page := browserPage{
		Query:       query,
		Status:      status,
		GeneratedAt: time.Now().Format("02/01/2006 15:04:05"),
		StatusOptions: []statusOption{
			{Label: "Tất cả trạng thái", Selected: status == ""},
			{Value: "PASS", Label: "PASS", Selected: status == "PASS"},
			{Value: "FAILED", Label: "FAILED", Selected: status == "FAILED"},
			{Value: "PARTIAL", Label: "PARTIAL", Selected: status == "PARTIAL"},
			{Value: "CANCELLED", Label: "CANCELLED", Selected: status == "CANCELLED"},
			{Value: "PLANNED", Label: "PLANNED", Selected: status == "PLANNED"},
		},
	}
	queryLower := strings.ToLower(query)
	for _, item := range reports {
		page.Total++
		switch item.Status {
		case "PASS", "DONE":
			page.Passed++
		case "FAILED", "FAIL", "PARTIAL", "CANCELLED":
			page.Issues++
		default:
			page.Other++
		}
		if status != "" && item.Status != status {
			continue
		}
		searchable := strings.ToLower(strings.Join([]string{
			item.Title, item.Status, item.System, item.Profile, item.Kind, item.RelativePath,
		}, " "))
		if queryLower != "" && !strings.Contains(searchable, queryLower) {
			continue
		}
		page.Reports = append(page.Reports, item)
	}

	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	if err := reportIndexTemplate.Execute(response, page); err != nil {
		return
	}
}

func (b *reportBrowser) handleReport(response http.ResponseWriter, request *http.Request) {
	relativeURL := strings.TrimPrefix(request.URL.Path, "/reports/")
	if relativeURL == "" {
		http.Redirect(response, request, "/", http.StatusSeeOther)
		return
	}
	relativePath := filepath.FromSlash(relativeURL)
	fullPath, err := filepath.EvalSymlinks(filepath.Join(b.htmlRoot, relativePath))
	if err != nil {
		http.NotFound(response, request)
		return
	}
	relativeToRoot, err := filepath.Rel(b.htmlRoot, fullPath)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
		http.NotFound(response, request)
		return
	}
	if !strings.EqualFold(filepath.Ext(fullPath), HTML.Extension()) {
		http.NotFound(response, request)
		return
	}
	info, err := os.Lstat(fullPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	http.ServeFile(response, request, fullPath)
}

func (b *reportBrowser) listReports() ([]browserReport, error) {
	reports := make([]browserReport, 0)
	err := filepath.WalkDir(b.htmlRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(entry.Name()), HTML.Extension()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(b.htmlRoot, path)
		if err != nil {
			return err
		}
		report := readBrowserReport(path, filepath.ToSlash(relative), info)
		reports = append(reports, report)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(reports, func(left, right int) bool {
		if reports[left].ModifiedTime.Equal(reports[right].ModifiedTime) {
			return reports[left].RelativePath > reports[right].RelativePath
		}
		return reports[left].ModifiedTime.After(reports[right].ModifiedTime)
	})
	return reports, nil
}

var metadataPattern = regexp.MustCompile(`<meta name="syssetup-([a-z]+)" content="([^"]*)">`)
var titlePattern = regexp.MustCompile(`(?is)<title>(.*?)</title>`)
var badgePattern = regexp.MustCompile(`(?is)<div class="badge[^"]*">\s*([^<]+)\s*</div>`)

func readBrowserReport(path, relative string, info fs.FileInfo) browserReport {
	item := browserReport{
		Title:        strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Status:       "UNKNOWN",
		System:       firstPathSegment(relative),
		Finished:     info.ModTime().Format("02/01/2006 15:04:05"),
		ModifiedTime: info.ModTime(),
		Size:         humanSize(info.Size()),
		RelativePath: relative,
		URL:          "/reports/" + escapePath(relative),
	}

	file, err := os.Open(path)
	if err == nil {
		defer file.Close()
		content, readErr := io.ReadAll(io.LimitReader(file, 128<<10))
		if readErr == nil {
			values := make(map[string]string)
			for _, match := range metadataPattern.FindAllSubmatch(content, -1) {
				values[string(match[1])] = html.UnescapeString(string(match[2]))
			}
			item.Title = valueOr(values["title"], item.Title)
			item.Status = strings.ToUpper(valueOr(values["status"], item.Status))
			item.System = valueOr(values["system"], item.System)
			item.Profile = values["profile"]
			item.Kind = values["kind"]
			item.Finished = valueOr(values["finished"], item.Finished)
			if values["title"] == "" {
				if match := titlePattern.FindSubmatch(content); len(match) == 2 {
					item.Title = strings.TrimSpace(html.UnescapeString(string(match[1])))
				}
			}
			if values["status"] == "" {
				if match := badgePattern.FindSubmatch(content); len(match) == 2 {
					item.Status = strings.ToUpper(strings.TrimSpace(html.UnescapeString(string(match[1]))))
				}
			}
		}
	}
	item.StatusClass = browserStatusClass(item.Status)
	return item
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; frame-ancestors 'none'")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}

func browserAddress(address net.Addr) string {
	host, port, err := net.SplitHostPort(address.String())
	if err != nil {
		return address.String()
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func firstPathSegment(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) > 1 {
		return parts[0]
	}
	return "interactive"
}

func escapePath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func browserStatusClass(status string) string {
	switch strings.ToUpper(status) {
	case "PASS", "DONE":
		return "pass"
	case "FAILED", "FAIL", "CANCELLED":
		return "fail"
	case "PARTIAL", "SKIPPED", "PLANNED":
		return "warn"
	default:
		return "neutral"
	}
}

func humanSize(size int64) string {
	const unit = int64(1024)
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	labels := []string{"KB", "MB", "GB"}
	for _, label := range labels {
		value /= float64(unit)
		if value < float64(unit) || label == labels[len(labels)-1] {
			return fmt.Sprintf("%.1f %s", value, label)
		}
	}
	return fmt.Sprintf("%d B", size)
}

var reportIndexTemplate = template.Must(template.New("report-index").Parse(`<!doctype html>
<html lang="vi">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>SYSSETUP Reports</title>
  <style>
    :root{color-scheme:light dark;--bg:#eef3f9;--surface:#fff;--surface-2:#f7f9fc;--text:#172033;--muted:#68758a;--line:#dbe3ee;--brand:#076b83;--brand-2:#123b67;--pass:#168447;--pass-bg:#e7f7ed;--fail:#c43832;--fail-bg:#fdecea;--warn:#a46908;--warn-bg:#fff4d8;--neutral:#657086;--shadow:0 18px 45px rgba(31,54,82,.10)}
    @media(prefers-color-scheme:dark){:root{--bg:#0d131c;--surface:#151e2a;--surface-2:#101822;--text:#eef5ff;--muted:#9cabc0;--line:#293649;--brand:#39bdd5;--brand-2:#245d91;--pass:#6ed996;--pass-bg:#153525;--fail:#ff8f87;--fail-bg:#3b2022;--warn:#f3c768;--warn-bg:#392f1d;--neutral:#a7b4c7;--shadow:0 18px 48px rgba(0,0,0,.28)}}
    *{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.5 Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif}.page{width:min(1180px,calc(100% - 32px));margin:0 auto 48px}.hero{position:relative;overflow:hidden;margin:20px 0;padding:30px;border-radius:22px;color:#fff;background:linear-gradient(125deg,#103b69,#08758b 62%,#13a19e);box-shadow:var(--shadow)}.hero:after{content:"";position:absolute;width:280px;height:280px;border:55px solid rgba(255,255,255,.09);border-radius:50%;right:-95px;top:-120px}.eyebrow{font-size:12px;font-weight:800;letter-spacing:.16em;text-transform:uppercase;opacity:.78}.hero h1{position:relative;margin:6px 0 5px;font-size:clamp(28px,5vw,43px);line-height:1.1}.hero p{position:relative;margin:0;opacity:.84}.stats{display:grid;grid-template-columns:repeat(4,1fr);gap:14px;margin:18px 0}.stat,.panel{background:var(--surface);border:1px solid var(--line);box-shadow:var(--shadow)}.stat{padding:17px 18px;border-radius:15px}.stat span{display:block;color:var(--muted);font-size:12px;font-weight:750;text-transform:uppercase;letter-spacing:.06em}.stat strong{display:block;margin-top:3px;font-size:27px}.panel{border-radius:18px;overflow:hidden}.toolbar{display:flex;gap:10px;align-items:center;padding:16px;border-bottom:1px solid var(--line);background:var(--surface-2)}input,select,button,.reset{height:42px;border-radius:10px;border:1px solid var(--line);font:inherit}input,select{background:var(--surface);color:var(--text);padding:0 13px}input{flex:1;min-width:150px}button,.reset{display:inline-flex;align-items:center;justify-content:center;padding:0 17px;font-weight:750;text-decoration:none;cursor:pointer}button{border-color:var(--brand);background:var(--brand);color:white}.reset{color:var(--text);background:var(--surface)}.report-list{display:grid}.report{display:grid;grid-template-columns:minmax(260px,1.8fr) minmax(150px,.8fr) minmax(150px,.8fr) auto;gap:18px;align-items:center;padding:18px;border-bottom:1px solid var(--line);transition:background .15s ease}.report:last-child{border-bottom:0}.report:hover{background:var(--surface-2)}.title{font-size:16px;font-weight:780;color:var(--text);text-decoration:none}.title:hover{color:var(--brand)}.path,.sub,.label{color:var(--muted);font-size:12px}.path{margin-top:4px;overflow-wrap:anywhere}.label{text-transform:uppercase;font-weight:750;letter-spacing:.04em}.value{margin-top:2px}.badge{display:inline-flex;padding:5px 10px;border-radius:999px;font-size:11px;font-weight:850;letter-spacing:.04em}.badge.pass{color:var(--pass);background:var(--pass-bg)}.badge.fail{color:var(--fail);background:var(--fail-bg)}.badge.warn{color:var(--warn);background:var(--warn-bg)}.badge.neutral{color:var(--neutral);background:var(--surface-2)}.empty{text-align:center;padding:70px 20px}.empty strong{display:block;font-size:20px}.empty span{color:var(--muted)}footer{padding:16px;text-align:center;color:var(--muted);font-size:12px}
    @media(max-width:780px){.page{width:min(100% - 20px,1180px)}.hero{margin-top:10px;padding:24px}.stats{grid-template-columns:repeat(2,1fr)}.toolbar{flex-wrap:wrap}.toolbar input{flex-basis:100%}.report{grid-template-columns:1fr 1fr}.report-main{grid-column:1/-1}}@media(max-width:480px){.stats{grid-template-columns:1fr 1fr;gap:8px}.stat{padding:13px}.report{grid-template-columns:1fr}.report-main{grid-column:auto}.toolbar select,.toolbar button,.toolbar .reset{flex:1}}
  </style>
</head>
<body>
  <main class="page">
    <header class="hero"><div class="eyebrow">Operations dashboard</div><h1>SYSSETUP Reports</h1><p>Duyệt và kiểm tra tập trung các báo cáo HTML của hệ thống.</p></header>
    <section class="stats" aria-label="Report summary">
      <div class="stat"><span>Tổng report</span><strong>{{.Total}}</strong></div>
      <div class="stat"><span>Thành công</span><strong>{{.Passed}}</strong></div>
      <div class="stat"><span>Cần kiểm tra</span><strong>{{.Issues}}</strong></div>
      <div class="stat"><span>Trạng thái khác</span><strong>{{.Other}}</strong></div>
    </section>
    <section class="panel">
      <form class="toolbar" method="get" action="/">
        <input type="search" name="q" value="{{.Query}}" placeholder="Tìm theo tên, system, profile hoặc loại report..." aria-label="Tìm report">
        <select name="status" aria-label="Lọc trạng thái">{{range .StatusOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}</select>
        <button type="submit">Tìm kiếm</button><a class="reset" href="/">Đặt lại</a>
      </form>
      {{if .Reports}}<div class="report-list">
        {{range .Reports}}<article class="report">
          <div class="report-main"><a class="title" href="{{.URL}}" target="_blank" rel="noopener">{{.Title}}</a><div class="path">{{.RelativePath}}</div></div>
          <div><div class="label">System / Profile</div><div class="value">{{.System}}{{if .Profile}} / {{.Profile}}{{end}}</div><div class="sub">{{if .Kind}}{{.Kind}}{{else}}report{{end}}</div></div>
          <div><div class="label">Hoàn thành</div><div class="value">{{.Finished}}</div><div class="sub">{{.Size}}</div></div>
          <div><span class="badge {{.StatusClass}}">{{.Status}}</span></div>
        </article>{{end}}
      </div>{{else}}<div class="empty"><strong>Không tìm thấy report HTML</strong><span>Hãy chạy tool với <code>--report-format html</code> hoặc thay đổi bộ lọc.</span></div>{{end}}
      <footer>Danh sách được cập nhật khi tải lại trang · {{.GeneratedAt}}</footer>
    </section>
  </main>
</body>
</html>`))
