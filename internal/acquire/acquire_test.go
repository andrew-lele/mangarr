package acquire

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mangarr/internal/domain"
	"mangarr/internal/registry"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

const (
	cpGroupID       = "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b"
	novaGroupID     = "b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c"
	profileID       = "f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f"
	comixCPNative   = "9641"
	comixNovaNative = "4725"
)

const testGroupsYAML = `version: 1

groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "CP" ]
    sources:
      comix: "9641"

  b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c:
    aliases: [ "Nova" ]
    sources:
      comix: "4725"
`

const testProfilesYAML = `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Comix Preferred"
    preferredGroups: [ "CP" ]
    ignoredGroups: [ "b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c" ]
    fallback: "any"
`

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeRegistry writes groups.yaml and profiles.yaml into a temp dir and
// loads both registries through the production loaders.
func writeRegistry(t *testing.T) (domain.GroupRegistry, domain.ProfileRegistry) {
	t.Helper()

	root := t.TempDir()
	writeFile(t, root, registry.GroupsFileName, testGroupsYAML)
	writeFile(t, root, registry.ProfilesFileName, testProfilesYAML)

	groups, err := registry.LoadGroups(root)
	require.NoError(t, err)
	profiles, err := registry.LoadProfiles(root, &groups)
	require.NoError(t, err)
	return groups, profiles
}

func TestChapterDownloadsAndThenSkipsExistingArchive(t *testing.T) {
	t.Parallel()

	var imageBytes bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.Black)
	require.NoError(t, png.Encode(&imageBytes, img))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBytes.Bytes())
	}))
	defer server.Close()

	source := &pageSource{pages: []domain.ImageInfo{{ImageURL: server.URL}}}
	request := Request{
		Source: source,
		Manga: domain.Manga{
			Title: "Original Title",
		},
		Chapter: domain.Chapter{
			Number: mustChapterNumber("7.1"),
			Title:  "The Chapter",
		},
		DownloadDirectory: t.TempDir(),
		NamingTemplate:    "{manga:<.>} Ch. {num}{title: - <.>}",
		TitleOverride:     "Replacement: Title",
	}

	result, err := Chapter(t.Context(), zerolog.Nop(), request)
	require.NoError(t, err)
	require.Equal(t, Downloaded, result.Status)
	require.Equal(t, "Replacement Title Ch. 7.1 - The Chapter", result.Name)
	require.Equal(t, filepath.Join(request.DownloadDirectory, "Replacement Title", "Replacement Title Ch. 7.1 - The Chapter.cbz"), result.Path)
	require.Equal(t, 1, source.calls)

	archive, err := zip.OpenReader(result.Path)
	require.NoError(t, err)
	require.Len(t, archive.File, 1)
	require.NoError(t, archive.Close())

	result, err = Chapter(t.Context(), zerolog.Nop(), request)
	require.NoError(t, err)
	require.Equal(t, Skipped, result.Status)
	require.Equal(t, 1, source.calls, "skip must not resolve pages")
}

func TestChapterDryRunResolvesGroupDecision(t *testing.T) {
	t.Parallel()

	groups, profiles := writeRegistry(t)

	source := &pageSource{pages: []domain.ImageInfo{{ImageURL: "http://example.invalid/page.png"}}}
	request := Request{
		Source:            source,
		SourceKey:         "comix",
		Manga:             domain.Manga{Title: "Original Title"},
		Chapter:           domain.Chapter{Number: mustChapterNumber("7.1"), Title: "The Chapter", Group: comixCPNative},
		DownloadDirectory: t.TempDir(),
		NamingTemplate:    "{manga:<.>} Ch. {num}{title: - <.>}",
		TitleOverride:     "Replacement: Title",
		DryRun:            true,
		Groups:            &groups,
		Profiles:          &profiles,
		ProfileRef:        profileID,
	}

	result, err := Chapter(t.Context(), zerolog.Nop(), request)
	require.NoError(t, err)
	require.Equal(t, DryRun, result.Status)
	require.Equal(t, cpGroupID, result.Decision.CanonicalID, "native id resolves to the preferred canonical group")
	require.Equal(t, domain.OutcomePreferred, result.Decision.Outcome)
	require.Equal(t, 0, result.Decision.PreferredIndex)

	// An ignored native id is rejected with the canonical id attached.
	request.Chapter.Group = comixNovaNative
	result, err = Chapter(t.Context(), zerolog.Nop(), request)
	require.NoError(t, err)
	require.Equal(t, DryRun, result.Status)
	require.Equal(t, novaGroupID, result.Decision.CanonicalID)
	require.Equal(t, domain.OutcomeIgnored, result.Decision.Outcome)

	// An unknown source key (monitoredManga.source not in groups.yaml) cannot
	// resolve the native id; the outcome falls back to the profile policy.
	request.SourceKey = "nope"
	request.Chapter.Group = comixCPNative
	result, err = Chapter(t.Context(), zerolog.Nop(), request)
	require.NoError(t, err)
	require.Equal(t, "", result.Decision.CanonicalID)
	require.Equal(t, domain.OutcomeUnknown, result.Decision.Outcome)

	// Without a profile reference the decision stays empty, so dry-run logs
	// omit the [group=... decision=...] suffix entirely.
	request.ProfileRef = ""
	result, err = Chapter(t.Context(), zerolog.Nop(), request)
	require.NoError(t, err)
	require.Equal(t, "", result.Decision.CanonicalID)
}

func TestChapterDryRunReportsWouldBeArchiveWithoutDownloading(t *testing.T) {
	t.Parallel()

	source := &pageSource{pages: []domain.ImageInfo{{ImageURL: "http://example.invalid/page.png"}}}
	request := Request{
		Source: source,
		Manga: domain.Manga{
			Title: "Original Title",
		},
		Chapter: domain.Chapter{
			Number: mustChapterNumber("7.1"),
			Title:  "The Chapter",
		},
		DownloadDirectory: t.TempDir(),
		NamingTemplate:    "{manga:<.>} Ch. {num}{title: - <.>}",
		TitleOverride:     "Replacement: Title",
		DryRun:            true,
	}

	result, err := Chapter(t.Context(), zerolog.Nop(), request)
	require.NoError(t, err)
	require.Equal(t, DryRun, result.Status)
	require.Equal(t, "Replacement Title Ch. 7.1 - The Chapter", result.Name)
	require.Equal(t, filepath.Join(request.DownloadDirectory, "Replacement Title", "Replacement Title Ch. 7.1 - The Chapter.cbz"), result.Path)
	require.Equal(t, 0, source.calls, "dry run must not resolve pages")
	if _, err := os.Stat(result.Path); err == nil {
		t.Fatal("dry run must not create the archive")
	}
}

func TestChapterReturnsPageResolutionErrorWithoutPublishingArchive(t *testing.T) {
	t.Parallel()

	downloadDirectory := t.TempDir()
	request := Request{
		Source:            &pageSource{err: context.Canceled},
		Manga:             domain.Manga{Title: "Title"},
		Chapter:           domain.Chapter{Number: mustChapterNumber("2")},
		DownloadDirectory: downloadDirectory,
		NamingTemplate:    "Chapter {num}",
	}

	result, err := Chapter(t.Context(), zerolog.Nop(), request)
	require.ErrorIs(t, err, context.Canceled)
	require.NoFileExists(t, result.Path)

	entries, readErr := os.ReadDir(downloadDirectory)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func TestForcedReplacementFailurePreservesArchive(t *testing.T) {
	for _, failure := range []string{"pages", "fetch", "assembly", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failure == "cancelled" {
					cancel()
					<-r.Context().Done()
					return
				}
				if failure == "fetch" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write([]byte("invalid image data"))
			}))
			defer server.Close()
			source := &pageSource{pages: []domain.ImageInfo{{ImageURL: server.URL}}}
			if failure == "pages" {
				source.err = context.Canceled
			}
			request := Request{
				Source: source, Manga: domain.Manga{Title: "Fixture"},
				Chapter:           domain.Chapter{Number: mustChapterNumber("1")},
				DownloadDirectory: t.TempDir(), NamingTemplate: "{num}", Force: true,
			}
			path := filepath.Join(request.DownloadDirectory, "Fixture", "1.cbz")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte("original archive"), 0o644))
			_, err := Chapter(ctx, zerolog.Nop(), request)
			require.Error(t, err)
			if failure == "cancelled" || failure == "pages" {
				require.ErrorIs(t, err, context.Canceled)
			}
			require.Equal(t, 1, source.calls)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, "original archive", string(data))
			entries, err := os.ReadDir(filepath.Dir(path))
			require.NoError(t, err)
			require.Len(t, entries, 1, "failed replacement must not leave temporary archives")
		})
	}
}

type pageSource struct {
	pages []domain.ImageInfo
	err   error
	calls int
}

func mustChapterNumber(input string) domain.ChapterNumber {
	number, err := domain.ParseChapterNumber(input)
	if err != nil {
		panic(err)
	}
	return number
}

func (s *pageSource) Pages(context.Context, domain.Chapter) ([]domain.ImageInfo, error) {
	s.calls++
	return s.pages, s.err
}

func TestChapterSkipsSameNumberWithDifferentTitle(t *testing.T) {
	t.Parallel()

	server := pngServer(t)
	defer server.Close()

	source := &pageSource{pages: []domain.ImageInfo{{ImageURL: server.URL}}}
	base := Request{
		Source:            source,
		Manga:             domain.Manga{Title: "Blue Lock"},
		DownloadDirectory: t.TempDir(),
		NamingTemplate:    "{manga:<.>} Ch. {num:3}{title: - <.>}",
	}

	// First flow: source-pinned discovery carries no chapter title.
	first := base
	first.Chapter = domain.Chapter{Number: mustChapterNumber("361")}
	result, err := Chapter(t.Context(), zerolog.Nop(), first)
	require.NoError(t, err)
	require.Equal(t, Downloaded, result.Status)
	require.NoFileExists(t, filepath.Join(first.DownloadDirectory, "Blue Lock", "Blue Lock Ch. 361 - Chapter 361.cbz"))

	// Second flow (title+profile): the same chapter number now carries the
	// "Chapter 361" title -> a DIFFERENT rendered filename. The skip must
	// still fire because the chapter NUMBER is already present.
	second := base
	second.Chapter = domain.Chapter{Number: mustChapterNumber("361"), Title: "Chapter 361"}
	result, err = Chapter(t.Context(), zerolog.Nop(), second)
	require.NoError(t, err)
	require.Equal(t, Skipped, result.Status)
	require.Equal(t, 1, source.calls, "skip must not resolve pages")

	entries, err := os.ReadDir(filepath.Join(second.DownloadDirectory, "Blue Lock"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "the duplicate title variant must not be created")
}

func TestChapterSkipsDecimalSameNumberWithDifferentTitle(t *testing.T) {
	t.Parallel()

	server := pngServer(t)
	defer server.Close()

	source := &pageSource{pages: []domain.ImageInfo{{ImageURL: server.URL}}}
	base := Request{
		Source:            source,
		Manga:             domain.Manga{Title: "Blue Lock"},
		DownloadDirectory: t.TempDir(),
		NamingTemplate:    "{manga:<.>} Ch. {num}{title: - <.>}",
	}

	first := base
	first.Chapter = domain.Chapter{Number: mustChapterNumber("112.5")}
	result, err := Chapter(t.Context(), zerolog.Nop(), first)
	require.NoError(t, err)
	require.Equal(t, Downloaded, result.Status)

	second := base
	second.Chapter = domain.Chapter{Number: mustChapterNumber("112.5"), Title: "Chapter 112.5"}
	result, err = Chapter(t.Context(), zerolog.Nop(), second)
	require.NoError(t, err)
	require.Equal(t, Skipped, result.Status)

	// Force bypasses the number-based skip and re-downloads.
	second.Force = true
	result, err = Chapter(t.Context(), zerolog.Nop(), second)
	require.NoError(t, err)
	require.Equal(t, Downloaded, result.Status)

	entries, err := os.ReadDir(filepath.Join(second.DownloadDirectory, "Blue Lock"))
	require.NoError(t, err)
	require.Len(t, entries, 2, "force replaces by creating the title variant next to the original")
}

func TestArchiveForChapterExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "Blue Lock Ch. 361.cbz", "x")
	writeFile(t, dir, "Blue Lock Ch. 112.5 - Chapter 112.5.cbz", "x")
	writeFile(t, dir, "Blue Lock Ch. 007.cbz", "x") // padded {num:3} form
	writeFile(t, dir, "Blue Lock - 999.cbz", "x")   // no marker: exact-match only

	exact := filepath.Join(dir, "nothing.cbz")
	exists, err := archiveForChapterExists(dir, mustChapterNumber("361"), exact)
	require.NoError(t, err)
	require.True(t, exists)

	exists, err = archiveForChapterExists(dir, mustChapterNumber("112.5"), exact)
	require.NoError(t, err)
	require.True(t, exists)

	// Pad-normalized equality: archive "Ch. 007" covers chapter 7.
	exists, err = archiveForChapterExists(dir, mustChapterNumber("7"), exact)
	require.NoError(t, err)
	require.True(t, exists)

	// Missing chapter and no marker => false.
	exists, err = archiveForChapterExists(dir, mustChapterNumber("8"), exact)
	require.NoError(t, err)
	require.False(t, exists)

	exists, err = archiveForChapterExists(dir, mustChapterNumber("999"), filepath.Join(dir, "Blue Lock - 999.cbz"))
	require.NoError(t, err)
	require.True(t, exists, "no-marker template still matches its exact rendered name")

	// A missing series directory is not present.
	exists, err = archiveForChapterExists(filepath.Join(t.TempDir(), "nope"), mustChapterNumber("1"), exact)
	require.NoError(t, err)
	require.False(t, exists)
}

func pngServer(t *testing.T) *httptest.Server {
	t.Helper()

	var imageBytes bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.Black)
	require.NoError(t, png.Encode(&imageBytes, img))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBytes.Bytes())
	}))
	return server
}
