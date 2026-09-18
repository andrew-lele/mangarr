package registry

import (
	"testing"

	"mangarr/internal/domain"

	"github.com/stretchr/testify/require"
)

func testRegistry(t *testing.T) (domain.GroupRegistry, domain.ProfileRegistry) {
	t.Helper()

	groups := EmptyGroupRegistry()
	groups.Groups = map[string]*domain.Group{
		"g-asura":    {ID: "g-asura", Aliases: []string{"Asura"}, Sources: map[string]string{"atsumaru": "1", "mangafire": "1"}},
		"g-webtoon":  {ID: "g-webtoon", Aliases: []string{"Webtoon"}, Sources: map[string]string{"weebcentral": "1"}},
		"g-demonic":  {ID: "g-demonic", Aliases: []string{"Demonic"}, Sources: map[string]string{"mangafire": "2", "comix": "2"}},
		"g-flame":    {ID: "g-flame", Aliases: []string{"Flame"}, Sources: map[string]string{"atsumaru": "3"}},
		"g-firesolo": {ID: "g-firesolo", Aliases: []string{"FireSolo"}, Sources: map[string]string{"mangafire": "9"}},
	}
	require.NoError(t, buildIndexes(&groups))

	profiles := EmptyProfileRegistry()
	profiles.Profiles = map[string]*domain.Profile{
		"p-preferred": {
			ID:              "p-preferred",
			Name:            "Preferred Only",
			PreferredGroups: []string{"Asura", "Webtoon"},
			IgnoredGroups:   []string{"Demonic"},
			Fallback:        domain.FallbackAny,
		},
		"p-strict": {
			ID:              "p-strict",
			Name:            "Strict",
			PreferredGroups: []string{"Asura"},
		},
		"p-firesolo": {
			ID:              "p-firesolo",
			Name:            "Only Fire",
			PreferredGroups: []string{"FireSolo"},
		},
	}

	return groups, profiles
}

func TestProfileScanSourcesIsStrict(t *testing.T) {
	t.Parallel()

	groups, profiles := testRegistry(t)

	// Only the PREFERRED groups' sources are returned, in preferred order;
	// the ignored group's sources (mangafire, comix) are NOT scanned.
	sources := ProfileScanSources(&groups, &profiles, "Preferred Only")
	require.Equal(t, []string{"atsumaru", "mangafire", "weebcentral"}, sources)

	// Multiple preferred groups on the same source collapse to one entry;
	// here Asura maps atsumaru AND mangafire, so both are derived (the
	// known-source intersection happens in ScannableSources).
	sources = ProfileScanSources(&groups, &profiles, "Strict")
	require.Equal(t, []string{"atsumaru", "mangafire"}, sources)

	// Unknown profile ref yields nothing.
	require.Nil(t, ProfileScanSources(&groups, &profiles, "nope"))
}

func TestScannableSourcesIntersectsKnownSources(t *testing.T) {
	t.Parallel()

	groups, profiles := testRegistry(t)

	// mangafire has no mangarr adapter; the scan set must drop it.
	sources := ScannableSources(&groups, &profiles, "Preferred Only", []string{"atsumaru", "weebcentral", "comix"})
	require.Equal(t, []string{"atsumaru", "weebcentral"}, sources)
}

func TestValidateMonitoredEntry(t *testing.T) {
	t.Parallel()

	groups, profiles := testRegistry(t)
	known := []string{"atsumaru", "weebcentral", "comix"}

	// Legacy source-pinned entry (and even a bare-source entry) pass through:
	// the source-pinned path validates them per-manga.
	require.NoError(t, ValidateMonitoredEntry("Pinned", &domain.MonitoredManga{Source: "comix", Manga: "https://comix.to/title/x"}, &groups, &profiles, known))
	require.NoError(t, ValidateMonitoredEntry("Bare", &domain.MonitoredManga{Source: "comix"}, &groups, &profiles, known))

	// Title+profile entry with an in-profile source is valid.
	require.NoError(t, ValidateMonitoredEntry("Tracked", &domain.MonitoredManga{QualityProfile: "Preferred Only"}, &groups, &profiles, known))

	// Title+profile entry for an unknown profile is invalid.
	err := ValidateMonitoredEntry("Tracked", &domain.MonitoredManga{QualityProfile: "Nope"}, &groups, &profiles, known)
	require.ErrorContains(t, err, `references unknown quality profile "Nope"`)

	// Title+profile entry whose profile maps zero mangarr-supported sources
	// (mangafire-only preferred group) is invalid.
	err = ValidateMonitoredEntry("Tracked", &domain.MonitoredManga{QualityProfile: "Only Fire"}, &groups, &profiles, known)
	require.ErrorContains(t, err, "maps no supported scan sources")

	// Entry with neither source nor profile is invalid.
	err = ValidateMonitoredEntry("Empty", &domain.MonitoredManga{}, &groups, &profiles, known)
	require.ErrorContains(t, err, "no source/manga and no qualityProfile")
}

func TestValidateMonitoredEntriesFirstError(t *testing.T) {
	t.Parallel()

	groups, profiles := testRegistry(t)
	known := []string{"atsumaru"}

	err := ValidateMonitoredEntries(map[string]*domain.MonitoredManga{
		"Good":   {Source: "comix", Manga: "https://comix.to/title/y"},
		"Bad":    {QualityProfile: "Missing"},
		"Gooder": {QualityProfile: "Preferred Only"},
	}, &groups, &profiles, known)
	require.ErrorContains(t, err, `"Bad" references unknown quality profile "Missing"`)
}
