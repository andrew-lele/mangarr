# Atsumaru greedy profile-driven group selection (2026-09-18)

## Problem

Atsumaru chapters carry a per-manga scoped ScanID, and NUMBERS can be
translated by several of the manga's scanlators (Asura AND Webtoon both do
ch. 42). Two blockers for profile-driven selection:

1. `Discover` filters chapters to the pinned `a.ScanID`, so chapters from
   other groups never reach acquisition, and `ValidateInput` demands a
   ScanID, blocking profile-only manga (no pinned group).
2. `ResolveNativeGroup` bridges ONE scopedID -> name -> AliasIndex; it
   cannot choose BETWEEN candidate groups by profile preference.

## Scope

- `internal/domain/registry.go`: `NativeResolver` signature becomes
  `func(groups *GroupRegistry, profile *Profile, nativeGroup string) string`
  — resolve already found the profile before invoking the resolver, so the
  resolver receives it directly (no ref re-lookup, resolve.Resolve shape
  stays stable).
- `internal/resolve/resolve.go`: `ResolveWithResolver` passes the found
  `profile` through; nullary default path unchanged.
- `internal/source/atsumaru.go`:
  - struct gains `QualityProfile string`; `NewAtsumaru(mangaURL, scanID,
    qualityProfile string)`; `ValidateInput` allows an empty ScanID when a
    qualityProfile is set; `Discover` includes EVERY scanlator's chapters
    (each `Chapter.Group` = its ScanID) when a qualityProfile is set and
    keeps the pinned-ScanID filter otherwise (back-compat).
  - `ResolveNativeGroup(groups, profile, nativeGroup)` becomes the GREEDY
    matcher: iterate the profile's `PreferredGroups` in order; for each ref
    (alias or UUID) resolve its canonical and return it when any of the
    manga's cached scanlator NAMES resolves to that canonical (earliest
    preferred wins, n <= 10, linear). No preferred match -> "" so Resolve's
    unlisted-group policy applies.
- `internal/source/source.go`: registry passes `m.QualityProfile` into
  `NewAtsumaru`; the atsumaru NativeResolver factory closure forwards the
  new signature.
- Tests: resolve seam (stub greedy honoring profile order via the passed
  profile), atsumaru greedy acceptance ([Asura, Webtoon] + preferred Asura
  -> Asura canonical; Webtoon-first -> Webtoon; absent preferred -> skip /
  unlisted), ValidateInput profile case, Discover unfiltered mode + pinned
  back-compat.
- Docs: USAGE.md (atsumaru quality-profile mode: omit -g when
  qualityProfile is set; chapter numbers translated by several groups
  resolve greedily), source-adapters.md behavior bullet, exec plan.

## Decisions

- Greedy over the MANGA's scanlator list (a.Scanlators), not the chapter's
  single ScanID: the chapter map is keyed by number and the API returns one
  row per (number, scanlator), so a chapter's own ScanID cannot represent
  "all groups for this number". The task-sanctioned approximation: the
  earliest preferred canonical that any manga scanlator name matches wins.
  KNOWN GAP: for a number translated by several groups, the acquisition
  downloads the LAST row the API returned for that number (map overwrite);
  the profile DECISION reflects the greedy pick but does not yet swap which
  row downloads. Follow-up: per-number candidate selection needs the group
  registry at Discover time (larger seam).
- Profile passed by value-of-reference (*domain.Profile): the resolver uses
  only PreferredGroups order; ignoredGroups/unlisted still flow through
  Resolve unchanged.

## Verification

- `go test ./...` and `go test -race ./...` keep all groups green plus the
  new tests; `go build ./...`; `gofmt -l .` clean.
- Live: atsumaru dry-run against real atsu.moe stays green (profile mode).
- comix/mangadex etc. unaffected (nil resolver default).

## Out of scope / follow-up

- Steering the actual downloaded row per duplicated chapter number by the
  profile decision (see Known GAP above).