# Monitored Wiring + Quality Profile Decisions (DS4)

## Problem

The registry (`groups.yaml` / `profiles.yaml`) and the resolver from
DS1-DS3 exist, but nothing consumes them: monitor never loads the registries
and never passes a quality profile, download has no way to select one, and
dry-run reports carry no group decision. `MonitoredManga.qualityProfile` was
added to the domain in DS2 but was never wired anywhere.

## Scope

- `cmd/monitor.go`: `runMonitorCycle` loads groups.yaml + profiles.yaml each
  cycle via `registry.LoadGroups` / `registry.LoadProfiles` on
  `cfg.ConfigPath` (same shape as download's `requestGroups` /
  `requestProfiles`) and threads `SourceKey = monitoredManga.Source`, the
  registries, and `ProfileRef = monitoredManga.QualityProfile` into
  `acquire.Request`. The dry-run log appends
  `[group=<uuid> decision=<outcome>]` exactly like download.
- `cmd`: `decisionLogSuffix(domain.Decision)` shared by the download and
  monitor dry-run logs (collapses the logic download inlined in the WIP).
- `domain.MonitoredManga.qualityProfile` config key, `download --quality-profile`
  flag (`--series` falls back to the entry's `QualityProfile`), config `dryRun`
  + `download --dry-run`, and `acquire.Request.SourceKey/Groups/Profiles/
  ProfileRef` -> `Result.Decision` (shipped as part of the DS4 WIP, folded into
  this commit).
- Tests: acquire decision data path through the production registry loaders
  (`writeRegistry` mirror of `internal/resolve/resolve_test.go`), monitor
  dry-run end-to-end through `runMonitorCycle` (offline cubari gist), registry
  load failure aborting the cycle, and `decisionLogSuffix` rendering.
- Docs: `docs/USAGE.md` documents `--quality-profile`, the `qualityProfile`
  config key, the `[group=... decision=...]` dry-run suffix, and the
  groups.yaml / profiles.yaml schemas; the generated config template shows the
  optional `qualityProfile` key.

## Decisions

- Registries load once per `runMonitorCycle` (not per manga, not once per
  process): registry edits are picked up each check interval without restarting
  monitor. A load failure aborts the cycle with a logged error and retries next
  interval, matching the existing invalid-download-location pattern.
- `monitorManga` receives the registries as `*domain.GroupRegistry` /
  `*domain.ProfileRegistry` parameters; `runMonitorCycle` passes
  `&requestGroups` / `&requestProfiles` (read-only, shared across the per-manga
  errgroup tasks).
- Empty `qualityProfile` disables resolution (no decision, no suffix): the
  acquire layer only resolves when `Profiles != nil && ProfileRef != ""`.
- `decisionLogSuffix` gates on `Decision.CanonicalID != ""`, so an unregistered
  native id or unset profile omits the suffix (mirrors the download code it
  replaces).
- The monitor decision test surface is split because the only group-carrying
  sources (comix, atsumaru) need live network: the decision DATA path is tested
  at the acquire seam (`TestChapterDryRunResolvesGroupDecision`) and the
  suffix RENDERING at the helper (`TestDecisionSuffix`), while
  `TestMonitorDryRunReportsWouldBeDownload` proves the monitor call sites
  wire `DryRun` + registries + `SourceKey` end-to-end with an offline gist.
- `MANGARR__DRY_RUN` joins the environment override list in USAGE.md.

## Verification

- `go build ./...`
- `go test ./...` (13 packages)
- `go test -race ./...`
- `gofmt -l` on changed files