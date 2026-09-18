package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mangarr/internal/domain"
	"mangarr/internal/sharedhttp"

	"github.com/PuerkitoBio/goquery"
	"github.com/avast/retry-go"
)

// NewSearcher returns a domain.Searcher for the named source, or a clear
// error when the source cannot look up series by title. Title-based tracked
// entries (monitoredManga { title: { qualityProfile } }) call this for every
// source in the profile's scan set; a search-unavailable source is skipped by
// the caller (monitor logs it) rather than failing the entry.
func NewSearcher(sourceKey string) (domain.Searcher, error) {
	switch sourceKey {
	case "atsumaru":
		return atsumaruSearcher{baseURL: atsumaruURL}, nil
	case "weebcentral":
		return weebcentralSearcher{baseURL: weebcentralURL}, nil
	case "mangadex":
		return mangadexSearcher{baseURL: mangadexURL}, nil
	}

	return nil, fmt.Errorf("source %s does not support title search", sourceKey)
}

// atsumaruSearcher queries the same /collections/manga/documents/search
// endpoint (SolarL export API) the keiyoushi extension uses. The filter_by
// chain mirrors the extension's battle-tested default filter set.
type atsumaruSearcher struct {
	baseURL string
}

const atsumaruSearchFilter = "hidden:!=true && (mbContentRating:=[`Safe`,`Suggestive`,`Erotica`] || mbContentRating:!=*) && medium:!=[`Novel`] && views:>0"

func (s atsumaruSearcher) Search(ctx context.Context, title string) ([]domain.SearchResult, error) {
	params := url.Values{}
	params.Set("q", title)
	params.Set("filter_by", atsumaruSearchFilter)
	params.Set("query_by", "title,englishTitle,otherNames,authors")
	params.Set("page", "1")
	params.Set("per_page", "20")

	rawURL, err := url.JoinPath(s.baseURL, "collections/manga/documents/search")
	if err != nil {
		return nil, fmt.Errorf("building Atsumaru search URL: %w", err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing Atsumaru search URL: %w", err)
	}
	parsed.RawQuery = params.Encode()

	var response struct {
		Hits []struct {
			Document struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"document"`
		} `json:"hits"`
	}
	if err := searchJSON(ctx, parsed.String(), &response); err != nil {
		return nil, fmt.Errorf("searching Atsumaru: %w", err)
	}

	results := make([]domain.SearchResult, 0, len(response.Hits))
	for _, hit := range response.Hits {
		if hit.Document.ID == "" {
			continue
		}
		results = append(results, domain.SearchResult{
			Title: strings.TrimSpace(hit.Document.Title),
			URL:   strings.TrimSuffix(s.baseURL, "/") + "/manga/" + hit.Document.ID,
		})
	}

	return results, nil
}

// weebcentralSearcher scrapes the /search/data results page that the site's
// own search uses; each result renders two anchors with the same series href
// (cover + title), de-duplicated by URL below.
type weebcentralSearcher struct {
	baseURL string
}

func (s weebcentralSearcher) Search(ctx context.Context, title string) ([]domain.SearchResult, error) {
	params := url.Values{}
	params.Set("text", title)
	params.Set("limit", "20")
	params.Set("offset", "0")
	params.Set("display_mode", "Full Display")

	rawURL, err := url.JoinPath(s.baseURL, "search/data")
	if err != nil {
		return nil, fmt.Errorf("building Weeb Central search URL: %w", err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing Weeb Central search URL: %w", err)
	}
	parsed.RawQuery = params.Encode()

	body, err := searchBody(ctx, parsed.String())
	if err != nil {
		return nil, fmt.Errorf("searching Weeb Central: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parsing Weeb Central search results: %w", err)
	}

	type entry struct {
		url   string
		title string
	}
	seen := make(map[string]entry)
	doc.Find("a[href]").Each(func(_ int, selection *goquery.Selection) {
		href, _ := selection.Attr("href")
		if !strings.Contains(href, "/series/") {
			return
		}
		resolvedURL, err := resolveAgainstBase(s.baseURL, href)
		if err != nil || resolvedURL == "" {
			return
		}
		text := strings.TrimSpace(selection.Text())
		existing, ok := seen[resolvedURL]
		if ok {
			if existing.title == "" {
				existing.title = text
				seen[resolvedURL] = existing
			}
			return
		}
		seen[resolvedURL] = entry{url: resolvedURL, title: text}
	})

	results := make([]domain.SearchResult, 0, len(seen))
	for _, e := range seen {
		results = append(results, domain.SearchResult{Title: e.title, URL: e.url})
	}

	return results, nil
}

// mangadexSearcher queries the official API /manga?title= endpoint; the
// native id IS the mangadex UUID the source adapter accepts.
type mangadexSearcher struct {
	baseURL string
}

func (s mangadexSearcher) Search(ctx context.Context, title string) ([]domain.SearchResult, error) {
	params := url.Values{}
	params.Set("title", title)
	params.Set("limit", "25")

	rawURL, err := url.JoinPath(s.baseURL, "manga")
	if err != nil {
		return nil, fmt.Errorf("building MangaDex search URL: %w", err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing MangaDex search URL: %w", err)
	}
	parsed.RawQuery = params.Encode()

	var response struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Title map[string]string `json:"title"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := searchJSON(ctx, parsed.String(), &response); err != nil {
		return nil, fmt.Errorf("searching MangaDex: %w", err)
	}

	results := make([]domain.SearchResult, 0, len(response.Data))
	for _, item := range response.Data {
		title := displayTitle(item.Attributes.Title)
		results = append(results, domain.SearchResult{Title: title, URL: item.ID})
	}

	return results, nil
}

// displayTitle picks the first available localized title in a preferred order.
func displayTitle(localized map[string]string) string {
	for _, lang := range []string{"en", "ja-ro", "ja", "ko", "zh-ro", "zh"} {
		if t, ok := localized[lang]; ok && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	for _, t := range localized {
		return strings.TrimSpace(t)
	}
	return ""
}

func newSearchClient() http.Client {
	return http.Client{
		Timeout:   60 * time.Second,
		Transport: sharedhttp.Transport,
	}
}

// searchJSON performs one GET and decodes a JSON response with the shared
// retry policy.
func searchJSON(ctx context.Context, rawURL string, target any) error {
	body, err := searchBody(ctx, rawURL)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decoding search response from %s: %w", rawURL, err)
	}
	return nil
}

// searchBody performs one GET with retries and returns the raw response body.
func searchBody(ctx context.Context, rawURL string) ([]byte, error) {
	var body []byte
	err := retry.Do(func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return fmt.Errorf("creating search request for %s: %w", rawURL, err)
		}
		req.Header.Set("User-Agent", "mangarr")

		resp, err := sharedhttp.ExecRequest(newSearchClient(), req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("reading search response from %s: %w", rawURL, err)
		}
		return nil
	}, sharedhttp.RetryOptions(ctx)...)
	if err != nil {
		return nil, err
	}
	return body, nil
}
