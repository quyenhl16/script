package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/quyenhl16/script/internal/domain"
)

func TestResolveOrdersDependenciesAndAppliesDefaults(t *testing.T) {
	root := t.TempDir()
	writeFeature(t, root, "base", `{
  "apiVersion":"syssetup/v1", "id":"base", "name":"Base",
  "version":"1.0.0", "entrypoint":"run.sh", "timeoutSeconds":30
}`)
	writeFeature(t, root, "web", `{
  "apiVersion":"syssetup/v1", "id":"web", "name":"Web",
  "version":"1.0.0", "entrypoint":"run.sh", "timeoutSeconds":30,
  "dependsOn":["base"],
  "parameters":{"port":{"type":"integer","default":8080}}
}`)

	registry, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.Profile{
		APIVersion: "syssetup/v1",
		Name:       "test",
		Features:   []domain.FeatureSelection{{ID: "web"}},
	}
	resolved, err := registry.Resolve(profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 2 || resolved[0].Feature.ID != "base" || resolved[1].Feature.ID != "web" {
		t.Fatalf("unexpected order: %#v", resolved)
	}
	if resolved[1].Parameters["port"] != float64(8080) {
		t.Fatalf("default parameter missing: %#v", resolved[1].Parameters)
	}
}

func TestResolveRejectsDependencyCycle(t *testing.T) {
	root := t.TempDir()
	writeFeature(t, root, "a", `{
  "apiVersion":"syssetup/v1", "id":"a", "name":"A", "version":"1",
  "entrypoint":"run.sh", "timeoutSeconds":30, "dependsOn":["b"]
}`)
	writeFeature(t, root, "b", `{
  "apiVersion":"syssetup/v1", "id":"b", "name":"B", "version":"1",
  "entrypoint":"run.sh", "timeoutSeconds":30, "dependsOn":["a"]
}`)
	registry, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Resolve(domain.Profile{Name: "test", Features: []domain.FeatureSelection{{ID: "a"}}})
	if err == nil {
		t.Fatal("expected dependency cycle error")
	}
}

func TestListSortsFeaturesAlphabeticallyByID(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"zulu", "Bravo", "alpha"} {
		writeFeature(t, root, id, `{
  "apiVersion":"syssetup/v1", "id":"`+id+`", "name":"`+id+`",
  "version":"1", "entrypoint":"run.sh", "timeoutSeconds":30
}`)
	}
	registry, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	features := registry.List()
	for index, want := range []string{"alpha", "Bravo", "zulu"} {
		if features[index].ID != want {
			t.Fatalf("feature %d = %q, want %q", index, features[index].ID, want)
		}
	}
}

func TestResolveRejectsRemoteOnlyFeature(t *testing.T) {
	root := t.TempDir()
	writeFeature(t, root, "remote-task", `{
  "apiVersion":"syssetup/v1", "id":"remote-task", "name":"Remote task",
  "version":"1", "entrypoint":"run.sh", "timeoutSeconds":30, "remoteOnly":true
}`)
	registry, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Resolve(domain.Profile{Name: "test", Features: []domain.FeatureSelection{{ID: "remote-task"}}})
	if err == nil || !strings.Contains(err.Error(), "remote-only") {
		t.Fatalf("expected remote-only error, got %v", err)
	}
}

func writeFeature(t *testing.T, root, id, manifest string) {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "feature.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "run.sh"), []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
