package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProfilesSortsJSONProfiles(t *testing.T) {
	root := t.TempDir()
	writeTestProfile(t, root, "zulu.json", "Zulu")
	writeTestProfile(t, root, "alpha.json", "alpha")
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	profiles, err := LoadProfiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 || profiles[0].Name != "alpha" || profiles[1].Name != "Zulu" {
		t.Fatalf("unexpected profiles: %#v", profiles)
	}
}

func TestLoadProfilesRejectsDuplicateNames(t *testing.T) {
	root := t.TempDir()
	writeTestProfile(t, root, "first.json", "same")
	writeTestProfile(t, root, "second.json", "SAME")
	if _, err := LoadProfiles(root); err == nil {
		t.Fatal("expected duplicate profile name error")
	}
}

func TestLoadProfilesAllowsEmptyDirectory(t *testing.T) {
	profiles, err := LoadProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected no profiles, got %#v", profiles)
	}
}

func writeTestProfile(t *testing.T, root, filename, name string) {
	t.Helper()
	content := `{"apiVersion":"syssetup/v1","name":"` + name + `","features":[{"id":"test"}]}`
	if err := os.WriteFile(filepath.Join(root, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
