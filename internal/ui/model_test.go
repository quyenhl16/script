package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/registry"
	"github.com/quyenhl16/script/internal/runner"
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
