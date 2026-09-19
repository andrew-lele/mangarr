// Package acquire resolves and stores one manga chapter.
package acquire

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mangarr/internal/domain"
	"mangarr/internal/download"
	"mangarr/internal/files"
	"mangarr/internal/resolve"
	"mangarr/internal/sanitize"
	"mangarr/internal/templater"

	"github.com/rs/zerolog"
)

type Status uint8

const (
	Downloaded Status = iota
	Skipped
	DryRun
)

type PageSource interface {
	Pages(context.Context, domain.Chapter) ([]domain.ImageInfo, error)
}

type Request struct {
	Source            PageSource
	SourceKey         string
	Manga             domain.Manga
	Chapter           domain.Chapter
	DownloadDirectory string
	NamingTemplate    string
	TitleOverride     string
	Force             bool
	DryRun            bool
	Groups            *domain.GroupRegistry
	Profiles          *domain.ProfileRegistry
	ProfileRef        string
	// ResolveNativeGroup is the optional per-source native-id resolver
	// extension (atsumaru: scoped ScanID -> canonical group via the
	// scanlator-name alias bridge). nil keeps the default NativeIndex
	// lookup in resolve.
	ResolveNativeGroup domain.NativeResolver
}

type Result struct {
	Status    Status
	Name      string
	Path      string
	SourceKey string
	Decision  domain.Decision
}

func Chapter(ctx context.Context, log zerolog.Logger, request Request) (Result, error) {
	manga := request.Manga
	if title := sanitize.Filename(request.TitleOverride); title != "" {
		manga.Title = title
	}

	name := templater.New(manga, request.Chapter).ExecTemplate(request.NamingTemplate)
	archiveName := sanitize.Filename(name) + ".cbz"
	archivePath := filepath.Join(request.DownloadDirectory, manga.Title, archiveName)
	result := Result{Name: name, Path: archivePath, SourceKey: request.SourceKey}

	if request.Profiles != nil && request.ProfileRef != "" {
		result.Decision = resolve.ResolveWithResolver(
			request.Groups,
			request.Profiles,
			request.SourceKey,
			request.Chapter.Group,
			request.ProfileRef,
			request.ResolveNativeGroup,
		)
	}

	if request.DryRun {
		// Dry-run: report the would-be archive without resolving pages or
		// touching the network or disk. Sufficient to verify the chapter,
		// group, and naming end-to-end without downloading.
		result.Status = DryRun
		return result, nil
	}

	// A chapter number is considered already present when ANY existing
	// archive in the series directory covers it, not just the exact rendered
	// filename: the same number can render different names across flows
	// (source-pinned vs title+profile) because the chapter Title value
	// differs ("Ch. 361" vs "Ch. 361 - Chapter 361"), which would otherwise
	// download a duplicate archive for the same chapter.
	exists, err := archiveForChapterExists(filepath.Dir(archivePath), request.Chapter.Number, archivePath)
	if err != nil {
		return result, err
	}
	if exists {
		if !request.Force {
			result.Status = Skipped
			return result, nil
		}
	}

	pages, err := request.Source.Pages(ctx, request.Chapter)
	if err != nil {
		return result, fmt.Errorf("getting pages for chapter %s: %w", request.Chapter.Number, err)
	}
	request.Chapter.ImageInfo = pages

	log.Info().Msgf("Downloading %q", name)
	if err := download.Chapter(
		ctx,
		log,
		archivePath,
		request.Chapter,
		manga.IsManhwa,
		func(log zerolog.Logger, sourceDir, cbzPath string, isManhwa bool) error {
			return files.CreateCbzArchive(ctx, log, sourceDir, cbzPath, isManhwa, files.ComicInfo{
				Series: manga.Title,
				Number: request.Chapter.Number.String(),
				Title:  request.Chapter.Title,
			})
		},
	); err != nil {
		return result, fmt.Errorf("downloading chapter %q: %w", name, err)
	}

	result.Status = Downloaded
	return result, nil
}

// archiveChapterPattern extracts the chapter-number token from an archive
// filename rendered by a naming template that uses a "Ch."/"Chapter"
// marker (the standard templates do); the capture tolerates the padded
// ({num:3}) form and decimals ("361.5").
var archiveChapterPattern = regexp.MustCompile(`(?:Ch\.?|Chapter)\s*(\d+(?:\.\d+)?)`)

// archiveForChapterExists reports whether the series directory already holds
// an archive for the given chapter number. Existing archive filenames are
// parsed for their chapter number, so archives whose rendered names differ
// only by a chapter-title suffix ("Ch. 361" vs "Ch. 361 - Chapter 361") or
// by number padding still count as present. Archives rendered from naming
// templates without a "Ch."/"Chapter" marker cannot be matched numerically;
// they fall back to the exact rendered filename (the previous behavior).
func archiveForChapterExists(seriesDir string, number domain.ChapterNumber, exactPath string) (bool, error) {
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading series directory %s: %w", seriesDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".cbz") {
			continue
		}
		matches := archiveChapterPattern.FindStringSubmatch(entry.Name())
		if len(matches) < 2 {
			continue
		}
		existing, err := domain.ParseChapterNumber(matches[1])
		if err != nil {
			continue
		}
		if existing.Equal(number) {
			return true, nil
		}
	}

	// No numerically matching archive: keep the exact-filename check for
	// templates without a number marker.
	if _, err := os.Stat(exactPath); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("checking archive %s: %w", exactPath, err)
	}

	return false, nil
}
