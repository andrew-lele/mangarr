package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mangarr/internal/domain"
	"mangarr/internal/sanitize"
	"mangarr/internal/sharedhttp"

	groupregistry "mangarr/internal/registry"

	"github.com/avast/retry-go"
)

const atsumaruURL = "https://atsu.moe"

type atsumaru struct {
	MangaURL string
	ScanID   string
	Client   *http.Client
	BaseURL  string

	// QualityProfile identifies the quality profile the caller assigned to
	// this manga (from MonitoredManga.qualityProfile). When set, Discovery
	// includes chapters from EVERY scanlator (no pinned-ScanID filter, so a
	// group choice can be profile-driven) and an empty ScanID is allowed;
	// when empty, the pinned-ScanID behavior is preserved for back-compat.
	QualityProfile string

	// Scanlators caches the manga page's scanlation groups (scoped ScanID +
	// stable name) once fetched, in reader order. The cache feeds the group
	// resolver bridge (ResolveNativeGroup): atsumaru ScanIDs are scoped
	// per-manga, so a chapter's ScanID resolves to a canonical group via its
	// scanlator NAME, never via a global native-id map.
	Scanlators []domain.ScanlationGroup
}

type atsumaruMangaResponse struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	ForceStrip bool              `json:"forceStrip"`
	Chapters   []atsumaruChapter `json:"chapters"`
}

type atsumaruChapter struct {
	ID     string               `json:"id"`
	Title  string               `json:"title"`
	Number domain.ChapterNumber `json:"number"`
	ScanID string               `json:"scanId"`
}

type atsumaruReadChapterResponse struct {
	ReadChapter struct {
		ID    string         `json:"id"`
		Title string         `json:"title"`
		Pages []atsumaruPage `json:"pages"`
	} `json:"readChapter"`
}

type atsumaruMangaPageResponse struct {
	MangaPage struct {
		Scanlators []atsumaruScanlator `json:"scanlators"`
	} `json:"mangaPage"`
}

type atsumaruScanlator struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type atsumaruPage struct {
	Image string `json:"image"`
}

func NewAtsumaru(mangaURL, scanID, qualityProfile string) domain.Source {
	client := http.Client{
		Timeout:   60 * time.Second,
		Transport: sharedhttp.Transport,
	}

	return &atsumaru{
		MangaURL:       mangaURL,
		ScanID:         scanID,
		QualityProfile: qualityProfile,
		Client:         &client,
		BaseURL:        atsumaruURL,
	}
}

// NewAtsumaruGroupLister builds an Atsumaru scanlation-group listing client.
// Atsumaru groups ARE the manga's scanlators, so the lister is the atsumaru
// source adapter used for its Groups capability with the manga URL; the scan
// ID is not needed for listing.
func NewAtsumaruGroupLister(mangaURL string) domain.GroupLister {
	return NewAtsumaru(mangaURL, "", "").(domain.GroupLister)
}

func (a *atsumaru) String() string {
	return "Atsumaru"
}

func (a *atsumaru) ValidateInput() error {
	if len(a.MangaURL) == 0 {
		return fmt.Errorf("atsumaru manga URL is required")
	}

	if len(a.ScanID) == 0 && a.QualityProfile == "" {
		return fmt.Errorf("atsumaru scan ID is required")
	}

	parsed, err := url.Parse(a.MangaURL)
	if err != nil {
		return fmt.Errorf("parsing URL %s: %w", a.MangaURL, err)
	}

	if parsed.Scheme != "https" || strings.ToLower(parsed.Host) != "atsu.moe" {
		return fmt.Errorf("the URL for Atsumaru must start with %s", atsumaruURL)
	}

	if _, err := a.extractMangaID(); err != nil {
		return err
	}

	return nil
}

func (a *atsumaru) Discover(ctx context.Context) (domain.Manga, error) {
	mangaID, err := a.extractMangaID()
	if err != nil {
		return domain.Manga{}, err
	}

	apiURL, err := a.apiURL("api/manga/info", url.Values{"mangaId": []string{mangaID}})
	if err != nil {
		return domain.Manga{}, err
	}

	var mangaResp atsumaruMangaResponse
	if err := a.getJSON(ctx, apiURL, &mangaResp); err != nil {
		return domain.Manga{}, fmt.Errorf("getting manga info from %s: %w", apiURL, err)
	}

	// Load the scanlator cache now so the group resolver bridge can map the
	// chapters' scoped ScanIDs to canonical groups by name at resolve time
	// (resolve.Resolve is pure and runs after Discover). A page-fetch failure
	// is non-fatal: chapters still download, and resolution degrades to
	// unresolvable instead of blocking the manga.
	if err := a.loadScanlators(ctx); err != nil {
		_ = err
	}

	if len(mangaResp.Title) == 0 {
		return domain.Manga{}, fmt.Errorf("getting manga for ID %s", mangaID)
	}

	manga := domain.Manga{
		ID:       mangaResp.ID,
		URL:      a.MangaURL,
		Title:    sanitize.Filename(mangaResp.Title),
		Chapters: make(map[domain.ChapterNumber]domain.Chapter),
		IsManhwa: mangaResp.ForceStrip,
	}
	if manga.ID == "" {
		manga.ID = mangaID
	}

	// Pinned group mode (no qualityProfile): keep only the selected group's
	// chapters, as before. Profile-driven mode: include chapters from EVERY
	// scanlator (each row keeps its own ScanID as the Chapter.Group) so the
	// greedy resolver can choose a group by profile preference. The chapter
	// map is keyed by number; when several scanlators translate the same
	// number, the last row the API returned wins the map slot.
	for _, chapter := range mangaResp.Chapters {
		if a.QualityProfile == "" && chapter.ScanID != a.ScanID {
			continue
		}

		manga.Chapters[chapter.Number] = domain.Chapter{
			ID:     chapter.ID,
			Number: chapter.Number,
			Title:  sanitize.Filename(chapter.Title),
			Group:  chapter.ScanID,
		}
	}

	if len(manga.Chapters) == 0 {
		return domain.Manga{}, fmt.Errorf("getting chapters for manga %s", manga.Title)
	}

	return manga, nil
}

func (a *atsumaru) Pages(ctx context.Context, chapter domain.Chapter) ([]domain.ImageInfo, error) {
	mangaID, err := a.extractMangaID()
	if err != nil {
		return nil, err
	}

	apiURL, err := a.apiURL("api/read/chapter", url.Values{
		"mangaId":   []string{mangaID},
		"chapterId": []string{chapter.ID},
	})
	if err != nil {
		return nil, err
	}

	var chapterResp atsumaruReadChapterResponse
	if err := a.getJSON(ctx, apiURL, &chapterResp); err != nil {
		return nil, fmt.Errorf("getting chapter pages from %s: %w", apiURL, err)
	}

	imageInfos := make([]domain.ImageInfo, 0, len(chapterResp.ReadChapter.Pages))
	for _, page := range chapterResp.ReadChapter.Pages {
		imageURL, err := a.resolveImageURL(page.Image)
		if err != nil {
			return nil, fmt.Errorf("resolving image URL %s: %w", page.Image, err)
		}

		imageInfos = append(imageInfos, domain.ImageInfo{ImageURL: imageURL})
	}

	if len(imageInfos) == 0 {
		return nil, fmt.Errorf("getting image URLs for chapter ID %s", chapter.ID)
	}

	return imageInfos, nil
}

// Groups lists the manga's scanlation groups from the manga page endpoint
// (mangaPage.scanlators), cached for reuse. Atsumaru has no global group
// catalog: a scan ID is the ScanID a manga's chapters carry and maps to the
// manga's own translators, so the manga URL is required input. query filters
// the scanlators client-side (case-insensitive substring on name); the
// returned order is the reader's order (primary translator first).
func (a *atsumaru) Groups(ctx context.Context, query string) ([]domain.ScanlationGroup, error) {
	if a.MangaURL == "" {
		return nil, fmt.Errorf("listing Atsumaru groups requires a manga URL")
	}
	if err := a.loadScanlators(ctx); err != nil {
		return nil, err
	}

	groups := make([]domain.ScanlationGroup, 0, len(a.Scanlators))
	for _, scanlator := range a.Scanlators {
		if query != "" && !strings.Contains(strings.ToLower(scanlator.Name), strings.ToLower(query)) {
			continue
		}
		groups = append(groups, scanlator)
	}

	return groups, nil
}

// loadScanlators fetches the manga page's scanlation groups once and caches
// them (scoped ScanID -> stable name) for group listing and the resolver
// bridge. It is shared by Groups and Discover; repeated calls reuse the
// cache.
func (a *atsumaru) loadScanlators(ctx context.Context) error {
	if a.Scanlators != nil {
		return nil
	}

	mangaID, err := a.extractMangaID()
	if err != nil {
		return fmt.Errorf("loading Atsumaru scanlators: %w", err)
	}

	apiURL, err := a.apiURL("api/manga/page", url.Values{"id": []string{mangaID}})
	if err != nil {
		return fmt.Errorf("loading Atsumaru scanlators: %w", err)
	}

	var pageResp atsumaruMangaPageResponse
	if err := a.getJSON(ctx, apiURL, &pageResp); err != nil {
		return fmt.Errorf("loading Atsumaru scanlators from %s: %w", apiURL, err)
	}

	scanlators := make([]domain.ScanlationGroup, 0, len(pageResp.MangaPage.Scanlators))
	for _, scanlator := range pageResp.MangaPage.Scanlators {
		if scanlator.ID == "" || scanlator.Name == "" {
			continue
		}
		scanlators = append(scanlators, domain.ScanlationGroup{ID: scanlator.ID, Name: scanlator.Name})
	}
	a.Scanlators = scanlators

	return nil
}

// scanlatorName returns the stable scanlation-group name for a manga-scoped
// ScanID from the cached manga page, or "" when the id is not one of the
// manga's scanlators.
func (a *atsumaru) scanlatorName(scopedID string) string {
	for _, scanlator := range a.Scanlators {
		if scanlator.ID == scopedID {
			return scanlator.Name
		}
	}
	return ""
}

// ResolveNativeGroup implements the per-source resolver extension
// (domain.NativeResolver) with GREEDY multi-candidate selection. An
// atsumaru chapter number is scoped to one ScanID, but several of the
// manga's scanlators can translate the same number, so the single chapter
// id cannot pick a group; the manga's cached scanlator NAMES are the
// candidates. The resolver walks the profile's preferredGroups in order and
// returns the canonical UUID of the earliest preferred ref (alias or UUID)
// whose canonical is also one of the manga's scanlators; a nil/unknown
// nativeGroup is ignored. No preferred match returns "" so resolve applies
// its unlisted-group policy (ignoredGroups still checked by resolve).
func (a *atsumaru) ResolveNativeGroup(groups *domain.GroupRegistry, profile *domain.Profile, nativeGroup string) string {
	if profile == nil || len(a.Scanlators) == 0 {
		return ""
	}

	for _, preferredRef := range profile.PreferredGroups {
		canonical := groupregistry.ResolveGroupID(groups, preferredRef)
		if canonical == "" {
			continue
		}
		if a.hasScanlatorNamed(groups, canonical) {
			return canonical
		}
	}

	return ""
}

// hasScanlatorNamed reports whether any of the manga's cached scanlator
// names resolves to the canonical group UUID.
func (a *atsumaru) hasScanlatorNamed(groups *domain.GroupRegistry, canonical string) bool {
	for _, scanlator := range a.Scanlators {
		if groupregistry.ResolveGroupID(groups, scanlator.Name) == canonical {
			return true
		}
	}
	return false
}

func (a *atsumaru) extractMangaID() (string, error) {
	parsed, err := url.Parse(a.MangaURL)
	if err != nil {
		return "", fmt.Errorf("parsing URL %s: %w", a.MangaURL, err)
	}

	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "manga" || parts[1] == "" {
		return "", fmt.Errorf("invalid Atsumaru manga URL path")
	}

	return parts[1], nil
}

func (a *atsumaru) apiURL(path string, params url.Values) (string, error) {
	u, err := url.JoinPath(a.BaseURL, path)
	if err != nil {
		return "", fmt.Errorf("building URL: %w", err)
	}

	parsed, err := url.Parse(u)
	if err != nil {
		return "", fmt.Errorf("parsing URL %s: %w", u, err)
	}

	parsed.RawQuery = params.Encode()
	return parsed.String(), nil
}

func (a *atsumaru) getJSON(ctx context.Context, rawURL string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", "mangarr")

	retryErr := retry.Do(func() error {
		resp, err := sharedhttp.ExecRequest(*a.Client, req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			return retry.Unrecoverable(fmt.Errorf("decoding response: %w", err))
		}

		return nil
	},
		sharedhttp.RetryOptions(ctx)...,
	)
	if retryErr != nil {
		return fmt.Errorf("executing request %s: %w", req.URL, retryErr)
	}

	return nil
}

func (a *atsumaru) resolveImageURL(rawImageURL string) (string, error) {
	base, err := url.Parse(a.BaseURL)
	if err != nil {
		return "", err
	}

	imageURL, err := url.Parse(rawImageURL)
	if err != nil {
		return "", err
	}

	resolved := base.ResolveReference(imageURL)
	// Image content is served from the CDN host: the chapter API resolves page
	// URLs to atsu.moe, whose /static/pages/... paths answer 410 while the same
	// content on cdn.atsu.moe returns 200. Mirror keiyoushi's host rewrite
	// (https://atsu.moe/... -> https://cdn.atsu.moe/...) leaving the path
	// untouched; any other host passes through unchanged.
	if strings.ToLower(resolved.Hostname()) == "atsu.moe" {
		resolved.Scheme = "https"
		resolved.Host = "cdn." + resolved.Host
	}

	return resolved.String(), nil
}
