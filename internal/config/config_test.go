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

func TestLoadProfilesDiscoversSystemDirectoriesAndAllowsSameNameAcrossSystems(t *testing.T) {
	root := t.TempDir()
	firstSystem := filepath.Join(root, "01HTX")
	secondSystem := filepath.Join(root, "02HCM")
	for _, directory := range []string{firstSystem, secondSystem} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestProfile(t, directory, "base-server.json", "base-server")
	}
	writeTestProfile(t, firstSystem, "network.json", "network")

	profiles, err := LoadProfiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 {
		t.Fatalf("profiles = %d, want 3: %#v", len(profiles), profiles)
	}
	wantSystems := []string{"01HTX", "01HTX", "02HCM"}
	for index, want := range wantSystems {
		if profiles[index].System != want {
			t.Fatalf("profiles[%d].System = %q, want %q", index, profiles[index].System, want)
		}
	}
}

func TestLoadProfileReadsAndValidatesRemoteServers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "base-server.json")
	content := `{
  "apiVersion":"syssetup/v1", "name":"base-server",
  "features":[{"id":"test"}],
  "remoteServers":[{"name":"node-1","ip":"10.0.0.1","port":2222,"username":"root","password":"secret"}]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	profile, err := LoadProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.RemoteServers) != 1 || profile.RemoteServers[0].Port != 2222 || profile.RemoteServers[0].Username != "root" {
		t.Fatalf("unexpected remote servers: %#v", profile.RemoteServers)
	}

	invalid := `{"apiVersion":"syssetup/v1","name":"invalid","features":[{"id":"test"}],"remoteServers":[{"ip":"10.0.0.2","username":"root"}]}`
	if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfile(path); err == nil {
		t.Fatal("expected missing remote server password error")
	}
}

func writeTestProfile(t *testing.T, root, filename, name string) {
	t.Helper()
	content := `{"apiVersion":"syssetup/v1","name":"` + name + `","features":[{"id":"test"}]}`
	if err := os.WriteFile(filepath.Join(root, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
