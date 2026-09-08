package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/registry"
	"github.com/quyenhl16/script/internal/runner"
	"github.com/quyenhl16/script/internal/workflow"
)

func TestFilterAndSelectionBuildExecutionPlan(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "base", "")
	writeDashboardFeature(t, root, "web", `,"dependsOn":["base"]`)
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), reg, domain.Profile{APIVersion: "syssetup/v1", Name: "test"}, runner.Options{DryRun: true})
	m.filter.SetValue("web")
	m.refreshRows()
	if got := len(m.table.Rows()); got != 1 {
		t.Fatalf("filtered rows = %d, want 1", got)
	}
	if got := m.table.Rows()[0][1]; got != "2" {
		t.Fatalf("filtered feature index = %q, want stable alphabetical index 2", got)
	}
	m.toggleCurrent()
	if len(m.resolved) != 2 || m.resolved[0].Feature.ID != "base" || m.resolved[1].Feature.ID != "web" {
		t.Fatalf("unexpected plan: %#v", m.resolved)
	}
}

func TestDashboardViewAndDryRun(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "base", "")
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.Profile{
		APIVersion: "syssetup/v1",
		Name:       "test",
		Features:   []domain.FeatureSelection{{ID: "base"}},
	}
	m := newModel(context.Background(), reg, profile, runner.Options{DryRun: true})
	m.resize(120, 30)
	view := m.View()
	for _, expected := range []string{"SYSSETUP", "Features", "base", "Entrypoint"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("dashboard does not contain %q", expected)
		}
	}

	buffer := &safeBuffer{}
	options := runner.Options{DryRun: true, Output: buffer}
	message := runFeaturesCmd(context.Background(), m.resolved, options, buffer)()
	finished, ok := message.(runFinishedMsg)
	if !ok || finished.err != nil {
		t.Fatalf("dry-run result = %#v", message)
	}
	if len(finished.results) != 1 || finished.results[0].Status != domain.StatusPlanned {
		t.Fatalf("unexpected dry-run results: %#v", finished.results)
	}
}

func TestFeatureDetailShowsRequiredOptionalAndProfileParameters(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "configured", `,"parameters":{
		"namespace":{"type":"string","description":"Kubernetes namespace","required":true},
		"container":{"type":"string","description":"Optional container","default":""},
		"source_file":{"type":"string","description":"Local source file","required":true}
	}`)
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.Profile{
		APIVersion: "syssetup/v1",
		Name:       "test",
		Features: []domain.FeatureSelection{{
			ID:         "configured",
			Parameters: map[string]any{"namespace": "pramf01"},
		}},
	}
	m := newModel(context.Background(), reg, profile, runner.Options{DryRun: true})
	m.resize(140, 45)
	view := m.View()
	for _, expected := range []string{
		"Parameters (2 required, 1 optional)",
		"namespace", "required", `profile: "pramf01"`,
		"container", "optional", `default: ""`,
		"source_file", "MISSING", "PgUp/PgDn",
	} {
		if !strings.Contains(view, expected) {
			t.Fatalf("feature detail does not contain %q: %s", expected, view)
		}
	}
}

func TestSSHFormMasksPasswordAndSwitchesMode(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "base", "")
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), reg, domain.Profile{APIVersion: "syssetup/v1", Name: "test"}, runner.Options{})
	m.resize(120, 30)
	m.activeTab = tabSSH
	m.sshInputs[sshHosts].SetValue("10.0.0.1,10.0.0.2:2222")
	m.sshInputs[sshUser].SetValue("admin")
	m.sshInputs[sshPassword].SetValue("top-secret")
	m.sshInputs[sshCommandValue].SetValue("uname -a")

	view := m.View()
	for _, expected := range []string{"Remote SSH", "COMMAND", "10.0.0.1", "uname -a"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("SSH form does not contain %q", expected)
		}
	}
	if strings.Contains(view, "top-secret") {
		t.Fatal("SSH form leaked password")
	}
	m.resize(80, 30)
	m.sshOutput.SetContent("narrow-layout-result")
	if view := m.View(); !strings.Contains(view, "narrow-layout-result") {
		t.Fatal("narrow SSH layout hid per-server results")
	}

	m.toggleSSHMode()
	if view := m.View(); !strings.Contains(view, "SCRIPT") || !strings.Contains(view, "create-bond-vlan/run.sh") || !strings.Contains(view, "bond2.306") {
		t.Fatalf("script mode was not rendered: %s", view)
	}
}

func TestReportFormatDefaultsToMarkdownAndTogglesSimply(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "base", "")
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), reg, domain.Profile{APIVersion: "syssetup/v1", Name: "test"}, runner.Options{})
	if m.reportFormat != "md" {
		t.Fatalf("default report format = %q, want md", m.reportFormat)
	}
	m.toggleReportFormat()
	if m.reportFormat != "html" {
		t.Fatalf("toggled report format = %q, want html", m.reportFormat)
	}
	m.toggleReportFormat()
	if m.reportFormat != "md" {
		t.Fatalf("second toggled report format = %q, want md", m.reportFormat)
	}
}

func TestCompletedFeatureRunWritesSelectedReportFormat(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "base", "")
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	reportsDir := t.TempDir()
	profile := domain.Profile{APIVersion: "syssetup/v1", Name: "test", System: "01HTX", Features: []domain.FeatureSelection{{ID: "base"}}}
	m := newModel(context.Background(), reg, profile, runner.Options{ReportsDir: reportsDir, ReportFormat: "html"})
	m.runStartedAt = time.Now().Add(-time.Second)
	m.Update(runFinishedMsg{results: []domain.Result{{FeatureID: "base", Status: domain.StatusDone, Message: "configured"}}, output: "[PASS] complete"})
	matches, err := filepath.Glob(filepath.Join(reportsDir, "01HTX", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || !strings.Contains(m.notice, matches[0]) {
		t.Fatalf("report files = %#v, notice = %q", matches, m.notice)
	}
}

func TestSSHFormValidationDoesNotStartRun(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "base", "")
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), reg, domain.Profile{APIVersion: "syssetup/v1", Name: "test"}, runner.Options{})
	m.activeTab = tabSSH
	m.startRemoteRun()
	if m.remoteRunning || m.remoteErr == nil {
		t.Fatalf("expected validation error, running=%v error=%v", m.remoteRunning, m.remoteErr)
	}
}

func TestSSHLoadsRemoteServersFromSameSystemBaseProfile(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "base", "")
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	baseProfile := domain.Profile{
		APIVersion: "syssetup/v1",
		Name:       "base-server",
		System:     "01HTX",
		Features:   []domain.FeatureSelection{{ID: "base"}},
		RemoteServers: []domain.RemoteServer{
			{Name: "node-1", IP: "10.0.0.1", Username: "root", Password: "one"},
			{Name: "node-2", IP: "server.example", Port: 2222, Username: "admin", Password: "two"},
		},
	}
	featureProfile := domain.Profile{
		APIVersion: "syssetup/v1",
		Name:       "network",
		System:     "01HTX",
		Features:   []domain.FeatureSelection{{ID: "base"}},
	}
	m := newModelWithProfiles(context.Background(), reg, nil, []domain.Profile{baseProfile, featureProfile}, featureProfile, runner.Options{})
	m.loadBaseServers()
	servers, err := m.sshServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[0].Address != "10.0.0.1:22" || servers[0].Password != "one" {
		t.Fatalf("unexpected first server: %#v", servers)
	}
	if servers[1].Address != "server.example:2222" || servers[1].User != "admin" || servers[1].Password != "two" {
		t.Fatalf("unexpected second server: %#v", servers[1])
	}

	m.sshInputs[sshHosts].SetValue("192.0.2.10")
	m.sshInputs[sshUser].SetValue("manual")
	m.sshInputs[sshPassword].SetValue("manual-secret")
	servers, err = m.sshServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Address != "192.0.2.10:22" || servers[0].User != "manual" {
		t.Fatalf("manual server did not take precedence: %#v", servers)
	}
}

func TestSSHDoesNotLoadBaseProfileFromAnotherSystem(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "base", "")
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	baseProfile := domain.Profile{
		APIVersion:    "syssetup/v1",
		Name:          "base-server",
		System:        "02HCM",
		Features:      []domain.FeatureSelection{{ID: "base"}},
		RemoteServers: []domain.RemoteServer{{IP: "10.0.0.1", Username: "root", Password: "secret"}},
	}
	featureProfile := domain.Profile{APIVersion: "syssetup/v1", Name: "network", System: "01HTX", Features: []domain.FeatureSelection{{ID: "base"}}}
	m := newModelWithProfiles(context.Background(), reg, nil, []domain.Profile{featureProfile, baseProfile}, featureProfile, runner.Options{})
	m.loadBaseServers()
	if m.remoteErr == nil || len(m.profileServers) != 0 {
		t.Fatalf("expected system-scoped base-server error, servers=%#v error=%v", m.profileServers, m.remoteErr)
	}
}

func TestRemoteFeatureOpensSSHScriptForm(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "create-bond-vlan", `,"remoteOnly":true,"requireRoot":true`)
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), reg, domain.Profile{APIVersion: "syssetup/v1", Name: "test"}, runner.Options{})
	m.toggleCurrent()
	if m.activeTab != tabSSH || m.sshMode != sshScript || m.sshFocus != sshScriptArgs {
		t.Fatalf("remote feature did not open SSH script form: tab=%d mode=%d focus=%d", m.activeTab, m.sshMode, m.sshFocus)
	}
	wantPath := filepath.Join(root, "create-bond-vlan", "run.sh")
	if got := m.sshInputs[sshScriptPath].Value(); got != wantPath {
		t.Fatalf("script path = %q, want %q", got, wantPath)
	}
	if m.selected["create-bond-vlan"] {
		t.Fatal("remote-only feature was added to the local execution plan")
	}
}

func TestWorkflowLoadsIntoSSHForm(t *testing.T) {
	featuresRoot := t.TempDir()
	for _, id := range []string{"create-local-path", "create-bond-vlan", "verify-network"} {
		writeDashboardFeature(t, featuresRoot, id, `,"remoteOnly":true`)
	}
	features, err := registry.Load(featuresRoot)
	if err != nil {
		t.Fatal(err)
	}
	workflowsRoot := t.TempDir()
	workflowDirectory := filepath.Join(workflowsRoot, "prepare-setup-deploy")
	if err := os.MkdirAll(workflowDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "apiVersion":"syssetup/workflow/v1", "id":"prepare-setup-deploy",
  "name":"Prepare Setup Deploy", "version":"1", "steps":[
    {"id":"paths","feature":"create-local-path"},
    {"id":"vlan","feature":"create-bond-vlan","needs":["paths"]},
    {"id":"verify","feature":"verify-network","needs":["vlan"],
     "deriveArgs":{"step":"vlan","argumentIndex":0,"requireEach":true}}
  ]
}`
	if err := os.WriteFile(filepath.Join(workflowDirectory, "workflow.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	workflowRegistry, err := workflow.Load(workflowsRoot, features)
	if err != nil {
		t.Fatal(err)
	}
	m := newModelWithWorkflows(context.Background(), features, workflowRegistry, domain.Profile{APIVersion: "syssetup/v1", Name: "test"}, runner.Options{})
	m.resize(120, 30)
	m.activeTab = tabWorkflows
	if view := m.View(); !strings.Contains(view, "prepare-setup-deploy") || !strings.Contains(view, "1.3  verify-network") {
		t.Fatalf("workflow view is incomplete: %s", view)
	}
	m.loadCurrentWorkflow()
	if m.activeTab != tabSSH || m.sshMode != sshWorkflow || m.sshFocus != sshWorkflowConfig {
		t.Fatalf("workflow did not open SSH form: tab=%d mode=%d focus=%d", m.activeTab, m.sshMode, m.sshFocus)
	}
	if got := m.sshInputs[sshWorkflowConfig].Value(); got != filepath.Join("workflow-configs", "prepare-setup-deploy.json") {
		t.Fatalf("workflow config path = %q", got)
	}
}

func TestLocalWorkflowStartsWithoutSSHServer(t *testing.T) {
	featuresRoot := t.TempDir()
	writeDashboardFeature(t, featuresRoot, "local-check", `,"workflowCompatible":true`)
	features, err := registry.Load(featuresRoot)
	if err != nil {
		t.Fatal(err)
	}
	workflowsRoot := t.TempDir()
	workflowDirectory := filepath.Join(workflowsRoot, "local-validation")
	if err := os.MkdirAll(workflowDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "apiVersion":"syssetup/workflow/v1", "id":"local-validation", "name":"Local Validation", "version":"1",
  "executionMode":"local", "steps":[{"id":"check", "feature":"local-check"}]
}`
	if err := os.WriteFile(filepath.Join(workflowDirectory, "workflow.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	workflowRegistry, err := workflow.Load(workflowsRoot, features)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "local-validation.json")
	config := `{
  "apiVersion":"syssetup/workflow-config/v1", "workflow":"local-validation",
  "steps":{"check":[{"args":["verify"]}]}
}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newModelWithWorkflows(context.Background(), features, workflowRegistry, domain.Profile{APIVersion: "syssetup/v1", Name: "test"}, runner.Options{})
	m.loadCurrentWorkflow()
	m.sshInputs[sshWorkflowConfig].SetValue(configPath)
	if got := m.sshFieldOrder(); len(got) != 1 || got[0] != sshWorkflowConfig {
		t.Fatalf("local workflow fields = %#v, want config only", got)
	}
	if command := m.startRemoteRun(); command == nil {
		t.Fatal("local workflow did not start")
	}
	if m.remoteErr != nil || !m.remoteRunning || !strings.Contains(m.notice, "locally") {
		t.Fatalf("local workflow state: running=%t notice=%q err=%v", m.remoteRunning, m.notice, m.remoteErr)
	}
}

func TestProfileCanBeSwitchedWithoutRestartingTUI(t *testing.T) {
	root := t.TempDir()
	writeDashboardFeature(t, root, "base", "")
	writeDashboardFeature(t, root, "verify", `,"parameters":{"xml_file":{"type":"string","required":true}}`)
	reg, err := registry.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	baseProfile := domain.Profile{
		APIVersion: "syssetup/v1",
		Name:       "base",
		Features:   []domain.FeatureSelection{{ID: "base"}},
	}
	verifyProfile := domain.Profile{
		APIVersion:  "syssetup/v1",
		Name:        "verify-config",
		Description: "Verify an XML file",
		Features: []domain.FeatureSelection{{
			ID:         "verify",
			Parameters: map[string]any{"xml_file": "/tmp/config.xml"},
		}},
	}
	m := newModelWithProfiles(
		context.Background(), reg, nil,
		[]domain.Profile{baseProfile, verifyProfile}, baseProfile,
		runner.Options{DryRun: true},
	)
	m.resize(120, 30)
	m.activeTab = tabProfiles
	m.profileTable.SetCursor(1)
	view := m.View()
	if !strings.Contains(view, "verify-config") || !strings.Contains(view, "xml_file") {
		t.Fatalf("profile view is incomplete: %s", view)
	}

	m.loadCurrentProfile()
	if m.activeTab != tabFeatures || m.profile.Name != "verify-config" {
		t.Fatalf("profile was not loaded: tab=%d profile=%q", m.activeTab, m.profile.Name)
	}
	if m.selected["base"] || !m.selected["verify"] {
		t.Fatalf("selection was not replaced: %#v", m.selected)
	}
	if got := m.profileParameters["verify"]["xml_file"]; got != "/tmp/config.xml" {
		t.Fatalf("profile parameter was not loaded: %#v", got)
	}
	if len(m.resolved) != 1 || m.resolved[0].Feature.ID != "verify" {
		t.Fatalf("unexpected resolved plan: %#v", m.resolved)
	}
}

func writeDashboardFeature(t *testing.T, root, id, extra string) {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"apiVersion":"syssetup/v1","id":"` + id + `","name":"` + id + `","version":"1","entrypoint":"run.sh","timeoutSeconds":30` + extra + `}`
	if err := os.WriteFile(filepath.Join(directory, "feature.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "run.sh"), []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
