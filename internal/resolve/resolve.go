// Package resolve maps a chapter's native scanlation-group id to a canonical
// group UUID and, from there, to a quality-profile decision at download time.
package resolve

import (
	"strings"

	"mangarr/internal/domain"
	"mangarr/internal/registry"
)

// Resolve returns the quality-profile decision for a chapter's scanlation
// group using the default native-id resolver (NativeIndex lookup).
//
// source is the lowercase source key used in groups.yaml (e.g. "comix"), and
// nativeGroup is the group id the source attached to the chapter (comix
// numeric GroupID, atsumaru ScanID). profileRef selects a profile from
// profiles by id or name.
//
// The native id is looked up in the group registry as "source:nativeGroup",
// so any source whose native ids appear in groups.yaml works unchanged
// (including future mangadex scanlation UUIDs). A chapter's group that is in
// the profile's ignoredGroups yields OutcomeIgnored; an ignoredGroups match is
// a hard rejection that wins over a preferredGroups overlap. Otherwise a
// preferredGroups match yields OutcomePreferred with the list index. Groups
// that match neither list fall back to the profile's fallback policy:
// FallbackNever rejects them as OutcomeIgnored, FallbackAny leaves them
// OutcomeUnknown.
func Resolve(groups *domain.GroupRegistry, profiles *domain.ProfileRegistry, source, nativeGroup, profileRef string) domain.Decision {
	return ResolveWithResolver(groups, profiles, source, nativeGroup, profileRef, nil)
}

// ResolveWithResolver extends Resolve with an optional per-source native-id
// resolver (domain.NativeResolver). A nil resolver keeps the default
// "source:nativeGroup" NativeIndex lookup; a non-nil resolver replaces the
// native-id -> canonical step for sources whose native ids are scoped
// per-manga (atsumaru: the source adapter bridges the chapter's ScanID to the
// stable scanlator name, then maps the name through the registry's AliasIndex).
func ResolveWithResolver(groups *domain.GroupRegistry, profiles *domain.ProfileRegistry, source, nativeGroup, profileRef string, resolver domain.NativeResolver) domain.Decision {
	decision := domain.Decision{Outcome: domain.OutcomeUnknown, PreferredIndex: -1}

	profile := findProfile(profiles, profileRef)
	if profile == nil {
		return decision
	}

	source = strings.TrimSpace(source)
	nativeGroup = strings.TrimSpace(nativeGroup)
	if source == "" || nativeGroup == "" {
		return decision
	}

	canonicalID := ""
	if resolver != nil {
		canonicalID = resolver(groups, nativeGroup)
	} else {
		canonicalID = registry.ResolveGroupID(groups, source+":"+nativeGroup)
	}
	decision.CanonicalID = canonicalID
	decision.GroupName = groupName(groups, canonicalID)

	if canonicalID == "" {
		decision.Outcome = outcomeForUnlisted(profile)
		return decision
	}

	// ignoredGroups is a hard rejection and wins over preferredGroups when a
	// group is listed in both (mirrors Sonarr's negative-score handling).
	if containsResolved(profile.IgnoredGroups, groups, canonicalID) {
		decision.Outcome = domain.OutcomeIgnored
		return decision
	}

	if index, ok := indexOfResolved(profile.PreferredGroups, groups, canonicalID); ok {
		decision.Outcome = domain.OutcomePreferred
		decision.PreferredIndex = index
		return decision
	}

	decision.Outcome = outcomeForUnlisted(profile)
	return decision
}

// findProfile returns the profile referenced by id or name, or nil when no
// profile matches.
func findProfile(profiles *domain.ProfileRegistry, ref string) *domain.Profile {
	for id, profile := range profiles.Profiles {
		if id == ref || (profile != nil && profile.Name == ref) {
			return profile
		}
	}
	return nil
}

// indexOfResolved returns the index of canonicalID within refs, where each
// ref is resolved through the group registry (so preferredGroups may list
// canonical ids or aliases), reporting whether it was found.
func indexOfResolved(refs []string, groups *domain.GroupRegistry, canonicalID string) (int, bool) {
	for i, ref := range refs {
		if registry.ResolveGroupID(groups, ref) == canonicalID {
			return i, true
		}
	}
	return 0, false
}

// containsResolved reports whether any ref in refs resolves to canonicalID.
func containsResolved(refs []string, groups *domain.GroupRegistry, canonicalID string) bool {
	_, ok := indexOfResolved(refs, groups, canonicalID)
	return ok
}

// outcomeForUnlisted maps the profile's fallback policy to the decision for a
// group that is not among the profile's preferred or ignored groups.
func outcomeForUnlisted(profile *domain.Profile) string {
	if profile.Fallback == domain.FallbackNever {
		return domain.OutcomeIgnored
	}
	return domain.OutcomeUnknown
}

// groupName returns the first human alias of the group with the given
// canonical id, or "" when the group is not in the registry.
func groupName(groups *domain.GroupRegistry, canonicalID string) string {
	group, ok := groups.Groups[canonicalID]
	if !ok || group == nil || len(group.Aliases) == 0 {
		return ""
	}
	return group.Aliases[0]
}
