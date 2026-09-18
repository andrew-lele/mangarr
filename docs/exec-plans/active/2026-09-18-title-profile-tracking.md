# Title + qualityProfile tracked entries with cross-source resolution (2026-09-18)

## Problem

monitoredManga entries are source-pinned (source + URL from the Kahon
migration). Radicle issue 4cf207b flips the supported form to
title + qualityProfile only: mangarr searches the SOURCES implied by the
profile's preferred-group per-source mappings, finds the title, and
downloads only chapters whose scanlation group passes the profile
(*arr-style). Source-pinned entries stay backward-compatible.

## Scope

- `internal/domain/source.go`: `SearchResult` + `Searcher` (optional
  capability: Search(ctx, title) -> candidate series).
- `internal/source/search.go`: `NewSearcher(sourceKey)` dispatch — atsumaru
  (`/collections/manga/documents/search`, keiyoushi filter set), weebcentral
  (`/search/data` HTML, href dedupe), mangadex (`GET api.mangadex.org/manga?
  title=`); the rest answer "does not support title search" and callers skip.
  Searchers carry a baseURL so they are fixture-testable.
- `internal/registry/profile.go`: `ProfileScanSources` (STRICT preferred-
  groups-only union, preferred order), `ScannableSources` (intersected with
  mangarr source keys), `ValidateMonitoredEntry(s|Entries)` — title+profile
  entries must resolve >= 1 supported scan source; source-pinned entries
  (including partial ones) pass through to the legacy path.
- `internal/resolve/resolve.go`: `BestCandidate(profile, candidates)` —
  earliest preferredGroup index wins; ignored = hard reject; unknown only
  under fallback any (minimum-score-0); deterministic source order on ties.
  `findProfile` exported as FindProfile for the merge.
- `internal/source/source.go`: `Keys()` for the supported-source set.
- `cmd/profilemanga.go`: `trackTitle` (search every scanned source -> pick
  exact-title-match-else-first -> Discover; soft skips per source),
  `bestPerNumber` (resolve each (source, chapter) with the per-source
  NativeResolver, pick BestCandidate), `pickSeries`, `logTrackSkips`.
- `cmd/monitor.go`: title+profile entries run the profile pipeline (latest
  winner acquired); cycle validates entries (log+skip invalid, no hard
  abort); `reportAcquisition` shared with the legacy path.
- `cmd/download.go` + `download_config.go`: `download -c --series` on a
  title+profile entry uses the same pipeline with normal chapter selectors;
  the source/manga-required validation is relaxed when a qualityProfile is
  set.
- Tests: BestCandidate (preferred-over-unknown incl. earliest-index, fallback
  any vs never, ignored hard reject), registry strict scan set + validation
  (legacy pass-through, unknown profile, no-scan-sources), NewSearcher
  dispatch + three search fixtures.
- Docs: USAGE.md "Title-based tracked entries" (incl. STRICT + fallback
  semantics), monitor expanded config, template + sample config; source-
  adapters.md search capability.

## Decisions

- STRICT scan set: only sources listed by the profile's PREFERRED groups are
  searched (ignored-group or unmapped sources never scanned) — fallback:any
  cannot pull in off-profile sources.
- Exact-title match else first search hit (Sonarr first-hit convention).
- Squad per-entry validation in the monitor cycle (log+skip, live registry
  reload stays per-cycle); download --series fails hard via the same helper.
- atsumaru chapters still resolve via the greedy scanlator-name bridge; the
  cross-source merge picks BETWEEN sources by profile preference.

## Verification

- `go test ./...` 13/13 groups green + new tests; -race + build + gofmt.
- Live (this host, real atsu.moe): "The Knight Only Lives Today" title-only
  entry -> strict set {atsumaru, comix, weebcentral}; comix skipped
  (no search); atsumaru matched -> `[source=atsumaru group=Asura
  decision=preferred]`; Kagurabachi (Alpha/Delta only) -> no preferred match
  -> `[source=atsumaru]` downloaded under fallback any. weebcentral skips
  from this host (CF 403) without crashing.

## Out of scope / follow-up

- Search endpoints for asurascans/mangaplus/flamecomics/comix/cubari
  (currently skipped; add adapters when profiles need them).
- mangadex URL-based search result matching (native UUID used directly).
- per-source search ranking beyond exact-title-else-first.