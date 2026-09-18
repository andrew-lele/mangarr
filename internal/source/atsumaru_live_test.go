package source

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAtsumaruLiveListsScanlators performs one real Atsumaru API call to
// verify group discovery against the live manga page endpoint. It is a field
// test: it needs network access to atsu.moe, so it is skipped unless
// MANGARR_LIVE_ATSUMARU is set and is never part of the local or CI gate.
//
// Run on a host with network access:
//
//	MANGARR_LIVE_ATSUMARU=1 go test ./internal/source
func TestAtsumaruLiveListsScanlators(t *testing.T) {
	t.Parallel()

	if os.Getenv("MANGARR_LIVE_ATSUMARU") == "" {
		t.Skip("MANGARR_LIVE_ATSUMARU is not set; this test needs network access to atsu.moe")
	}

	lister := NewAtsumaruGroupLister("https://atsu.moe/manga/Q5Mqy")
	groups, err := lister.Groups(t.Context(), "")
	require.NoError(t, err)
	require.Greater(t, len(groups), 0)
	for _, group := range groups {
		require.True(t, group.ID != "")
		require.True(t, group.Name != "")
	}
}
