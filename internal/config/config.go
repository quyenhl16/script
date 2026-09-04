package config

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/quyenhl16/script/internal/domain"
)

func LoadProfile(path string) (domain.Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Profile{}, fmt.Errorf("read profile %q: %w", path, err)
	}

	var profile domain.Profile
	if err := json.Unmarshal(data, &profile); err != nil {
		return domain.Profile{}, fmt.Errorf("parse profile %q: %w", path, err)
	}
	if profile.APIVersion != "syssetup/v1" {
		return domain.Profile{}, fmt.Errorf("profile %q uses unsupported apiVersion %q", path, profile.APIVersion)
	}
	if profile.Name == "" || len(profile.Features) == 0 {
		return domain.Profile{}, fmt.Errorf("profile must have a name and at least one feature")
	}
	for index, server := range profile.RemoteServers {
		if strings.TrimSpace(server.IP) == "" {
			return domain.Profile{}, fmt.Errorf("profile %q remoteServers[%d] must have an ip", path, index)
		}
		if strings.TrimSpace(server.Username) == "" || server.Password == "" {
			return domain.Profile{}, fmt.Errorf("profile %q remoteServers[%d] must have username and password", path, index)
		}
		if server.Port < 0 || server.Port > 65535 {
			return domain.Profile{}, fmt.Errorf("profile %q remoteServers[%d] has invalid port %d", path, index, server.Port)
		}
	}
	profile.Path = filepath.Clean(path)
	profile.System = filepath.Base(filepath.Dir(profile.Path))
	return profile, nil
}

func LoadProfiles(root string) ([]domain.Profile, error) {
	root = filepath.Clean(root)
	profiles := make([]domain.Profile, 0)
	seen := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			return nil
		}
		profile, err := LoadProfile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("resolve profile system for %q: %w", path, err)
		}
		if relative == "." {
			profile.System = ""
		} else {
			profile.System = filepath.ToSlash(relative)
		}
		key := strings.ToLower(profile.System + "\x00" + profile.Name)
		if previous, exists := seen[key]; exists {
			return fmt.Errorf("duplicate profile name %q in system %q: %q and %q", profile.Name, profile.System, previous, path)
		}
		seen[key] = path
		profiles = append(profiles, profile)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read profile directory %q: %w", root, err)
	}
	sort.Slice(profiles, func(i, j int) bool {
		leftSystem := strings.ToLower(profiles[i].System)
		rightSystem := strings.ToLower(profiles[j].System)
		if leftSystem != rightSystem {
			return leftSystem < rightSystem
		}
		left := strings.ToLower(profiles[i].Name)
		right := strings.ToLower(profiles[j].Name)
		if left == right {
			return profiles[i].Name < profiles[j].Name
		}
		return left < right
	})
	return profiles, nil
}
