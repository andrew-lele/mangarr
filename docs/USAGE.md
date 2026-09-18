# Usage Reference

Verified against CLI help and config/runtime code on 2026-08-09.

Use this doc when the root README is not enough and you need command, config, or operator detail.

## Commands

### `download`

Download one chapter, many chapters, or everything available from a supported source.

```bash
mangarr download -d <download-dir> -s <source> -m <identifier> [flags]
```

Common flags:

| Flag | Meaning |
| --- | --- |
| `--series` | exact, case-sensitive entry name from `monitoredManga` in config |
| `-d`, `--downloadDirectory` | directory where `.cbz` files are written |
| `-s`, `--source` | source identifier such as `tcbscans` or `mangadex` |
| `-m`, `--manga` | source-specific manga identifier |
| `-g`, `--group` | group identifier for sources that support it |
| `-l`, `--language` | language code for sources that support it; default `en` |
| `-n`, `--naming` | filename template |
| `-o`, `--overwrite` | replace the parsed manga title in the output filename |
| `-f`, `--force` | re-download selected chapters even if their archives already exist |
| `--dry-run` | report which chapters would be downloaded (name + destination path) without downloading anything |
| `--quality-profile` | select the quality profile (by id or name from profiles.yaml) applied to each chapter's group decision |

Chapter selection flags are mutually exclusive:

- `-L`, `--latest`: latest chapter; default when no selector is set
- `-1`, `--first`: first chapter
- `-C`, `--chapters`: specific chapters or ranges such as `1,3,5` or `1-10`
- `-A`, `--all`: all available chapters

Existing archives are skipped unless `-f` / `--force` is supplied. Force works with
any chapter selector above. It replaces only the output paths calculated for
those chapters; it does not search the library for old files. To repair an
archive, use the same download directory, manga title, and naming template that
produced its path. A changed title or template can produce a new file instead.
The `--overwrite` flag changes the manga title used for the output directory and
filename; it does not force a download. Monitor mode has no force option and
continues to skip existing archives.

Dry-run applies to both `download` (`--dry-run`) and monitor mode (`dryRun: true`
in config.yaml, or the `MANGARR__DRY_RUN` env var): each selected chapter is
resolved through the normal pipeline up to the output archive name and path, and
reported instead of downloaded. No pages are fetched and no files or directories
are created. When a quality profile is set and the chapter's scanlation group is
registered in `groups.yaml`, the report appends `[group=<uuid> decision=<outcome>]`
with the profile's verdict (`preferred`, `ignored`, or `unknown`) for that group.

Each old archive remains in place until the replacement is downloaded and
assembled successfully. If replacement fails, or cancellation is detected before
publication, the old archive is preserved. Cancellation after the final check
can still allow publication. See the [runtime model](./design-docs/runtime-model.md#state)
for the publication sequence.

The command returns status 0 when all requested chapters are downloaded successfully or skipped. It returns a nonzero status when setup, discovery, selection, or any requested chapter download fails.

#### Download a configured series

Reuse an existing `monitoredManga` entry without repeating its source, URL/ID, or group:

```bash
mangarr download -c ~/.config/mangarr --series "One Piece" -C "1-3"
```

Quote names containing spaces. `--series` matches the config key, not the provider's
manga title. It uses the same config discovery, environment overrides, and full
config validation as `monitor` (including a nonempty `downloadLocation`). It reads
one snapshot without starting monitoring, watching, or rewriting the config.
The config file must already exist. A missing selected config causes an error
without creating a sample config, even when `MANGARR__DOWNLOAD_LOCATION` is set.

The entry supplies `source`, `manga`, `group`, `language`, `overwrite`, and
`qualityProfile`; global `downloadLocation` and `namingTemplate` supply the output
settings. Omitted entry language defaults to `en`; an omitted or empty
`qualityProfile` disables group decisions for the series. Explicit download flags
override these values, including explicit empty `--group`, `--overwrite`, or
`--quality-profile` to clear a configured value. Precedence is
explicit flags, then environment overrides, then YAML, then built-in defaults.
Config validation occurs before flag overrides, so the config itself must be valid.
Chapter selection always comes from the CLI and still defaults to latest.

Unknown entries, missing effective source/manga, and invalid source inputs fail
before provider discovery. A required entry field can be supplied by its CLI flag.
Without `--series`, `-d`, `-s`, and `-m` remain required and no config is loaded.
Monitor-only logging, profiling, and scheduling settings do not change download behavior.

Examples:

```bash
# Configured series with a one-off output directory
mangarr download -c ~/.config/mangarr --series "One Piece" -C "1-3" -d ./downloads

# Latest chapter from TCB Scans
mangarr download -d ./downloads -s tcbscans -m "One Piece"

# First English chapter from MangaDex for one group
mangarr download -d ./downloads -s mangadex -m "801513ba-a712-498c-8f57-cae55b38cc92" -g "277df5c9-a486-40f6-8dfa-c086c6b60935" -l en -1

# Specific chapters from MANGA Plus
mangarr download -d ./downloads -s mangaplus -m "100037" -C "6,17"

# Repair selected chapters already on disk
mangarr download -d ./downloads -s tcbscans -m "One Piece" -C "6,17" --force

# Chapter range from Cubari
mangarr download -d ./downloads -s cubari -m "https://git.io/OPM" -g "/r/OnePunchMan" -C "1-3"

# Latest chapter from Asura Scans
mangarr download -d ./downloads -s asurascans -m "https://asurascans.com/comics/solo-max-level-newbie-7f873ca6" -L

# Latest chapter from Atsumaru
mangarr download -d ./downloads -s atsumaru -m "https://atsu.moe/manga/Q5Mqy" -g "cmgzlsevifjhtm191rqugvee3" -L
```

#### Bulk downloads and rate limits

If a bulk download fails because a source is busy or temporarily unavailable,
wait and rerun the command without `--force` to fetch missing chapters. See the
[existing-archive rules](#download) for skip and replacement behavior.

Sources can limit traffic or become temporarily unavailable. Rate-limit handling
varies by source and request type, so a persistent limit or a failure during
source discovery may still require waiting before you try again. A chapter
download failure reports the affected chapter; partial chapters are not published.

### `monitor`

Run mangarr as a long-lived watcher that checks configured series on an interval.

```bash
mangarr monitor -c <config-dir>
```

Minimal config:

```yaml
downloadLocation: "/path/to/downloads"
checkInterval: 15

monitoredManga:
  One Piece:
    source: "tcbscans"
    manga: "One Piece"
```

Expanded config:

```yaml
downloadLocation: "/path/to/downloads"
namingTemplate: "{manga:<.>} Ch. {num:3}{title: - <.>}"
checkInterval: 15
pprofEnabled: false
pprofAddress: "127.0.0.1:6060"

monitoredManga:
  One Piece:
    source: "tcbscans"
    manga: "One Piece"

  Isekai Ojisan:
    source: "mangadex"
    manga: "d8f1d7da-8bb1-407b-8be3-10ac2894d3c6"
    group: "310361d7-52dd-4848-9b36-2eb4fcc95e83"
    language: "en"
    overwrite: "Uncle from Another World"
    qualityProfile: "Preferred Scanlators"

  Kagurabachi:
    source: "mangaplus"
    manga: "100274"

logLevel: "DEBUG"
#logPath: "/path/to/logs/mangarr.log"
#logMaxSize: 50
#logMaxBackups: 3
```

Config lookup order:

`qualityProfile` references a profile in `profiles.yaml` (by id or name) and
applies its `preferredGroups` / `ignoredGroups` / `fallback` policy to each
chapter's scanlation group as it downloads. Both `groups.yaml` and
`profiles.yaml` live next to `config.yaml` and are optional; without them, or
with an empty `qualityProfile`, no group decisions are made and dry-run reports
carry no `[group=...]` suffix.

```yaml
# groups.yaml: canonical group UUIDs with aliases and per-source native ids
version: 1
groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "CP" ]
    sources:
      comix: "9641"

# profiles.yaml: the quality profile referenced by qualityProfile
version: 1
profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Preferred Scanlators"
    preferredGroups: [ "CP" ]
    ignoredGroups: [ "b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c" ]
    fallback: "any" # "any" or "never"
```

Atsumaru is the exception to the per-source native-id model: its scan ids
are scoped PER-MANGA (the same group has a different id on every manga), so
`atsumaru: <id>` entries in `sources` cannot match chapters. Mangarr instead
resolves Atsumaru chapters by scanlator NAME: `mangarr groups atsumaru -m
<manga-url>` shows the names, and the group's chapter resolves whenever that
name is registered as an **alias** of a canonical group (case-insensitive):

```yaml
version: 1
groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "Asura" ]
```

Config lookup order:

1. If `-c` is set, mangarr reads `<config-dir>/config.yaml`.
2. `<user-config-dir>/mangarr/config.yaml` (for example,
   `~/.config/mangarr/config.yaml` on Linux).
3. `~/.mangarr/config.yaml`
4. `config.yaml` next to the binary

The current working directory is not an implicit config location. Use `-c .` to
load `./config.yaml` explicitly.

Environment overrides use the `MANGARR__` prefix:

- `MANGARR__DOWNLOAD_LOCATION`
- `MANGARR__NAMING_TEMPLATE`
- `MANGARR__CHECK_INTERVAL`
- `MANGARR__DRY_RUN`
- `MANGARR__PPROF_ENABLED`
- `MANGARR__PPROF_ADDRESS`
- `MANGARR__LOG_LEVEL`
- `MANGARR__LOG_PATH`
- `MANGARR__LOG_MAX_SIZE`
- `MANGARR__LOG_MAX_BACKUPS`

Monitor checks all configured manga once at startup. It then waits for the check
interval. Changes to monitored manga, naming, download location, check interval,
and log level reload while monitor runs. Environment overrides are reapplied to
each reload. Atomic replacement and delete-then-recreate saves remain watched.
An invalid, incomplete, or temporarily missing config keeps the last valid
settings active. Changes to pprof and log file output settings require a restart.

### `groups`

List or search scanlation groups for a source so you can build
`groups.yaml` / `profiles.yaml` native-id mappings from the terminal.

```bash
mangarr groups <source> [--search <query>] [flags]
```

| Flag | Meaning |
| --- | --- |
| `--search` | list groups whose name contains the query (case-insensitive); an empty query lists the source's first page of groups |
| `-m`, `--manga` | atsumaru: manga URL whose scanlation groups are listed (required for atsumaru) |
| `--impersonation-proxy` | comix: FlareSolverr-compatible relay origin that solves comix.to's Cloudflare challenge |

Each group prints as `ID\tName` (plus `\tSlug` for comix) — exactly the
native id (comix numeric group ID, atsumaru scan ID) you put in a
`groups.yaml` `sources` entry and the human name you might use as an
alias:

```bash
$ mangarr groups comix --search "flame" --impersonation-proxy http://127.0.0.1:8191
9641	Flame Comics	flame-comics
$ mangarr groups atsumaru -m https://atsu.moe/manga/Q5Mqy
cmgzlsevifjhtm191rqugvee3	Alpha
cmo61dqz80002zymjeo13wbw5	Delta
```

Group discovery per source:

- **comix**: queries the site's group catalog (`GET /api/v1/groups`) with
the search term as `keyword`. It uses the same request-token codec and
Cloudflare impersonation transport as chapter discovery, so `--impersonation-proxy`
must be set when comix.to answers direct requests with a challenge.
- **atsumaru**: lists the manga's scanlators (`/api/manga/page`), because
Atsumaru groups ARE the translators of a specific manga; `-m` is required
and the scan ids returned are the `-g` values the manga's chapters carry.
- Sources without a scanlation-group model (`tcbscans`, `mangaplus`,
`flamecomics`, `asurascans`, `cubari`, `weebcentral`, `mangadex`) print
`source <name> does not expose scanlation groups` and exit 0, so scripts
can iterate over sources.

### `version`

Show local build/version info and attempt an advisory update check. Local version
output succeeds when GitHub is unavailable.

```bash
mangarr version
```

## Source Inputs

| Source | `-s` value | Pass to `-m` | Extra input |
| --- | --- | --- | --- |
| [TCB Scans](https://tcbonepiecechapters.com/) | `tcbscans` | exact manga title | none |
| [MangaDex](https://mangadex.org/) | `mangadex` | manga UUID from the title URL | optional `-g` group UUID, optional `-l` language |
| [MANGA Plus](https://mangaplus.shueisha.co.jp/) | `mangaplus` | numeric title ID | none |
| [Flame Comics](https://flamecomics.xyz/) | `flamecomics` | full series URL | none |
| [Asura Scans](https://asurascans.com/) | `asurascans` | current `https://asurascans.com/comics/...` series URL | locked early-access chapters are skipped until public |
| [Cubari](https://cubari.moe/) | `cubari` | gist URL | required `-g` group such as `/r/OnePunchMan` |
| [Weeb Central](https://weebcentral.com/) | `weebcentral` | full series URL | none |
| [Comix](https://comix.to/) | `comix` | full `https://comix.to/title/...` URL | optional `-g` numeric group ID |
| [Atsumaru](https://atsu.moe/) | `atsumaru` | full `https://atsu.moe/manga/...` URL | required `-g` scan ID |

Comix depends on the provider's current frontend. A Comix frontend update can require a Mangarr update. Use `-g` when a title has duplicate chapter numbers from different groups.

Comix sits behind a Cloudflare challenge. When direct requests return 403 (the
`cf-mitigated: challenge` case), set the entry's `impersonationProxy` to a
local [FlareSolverr](https://github.com/FlareSolverr/FlareSolverr)-compatible
relay running on the same host as Mangarr (the challenge cookies are bound to
the IP that solved them):

```yaml
monitoredManga:
  One Piece:
    source: "comix"
    manga: "https://comix.to/title/106213-one-piece"
    group: "9641"
    impersonationProxy: "http://127.0.0.1:8191"
```

With `impersonationProxy` set, Mangarr asks the relay to solve Comix's
challenge once (the relay runs the site's JavaScript in a real browser),
keeps the minted clearance cookies, and then issues the API request directly
from the same egress IP with the relay-echoed browser User-Agent (the
clearance cookie is bound to the browser fingerprint that solved it, so a
different UA gets a 403; relays that do not echo a UA keep Mangarr's
Chrome-shaped fallback). The relay is contacted again when the cookies age
out (default 10 minutes) or the origin answers with a fresh challenge.
`impersonationProxy` only affects the Comix source and must be a bare
`http(s)://host[:port]` origin.

The relay is not bundled with Mangarr and no Chromium is required by the
Mangarr image itself. Deploy FlareSolverr (or a compatible fork) separately,
e.g. as a systemd service on the same host, and keep it on the same network
path as Mangarr.

## Naming Templates

Available variables:

| Variable | Meaning | Example |
| --- | --- | --- |
| `{manga:<.>}` | manga title | `One Piece` |
| `{num:3}` | chapter number, padded to 3 digits | `001` |
| `{num}` | chapter number, unpadded | `1` |
| `{title: - <.>}` | chapter title with a prefix only when a title exists | ` - Romance Dawn` |

Examples:

- `{manga:<.>} Ch. {num:3}{title: - <.>}` -> `One Piece Ch. 001 - Romance Dawn`
- `{manga:<.>} - {num:3}` -> `One Piece - 001`
- `{manga:<.>} Chapter {num}` -> `One Piece Chapter 1`

## Docker Compose

Current sources use direct HTTP and HTML extraction. The published image does not require Chromium.

```yaml
services:
  mangarr:
    container_name: mangarr
    image: ghcr.io/nuxencs/mangarr:latest
    restart: unless-stopped
    user: ${PUID}:${PGID}
    environment:
      - MANGARR__DOWNLOAD_LOCATION=/downloads
      - MANGARR__CHECK_INTERVAL=15
      - MANGARR__LOG_LEVEL=INFO
    volumes:
      - ./config:/config
      - ./downloads:/downloads
```

Start it:

```bash
docker compose up -d
```

## Advanced Ops

Enable profiling:

```yaml
pprofEnabled: true
pprofAddress: "127.0.0.1:6060"
```

Then:

```bash
mangarr monitor -c ./config
curl http://127.0.0.1:6060/debug/pprof/
go tool pprof http://127.0.0.1:6060/debug/pprof/profile?seconds=30
```

## Build From Source

Requirements:

- Go 1.27.0 or later

```bash
git clone https://github.com/nuxencs/mangarr.git
cd mangarr
go build -o mangarr
```
