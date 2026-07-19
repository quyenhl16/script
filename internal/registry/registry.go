package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/quyenhl16/script/internal/domain"
)

type Registry struct {
	features map[string]domain.Feature
}

func Load(root string) (*Registry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read feature directory %q: %w", root, err)
	}
	r := &Registry{features: make(map[string]domain.Feature)}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		manifestPath := filepath.Join(directory, "feature.json")
		data, err := os.ReadFile(manifestPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", manifestPath, err)
		}
		var feature domain.Feature
		if err := json.Unmarshal(data, &feature); err != nil {
			return nil, fmt.Errorf("parse %q: %w", manifestPath, err)
		}
		feature.Directory = directory
		if err := validate(feature); err != nil {
			return nil, fmt.Errorf("feature %q: %w", entry.Name(), err)
		}
		if _, exists := r.features[feature.ID]; exists {
			return nil, fmt.Errorf("duplicate feature ID %q", feature.ID)
		}
		r.features[feature.ID] = feature
	}
	if len(r.features) == 0 {
		return nil, fmt.Errorf("no features found in %q", root)
	}
	return r, nil
}

func validate(feature domain.Feature) error {
	if feature.APIVersion != "syssetup/v1" {
		return fmt.Errorf("unsupported apiVersion %q", feature.APIVersion)
	}
	if feature.ID == "" || feature.Name == "" || feature.Entrypoint == "" {
		return fmt.Errorf("id, name and entrypoint are required")
	}
	if feature.TimeoutSeconds <= 0 {
		return fmt.Errorf("timeoutSeconds must be greater than zero")
	}
	entrypoint := filepath.Join(feature.Directory, feature.Entrypoint)
	relative, err := filepath.Rel(feature.Directory, entrypoint)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("entrypoint escapes feature directory")
	}
	info, err := os.Stat(entrypoint)
	if err != nil || info.IsDir() {
		return fmt.Errorf("entrypoint %q is not a file", feature.Entrypoint)
	}
	for name, parameter := range feature.Parameters {
		if name == "" || (parameter.Type != "string" && parameter.Type != "integer" && parameter.Type != "boolean") {
			return fmt.Errorf("parameter %q has unsupported type %q", name, parameter.Type)
		}
	}
	return nil
}

func (r *Registry) List() []domain.Feature {
	features := make([]domain.Feature, 0, len(r.features))
	for _, feature := range r.features {
		features = append(features, feature)
	}
	sort.Slice(features, func(i, j int) bool { return features[i].ID < features[j].ID })
	return features
}

func (r *Registry) Resolve(profile domain.Profile) ([]domain.ResolvedFeature, error) {
	selected := make(map[string]map[string]any, len(profile.Features))
	for _, item := range profile.Features {
		if _, exists := selected[item.ID]; exists {
			return nil, fmt.Errorf("feature %q occurs more than once in profile", item.ID)
		}
		selected[item.ID] = item.Parameters
	}

	state := make(map[string]int)
	var resolved []domain.ResolvedFeature
	var visit func(string) error
	visit = func(id string) error {
		feature, exists := r.features[id]
		if !exists {
			return fmt.Errorf("unknown feature %q", id)
		}
		if state[id] == 1 {
			return fmt.Errorf("dependency cycle contains feature %q", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, dependency := range feature.DependsOn {
			if err := visit(dependency); err != nil {
				return fmt.Errorf("feature %q: %w", id, err)
			}
		}
		parameters, err := resolveParameters(feature, selected[id])
		if err != nil {
			return err
		}
		resolved = append(resolved, domain.ResolvedFeature{Feature: feature, Parameters: parameters})
		state[id] = 2
		return nil
	}
	for _, item := range profile.Features {
		if err := visit(item.ID); err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

func resolveParameters(feature domain.Feature, values map[string]any) (map[string]any, error) {
	result := make(map[string]any, len(feature.Parameters))
	for name := range values {
		if _, exists := feature.Parameters[name]; !exists {
			return nil, fmt.Errorf("feature %q has no parameter %q", feature.ID, name)
		}
	}
	for name, definition := range feature.Parameters {
		value, exists := values[name]
		if !exists {
			value, exists = definition.Default, definition.Default != nil
		}
		if !exists {
			if definition.Required {
				return nil, fmt.Errorf("feature %q requires parameter %q", feature.ID, name)
			}
			continue
		}
		if !matchesType(value, definition.Type) {
			return nil, fmt.Errorf("feature %q parameter %q must be %s", feature.ID, name, definition.Type)
		}
		result[name] = value
	}
	return result, nil
}

func matchesType(value any, kind string) bool {
	switch kind {
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "boolean":
		_, ok := value.(bool)
		return ok
	default:
		return false
	}
}
