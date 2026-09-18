package source

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSearcherDispatch(t *testing.T) {
	t.Parallel()

	for _, sourceKey := range []string{"atsumaru", "weebcentral", "mangadex"} {
		t.Run(sourceKey, func(t *testing.T) {
			t.Parallel()

			searcher, err := NewSearcher(sourceKey)
			require.NoError(t, err)
			require.NotNil(t, searcher)
		})
	}

	for _, sourceKey := range []string{"asurascans", "mangaplus", "flamecomics", "comix", "cubari", "tcbscans"} {
		t.Run(sourceKey, func(t *testing.T) {
			t.Parallel()

			_, err := NewSearcher(sourceKey)
			require.EqualError(t, err, "source "+sourceKey+" does not support title search")
		})
	}
}

func TestAtsumaruSearcherParsesHits(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/collections/manga/documents/search", r.URL.Path)
		require.Equal(t, "Kagurabachi", r.URL.Query().Get("q"))
		require.Contains(t, r.URL.Query().Get("filter_by"), "hidden:!=true")
		fmt.Fprint(w, `{
			"found": 2,
			"hits": [
				{"document": {"id": "Q5Mqy", "title": "Kagurabachi"}},
				{"document": {"id": "other", "title": "Kagurabachi Zero"}}
			]
		}`)
	}))
	defer server.Close()

	searcher := atsumaruSearcher{baseURL: server.URL}
	results, err := searcher.Search(t.Context(), "Kagurabachi")
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "Kagurabachi", results[0].Title)
	require.Equal(t, server.URL+"/manga/Q5Mqy", results[0].URL)
}

func TestWeebCentralSearcherDeduplicatesAndFilters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/search/data", r.URL.Path)
		require.Equal(t, "Spy x Family", r.URL.Query().Get("text"))
		// Cover + title anchors share the href; a non-series anchor must be
		// filtered.
		fmt.Fprint(w, `<html><body>
			<article><section>
				<a href="/series/01J76XYCYJ0P680SKX3QZ0NQD7/Spy-X-Family"><img alt="cover"></a>
				<a class="line-clamp-1" href="/series/01J76XYCYJ0P680SKX3QZ0NQD7/Spy-X-Family">Spy x Family</a>
				<a href="/chapters/01CHAP">latest chapter</a>
				<a href="https://weebcentral.com/series/01JQEDTB1GHB61Z5BNCGWDZX9J/four-lives-remain">Four Lives Remain</a>
			</section></article>
		</body></html>`)
	}))
	defer server.Close()

	searcher := weebcentralSearcher{baseURL: server.URL}
	results, err := searcher.Search(t.Context(), "Spy x Family")
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, server.URL+"/series/01J76XYCYJ0P680SKX3QZ0NQD7/Spy-X-Family", results[0].URL)
	require.Equal(t, "Spy x Family", results[0].Title)
}

func TestMangaDexSearcherUsesNativeUUID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/manga", r.URL.Path)
		require.Equal(t, "One Piece", r.URL.Query().Get("title"))
		fmt.Fprint(w, `{"total": 2, "data": [
			{"id": "a1c7c817-4e59-43b7-9365-09675a149a6f", "attributes": {"title": {"en": "One Piece", "ja-ro": "Wan Pīsu"}}},
			{"id": "b70113a5-32a3-44e8-a28f-0e88392808ba", "attributes": {"title": {"ja": "ワンピース"}}}
		]}`)
	}))
	defer server.Close()

	searcher := mangadexSearcher{baseURL: server.URL}
	results, err := searcher.Search(t.Context(), "One Piece")
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "One Piece", results[0].Title)
	require.Equal(t, "a1c7c817-4e59-43b7-9365-09675a149a6f", results[0].URL)
	require.Equal(t, "ワンピース", results[1].Title)
}
