package platform

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Info struct {
	ID      string
	Like    []string
	Version string
}

func Detect() (Info, error) {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return Info{}, fmt.Errorf("read /etc/os-release: %w", err)
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), "=")
		if found {
			values[key] = strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	if err := scanner.Err(); err != nil {
		return Info{}, err
	}
	return Info{
		ID:      strings.ToLower(values["ID"]),
		Like:    strings.Fields(strings.ToLower(values["ID_LIKE"])),
		Version: values["VERSION_ID"],
	}, nil
}

func (i Info) Supports(allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range append([]string{i.ID}, i.Like...) {
		for _, target := range allowed {
			if strings.EqualFold(candidate, target) {
				return true
			}
		}
	}
	return false
}
