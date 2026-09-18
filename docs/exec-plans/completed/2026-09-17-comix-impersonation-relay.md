# Comix Cloudflare challenge: impersonation relay transport (2026-09-17)

Status: implemented. **Field result (2026-09-18): comix.to escalated to the interactive managed-challenge tier while this was in flight.** Evidence from jihun: (1) API path returns `{"error":"captcha_required","challenge":"/@waf/challenge"}` to every non-browser client AND to FlareSolverr's own Chrome (fresh driver or with the minted pre-clearance cookie) — identical signature to mangafire.to; (2) root solve: FlareSolverr's browser parks on the "Security check" page (`__CF$cv$params` JS, never transitions); it reports "Challenge not detected!" because "Security check" is not in its CHALLENGE_TITLES; (3) cookie replay from non-Chrome TLS (OpenSSL curl, Chrome/153 UA): root → 302 challenge, API → 403 captcha_required — consistent with Cloudflare's clearance docs (cf_clearance validated against JA4 + HTTP/2 settings + IP).

Consequence: no automatic client passes comix.to today; it is in the same unresolved class as mangafire (interactive Turnstile/managed challenge). The relay transport stays (opt-in, protocol-validated, harmless; comix has fluctuated and other classic-challenge zones exist). Response-fallback mode (consume solution.response + envelope sniff) is the known-working pattern (Suwayomi flareSolverrAsResponseFallback) but is NOT worth building while the relay cannot clear the zone. Talk to the user before further work; a paid solver or semi-auto Turnstile clicking (Byparr-class) are the only realistic unblock options.

# Comix Cloudflare challenge: impersonation relay transport (2026-09-17)

## Problem

`comix.to` sits behind a Cloudflare managed challenge. From jihun
(residential IP), a browser-UA `curl` gets `302 -> /@waf/challenge` and a
plain request gets `403 cf-mitigated: challenge`; Mangarr's Go-TLS client
always gets `403`. TLS-byte impersonation alone cannot pass this class of
challenge (the zone requires executing the site's JavaScript; no Turnstile
iframe is served, so no paid captcha is needed). The pragmatic fix is the
staged plan's "FlareSolverr as a sidecar on jihun" step: a local
FlareSolverr-compatible relay solves the challenge with a real browser and
mints the zone's clearance cookie; Mangarr then re-issues the origin request
**directly** (same egress IP) with that cookie.

Design constraint discovered while researching the relay protocol
(FlareSolverr v3 DTOs): `solution.headers` is always empty and
`solution.response` is `driver.page_source` (status is always 200). A
pass-everything-through-the-relay design would therefore lose the `x-enc`
response header Comix uses to announce encrypted API envelopes, plus all
Mangarr request headers. Solving for cookies and then issuing the origin
request from Mangarr's own stack keeps every Comix-specific header/token
intact.

## Scope

- `internal/sharedhttp/impersonate.go`: opt-in `ImpersonatingTransport`
  (`http.RoundTripper`) that
  1. routes only configured target hosts (`comix.to`) through the relay;
  2. mints a clearance cookie via `POST {relay}/v1` `cmd=request.get` on the
     target host root (`{scheme}://{host}/`), forwarding any cached cookies;
  3. caches `cf_clearance`-style cookies per host for a TTL (default 10 min)
     and injects them into the outgoing request (`Cookie` header), letting
     the caller's cookie jar stack naturally;
  4. detects challenge-indicating responses (403/302 + `Location: /@waf/`,
     or `cf-mitigated: challenge`) and re-solves once before replaying.
  Non-target hosts pass through unchanged. The relay client uses its own
  plain local transport (no proxy, no jar) so relay traffic never loops.
- `internal/source/comix.go`: `NewComix(mangaURL, groupID, impersonationProxy)`;
  when set, `get()` sends a Chrome-shaped User-Agent (matching the browser
  profile that minted the cookie) and the source client uses
  `ImpersonatingTransport`. Invalid/malicious relay URLs fail fast in
  `ValidateInput` (loopback-friendly, must not point at `comix.to` itself).
- `internal/domain/config.go`: `MonitoredManga.impersonationProxy` key +
  config template. Config-only toggle (no CLI flag in this change).
- Tests: `internal/sharedhttp/impersonate_test.go` (httptest fake relay +
  fake origin), `internal/source/comix_test.go` additions,
  `internal/source/comix_live_test.go` (env-gated: `MANGARR_LIVE_SOLVER_URL`;
  skips everywhere except a jihun field run).
- Docs: `docs/USAGE.md`, `docs/design-docs/source-adapters.md`, `config.yaml`.

## Verification

- `go test ./...` and `go test -race ./...` keep all 13 groups green.
- `gofmt -l .` clean on changed files.
- Live-comix check happens on jihun against a deployed relay (field run,
  not part of the local gate): `MANGARR_LIVE_SOLVER_URL=http://127.0.0.1:8191`.

## Out of scope / follow-up

- Image downloads use `internal/download`'s own shared transport; if the
  image origin (comix.to/images) also enforces the challenge, a follow-up can
  reuse this transport for the image path (same clearance cookie TTL).
- ~~Adopting the relay's echoed `solution.userAgent` instead of the hardcoded~~
  ~~Chrome UA (one-line change once field-verified).~~ LANDED 2026-09-17
  (`userAgent` in `relaySession`, `applySessionCookies` adopts it when the
  relay echoes one; absent UA keeps the hardcoded fallback).
- mangafire.to stays research-only (interactive Turnstile: no relay can
  solve it reliably today).