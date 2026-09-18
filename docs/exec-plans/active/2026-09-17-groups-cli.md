# Per-source scanlation group search CLI (2026-09-17)

## Problem

The quality-profile feature (groups.yaml / profiles.yaml) keys groups by
per-source NATIVE ids — comix numeric `groupId`, atsumaru `scanId` — but
there is no way to discover those ids from the CLI. Operators must reverse
engineer the comix.to API (or scrape chapter lists) to learn that "Flame
Comics" is comix group `9641`. Add a `groups` command that lists/searches
scanlation groups per source so groups.yaml / profiles.yaml can be authored
from the terminal.

## Scope

- `internal/domain/source.go`: OPTIONAL `GroupLister` capability interface
  (`Groups(ctx, query) -> []ScanlationGroup{ID, Name, Slug}`) on top of
  `domain.Source`, plus `GroupSearchOptions{Source, Manga, ImpersonationProxy}`
  carrying the per-source inputs a lister needs (atsumaru's manga URL,
  comix's relay). Sources without a group model simply don't implement it.
- `internal/source/comix.go`: `comix.GroupGroups` queries the site's group
  catalog `GET /api/v1/groups?keyword=&limit=100` (token-signed + encrypted
  like every other API call, so the codec path and the impersonation relay
  work unchanged). `NewComixGroupLister(impersonationProxy)` reuses
  `NewComix`'s transport/codec setup. Fail-fast on a bad relay BEFORE any
  network call (ImpersonationErr check). `isComixProtectedPath` gains the
  `/groups` + `/groups/...` token paths.
- `internal/source/atsumaru.go`: `atsumaru.Groups` lists the manga's
  scanlators from `GET /api/manga/page?id=<mangaId>` (`mangaPage.scanlators[]`
  -> ScanID + name; per-manga because atsumaru groups ARE the manga's
  translators), filtering client-side by case-insensitive query.
- `internal/source/groups.go`: `NewGroupLister(GroupSearchOptions)` — the
  single dispatch used by the CLI; returns a clear
  `source X does not expose scanlation groups` error for tcbscans/mangaplus/
  flamecomics/asurascans/cubari/weebcentral/mangadex.
- `cmd/groups.go`: `mangarr groups <source> [--search q] [-m URL]
  [--impersonation-proxy origin]`. Positional source (deliverable shape
  `mangarr groups comix --search X`). Prints `ID\tName[\tSlug]` lines to
  stdout; ordering = source order (comix server relevance, atsumaru reader
  priority). A lister-selection miss prints the "does not expose scanlation
  groups" message and exits 0 (a successful "none" answer, scriptable);
  fetch/validation errors still fail nonzero.
- `cmd/root.go`: `dependencies.selectGroupLister` injection + registration.
- Tests: comix groups httptest (token/params/encrypted response), atsumaru
  groups httptest (URL query + query filter), `NewGroupLister` dispatch
  errors, CLI end-to-end with a mocked lister factory.
- Docs: `docs/USAGE.md` (new command section), `docs/design-docs/
  source-adapters.md` (capability + matrix note), this plan.

## Decisions

- One page, `limit=100`: the groups response pagination DTO shape is not
  verified against the live API (comix.to is Cloudflare-walled here); search
  (`keyword`) is the primary flow and one page is enough to discover a named
  group. No pagination walk (unlike `getChapters`) until field-verified.
- `Groups` takes the query string rather than the CLI filtering results:
  each adapter owns search semantics (comix passes `keyword` to the API,
  atsumaru filters locally).
- atsumaru requires `-m`: its groups are per-manga scanlators, so the manga
  URL is a required input for that source.

## Verification

- `go test ./...` and `go test -race ./...` keep the existing groups green
  plus the new ones; `go build ./...`; `gofmt -l .` clean on changed files.
- Live comix group search runs on jihun against the relay
  (`MANGARR_LIVE_SOLVER_URL=http://127.0.0.1:8191`); locally comix.to 403s
  behind Cloudflare, so the httptest suite is the local proof.

## Out of scope / follow-up

- mangadex group listing (MangaDex /group API) — user target is comix +
  atsumaru; adding it later is a fifth adapter method + matcher entry.
- Paginating the full comix group catalog until the live response shape is
  field-verified.
- Auto-inserting discovered groups into groups.yaml (operator copies the
  `ID\tName` line; a `--to-registry` flag could be a follow-up).