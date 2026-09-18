package registry

import (
	"fmt"
	"slices"

	"mangarr/internal/domain"
)

// ProfileScanSources returns the STRICT scan-source set for a profile: the
// union, in preferred-group order, of every source key listed in the
// per-source native-id maps of the profile's PREFERRED groups. Sources a user
// might map for ignored groups, or not map at all, are intentionally NOT
// included: fallback:any must never trigger for off-profile sources. Sources
// without a mangarr adapter are filtered out by the caller (ValidateMonitored
// Entries) using the mangarr source registry.
func ProfileScanSources(groups *domain.GroupRegistry, profiles *domain.ProfileRegistry, profileRef string) []string {
	profile := findProfile(profiles, profileRef)
	if profile == nil {
		return nil
	}

	var sources []string
	seen := make(map[string]bool)
	for _, groupRef := range profile.PreferredGroups {
		canonical := ResolveGroupID(groups, groupRef)
		group, ok := groups.Groups[canonical]
		if !ok || group == nil {
			continue
		}
		for sourceName := range group.Sources {
			if !seen[sourceName] {
				seen[sourceName] = true
				sources = append(sources, sourceName)
			}
		}
	}

	return sources
}

// findProfile returns the profile referenced by id or name, or nil.
func findProfile(profiles *domain.ProfileRegistry, ref string) *domain.Profile {
	for profileID, profile := range profiles.Profiles {
		if profileID == ref || (profile != nil && profile.Name == ref) {
			return profile
		}
	}
	return nil
}

// availableSources is the predicate form of the known-source filter.
type knownSourceSet map[string]bool

// ValidateMonitoredEntries checks every monitoredManga entry:
//
//   - source+manga entries (the source-pinned, Kahon-migrated form) keep
//     today's exact behavior and pass through unchanged;
//   - title+qualityProfile entries must resolve to at least one scanned
//     source: the profile must exist, and at least one of its preferred
//     groups must map a source key that mangarr supports.
//
// Entries without a profile and without an explicit source (nothing to
// do) are rejected. knownSources is the set of mangarr source keys (see
// source.Keys). The first invalid entry's error is returned.
func ValidateMonitoredEntries(entries map[string]*domain.MonitoredManga, groups *domain.GroupRegistry, profiles *domain.ProfileRegistry, knownSources []string) error {
	for title, entry := range entries {
		if err := ValidateMonitoredEntry(title, entry, groups, profiles, knownSources); err != nil {
			return err
		}
	}
	return nil
}

// ValidateMonitoredEntry validates one monitoredManga entry; see
// ValidateMonitoredEntries for the rules.
func ValidateMonitoredEntry(title string, entry *domain.MonitoredManga, groups *domain.GroupRegistry, profiles *domain.ProfileRegistry, knownSources []string) error {
	known := make(knownSourceSet, len(knownSources))
	for _, key := range knownSources {
		known[key] = true
	}

	if entry == nil {
		return fmt.Errorf("monitoredManga entry %q cannot be null", title)
	}
	// Any explicit source/manga — including partial entries like a bare
	// source — belongs to the source-pinned path: the legacy adapter
	// validation reports it per-manga, exactly as before this feature.
	if entry.Source != "" || entry.Manga != "" {
		return nil
	}

	if entry.QualityProfile == "" {
		return fmt.Errorf("monitoredManga entry %q has no source/manga and no qualityProfile; set qualityProfile to track by title, or source+manga for a pinned entry", title)
	}
	if !HasProfile(profiles, entry.QualityProfile) {
		return fmt.Errorf("monitoredManga entry %q references unknown quality profile %q", title, entry.QualityProfile)
	}

	mapped := ProfileScanSources(groups, profiles, entry.QualityProfile)
	for _, sourceKey := range mapped {
		if known[sourceKey] {
			return nil
		}
	}

	return fmt.Errorf("monitoredManga entry %q: profile %q maps no supported scan sources (has %v)", title, entry.QualityProfile, mapped)
}

// ScannableSources returns the profile's scan-source set restricted to the
// mangarr-supported source keys, preserving profile order. A nil profile
// yields nothing.
func ScannableSources(groups *domain.GroupRegistry, profiles *domain.ProfileRegistry, profileRef string, knownSources []string) []string {
	known := make(knownSourceSet, len(knownSources))
	for _, key := range knownSources {
		known[key] = true
	}

	return slices.DeleteFunc(ProfileScanSources(groups, profiles, profileRef), func(sourceKey string) bool {
		return !known[sourceKey]
	})
}
