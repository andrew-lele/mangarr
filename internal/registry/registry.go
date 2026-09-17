package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mangarr/internal/domain"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// GroupsFileName and ProfilesFileName are the registry file names, expected
// next to config.yaml in the config directory.
const (
	GroupsFileName   = "groups.yaml"
	ProfilesFileName = "profiles.yaml"
)

// EmptyGroupRegistry returns a registry with every field initialized, so
// callers can treat missing files as empty registries instead of errors.
func EmptyGroupRegistry() domain.GroupRegistry {
	return domain.GroupRegistry{
		Groups:      make(map[string]*domain.Group),
		AliasIndex:  make(map[string]string),
		NativeIndex: make(map[string]string),
	}
}

// EmptyProfileRegistry returns an empty profile registry.
func EmptyProfileRegistry() domain.ProfileRegistry {
	return domain.ProfileRegistry{
		Profiles: make(map[string]*domain.Profile),
	}
}

// resolveRegistryFile locates a sibling registry file in the config dir.
func resolveRegistryFile(configPath, fileName string) (string, error) {
	path := filepath.Join(filepath.Clean(configPath), fileName)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("%s not found or not a file", fileName)
	}
	return path, nil
}

// LoadGroups reads groups.yaml from the config directory and builds the
// registry plus reverse indexes. A missing file yields an empty registry
// (callers decide whether that is acceptable).
func LoadGroups(configPath string) (domain.GroupRegistry, error) {
	registry := EmptyGroupRegistry()

	filePath, err := resolveRegistryFile(configPath, GroupsFileName)
	if err != nil {
		return registry, nil
	}

	k := koanf.New(".")
	if err := k.Load(file.Provider(filePath), yaml.Parser()); err != nil {
		return registry, fmt.Errorf("reading %s: %w", filePath, err)
	}
	if err := k.Unmarshal("", &registry); err != nil {
		return registry, fmt.Errorf("decoding %s: %w", filePath, err)
	}

	if err := buildIndexes(&registry); err != nil {
		return registry, fmt.Errorf("indexing %s: %w", filePath, err)
	}

	return registry, nil
}

// buildIndexes fills AliasIndex and NativeIndex from the loaded Groups map
// and validates there are no duplicate aliases or native ids.
func buildIndexes(registry *domain.GroupRegistry) error {
	for groupID, group := range registry.Groups {
		if group == nil {
			continue
		}

		for _, alias := range group.Aliases {
			key := strings.ToLower(alias)
			if existing, ok := registry.AliasIndex[key]; ok {
				return fmt.Errorf("alias %q already mapped to %s", alias, existing)
			}
			registry.AliasIndex[key] = groupID
		}

		for sourceName, nativeID := range group.Sources {
			key := fmt.Sprintf("%s:%s", sourceName, nativeID)
			if existing, ok := registry.NativeIndex[key]; ok {
				return fmt.Errorf("native id %s:%s already mapped to %s", sourceName, nativeID, existing)
			}
			registry.NativeIndex[key] = groupID
		}
	}

	return nil
}

// ResolveGroupID maps a canonical id, alias, or (source:native) key to a
// canonical group id. Returns "" when nothing matches.
func ResolveGroupID(registry *domain.GroupRegistry, key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return ""
	}

	if _, ok := registry.Groups[trimmed]; ok {
		return trimmed
	}

	if id, ok := registry.AliasIndex[strings.ToLower(trimmed)]; ok {
		return id
	}

	if id, ok := registry.NativeIndex[trimmed]; ok {
		return id
	}

	return ""
}

// LoadProfiles reads profiles.yaml from the config directory, validates that
// every referenced group resolves, and returns the registry. A missing file
// yields an empty profile registry.
func LoadProfiles(configPath string, groups *domain.GroupRegistry) (domain.ProfileRegistry, error) {
	profiles := EmptyProfileRegistry()

	filePath, err := resolveRegistryFile(configPath, ProfilesFileName)
	if err != nil {
		return profiles, nil
	}

	k := koanf.New(".")
	if err := k.Load(file.Provider(filePath), yaml.Parser()); err != nil {
		return profiles, fmt.Errorf("reading %s: %w", filePath, err)
	}
	if err := k.Unmarshal("", &profiles); err != nil {
		return profiles, fmt.Errorf("decoding %s: %w", filePath, err)
	}

	if err := validateProfiles(&profiles, groups); err != nil {
		return profiles, fmt.Errorf("validating %s: %w", filePath, err)
	}

	return profiles, nil
}

// validateProfiles checks that each profile's group references resolve and
// that the fallback policy is one of the supported values.
func validateProfiles(profiles *domain.ProfileRegistry, groups *domain.GroupRegistry) error {
	for profileID, profile := range profiles.Profiles {
		if profile == nil {
			return fmt.Errorf("profile %q cannot be null", profileID)
		}

		switch profile.Fallback {
		case domain.FallbackAny, domain.FallbackNever:
		default:
			return fmt.Errorf("profile %q has unsupported fallback %q", profileID, profile.Fallback)
		}

		for _, groupRef := range profile.PreferredGroups {
			if ResolveGroupID(groups, groupRef) == "" {
				return fmt.Errorf("profile %q references unknown preferred group %q", profileID, groupRef)
			}
		}
		for _, groupRef := range profile.IgnoredGroups {
			if ResolveGroupID(groups, groupRef) == "" {
				return fmt.Errorf("profile %q references unknown ignored group %q", profileID, groupRef)
			}
		}
	}

	return nil
}

// HasProfile reports whether a profile with the given id or name exists.
func HasProfile(profiles *domain.ProfileRegistry, ref string) bool {
	for profileID, profile := range profiles.Profiles {
		if profileID == ref || (profile != nil && profile.Name == ref) {
			return true
		}
	}
	return false
}