package config

import (
	"encoding/json"
	"fmt"
	"os"

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
