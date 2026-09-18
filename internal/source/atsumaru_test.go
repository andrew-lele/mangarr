package source

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mangarr/internal/domain"
	"mangarr/internal/resolve"

	groupregistry "mangarr/internal/registry"

	"github.com/stretchr/testify/require"
)

func TestAtsumaruValidateInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:  "valid URL",
			input: "https://atsu.moe/manga/Q5Mqy",
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: "atsumaru manga URL is required",
		},
		{
			name:    "raw ID rejected",
			input:   "Q5Mqy",
			wantErr: "the URL for Atsumaru must start with https://atsu.moe",
		},
		{
			name:    "wrong host",
			input:   "https://example.com/manga/Q5Mqy",
			wantErr: "the URL for Atsumaru must start with https://atsu.moe",
		},
		{
			name:    "wrong path",
			input:   "https://atsu.moe/title/Q5Mqy",
			wantErr: "invalid Atsumaru manga URL path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := NewAtsumaru(tt.input, "scan-1", "")
			err := src.ValidateInput()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestAtsumaruValidateInputScanID(t *testing.T) {
	t.Parallel()

	// Without a quality profile a scan ID remains required (back-compat).
	src := NewAtsumaru("https://atsu.moe/manga/Q5Mqy", "", "")
	require.EqualError(t, src.ValidateInput(), "atsumaru scan ID is required")

	// A quality-profile-driven manga may omit the pinned group entirely.
	src = NewAtsumaru("https://atsu.moe/manga/Q5Mqy", "", "Preferred Scanlators")
	require.NoError(t, src.ValidateInput())
}

func TestAtsumaruDiscover(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/manga/info":
			require.Equal(t, "Q5Mqy", r.URL.Query().Get("mangaId"))

			fmt.Fprint(w, `{
				"id": "Q5Mqy",
				"title": "Kagurabachi",
				"forceStrip": true,
				"chapters": [
					{"id": "chapter-0", "title": "Chapter 0", "number": 0, "scanId": "scan-1"},
					{"id": "chapter-7-1", "title": "Chapter 7.1", "number": 7.1, "scanId": "scan-1"},
					{"id": "chapter-7-1-other", "title": "Chapter 7.1", "number": 7.1, "scanId": "scan-2"}
				]
			}`)
		case "/api/manga/page":
			require.Equal(t, "Q5Mqy", r.URL.Query().Get("id"))
			fmt.Fprint(w, `{"mangaPage":{"scanlators":[{"id":"scan-1","name":"Alpha"}]}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	src := newTestAtsumaru(server.URL + "/manga/Q5Mqy")

	manga, err := src.Discover(t.Context())
	require.NoError(t, err)
	require.Equal(t, "Q5Mqy", manga.ID)
	require.Equal(t, server.URL+"/manga/Q5Mqy", manga.URL)
	require.Equal(t, "Kagurabachi", manga.Title)
	require.True(t, manga.IsManhwa)
	require.Len(t, manga.Chapters, 2)
	// Discovery loads the scanlator cache that feeds the group resolver
	// bridge: scoped ScanIDs map to stable names.
	require.Equal(t, []domain.ScanlationGroup{{ID: "scan-1", Name: "Alpha"}}, src.Scanlators)

	ch0, ok := manga.Chapters[mustChapterNumber("0")]
	require.True(t, ok)
	require.Equal(t, "chapter-0", ch0.ID)
	require.Equal(t, "Chapter 0", ch0.Title)
	require.Equal(t, "scan-1", ch0.Group)

	ch71, ok := manga.Chapters[mustChapterNumber("7.1")]
	require.True(t, ok)
	require.Equal(t, "chapter-7-1", ch71.ID)
	require.Equal(t, "Chapter 7.1", ch71.Title)
	require.Equal(t, "scan-1", ch71.Group)
}

func TestAtsumaruDiscoverErrorsWhenScanIDHasNoChapters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/manga/info":
			fmt.Fprint(w, `{
				"id": "Q5Mqy",
				"title": "Kagurabachi",
				"chapters": [
					{"id": "chapter-1", "title": "Chapter 1", "number": 1, "scanId": "scan-2"}
				]
			}`)
		case "/api/manga/page":
			fmt.Fprint(w, `{"mangaPage":{"scanlators":[{"id":"scan-2","name":"Delta"}]}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	src := newTestAtsumaru(server.URL + "/manga/Q5Mqy")

	_, err := src.Discover(t.Context())
	require.EqualError(t, err, "getting chapters for manga Kagurabachi")
}

func TestAtsumaruDiscoverErrorsWhenNoChapters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/manga/info":
			fmt.Fprint(w, `{"id":"Q5Mqy","title":"Kagurabachi","chapters":[]}`)
		case "/api/manga/page":
			fmt.Fprint(w, `{"mangaPage":{"scanlators":[]}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	src := newTestAtsumaru(server.URL + "/manga/Q5Mqy")

	_, err := src.Discover(t.Context())
	require.EqualError(t, err, "getting chapters for manga Kagurabachi")
}

func TestAtsumaruResolveImageURLUsesCDN(t *testing.T) {
	t.Parallel()

	src := newTestAtsumaru("https://atsu.moe/manga/Q5Mqy")

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "relative path", raw: "/static/pages/p1/c1/x.avif", want: "https://cdn.atsu.moe/static/pages/p1/c1/x.avif"},
		{name: "absolute atsu.moe", raw: "https://atsu.moe/static/pages/p1/c1/x.avif", want: "https://cdn.atsu.moe/static/pages/p1/c1/x.avif"},
		{name: "protocol-relative", raw: "//atsu.moe/static/pages/p1/c1/x.avif", want: "https://cdn.atsu.moe/static/pages/p1/c1/x.avif"},
		{name: "http scheme", raw: "http://atsu.moe/static/pages/p1/c1/x.avif", want: "https://cdn.atsu.moe/static/pages/p1/c1/x.avif"},
		{name: "query preserved", raw: "/static/pages/p1/c1/x.avif?token=abc123", want: "https://cdn.atsu.moe/static/pages/p1/c1/x.avif?token=abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := src.resolveImageURL(tt.raw)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestAtsumaruResolveImageURLKeepsOtherHosts(t *testing.T) {
	t.Parallel()

	src := newTestAtsumaru("https://atsu.moe/manga/Q5Mqy")

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "already CDN host", raw: "https://cdn.atsu.moe/static/pages/p1/c1/x.avif", want: "https://cdn.atsu.moe/static/pages/p1/c1/x.avif"},
		{name: "foreign host", raw: "https://s3.amazonaws.com/static/pages/p1/c1/x.avif", want: "https://s3.amazonaws.com/static/pages/p1/c1/x.avif"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := src.resolveImageURL(tt.raw)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestAtsumaruPages(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/read/chapter", r.URL.Path)
		require.Equal(t, "Q5Mqy", r.URL.Query().Get("mangaId"))
		require.Equal(t, "yzmwX4", r.URL.Query().Get("chapterId"))

		fmt.Fprint(w, `{
			"readChapter": {
				"id": "yzmwX4",
				"title": "Chapter 121",
				"pages": [
					{"image": "/static/pages/yzmwX4/0.webp"},
					{"image": "https://cdn.atsu.test/static/pages/yzmwX4/1.webp"}
				]
			}
		}`)
	}))
	defer server.Close()

	src := newTestAtsumaru(server.URL + "/manga/Q5Mqy")
	chapter := domain.Chapter{
		ID:     "yzmwX4",
		Number: mustChapterNumber("121"),
	}

	pages, err := src.Pages(t.Context(), chapter)
	require.NoError(t, err)
	require.Len(t, pages, 2)
	require.Equal(t, server.URL+"/static/pages/yzmwX4/0.webp", pages[0].ImageURL)
	require.Equal(t, "https://cdn.atsu.test/static/pages/yzmwX4/1.webp", pages[1].ImageURL)
}

func TestAtsumaruPagesErrorsWhenNoPages(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"readChapter":{"id":"yzmwX4","title":"Chapter 121","pages":[]}}`)
	}))
	defer server.Close()

	src := newTestAtsumaru(server.URL + "/manga/Q5Mqy")
	chapter := domain.Chapter{
		ID:     "yzmwX4",
		Number: mustChapterNumber("121"),
	}

	_, err := src.Pages(t.Context(), chapter)
	require.EqualError(t, err, "getting image URLs for chapter ID yzmwX4")
}

func TestAtsumaruGroupsListsScanlators(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/manga/page", r.URL.Path)
		require.Equal(t, "Q5Mqy", r.URL.Query().Get("id"))

		fmt.Fprint(w, `{
			"mangaPage": {
				"id": "Q5Mqy",
				"title": "Kagurabachi",
				"scanlators": [
					{"id": "scan-1", "name": "O TRANSLATIONS"},
					{"id": "scan-2", "name": "Lagoon Scans"}
				]
			}
		}`)
	}))
	defer server.Close()

	src := newTestAtsumaru(server.URL + "/manga/Q5Mqy")

	groups, err := src.Groups(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, groups, 2)
	require.Equal(t, "scan-1", groups[0].ID)
	require.Equal(t, "O TRANSLATIONS", groups[0].Name)
	require.Equal(t, "scan-2", groups[1].ID)
	require.Equal(t, "Lagoon Scans", groups[1].Name)
}

func TestAtsumaruGroupsFiltersByQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{
			"mangaPage": {
				"scanlators": [
					{"id": "scan-1", "name": "Lagoon Scans"},
					{"id": "scan-2", "name": "O TRANSLATIONS"}
				]
			}
		}`)
	}))
	defer server.Close()

	src := newTestAtsumaru(server.URL + "/manga/Q5Mqy")

	groups, err := src.Groups(t.Context(), "lagoon")
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, "scan-1", groups[0].ID)
	require.Equal(t, "Lagoon Scans", groups[0].Name)
}

func TestAtsumaruGroupsRequiresMangaURL(t *testing.T) {
	t.Parallel()

	src := NewAtsumaruGroupLister("")

	_, err := src.Groups(t.Context(), "")
	require.EqualError(t, err, "listing Atsumaru groups requires a manga URL")
}

func TestAtsumaruResolveNativeGroupGreedyAcrossScanlators(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"mangaPage":{"scanlators":[
			{"id":"scoped-asura","name":"Asura"},
			{"id":"scoped-webtoon","name":"Webtoon"}
		]}}`)
	}))
	defer server.Close()

	src := newTestAtsumaru(server.URL + "/manga/Q5Mqy")
	require.NoError(t, src.loadScanlators(t.Context()))

	groups := writeAtsumaruGroups(t, `version: 1

groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "Asura" ]
  b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c:
    aliases: [ "Webtoon" ]
`)

	asuraFirst := &domain.Profile{PreferredGroups: []string{"Asura", "Webtoon"}}
	// The chapter's scoped ScanID is per-manga, so the manga's scanlator
	// NAMES are the candidates: profile order picks Asura even though the
	// chapter's own id is webtoon-scoped.
	require.Equal(t, "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b", src.ResolveNativeGroup(&groups, asuraFirst, "scoped-webtoon"))
	require.Equal(t, "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b", src.ResolveNativeGroup(&groups, asuraFirst, "ignored-id"))

	// Webtoon first in preferred order -> Webtoon canonical wins.
	webtoonFirst := &domain.Profile{PreferredGroups: []string{"Webtoon", "Asura"}}
	require.Equal(t, "b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c", src.ResolveNativeGroup(&groups, webtoonFirst, "scoped-asura"))

	// Earliest preferred that is actually present wins: Flame (absent) is
	// skipped, Asura (present) wins.
	flameFirst := &domain.Profile{PreferredGroups: []string{"Flame", "Asura"}}
	require.Equal(t, "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b", src.ResolveNativeGroup(&groups, flameFirst, ""))

	// No preferred match -> "" so resolve applies the unlisted policy.
	none := &domain.Profile{PreferredGroups: []string{"Flame"}}
	require.Equal(t, "", src.ResolveNativeGroup(&groups, none, "scoped-asura"))

	// A nil profile resolves nothing; an empty cache resolves nothing.
	require.Equal(t, "", src.ResolveNativeGroup(&groups, nil, "scoped-asura"))
	empty := newTestAtsumaru(server.URL + "/manga/Q5Mqy")
	require.Equal(t, "", empty.ResolveNativeGroup(&groups, asuraFirst, "scoped-asura"))
}

func TestAtsumaruDecisionPreferredThroughRealResolver(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"mangaPage":{"scanlators":[{"id":"scoped-asura","name":"Asura"}]}}`)
	}))
	defer server.Close()

	src := newTestAtsumaru(server.URL + "/manga/Q5Mqy")
	require.NoError(t, src.loadScanlators(t.Context()))

	groups := writeAtsumaruGroups(t, `version: 1

groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "Asura" ]
`)
	profiles := writeAtsumaruProfiles(t, `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Preferred Scanlators"
    preferredGroups: [ "Asura" ]
    ignoredGroups: [ ]
    fallback: "any"
`, &groups)
	resolver := func(groups *domain.GroupRegistry, profile *domain.Profile, nativeGroup string) string {
		return src.ResolveNativeGroup(groups, profile, nativeGroup)
	}
	decision := resolve.ResolveWithResolver(&groups, &profiles, "atsumaru", "scoped-asura", "f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f", resolver)
	require.Equal(t, domain.OutcomePreferred, decision.Outcome)
	require.Equal(t, 0, decision.PreferredIndex)
	require.Equal(t, "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b", decision.CanonicalID)

	// Without the extension the scoped id never resolves.
	decision = resolve.ResolveWithResolver(&groups, &profiles, "atsumaru", "scoped-asura", "f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f", nil)
	require.Equal(t, domain.OutcomeUnknown, decision.Outcome)
	require.Equal(t, "", decision.CanonicalID)
}

func TestAtsumaruDiscoverProfileDrivenIncludesEveryScanlator(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/manga/info":
			fmt.Fprint(w, `{
				"id": "Q5Mqy",
				"title": "Kagurabachi",
				"chapters": [
					{"id": "chapter-1-a", "title": "Chapter 1", "number": 1, "scanId": "scan-1"},
					{"id": "chapter-1-b", "title": "Chapter 1", "number": 1, "scanId": "scan-2"},
					{"id": "chapter-2-a", "title": "Chapter 2", "number": 2, "scanId": "scan-1"}
				]
			}`)
		case "/api/manga/page":
			fmt.Fprint(w, `{"mangaPage":{"scanlators":[{"id":"scan-1","name":"Alpha"},{"id":"scan-2","name":"Delta"}]}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	// Profile-driven discovery drops the pinned-ScanID filter, so chapters
	// from every scanlator are discovered. The map is keyed by number: two
	// scanlators translating chapter 1 means the LAST row the API returned
	// wins that slot (documented limitation).
	src := newTestAtsumaru(server.URL + "/manga/Q5Mqy")
	src.QualityProfile = "Preferred Scanlators"
	manga, err := src.Discover(t.Context())
	require.NoError(t, err)
	require.Len(t, manga.Chapters, 2)
	require.Equal(t, "scan-2", manga.Chapters[mustChapterNumber("1")].Group)
	require.Equal(t, "scan-1", manga.Chapters[mustChapterNumber("2")].Group)
}

func newTestAtsumaru(mangaURL string) *atsumaru {
	return &atsumaru{
		MangaURL: mangaURL,
		ScanID:   "scan-1",
		BaseURL:  strings.TrimSuffix(mangaURL, "/manga/Q5Mqy"),
		Client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func writeAtsumaruGroups(t *testing.T, groupsYAML string) domain.GroupRegistry {
	t.Helper()

	root := t.TempDir()
	writeFile(t, root, groupregistry.GroupsFileName, groupsYAML)

	groups, err := groupregistry.LoadGroups(root)
	require.NoError(t, err)
	return groups
}

func writeAtsumaruProfiles(t *testing.T, profilesYAML string, groups *domain.GroupRegistry) domain.ProfileRegistry {
	t.Helper()

	root := t.TempDir()
	writeFile(t, root, groupregistry.ProfilesFileName, profilesYAML)

	profiles, err := groupregistry.LoadProfiles(root, groups)
	require.NoError(t, err)
	return profiles
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
