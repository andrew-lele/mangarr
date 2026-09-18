# Source Adapters

Verified against `internal/source/` and the live Comix source on 2026-08-28.

All adapters implement the small `domain.Source` contract:

- `Discover` returns manga metadata and its chapter map.
- `Pages` returns an ordered page transport description for one chapter.
- adapters return values. They do not mutate caller-owned manga or chapter values.
- `source.Select` is the single registry used by download and monitor commands.

Sources with a scanlation-group model additionally implement the optional
`domain.GroupLister` capability (`Groups(ctx, query)` -> native-id group
listings), consumed by the `groups` CLI command; everything else reports
"does not expose scanlation groups". `source.NewGroupLister` is the
capability's registry (comix + atsumaru today).

Quality-profile resolution also has a per-source extension seam: sources
whose native group ids are GLOBAL (comix numeric GroupID, mangadex UUID)
resolve through the registry's `NativeIndex` (`source:nativeId` key), the
default in `internal/resolve`. Sources whose ids are scoped per-manga
(atsumaru ScanIDs vary per manga for the same group name) register a
`domain.NativeResolver` in `internal/source/source.go`
(`NewNativeGroupResolver`, keyed by source name); the atsumaru resolver
GREEDILY matches the manga's cached scanlator names against the profile's
`preferredGroups` order and returns the earliest preferred canonical (the
resolver receives the found profile from resolve). Atsumaru discovery is
profile-aware: a set `qualityProfile` disables the pinned-ScanID chapter
filter so every scanlator's chapters reach acquisition, and `-g` becomes
optional.

## Matrix

| Identifier | Input (`-m`) | Extra args | Validation highlights | Notes |
| --- | --- | --- | --- | --- |
| `tcbscans` | manga title | none | non-empty title | HTML scraping |
| `mangadex` | manga UUID | `-g` optional, `-l` optional | valid manga UUID | API-based |
| `mangaplus` | numeric title ID | none | strict numeric regex | mobile protobuf API; lazily registers a deterministic device secret |
| `flamecomics` | full series URL | none | `https://flamecomics.xyz` prefix | HTML + embedded JSON |
| `asurascans` | full series URL | none | `https://asurascans.com/comics/...` | server-rendered HTML; filters locked early-access chapters from discovery |
| `cubari` | gist URL | `-g` required | valid URL + non-empty group | images listed in the payload, or fetched from the `/proxy/...` path the gist points at |
| `weebcentral` | full series URL | none | `https://weebcentral.com` prefix | scraper + chapter image fragment fetch |
| `comix` | full manga URL | `-g` optional | `https://comix.to/title/...` prefix; numeric group when set | private API codec + referer-protected images + tile reconstruction; optional `impersonationProxy` relays Cloudflare challenge solving to a FlareSolverr-compatible sidecar (see [USAGE](./../USAGE.md#source-inputs)); group discovery reads `GET /api/v1/groups?keyword=` |
| `atsumaru` | full manga URL | `-g` required | `https://atsu.moe/manga/...` prefix + non-empty scan ID | API-based; group discovery lists the manga's scanlators from `/api/manga/page`; resolvable by scanlator NAME via the resolver extension |

## Rules

- validate early
- keep selectors and parsing local to the adapter
- prefer stable attributes or embedded data over brittle DOM traversal
- reuse `internal/sharedhttp/` before adding new transport helpers
- when an adapter drifts, add a regression test around the parsing seam if practical

## Shared Behavior

- unknown source values fail fast in source selection
- sources without a group model (tcbscans, mangaplus, flamecomics,
  asurascans, cubari, weebcentral, mangadex) fail group listing with
  `source <name> does not expose scanlation groups`
- `mangaplus` uses the mobile API because the web protobuf endpoint rejects current unauthenticated access; chapter discovery reads the current `chapter_list_v2` field with legacy list fields kept as fallback
- `asurascans` removes chapters marked `is_locked=true` or `is_premium=true` before returning the chapter map
- `weebcentral` fetches chapter images from the `/chapters/<id>/images` HTML fragment
- `comix` implements frontend build `35595e3de3c99889c1aa70`; it generates request tokens, decodes encrypted API envelopes, sends image request headers, and reconstructs scrambled tile images
- Comix scramble hashes are opaque routing keys. The two known legacy hashes select explicit seed prefixes; unknown hashes use prefix zero, matching the frontend fallback.
- `atsumaru` fetches chapter metadata from `/api/manga/info`, filters chapters by scan ID (unless a qualityProfile is set, when every scanlator's chapters are kept), and resolves relative page paths from `/api/read/chapter`; resolved page URLs on the `atsu.moe` host are rewritten to the CDN host `cdn.atsu.moe` (same path), mirroring the keiyoushi extension — the origin host serves 410 for `/static/pages/...`; `Discover` also loads `/api/manga/page` scanlators (non-fatal) so per-manga ScanIDs can bridge to canonical groups by name at resolve time
- source-specific image transforms use `domain.ImageProcessor`; the acquisition path owns transport and output while the source adapter owns the transform
- shared request retries use `internal/sharedhttp/`; see the [retry policy and scraper limitations](./runtime-model.md#http-retry-lifecycle)
- Manga Plus request errors omit query strings so registration and device secrets do not enter logs
- HTTP and Colly-backed requests inherit caller cancellation
- fixture-backed parser flows cover every supported source
- manhwa/long-strip handling flows through `selectedManga.IsManhwa`
