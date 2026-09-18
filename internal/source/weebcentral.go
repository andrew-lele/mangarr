package source

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"mangarr/internal/domain"
	"mangarr/internal/sanitize"
	"mangarr/internal/sharedhttp"

	"github.com/PuerkitoBio/goquery"
	"github.com/avast/retry-go"
	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/extensions"
)

const (
	weebcentralURL              = "https://weebcentral.com"
	weebcentralBrowserUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

var weebcentralChapterNumberPattern = regexp.MustCompile(`(?:Chapter|Ch\.|Mission) ?(\d+(\.\d+)?)`)

type weebcentral struct {
	MangaURL  string
	Collector *colly.Collector
	Client    http.Client
	BaseURL   string
}

func NewWeebCentral(mangaURL string) domain.Source {
	collector := colly.NewCollector(
		colly.AllowURLRevisit(),
	)
	extensions.RandomUserAgent(collector)

	collector.SetRequestTimeout(120 * time.Second)

	return &weebcentral{
		Collector: collector,
		MangaURL:  mangaURL,
		BaseURL:   weebcentralURL,
		Client: http.Client{
			Timeout:   120 * time.Second,
			Transport: sharedhttp.Transport,
		},
	}
}

func (w *weebcentral) String() string {
	return "Weeb Central"
}

func (w *weebcentral) ValidateInput() error {
	if len(w.MangaURL) == 0 {
		return fmt.Errorf("weebcentral manga URL is required")
	}

	parsed, err := url.Parse(w.MangaURL)
	if err != nil {
		return fmt.Errorf("parsing URL %s: %w", w.MangaURL, err)
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "weebcentral.com") {
		return fmt.Errorf("the URL for Weeb Central must use %s", weebcentralURL)
	}

	return nil
}

// Discover gets the selected manga and its chapters from Weeb Central.
func (w *weebcentral) Discover(ctx context.Context) (domain.Manga, error) {
	manga, err := w.getManga(ctx)
	if err != nil {
		return domain.Manga{}, err
	}
	if err := w.getChapters(ctx, manga); err != nil {
		return domain.Manga{}, err
	}

	return manga, nil
}

func (w *weebcentral) getManga(ctx context.Context) (domain.Manga, error) {
	var manga domain.Manga
	var errors []error
	c := w.Collector.Clone()
	c.Context = ctx

	c.OnError(func(r *colly.Response, err error) {
		errors = append(errors, fmt.Errorf("requesting URL %s: %w", r.Request.URL, err))
	})

	c.OnHTML("h1.hidden", func(e *colly.HTMLElement) {
		manga = domain.Manga{
			Title:    sanitize.Filename(e.Text),
			Chapters: make(map[domain.ChapterNumber]domain.Chapter),
		}
	})

	err := c.Visit(w.MangaURL)
	if err != nil {
		return domain.Manga{}, fmt.Errorf("visiting URL %s: %w", w.MangaURL, err)
	}

	if len(errors) > 0 {
		return domain.Manga{}, fmt.Errorf("processing %d URLs: %w", len(errors), errors[0])
	}

	return manga, nil
}

func (w *weebcentral) getChapters(ctx context.Context, manga domain.Manga) error {
	var errors []error
	c := w.Collector.Clone()
	c.Context = ctx

	c.OnError(func(r *colly.Response, err error) {
		errors = append(errors, fmt.Errorf("requesting URL %s: %w", r.Request.URL, err))
	})

	// The full chapter list is served by the /full-chapter-list FRAGMENT that
	// the series page's "See all chapters" button htmx-loads into
	// #chapter-list; the static series page itself only renders recent /
	// last-read rows (9 anchors for a 158-chapter series), so it cannot be
	// the chapter source. Fragment rows (field-verified 2026-09-18):
	//
	//	<a href="/chapters/01M19K9XZJ3BMNB4SW07ZQ8JYE"
	//	   class="hover:bg-base-300 flex-1 flex items-center p-2">
	//	    <span class="grow flex items-center gap-2">
	//	      <span class="">Mission 140</span>   (or "Chapter 147")
	//	      <span class="hidden md:inline">Last Read</span>
	//	    </span>
	//	    <time ...>2026-08-30...</time>
	//	</a>
	//
	// Only anchors whose href is a /chapters/<id> link are chapter rows;
	// everything else in the fragment is skipped. The chapter name is read
	// from span.grow (the row's date lives outside it), accepting the
	// series-dependent naming ("Chapter"/"Ch."/"Mission") and ignoring the
	// trailing "Last Read" marker; unparseable rows are skipped.
	c.OnHTML("a", func(e *colly.HTMLElement) {
		href := e.Attr("href")
		if !strings.HasPrefix(href, "/chapters/") {
			return
		}

		chapterURL, err := resolveAgainstBase(w.BaseURL, href)
		if err != nil {
			errors = append(errors, fmt.Errorf("resolving chapter URL: %w", err))
			return
		}

		number, err := w.getChapterNumber(e.ChildText("span.grow"))
		if err != nil {
			// Skip chapters that don't match the regex pattern instead of failing
			return
		}

		manga.Chapters[number] = domain.Chapter{
			URL:    chapterURL,
			Number: number,
		}
	})

	path, err := fullChapterListURL(w.MangaURL)
	if err != nil {
		return err
	}

	err = c.Visit(path)
	if err != nil {
		return fmt.Errorf("visiting URL %s: %w", path, err)
	}

	if len(errors) > 0 {
		return fmt.Errorf("processing %d URLs: %w", len(errors), errors[0])
	}

	if len(manga.Chapters) == 0 {
		return fmt.Errorf("getting chapters for manga %s", manga.Title)
	}

	return nil
}

// fullChapterListURL builds the /full-chapter-list fragment URL for a
// series page URL. The fragment lives at /series/<id>/full-chapter-list
// WITHOUT the title slug: joining "full-chapter-list" onto the full series
// URL yields /series/<id>/<slug>/full-chapter-list, which the site answers
// with its /404 page (verified 2026-09-18 on jihun against the real site).
func fullChapterListURL(mangaURL string) (string, error) {
	parsed, err := url.Parse(mangaURL)
	if err != nil {
		return "", fmt.Errorf("parsing URL %s: %w", mangaURL, err)
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 || segments[0] != "series" || segments[1] == "" {
		return "", fmt.Errorf("URL %s is not a /series/<id>/<slug> page", mangaURL)
	}

	return fmt.Sprintf("%s://%s/series/%s/full-chapter-list", parsed.Scheme, parsed.Host, segments[1]), nil
}

// Pages gets all image URLs for a chapter.
func (w *weebcentral) Pages(ctx context.Context, chapter domain.Chapter) ([]domain.ImageInfo, error) {
	imageURL, err := w.chapterImagesURL(chapter.URL)
	if err != nil {
		return nil, fmt.Errorf("building chapter images URL from %s: %w", chapter.URL, err)
	}

	body, err := w.fetch(ctx, imageURL)
	if err != nil {
		return nil, fmt.Errorf("fetching chapter images %s: %w", imageURL, err)
	}

	imageURLs, err := w.extractImageURLs(body)
	if err != nil {
		return nil, fmt.Errorf("extracting chapter image URLs from %s: %w", imageURL, err)
	}

	if len(imageURLs) == 0 {
		return nil, fmt.Errorf("getting image URLs for chapter %s", chapter.Number)
	}

	imageInfos := make([]domain.ImageInfo, 0, len(imageURLs))
	for _, imageURL := range imageURLs {
		imageInfos = append(imageInfos, domain.ImageInfo{ImageURL: imageURL})
	}

	return imageInfos, nil
}

func (w *weebcentral) chapterImagesURL(chapterURL string) (string, error) {
	parsed, err := url.Parse(chapterURL)
	if err != nil {
		return "", err
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""

	imageURL, err := url.JoinPath(parsed.String(), "images")
	if err != nil {
		return "", err
	}

	u, err := url.Parse(imageURL)
	if err != nil {
		return "", err
	}

	params := u.Query()
	params.Set("is_prev", "False")
	params.Set("current_page", "1")
	params.Set("reading_style", "long_strip")
	u.RawQuery = params.Encode()

	return u.String(), nil
}

func (w *weebcentral) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	var body []byte

	err := retry.Do(func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return fmt.Errorf("creating request for %s: %w", rawURL, err)
		}

		req.Header.Set("User-Agent", weebcentralBrowserUserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		resp, err := sharedhttp.ExecRequest(w.Client, req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("reading response body from %s: %w", rawURL, err)
		}

		return nil
	}, sharedhttp.RetryOptions(ctx)...)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func (w *weebcentral) extractImageURLs(body []byte) ([]string, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	imageURLs := make([]string, 0)
	doc.Find("img[src]").Each(func(_ int, selection *goquery.Selection) {
		src := strings.TrimSpace(selection.AttrOr("src", ""))
		if len(src) == 0 {
			return
		}

		resolvedURL, err := resolveAgainstBase(w.BaseURL, src)
		if err != nil {
			return
		}

		imageURLs = append(imageURLs, resolvedURL)
	})

	return dedupeStrings(imageURLs), nil
}

// getChapterNumber gets the chapter number from the scraped chapter name
func (w *weebcentral) getChapterNumber(name string) (domain.ChapterNumber, error) {
	// FindSubmatch returns an array where the first element is the full match, and the rest are submatches.
	matches := weebcentralChapterNumberPattern.FindStringSubmatch(name)

	if len(matches) <= 1 {
		return domain.ChapterNumber{}, fmt.Errorf("finding matches in %s", name)
	}

	number, err := domain.ParseChapterNumber(matches[1])
	if err != nil {
		return domain.ChapterNumber{}, fmt.Errorf("parsing chapter number from %s: %w", name, err)
	}

	return number, nil
}
