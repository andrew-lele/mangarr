package source

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestComixLiveThroughImpersonationRelay performs one real Comix API call
// through a FlareSolverr-compatible relay (default endpoint shape:
// http://127.0.0.1:8191). It is a field test: the relay must run on the
// same host and egress IP as Mangarr (the challenge cookies are IP-bound),
// so it is skipped unless MANGARR_LIVE_SOLVER_URL is set and is never part
// of the local or CI gate.
//
// Run on the deployment host (jihun):
//
//	MANGARR_LIVE_SOLVER_URL=http://127.0.0.1:8191 go test ./internal/source
func TestComixLiveThroughImpersonationRelay(t *testing.T) {
	t.Parallel()

	relayURL := os.Getenv("MANGARR_LIVE_SOLVER_URL")
	if relayURL == "" {
		t.Skip("MANGARR_LIVE_SOLVER_URL is not set; this test needs a live FlareSolverr-compatible relay on the deployment host")
	}

	source := NewComix("https://comix.to/title/106213-one-piece", "", relayURL).(*comix)
	require.NoError(t, source.ValidateInput())

	// One API call, not full discovery: walks the Cloudflare challenge once
	// through the relay, then issues the request directly with the minted
	// clearance cookie. A codec failure here means the frontend build rotated
	// (see docs/design-docs/source-adapters.md), a relay failure means the
	// solve path is broken.
	manga, err := source.getManga(t.Context())
	require.NoError(t, err)
	require.True(t, manga.ID != "")
	require.True(t, manga.Title != "")

	// Group discovery rides the same minted clearance: one more signed API
	// call to the group catalog, which must answer with the known groups
	// (numeric GroupIDs feeding groups.yaml native ids).
	groups, err := source.Groups(t.Context(), "Flame")
	require.NoError(t, err)
	require.Greater(t, len(groups), 0)
	require.True(t, groups[0].ID != "")
	require.True(t, groups[0].Name != "")
}
