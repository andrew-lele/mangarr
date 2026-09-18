# Per-source resolver extensions: atsumaru scanlator-name bridge (2026-09-18)

## Problem

Atsumaru group ids are PER-MANGA scoped: `scanlationMangaId` (the chapter's
`Group`, e.g. `cmgzkjxs70k8hm191srm4qxf9`) changes per manga for the same
group NAME ("Asura" on manga A != "Asura" on manga B). The registry's single
`atsumaru: <id>` NativeIndex leaf therefore cannot resolve Atsumaru chapters
to a canonical UUID. The group's human name is the stable key, and the
registry already maps names through AliasIndex.

## Scope

- `internal/domain/registry.go`: `NativeResolver` func type
  (`(groups, nativeGroup) -> canonicalID`), the optional per-source extension
  over the default NativeIndex lookup.
- `internal/resolve/resolve.go`: `ResolveWithResolver` — same body as
  `Resolve` plus an optional `resolver domain.NativeResolver`; nil keeps the
  current `registry.ResolveGroupID(groups, source+":"+nativeGroup)` path.
  `Resolve` stays backward-compatible (delegates with nil) so all existing
  callers and tests are untouched.
- `internal/source/atsumaru.go`: the adapter caches the manga-page scanlators
  it already fetches for `Groups()` (an ordered `Scanlators` list +
  `loadScanlators(ctx)` shared by `Groups` and `Discover`); new
  `ResolveNativeGroup(groups, scopedID)` bridges scopedID -> scanlator NAME ->
  `registry.ResolveGroupID` (AliasIndex). Discover loads the scanlators so the
  bridge is pure and ready at resolve time; a page-fetch failure is
  NON-fatal (downloads keep working; resolution degrades to unresolvable).
- `internal/source/source.go`: `nativeResolvers` registry keyed by source
  name + `NewNativeGroupResolver(sourceKey, s)` — the task's requested
  registration point; returns nil for sources without an extension (comix,
  mangadex...) so they keep the default path.
- `internal/acquire/acquire.go`: `Request.ResolveNativeGroup domain.NativeResolver`
  threaded into `resolve.ResolveWithResolver`; nil by default (all existing
  Request literals compile unchanged).
- `cmd/download.go` + `cmd/monitor.go`: build the resolver from the source
  key + adapter instance.
- Tests: resolve extension seam (custom resolver -> preferred/unknown, nil ==
  default), atsumaru bridge unit (httptest manga-page -> ResolveNativeGroup ->
  canonical via alias "Asura"), full decision through the real resolver,
  source dispatch (atsumaru non-nil, comix nil). Existing atsumaru Discover
  tests' handlers gain an `/api/manga/page` route.
- Docs: `docs/USAGE.md` groups.yaml note (atsumaru resolution is by
  scanlator-name alias, per-manga scan ids are NOT registry keys),
  `docs/design-docs/source-adapters.md` capability row, this plan.

## Decisions

- Per-call resolver param, NOT a global resolver registry: atsumaru's bridge
  is per-INSTANCE (the manga-scoped scanlator cache), so a process-global
  "registered" resolver would leak one manga's data into another's chapters
  under monitor's concurrency. The resolver is threaded from the source
  instance through acquire.Request, and the per-source DISPATCH registry
  lives in `internal/source/source.go` (keyed by source name) exactly like
  `source.Select` / `NewGroupLister`.
- Eager scanlator load in Discover: `Resolve` is pure (no ctx, no network),
  so the bridge data must exist before acquire resolves. Discover already
  runs before every Chapter acquisition in both download and monitor.
- Chapter `Group` stays the scoped ScanID; the resolver bridge does
  ScanID -> NAME -> AliasIndex at decision time.

## Verification

- `go test ./...` and `go test -race ./...` keep all groups green plus the
  new resolve/source tests; `go build ./...`; `gofmt -l .` clean.

## Out of scope / follow-up

- mangadex resolver extension (global UUIDs already work through NativeIndex).
- Registering atsumaru groups INTO groups.yaml automatically (groups CLI
  prints name + per-manga scan id; operators alias by NAME).