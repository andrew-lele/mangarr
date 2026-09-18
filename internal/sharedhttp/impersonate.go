package sharedhttp

// ImpersonatingTransport routes requests for Cloudflare-protected hosts
// through a FlareSolverr-compatible relay ("impersonation relay"), then
// issues the actual request directly from this process.
//
// The relay runs the site's JavaScript challenge with a real browser and
// returns the cookies the zone grants after the solve (POST /v1,
// cmd=request.get; see https://github.com/FlareSolverr/FlareSolverr). The
// cookies are valid only for the IP that solved the challenge, so the relay
// and this process must share egress (Mangarr and the relay run on the same
// host). We re-issue the origin request ourselves rather than returning the
// relay's rendered page: the origin response headers (for example Comix's
// x-enc envelope flag) are preserved, and Mangarr keeps full control of
// request headers and tokens. The transport never sends non-target hosts
// through the relay.
//
// Only the Cookie header is ever mutated on the outgoing request, and only
// for target hosts with a freshly minted clearance. RoundTrip is safe for
// concurrent use; at most one solve per host runs at a time and concurrent
// callers reuse the in-flight result once stored.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// ImpersonationCookieTTL bounds how long a minted clearance cookie is
	// reused before the transport re-solves. Cloudflare clearance cookies
	// are short-lived; the transport re-solves on demand when the origin
	// responds with a fresh challenge regardless of this cap.
	ImpersonationCookieTTL time.Duration = 10 * time.Minute

	// ImpersonationMaxSolveTime is the budget granted to the relay for one
	// challenge solve, forwarded as the relay's maxTimeout (milliseconds).
	ImpersonationMaxSolveTime time.Duration = 60 * time.Second
)

type ImpersonatingTransport struct {
	inner       http.RoundTripper
	relay       *url.URL
	relayClient *http.Client
	targetHosts map[string]bool
	maxTimeout  time.Duration
	cookieTTL   time.Duration
	mu          sync.Mutex
	sessions    map[string]relaySession
}

// relaySession is the set of cookies a solve minted for one host, the time
// they were obtained, and the browser User-Agent the relay used to solve the
// challenge (if the relay echoes one). The clearance cookie is bound to that
// UA, so the direct request must present it or Cloudflare rejects the cookie.
type relaySession struct {
	cookies   map[string]string
	minted    time.Time
	userAgent string
}

// relayEnvelope is the subset of the FlareSolverr /v1 response DTO Mangarr
// consumes. solution.response is page_source (headers and the status always
// 200) and is intentionally ignored: the origin request is re-issued
// directly by RoundTrip.
type relayEnvelope struct {
	Status   string         `json:"status"`
	Message  string         `json:"message"`
	Solution *relaySolution `json:"solution"`
}

type relaySolution struct {
	Cookies   []relayCookie `json:"cookies"`
	UserAgent string        `json:"userAgent"`
}

type relayCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func NewImpersonatingTransport(inner http.RoundTripper, relay *url.URL, targetHosts []string) *ImpersonatingTransport {
	t := &ImpersonatingTransport{
		inner:       inner,
		relay:       relay,
		targetHosts: make(map[string]bool, len(targetHosts)),
		sessions:    make(map[string]relaySession),
		maxTimeout:  ImpersonationMaxSolveTime,
		cookieTTL:   ImpersonationCookieTTL,
	}
	for _, host := range targetHosts {
		t.targetHosts[host] = true
	}
	// The relay client dials the relay directly (never through the relay
	// transport or the environment proxy) and carries no cookie jar.
	timeout := t.maxTimeout + 30*time.Second
	t.relayClient = &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:       (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true,
		},
	}
	return t
}

func (t *ImpersonatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.targetHosts[req.URL.Hostname()] {
		return t.inner.RoundTrip(req)
	}

	if err := t.ensureClearance(req); err != nil {
		return nil, err
	}

	return t.roundTripDirect(req, true)
}

// ensureClearance mints a session for the request's host when none is fresh.
// Concurrency: at most one solve per host runs; callers that lose the race
// reuse the winner's session.
func (t *ImpersonatingTransport) ensureClearance(req *http.Request) error {
	host := req.URL.Hostname()
	if t.sessionFresh(host) {
		return nil
	}

	t.mu.Lock()
	if session, ok := t.sessions[host]; ok && sessionFreshAt(session, t.cookieTTL) {
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()

	session, err := t.solve(req)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.sessions[host] = session
	t.mu.Unlock()

	return nil
}

// roundTripDirect issues the request through the inner transport with the
// minted clearance cookies applied. When the origin answers with a fresh
// challenge (stale cookie), the session is dropped and re-minted once before
// the request is replayed.
func (t *ImpersonatingTransport) roundTripDirect(req *http.Request, allowResolve bool) (*http.Response, error) {
	session, fresh := t.sessionFor(req.URL.Hostname())
	if !fresh {
		return nil, fmt.Errorf("no Cloudflare clearance for %s", req.URL.Hostname())
	}
	applySessionCookies(req, session)

	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if !allowResolve || !challengeIndicating(*resp) {
		return resp, nil
	}

	_ = resp.Body.Close()
	t.dropSession(req.URL.Hostname())
	if err := t.ensureClearance(req); err != nil {
		return nil, err
	}
	session, fresh = t.sessionFor(req.URL.Hostname())
	if !fresh {
		return nil, fmt.Errorf("re-solving Cloudflare clearance for %s: relay returned no cookies", req.URL.Hostname())
	}
	applySessionCookies(req, session)

	return t.inner.RoundTrip(req)
}

func (t *ImpersonatingTransport) solve(req *http.Request) (relaySession, error) {
	host := req.URL.Hostname()

	t.mu.Lock()
	var cached relaySession
	if session, ok := t.sessions[host]; ok {
		cached = session
	}
	t.mu.Unlock()

	endpoint, err := url.JoinPath(t.relay.String(), "v1")
	if err != nil {
		return relaySession{}, fmt.Errorf("building impersonation relay URL: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"cmd":        "request.get",
		"url":        relaySolveURL(req),
		"maxTimeout": int(t.maxTimeout / time.Millisecond),
		"cookies":    relayCookieSpecs(cached.cookies),
	})
	if err != nil {
		return relaySession{}, fmt.Errorf("encoding impersonation relay request: %w", err)
	}

	relayReq, err := http.NewRequestWithContext(req.Context(), http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return relaySession{}, fmt.Errorf("creating impersonation relay request: %w", err)
	}
	relayReq.Header.Set("Content-Type", "application/json")

	resp, err := t.relayClient.Do(relayReq)
	if err != nil {
		return relaySession{}, fmt.Errorf("impersonation relay %s unreachable: %w", t.relay, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return relaySession{}, fmt.Errorf("reading impersonation relay response: %w", err)
	}

	var envelope relayEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return relaySession{}, fmt.Errorf("decoding impersonation relay response: %w", err)
	}
	if envelope.Status != "ok" {
		return relaySession{}, fmt.Errorf(
			"solving Cloudflare challenge for %s: relay error: %s", host, envelope.Message,
		)
	}
	if envelope.Solution == nil {
		return relaySession{}, fmt.Errorf("solving Cloudflare challenge for %s: relay returned no solution", host)
	}

	cookies := make(map[string]string)
	for _, cookie := range envelope.Solution.Cookies {
		if cookie.Name != "" {
			cookies[cookie.Name] = cookie.Value
		}
	}
	if len(cookies) == 0 {
		return relaySession{}, fmt.Errorf("solving Cloudflare challenge for %s: relay returned no cookies", host)
	}

	return relaySession{cookies: cookies, minted: time.Now(), userAgent: envelope.Solution.UserAgent}, nil
}

func (t *ImpersonatingTransport) sessionFor(host string) (relaySession, bool) {
	t.mu.Lock()
	var session relaySession
	if cached, ok := t.sessions[host]; ok {
		session = cached
	}
	t.mu.Unlock()

	return session, sessionFreshAt(session, t.cookieTTL)
}

func (t *ImpersonatingTransport) sessionFresh(host string) bool {
	_, fresh := t.sessionFor(host)
	return fresh
}

func (t *ImpersonatingTransport) dropSession(host string) {
	t.mu.Lock()
	delete(t.sessions, host)
	t.mu.Unlock()
}

func sessionFreshAt(session relaySession, ttl time.Duration) bool {
	return len(session.cookies) > 0 && time.Now().Sub(session.minted) <= ttl
}

// relaySolveURL targets the host root so the zone challenge (zone-scoped
// cookies) is solved once, independent of any API-side constraints the
// request URL might carry.
func relaySolveURL(req *http.Request) string {
	authority := req.URL.Hostname()
	port := req.URL.Port()
	if port != "" && !defaultPort(req.URL.Scheme, port) {
		authority = authority + ":" + port
	}
	return req.URL.Scheme + "://" + authority + "/"
}

func defaultPort(scheme, port string) bool {
	return (scheme == "https" && port == "443") || (scheme == "http" && port == "80")
}

// relayCookieSpecs converts the cached clearance cookies back into the
// relay's cookie list so a solve reuses whatever is still valid.
func relayCookieSpecs(cookies map[string]string) []map[string]string {
	specs := make([]map[string]string, 0, len(cookies))
	for name, value := range cookies {
		specs = append(specs, map[string]string{"name": name, "value": value})
	}
	return specs
}

// applySessionCookies merges the minted clearance cookies into the request's
// Cookie header, overriding any jar-supplied values with the same names, and
// adopts the relay-echoed browser User-Agent (the clearance cookie is bound
// to the fingerprint that solved it) when the relay provided one. The caller
// never reuses a request after RoundTrip, so mutating the headers is safe
// here.
func applySessionCookies(req *http.Request, session relaySession) {
	if len(session.cookies) == 0 {
		return
	}
	req.Header.Set("Cookie", mergeCookies(req.Header.Get("Cookie"), session.cookies))
	if session.userAgent != "" {
		req.Header.Set("User-Agent", session.userAgent)
	}
}

func mergeCookies(existing string, fresh map[string]string) string {
	merged := make(map[string]string)
	order := make([]string, 0, len(fresh))
	if existing != "" {
		for _, pair := range strings.Split(existing, ";") {
			name, value, found := strings.Cut(pair, "=")
			name = strings.TrimSpace(name)
			if name == "" || !found {
				continue
			}
			if _, present := merged[name]; !present {
				order = append(order, name)
			}
			if _, overridden := fresh[name]; !overridden {
				merged[name] = strings.TrimSpace(value)
			}
		}
	}
	for name, value := range fresh {
		if _, present := merged[name]; !present {
			order = append(order, name)
		}
		merged[name] = value
	}

	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, name+"="+merged[name])
	}
	return strings.Join(parts, "; ")
}

// challengeIndicating detects Cloudflare re-challenges (a stale clearance
// cookie or a new challenge window) on responses the inner transport
// returned for a target host.
func challengeIndicating(resp http.Response) bool {
	if resp.Header.Get("cf-mitigated") == "challenge" {
		return true
	}
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusForbidden {
		return false
	}
	location := resp.Header.Get("Location")
	return strings.HasPrefix(location, "/@waf/")
}
