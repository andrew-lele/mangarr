package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"mangarr/internal/domain"
	"mangarr/internal/registry"

	"github.com/stretchr/testify/require"
)

const (
	cpGroupID       = "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b"
	novaGroupID     = "b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c"
	profileID       = "f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f"
	profileName     = "Comix Preferred"
	comixCPNative   = "9641"
	comixNovaNative = "4725"
)

const testGroupsYAML = `version: 1

groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "CP" ]
    sources:
      comix: "9641"

  b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c:
    aliases: [ "Nova" ]
    sources:
      comix: "4725"
      atsumaru: "scan-nova"
`

const testProfilesYAML = `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Comix Preferred"
    preferredGroups: [ "CP" ]
    ignoredGroups: [ "b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c" ]
    fallback: "any"
`

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeRegistry writes groups.yaml and profiles.yaml into a temp dir and
// loads both registries through the production loaders.
func writeRegistry(t *testing.T, groupsYAML, profilesYAML string) (domain.GroupRegistry, domain.ProfileRegistry) {
	t.Helper()

	root := t.TempDir()
	writeFile(t, root, registry.GroupsFileName, groupsYAML)
	writeFile(t, root, registry.ProfilesFileName, profilesYAML)

	groups, err := registry.LoadGroups(root)
	require.NoError(t, err)
	profiles, err := registry.LoadProfiles(root, &groups)
	require.NoError(t, err)
	return groups, profiles
}

func TestResolveHandlesEmptyRegistries(t *testing.T) {
	t.Parallel()

	var groups domain.GroupRegistry
	var profiles domain.ProfileRegistry

	decision := Resolve(&groups, &profiles, "comix", "9641", profileID)
	require.Equal(t, domain.OutcomeUnknown, decision.Outcome)
	require.Equal(t, -1, decision.PreferredIndex)
	require.Equal(t, "", decision.CanonicalID)
}

func TestResolveUnknownWhenProfileRefDoesNotMatch(t *testing.T) {
	t.Parallel()

	groups, profiles := writeRegistry(t, testGroupsYAML, testProfilesYAML)

	decision := Resolve(&groups, &profiles, "comix", comixCPNative, "no-such-profile")
	require.Equal(t, domain.OutcomeUnknown, decision.Outcome)
	require.Equal(t, -1, decision.PreferredIndex)
	require.Equal(t, "", decision.CanonicalID)
}

func TestResolveUnknownWhenChapterHasNoGroup(t *testing.T) {
	t.Parallel()

	groups, profiles := writeRegistry(t, testGroupsYAML, testProfilesYAML)

	decision := Resolve(&groups, &profiles, "comix", "  ", profileID)
	require.Equal(t, domain.OutcomeUnknown, decision.Outcome)
	require.Equal(t, "", decision.CanonicalID)
}

func TestResolvePreferredByNativeID(t *testing.T) {
	t.Parallel()

	groups, profiles := writeRegistry(t, testGroupsYAML, testProfilesYAML)

	decision := Resolve(&groups, &profiles, "comix", comixCPNative, profileID)
	require.Equal(t, domain.OutcomePreferred, decision.Outcome)
	require.Equal(t, 0, decision.PreferredIndex)
	require.Equal(t, cpGroupID, decision.CanonicalID)
}

func TestResolvePreferredByProfileName(t *testing.T) {
	t.Parallel()

	groups, profiles := writeRegistry(t, testGroupsYAML, testProfilesYAML)

	decision := Resolve(&groups, &profiles, "comix", comixCPNative, profileName)
	require.Equal(t, domain.OutcomePreferred, decision.Outcome)
	require.Equal(t, 0, decision.PreferredIndex)
}

func TestResolvePreferredIndexReflectsProfileOrder(t *testing.T) {
	t.Parallel()

	profilesYAML := `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Two Preferred"
    preferredGroups: [ "Nova", "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b" ]
    ignoredGroups: [ ]
    fallback: "any"
`
	groups, profiles := writeRegistry(t, testGroupsYAML, profilesYAML)

	// Nova (comix 4725) is first in the list; CP (comix 9641) is second and
	// referenced by its canonical UUID rather than an alias.
	decision := Resolve(&groups, &profiles, "comix", comixNovaNative, profileID)
	require.Equal(t, domain.OutcomePreferred, decision.Outcome)
	require.Equal(t, 0, decision.PreferredIndex)

	decision = Resolve(&groups, &profiles, "comix", comixCPNative, profileID)
	require.Equal(t, domain.OutcomePreferred, decision.Outcome)
	require.Equal(t, 1, decision.PreferredIndex)
}

func TestResolveIgnoredByNativeID(t *testing.T) {
	t.Parallel()

	groups, profiles := writeRegistry(t, testGroupsYAML, testProfilesYAML)

	decision := Resolve(&groups, &profiles, "comix", comixNovaNative, profileID)
	require.Equal(t, domain.OutcomeIgnored, decision.Outcome)
	require.Equal(t, -1, decision.PreferredIndex)
	require.Equal(t, novaGroupID, decision.CanonicalID)
}

func TestResolveIgnoredWinsOverPreferred(t *testing.T) {
	t.Parallel()

	profilesYAML := `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Overlap"
    preferredGroups: [ "Nova" ]
    ignoredGroups: [ "Nova" ]
    fallback: "any"
`
	groups, profiles := writeRegistry(t, testGroupsYAML, profilesYAML)

	decision := Resolve(&groups, &profiles, "comix", comixNovaNative, profileID)
	require.Equal(t, domain.OutcomeIgnored, decision.Outcome)
}

func TestResolveUnknownForUnregisteredNativeID(t *testing.T) {
	t.Parallel()

	groups, profiles := writeRegistry(t, testGroupsYAML, testProfilesYAML)

	decision := Resolve(&groups, &profiles, "comix", "9999", profileID)
	require.Equal(t, domain.OutcomeUnknown, decision.Outcome)
	require.Equal(t, -1, decision.PreferredIndex)
	require.Equal(t, "", decision.CanonicalID)
}

func TestResolveUnregisteredNativeIDWithFallbackNeverIsIgnored(t *testing.T) {
	t.Parallel()

	profilesYAML := `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Strict"
    preferredGroups: [ "CP" ]
    ignoredGroups: [ ]
    fallback: "never"
`
	groups, profiles := writeRegistry(t, testGroupsYAML, profilesYAML)

	decision := Resolve(&groups, &profiles, "comix", "9999", profileID)
	require.Equal(t, domain.OutcomeIgnored, decision.Outcome)
	require.Equal(t, "", decision.CanonicalID)
}

func TestResolveKnownUnlistedGroupWithFallbackAnyIsUnknown(t *testing.T) {
	t.Parallel()

	profilesYAML := `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "CP Only"
    preferredGroups: [ "CP" ]
    ignoredGroups: [ ]
    fallback: "any"
`
	groups, profiles := writeRegistry(t, testGroupsYAML, profilesYAML)

	decision := Resolve(&groups, &profiles, "comix", comixNovaNative, profileID)
	require.Equal(t, domain.OutcomeUnknown, decision.Outcome)
	require.Equal(t, novaGroupID, decision.CanonicalID)
}

func TestResolveKnownUnlistedGroupWithFallbackNeverIsIgnored(t *testing.T) {
	t.Parallel()

	profilesYAML := `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "CP Only Strict"
    preferredGroups: [ "CP" ]
    ignoredGroups: [ ]
    fallback: "never"
`
	groups, profiles := writeRegistry(t, testGroupsYAML, profilesYAML)

	decision := Resolve(&groups, &profiles, "comix", comixNovaNative, profileID)
	require.Equal(t, domain.OutcomeIgnored, decision.Outcome)
	require.Equal(t, novaGroupID, decision.CanonicalID)
}

func TestResolveUsesSourceSpecificNativeKey(t *testing.T) {
	t.Parallel()

	groups, profiles := writeRegistry(t, testGroupsYAML, testProfilesYAML)

	// Nova's atsumaru scan id resolves under the atsumaru source key...
	decision := Resolve(&groups, &profiles, "atsumaru", "scan-nova", profileID)
	require.Equal(t, domain.OutcomeIgnored, decision.Outcome)
	require.Equal(t, novaGroupID, decision.CanonicalID)

	// ...but the same value means nothing under the wrong source name.
	decision = Resolve(&groups, &profiles, "comix", "scan-nova", profileID)
	require.Equal(t, domain.OutcomeUnknown, decision.Outcome)

	// An unknown source name resolves nothing either.
	decision = Resolve(&groups, &profiles, "nope", comixCPNative, profileID)
	require.Equal(t, domain.OutcomeUnknown, decision.Outcome)
}

func TestResolveWithResolverUsesPerSourceExtension(t *testing.T) {
	t.Parallel()

	groupsYAML := `version: 1

groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "Asura" ]

  b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c:
    aliases: [ "Webtoon" ]

  d4e34b88-1000-4a4e-9c00-9e6f5d2b0a4c:
    aliases: [ "Flame" ]
`
	profilesYAML := `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Asura First"
    preferredGroups: [ "Asura", "Webtoon" ]
    ignoredGroups: [ ]
    fallback: "any"

  c3d11a76-2234-42e0-9b30-8e6f5d2b0a4c:
    name: "Webtoon First"
    preferredGroups: [ "Webtoon", "Asura" ]
    ignoredGroups: [ ]
    fallback: "any"
`
	groups, profiles := writeRegistry(t, groupsYAML, profilesYAML)

	// A stub mimicking the atsumaru greedy resolver: the chapter's scoped
	// ScanID is per-manga, so the manga's scanlator NAMES are the
	// candidates, and the profile's preferredGroups ORDER picks the winner
	// (atsumaru ids are scoped per-manga, never "atsumaru:<id>" entries).
	resolver := func(groups *domain.GroupRegistry, profile *domain.Profile, nativeGroup string) string {
		candidates := []string{"Webtoon", "Asura"}
		for _, preferredRef := range profile.PreferredGroups {
			canonical := registry.ResolveGroupID(groups, preferredRef)
			if canonical == "" {
				continue
			}
			for _, name := range candidates {
				if registry.ResolveGroupID(groups, name) == canonical {
					return canonical
				}
			}
		}
		return ""
	}

	// Asura wins because it is earlier in the preferred order, even though
	// the resolver's candidates list Webtoon first and the chapter's own
	// scoped id is arbitrary.
	decision := ResolveWithResolver(&groups, &profiles, "atsumaru", "scoped-asura", profileID, resolver)
	require.Equal(t, domain.OutcomePreferred, decision.Outcome)
	require.Equal(t, 0, decision.PreferredIndex)
	require.Equal(t, cpGroupID, decision.CanonicalID)

	// Webtoon first in preferred order -> Webtoon canonical wins.
	webtoonProfile := "c3d11a76-2234-42e0-9b30-8e6f5d2b0a4c"
	decision = ResolveWithResolver(&groups, &profiles, "atsumaru", "scoped-asura", webtoonProfile, resolver)
	require.Equal(t, domain.OutcomePreferred, decision.Outcome)
	require.Equal(t, 0, decision.PreferredIndex)
	require.Equal(t, novaGroupID, decision.CanonicalID)

	// No preferred candidate match falls back to the unlisted-group policy
	// (fallback any -> unknown, no canonical).
	flameOnly := "f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f"
	flameProfilesYAML := `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Flame Only"
    preferredGroups: [ "Flame" ]
    ignoredGroups: [ ]
    fallback: "any"
`
	testGroups, testProfiles := writeRegistry(t, groupsYAML, flameProfilesYAML)
	decision = ResolveWithResolver(&testGroups, &testProfiles, "atsumaru", "scoped-asura", flameOnly, resolver)
	require.Equal(t, domain.OutcomeUnknown, decision.Outcome)
	require.Equal(t, "", decision.CanonicalID)

	// The same scoped id through the DEFAULT (nil) resolver resolves
	// nothing, because per-manga ids are never global NativeIndex keys.
	decision = ResolveWithResolver(&groups, &profiles, "atsumaru", "scoped-asura", profileID, nil)
	require.Equal(t, domain.OutcomeUnknown, decision.Outcome)
	require.Equal(t, "", decision.CanonicalID)
}
