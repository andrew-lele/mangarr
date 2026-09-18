package source

import (
	"testing"

	"mangarr/internal/domain"

	"github.com/stretchr/testify/require"
)

func TestSelectSupportsEveryRegisteredSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		identifier string
		name       string
	}{
		{identifier: "asurascans", name: "Asura Scans"},
		{identifier: "atsumaru", name: "Atsumaru"},
		{identifier: "comix", name: "Comix"},
		{identifier: "cubari", name: "Cubari"},
		{identifier: "flamecomics", name: "Flame Comics"},
		{identifier: "mangadex", name: "MangaDex"},
		{identifier: "mangaplus", name: "MANGA Plus"},
		{identifier: "tcbscans", name: "TCB Scans"},
		{identifier: "weebcentral", name: "Weeb Central"},
	}

	for _, tt := range tests {
		t.Run(tt.identifier, func(t *testing.T) {
			t.Parallel()

			src, err := Select(domain.MonitoredManga{Source: tt.identifier})
			require.NoError(t, err)
			require.Equal(t, tt.name, src.String())
		})
	}
}

func TestSelectRejectsUnknownSource(t *testing.T) {
	t.Parallel()

	_, err := Select(domain.MonitoredManga{Source: "unknown"})
	require.EqualError(t, err, "unknown monitored manga source unknown")
}

func TestNewNativeGroupResolverDispatch(t *testing.T) {
	t.Parallel()

	atsumaru, err := Select(domain.MonitoredManga{
		Source: "atsumaru",
		Manga:  "https://atsu.moe/manga/Q5Mqy",
		Group:  "scan-1",
	})
	require.NoError(t, err)
	require.NotNil(t, NewNativeGroupResolver("atsumaru", atsumaru))

	// Sources with global native ids (comix) and unknown names have no
	// resolver extension and keep the default NativeIndex lookup.
	comix, err := Select(domain.MonitoredManga{Source: "comix"})
	require.NoError(t, err)
	require.Nil(t, NewNativeGroupResolver("comix", comix))
	require.Nil(t, NewNativeGroupResolver("unknown", comix))
}
