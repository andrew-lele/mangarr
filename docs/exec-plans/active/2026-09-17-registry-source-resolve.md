# Registry Source Resolution (DS3)

## Problem

Quality profiles reference canonical group UUIDs, but chapters leaving source
adapters carry only native ids (comix numeric `GroupID`, atsumaru `ScanID`).
There is no download-time path from a chapter's native group id to a profile
decision (preferred / ignored / unknown).

## Scope

- Added `domain.Chapter.Group` (string) so chapters carry source-specific group
  identity up from the adapters.
- Populated the field in the comix adapter (`comixChapter.GroupID`) and the
  atsumaru adapter (`atsumaruChapter.ScanID`). MangaDex is deferred; the
  resolver already works for it since groups.yaml maps `mangadex: <uuid>`
  through the same `source:nativeGroup` key shape.
- Added `internal/resolve` with `resolve.Resolve` (native group + source name +
  GroupRegistry + ProfileRegistry + profile ref -> `domain.Decision`), plus the
  `domain.Decision` type and outcome constants.
- Added resolver unit tests loaded through the production
  `registry.LoadGroups` / `LoadProfiles` pipeline.

## Decisions

- `Resolve` takes a profile reference (id or name); the caller decides which
  profile applies to a monitored manga.
- Native lookup is `source:nativeGroup` via `registry.ResolveGroupID`, so any
  source whose native ids are in groups.yaml works without adapter changes.
- `ignoredGroups` is a hard rejection and wins over a `preferredGroups` overlap
  (`OutcomeIgnored`), mirroring Sonarr negative-score handling.
- `preferredGroups` yields `OutcomePreferred` with the list index (consumers
  rank by it); lower index = higher preference.
- Known-but-unlisted groups follow the profile fallback: `never` ->
  `OutcomeIgnored`, `any` -> `OutcomeUnknown`. Unregistered native ids
  likewise. A missing profile or empty native group -> `OutcomeUnknown`.
- `domain.Decision` lives in `domain` (not `internal/resolve`) so downstream
  consumers (DS4 monitor) depend only on domain.
- No docs/config/CLI surface changed; `domain.Chapter.Group` is additive.
- `gofmt` reflow of DS1/DS2 doc comments in `internal/registry/*` deliberately
  not included in this commit (base-commit formatting, handled on stack
  consolidation).

## Verification

- `go build ./...`
- `go test ./...` (all packages, 13 new resolver tests)
- `go test -race ./...`
- Resolver tests cover: empty registries, unknown profile ref, empty native
  group, preferred by native id / alias / canonical UUID / profile name,
  preference index order, ignored by native id, ignored-over-preferred
  overlap, unregistered native id under both fallbacks, known-unlisted group
  under both fallbacks, and source-specific native keys (atsumaru `scan-nova`
  resolves only under the `atsumaru` source key).
- Adapter tests extended to assert `Chapter.Group` populations for comix and
  atsumaru.
