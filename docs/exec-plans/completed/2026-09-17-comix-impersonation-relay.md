# Comix Cloudflare challenge: impersonation relay transport (2026-09-17)

Status: implemented. Relay end verified on jihun (2026-09-17): running FlareSolverr 3.5.0 (podman, 127.0.0.1:8191) solves comix.to's challenge in ~1.1s and mints a long-lived cf_clearance (.comix.to, expiry 2027); POST /v1 response matches the DTOs the transport was built against (solution.cookies carries the cookies; headers is omitted; userAgent echoed). Mangarr end pending deployment: run colmena apply on jihun, then monitor/download a comix entry with impersonationProxy set, or the live test MANGARR_LIVE_SOLVER_URL=http://127.0.0.1:8191.

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
- Adopting the relay's echoed `solution.userAgent` instead of the hardcoded
  Chrome UA (one-line change once field-verified).
- mangafire.to stays research-only (interactive Turnstile: no relay can
  solve it reliably today).