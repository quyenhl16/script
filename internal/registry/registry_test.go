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

func TestBundledEnableSCTPIsRemoteOnly(t *testing.T) {
	registry, err := Load(filepath.Join("..", "..", "features"))
	if err != nil {
		t.Fatal(err)
	}
	feature, found := registry.Get("enable-sctp")
	if !found {
		t.Fatal("bundled enable-sctp feature was not found")
	}
	if !feature.RemoteOnly || !feature.RequireRoot {
		t.Fatalf("enable-sctp must be a root remote-only feature: %#v", feature)
	}
	if len(feature.SupportedOS) != 1 || feature.SupportedOS[0] != "rhel" {
		t.Fatalf("enable-sctp supportedOS = %#v, want [rhel]", feature.SupportedOS)
	}
}

func TestBundledVerifyXMLConfigIsLocal(t *testing.T) {
	registry, err := Load(filepath.Join("..", "..", "features"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve(domain.Profile{
		APIVersion: "syssetup/v1",
		Name:       "verify-xml",
		Features: []domain.FeatureSelection{{
			ID: "verify-xml-config",
			Parameters: map[string]any{
				"xml_file": "/tmp/system.xml",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Feature.RemoteOnly {
		t.Fatalf("verify-xml-config must resolve as a local feature: %#v", resolved)
	}
	if got := resolved[0].Parameters["rules_file"]; got != "checks/xml/system-critical-paths.json" {
		t.Fatalf("rules_file default = %#v", got)
	}
}

func TestBundledAMFSystemCheckGuideIsLocal(t *testing.T) {
	registry, err := Load(filepath.Join("..", "..", "features"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve(domain.Profile{
		APIVersion: "syssetup/v1",
		Name:       "amf-guide",
		Features:   []domain.FeatureSelection{{ID: "amf-system-check-guide"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Feature.RemoteOnly || resolved[0].Feature.RequireRoot {
		t.Fatalf("AMF guide must be an unprivileged local feature: %#v", resolved)
	}
	feature := resolved[0].Feature
	if !strings.Contains(feature.Name, "Hướng dẫn kiểm tra hệ thống AMF") ||
		!strings.Contains(feature.Description, "song ngữ Anh-Việt") {
		t.Fatalf("AMF guide metadata must describe Vietnamese support: %#v", feature)
	}
	content, err := os.ReadFile(filepath.Join(feature.Directory, feature.Entrypoint))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(content)
	for _, expected := range []string{
		"HƯỚNG DẪN KIỂM TRA HỆ THỐNG AMF",
		"TRƯỚC KHI CÀI ĐẶT AMF CNF",
		"SAU KHI CÀI ĐẶT AMF CNF",
		"THÔNG TIN BỔ SUNG",
		"Note / Lưu ý:",
	} {
		if !strings.Contains(guide, expected) {
			t.Errorf("AMF guide is missing bilingual content %q", expected)
		}
	}
}

func TestBundledLoadNetConfConfigIsLocal(t *testing.T) {
	registry, err := Load(filepath.Join("..", "..", "features"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve(domain.Profile{
		APIVersion: "syssetup/v1",
		Name:       "load-netconf",
		Features: []domain.FeatureSelection{{
			ID: "load-netconf-config",
			Parameters: map[string]any{
				"namespace":          "test-ns",
				"source_config_file": "/tmp/config.xml",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Feature.RemoteOnly {
		t.Fatalf("load-netconf-config must resolve as a local feature: %#v", resolved)
	}
	if got := resolved[0].Parameters["destination_config_file"]; got != "config.xml" {
		t.Fatalf("destination_config_file default = %#v", got)
	}
	if got := resolved[0].Parameters["confd_dir"]; got != "." {
		t.Fatalf("confd_dir default = %#v", got)
	}
}

func TestBundledExportNetConfConfigIsLocal(t *testing.T) {
	registry, err := Load(filepath.Join("..", "..", "features"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve(domain.Profile{
		APIVersion: "syssetup/v1",
		Name:       "export-netconf",
		Features: []domain.FeatureSelection{{
			ID: "export-netconf-config",
			Parameters: map[string]any{
				"namespace": "test-ns",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Feature.RemoteOnly {
		t.Fatalf("export-netconf-config must resolve as a local feature: %#v", resolved)
	}
	if got := resolved[0].Parameters["remote_config_file"]; got != "amf-running-config.xml" {
		t.Fatalf("remote_config_file default = %#v", got)
	}
	if got := resolved[0].Parameters["destination_config_file"]; got != "amf-running-config.xml" {
		t.Fatalf("destination_config_file default = %#v", got)
	}
	if got := resolved[0].Parameters["overwrite"]; got != false {
		t.Fatalf("overwrite default = %#v", got)
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
