package config

import (
	"encoding/json"
	"fmt"
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
	return profile, nil
}

func LoadProfiles(root string) ([]domain.Profile, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read profile directory %q: %w", root, err)
	}
	profiles := make([]domain.Profile, 0, len(entries))
	seen := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		profile, err := LoadProfile(path)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(profile.Name)
		if previous, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate profile name %q in %q and %q", profile.Name, previous, path)
		}
		seen[key] = path
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		left := strings.ToLower(profiles[i].Name)
		right := strings.ToLower(profiles[j].Name)
		if left == right {
			return profiles[i].Name < profiles[j].Name
		}
		return left < right
	})
	return profiles, nil
}
