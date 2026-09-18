package sharedhttp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImpersonatingTransportMintsAndReusesClearanceCookie(t *testing.T) {
	t.Parallel()

	var relayHits atomic.Int32
	var directHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		cookie := r.Header.Get("Cookie")
		require.True(t, cookie == "cf_clearance=aaaa; __cf_bm=bbbb" || cookie == "__cf_bm=bbbb; cf_clearance=aaaa")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"ok": "true"}))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayHits.Add(1)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload struct {
			Cmd        string `json:"cmd"`
			Url        string `json:"url"`
			MaxTimeout int    `json:"maxTimeout"`
		}
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, "request.get", payload.Cmd)
		require.Equal(t, target.URL+"/", payload.Url)
		require.Greater(t, payload.MaxTimeout, 0)
		writeRelaySolution(t, w, []map[string]string{
			{"name": "cf_clearance", "value": "aaaa"},
			{"name": "__cf_bm", "value": "bbbb"},
		})
	}))
	defer relay.Close()

	transport := newImpersonatingTransportForTest(t, target, relay, []string{"127.0.0.1"})
	client := http.Client{Timeout: 10 * time.Second, Transport: transport}

	require.Equal(t, int32(0), relayHits.Load())
	first, err := client.Do(requireRequest(t, target.URL))
	require.NoError(t, err)
	defer first.Body.Close()
	require.Equal(t, http.StatusOK, first.StatusCode)
	require.Equal(t, int32(1), relayHits.Load())
	require.Equal(t, int32(1), directHits.Load())

	second, err := client.Do(requireRequest(t, target.URL))
	require.NoError(t, err)
	defer second.Body.Close()
	require.Equal(t, http.StatusOK, second.StatusCode)
	// The minted cookie is reused: no second solve, origin request replays.
	require.Equal(t, int32(1), relayHits.Load())
	require.Equal(t, int32(2), directHits.Load())
}

func TestImpersonatingTransportForwardsExistingCookiesWhenReSolving(t *testing.T) {
	t.Parallel()

	var relayHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"ok": "true"}))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayHits.Add(1)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload struct {
			Cookies []map[string]string `json:"cookies"`
		}
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Len(t, payload.Cookies, 1)
		require.Equal(t, "cf_clearance", payload.Cookies[0]["name"])
		require.Equal(t, "stale", payload.Cookies[0]["value"])
		writeRelaySolution(t, w, []map[string]string{{"name": "cf_clearance", "value": "fresh"}})
	}))
	defer relay.Close()

	transport := newImpersonatingTransportForTest(t, target, relay, []string{"127.0.0.1"})
	transport.mu.Lock()
	transport.sessions["127.0.0.1"] = relaySession{
		cookies: map[string]string{"cf_clearance": "stale"},
		minted:  time.Now().Add(-2 * transport.cookieTTL),
	}
	transport.mu.Unlock()

	client := http.Client{Timeout: 10 * time.Second, Transport: transport}
	resp, err := client.Do(requireRequest(t, target.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, int32(1), relayHits.Load())
}

func TestImpersonatingTransportReSolvesOnChallengeResponse(t *testing.T) {
	t.Parallel()

	var relayHits atomic.Int32
	var directHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempt := directHits.Add(1)
		if attempt == 1 {
			w.Header().Set("cf-mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"ok": "true"}))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit := relayHits.Add(1)
		value := "fresh"
		if hit == 1 {
			value = "expired"
		}
		writeRelaySolution(t, w, []map[string]string{{"name": "cf_clearance", "value": value}})
	}))
	defer relay.Close()

	transport := newImpersonatingTransportForTest(t, target, relay, []string{"127.0.0.1"})
	client := http.Client{Timeout: 10 * time.Second, Transport: transport}

	resp, err := client.Do(requireRequest(t, target.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, int32(2), relayHits.Load())
	require.Equal(t, int32(2), directHits.Load())
}

func TestImpersonatingTransportLeavesNonTargetHostsAlone(t *testing.T) {
	t.Parallel()

	var relayHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"ok": "true"}))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		relayHits.Add(1)
		writeRelaySolution(t, w, []map[string]string{{"name": "cf_clearance", "value": "aaaa"}})
	}))
	defer relay.Close()

	transport := newImpersonatingTransportForTest(t, target, relay, []string{"comix.to"})
	client := http.Client{Timeout: 10 * time.Second, Transport: transport}

	resp, err := client.Do(requireRequest(t, target.URL))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, int32(0), relayHits.Load())
}

func TestImpersonatingTransportSurfacesRelayErrors(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"ok": "true"}))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "Challenge not solved",
		}))
	}))
	defer relay.Close()

	transport := newImpersonatingTransportForTest(t, target, relay, []string{"127.0.0.1"})
	client := http.Client{Timeout: 10 * time.Second, Transport: transport}

	_, err := client.Do(requireRequest(t, target.URL))
	require.Error(t, err)
	require.Contains(t, err.Error(), "Challenge not solved")
}

func TestImpersonatingTransportRequiresRelayCookies(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]string{"ok": "true"}))
	}))
	defer target.Close()

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRelaySolution(t, w, []map[string]string{})
	}))
	defer relay.Close()

	transport := newImpersonatingTransportForTest(t, target, relay, []string{"127.0.0.1"})
	client := http.Client{Timeout: 10 * time.Second, Transport: transport}

	_, err := client.Do(requireRequest(t, target.URL))
	require.Error(t, err)
	require.Contains(t, err.Error(), "relay returned no cookies")
}

func newImpersonatingTransportForTest(t *testing.T, target, relay *httptest.Server, targetHosts []string) *ImpersonatingTransport {
	t.Helper()

	relayURL, err := url.Parse(relay.URL)
	require.NoError(t, err)

	return NewImpersonatingTransport(target.Client().Transport, relayURL, targetHosts)
}

func requireRequest(t *testing.T, target string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)

	return req
}

func writeRelaySolution(t *testing.T, w http.ResponseWriter, cookies []map[string]string) {
	t.Helper()

	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "Challenge solved!",
		"solution": map[string]any{
			"status":   200,
			"headers":  map[string]any{},
			"response": "<html>solved</html>",
			"cookies":  cookies,
		},
	}))
}
