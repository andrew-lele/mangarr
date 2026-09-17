package domain

// Registry types for canonical scanlation-group identity.
//
// A scanlation group is identified by an OPAQUE canonical UUID (stable,
// collision-free) plus human-readable aliases. Each group maps to per-source
// NATIVE ids (comix numeric GroupID, atsumaru ScanID, mangadex scanlation
// uuid). Quality profiles reference canonical UUIDs (or aliases, resolved at
// load time), never native ids, so one profile works across every source.

// Fallback policy constants drive what happens when a chapter's group is not
// among the profile's preferred or ignored groups.
const (
	FallbackAny   = "any"   // unknown groups allowed, lowest preference (Sonarr convention)
	FallbackNever = "never" // only preferred/known groups are accepted
)

// Group is a single registry entry tying a canonical UUID to its aliases and
// per-source native ids.
type Group struct {
	ID      string            `yaml:"id"`
	Aliases []string          `yaml:"aliases"`
	Sources map[string]string `yaml:"sources"`
}

// GroupRegistry holds all groups plus reverse indexes for lookup.
//
//   Groups[canonicalId] -> Group
//   AliasIndex[lowercased alias] -> canonicalId
//   NativeIndex["<source>:<nativeId>"] -> canonicalId
type GroupRegistry struct {
	Groups map[string]*Group `yaml:"groups"`

	AliasIndex map[string]string
	NativeIndex map[string]string
}

// Profile is a named quality profile referencing canonical group UUIDs.
type Profile struct {
	ID              string   `yaml:"id"`
	Name            string   `yaml:"name"`
	PreferredGroups []string `yaml:"preferredGroups"`
	IgnoredGroups   []string `yaml:"ignoredGroups"`
	Fallback        string   `yaml:"fallback"`
}

// ProfileRegistry holds all profiles plus an id index.
type ProfileRegistry struct {
	Profiles map[string]*Profile `yaml:"profiles"`
}