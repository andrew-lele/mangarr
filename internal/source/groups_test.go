package source

import (
	"testing"

	"mangarr/internal/domain"

	"github.com/stretchr/testify/require"
)

func TestNewGroupListerSupportsGroupSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options domain.GroupSearchOptions
	}{
		{name: "comix", options: domain.GroupSearchOptions{Source: "comix", ImpersonationProxy: "http://127.0.0.1:8191"}},
		{name: "atsumaru", options: domain.GroupSearchOptions{Source: "atsumaru", Manga: "https://atsu.moe/manga/Q5Mqy"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lister, err := NewGroupLister(tt.options)
			require.NoError(t, err)
			require.NotNil(t, lister)
		})
	}
}

func TestNewGroupListerRejectsSourcesWithoutGroups(t *testing.T) {
	t.Parallel()

	for _, sourceID := range []string{
		"tcbscans",
		"mangaplus",
		"flamecomics",
		"asurascans",
		"cubari",
		"weebcentral",
		"mangadex",
	} {
		t.Run(sourceID, func(t *testing.T) {
			t.Parallel()

			_, err := NewGroupLister(domain.GroupSearchOptions{Source: sourceID})
			require.EqualError(t, err, "source "+sourceID+" does not expose scanlation groups")
		})
	}
}

func TestNewGroupListerRejectsUnknownSource(t *testing.T) {
	t.Parallel()

	_, err := NewGroupLister(domain.GroupSearchOptions{Source: "unknown"})
	require.EqualError(t, err, "unknown source unknown")
}
