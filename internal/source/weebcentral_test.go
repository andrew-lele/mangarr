package source

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mangarr/internal/domain"

	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/extensions"
	"github.com/stretchr/testify/require"
)

func TestWeebCentralPagesExtractsChapterAssets(t *testing.T) {
	t.Parallel()

	const chapterPath = "/chapters/01TESTCHAPTER"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case chapterPath + "/images":
			require.Equal(t, "False", r.URL.Query().Get("is_prev"))
			require.Equal(t, "1", r.URL.Query().Get("current_page"))
			require.Equal(t, "long_strip", r.URL.Query().Get("reading_style"))

			fmt.Fprint(w, `
				<section>
					<img src="https://cdn.weebcentral.test/manga/chapter-001.png" />
					<img src="/media/chapter-002.png" />
					<img src="https://cdn.weebcentral.test/manga/chapter-001.png" />
				</section>
			`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	src := newTestWeebCentral(server.URL+"/series/series-id", server.URL)

	chapter := domain.Chapter{
		URL:    server.URL + chapterPath + "?foo=bar#reader",
		Number: mustChapterNumber("340.2"),
	}

	pages, err := src.Pages(t.Context(), chapter)
	require.NoError(t, err)
	require.Len(t, pages, 2)
	require.Equal(t, "https://cdn.weebcentral.test/manga/chapter-001.png", pages[0].ImageURL)
	require.Equal(t, server.URL+"/media/chapter-002.png", pages[1].ImageURL)
}

func TestWeebCentralScrapesSeriesPageChapterList(t *testing.T) {
	t.Parallel()

	const chapterPath = "/chapters/01CHAP356"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/series/series-id":
			// Current weebcentral markup (field-verified 2026-09-18): chapter
			// rows are <a href="/chapters/<id>">Chapter N[ Last Read]</a>;
			// navbar/related links and the "See all chapters" anchor must not
			// become chapters.
			fmt.Fprint(w, `<html><body>
				<nav>
					<a href="/about">About</a>
					<a href="https://weebcentral.com/series/series-id/more">More series</a>
				</nav>
				<section>
					<a class="hover:bg-base-300 flex-1 flex items-center p-2" href="/chapters/01CHAP356">Chapter 356 Last Read</a>
					<a class="hover:bg-base-300 flex-1 flex items-center p-2" href="/chapters/01CHAP357">Chapter 357</a>
					<a class="hover:bg-base-300 flex-1 flex items-center p-2" href="/chapters/01CHAP3575">Chapter 357.5</a>
					<a class="hover:bg-base-300 flex-1 flex items-center p-2" href="/chapters/01CHAPJUNK">Read the latest</a>
					<a class="flex" href="/series/series-id/full-chapter-list">See all chapters</a>
				</section>
			</body></html>`)
		case chapterPath + "/images":
			fmt.Fprint(w, `<img src="/media/chapter-356.png" />`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	src := newTestWeebCentral(server.URL+"/series/series-id", server.URL)
	manga := domain.Manga{
		Title:    "Blue Lock",
		Chapters: make(map[domain.ChapterNumber]domain.Chapter),
	}

	require.NoError(t, src.getChapters(t.Context(), manga))
	// Three parseable chapter rows; the "Last Read" marker is stripped by
	// the number regex, junk-title and non-/chapters/ anchors are skipped.
	require.Len(t, manga.Chapters, 3)
	require.Equal(t, server.URL+"/chapters/01CHAP356", manga.Chapters[mustChapterNumber("356")].URL)
	require.Equal(t, server.URL+"/chapters/01CHAP357", manga.Chapters[mustChapterNumber("357")].URL)
	require.Equal(t, server.URL+"/chapters/01CHAP3575", manga.Chapters[mustChapterNumber("357.5")].URL)

	// The discovered chapter drives the existing pages flow unchanged.
	pages, err := src.Pages(t.Context(), manga.Chapters[mustChapterNumber("356")])
	require.NoError(t, err)
	require.Equal(t, server.URL+"/media/chapter-356.png", pages[0].ImageURL)
}

func TestWeebCentralGetChaptersErrorsWhenPageHasNoChapterAnchors(t *testing.T) {
	t.Parallel()

	// The /full-chapter-list endpoint now answers with a 404 page wrapper;
	// scraping it must fail the same way a chapterless series page does.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><head><title>404 | Weeb Central</title></head><body><a href="/">Home</a><a href="/series/series-id">Back</a></body></html>`)
	}))
	defer server.Close()

	src := newTestWeebCentral(server.URL+"/series/series-id", server.URL)
	manga := domain.Manga{
		Title:    "Blue Lock",
		Chapters: make(map[domain.ChapterNumber]domain.Chapter),
	}

	err := src.getChapters(t.Context(), manga)
	require.EqualError(t, err, "getting chapters for manga Blue Lock")
}

func TestWeebCentralPagesErrorsWhenFragmentHasNoImages(t *testing.T) {
	t.Parallel()

	const chapterPath = "/chapters/01EMPTYCHAPTER"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case chapterPath + "/images":
			fmt.Fprint(w, `<section><p>No pages.</p></section>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	src := newTestWeebCentral(server.URL+"/series/series-id", server.URL)

	chapter := domain.Chapter{
		URL:    server.URL + chapterPath,
		Number: mustChapterNumber("1"),
	}

	_, err := src.Pages(t.Context(), chapter)
	require.EqualError(t, err, "getting image URLs for chapter 1")
}

func TestWeebCentralRejectsPrefixHostSpoofing(t *testing.T) {
	source := NewWeebCentral("https://weebcentral.com.example/series/fixture")
	if err := source.ValidateInput(); err == nil {
		t.Fatal("expected spoofed host to fail validation")
	}
}

func newTestWeebCentral(mangaURL, baseURL string) *weebcentral {
	collector := colly.NewCollector(
		colly.AllowURLRevisit(),
	)
	extensions.RandomUserAgent(collector)
	collector.SetRequestTimeout(10 * time.Second)

	return &weebcentral{
		MangaURL:  mangaURL,
		Collector: collector,
		BaseURL:   baseURL,
		Client: http.Client{
			Timeout: 10 * time.Second,
		},
	}
}
