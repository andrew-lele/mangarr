package files

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RetrofitStatus describes what happened to one archive during a retrofit run.
type RetrofitStatus string

const (
	// RetrofitRebuilt marks an archive that received a ComicInfo.xml entry.
	RetrofitRebuilt RetrofitStatus = "rebuilt"
	// RetrofitWouldRebuild marks an archive that would be rebuilt in a dry
	// run; nothing was written.
	RetrofitWouldRebuild RetrofitStatus = "would-rebuild"
	// RetrofitAlreadyHasMetadata marks an archive that already carries
	// ComicInfo.xml and was left untouched.
	RetrofitAlreadyHasMetadata RetrofitStatus = "already-has-comicinfo"
	// RetrofitSkippedNoNumber marks an archive whose filename carries no
	// recognizable chapter number; it was left untouched.
	RetrofitSkippedNoNumber RetrofitStatus = "skipped-no-chapter-number"
)

// RetrofitEntry is the outcome for one archive file.
type RetrofitEntry struct {
	Path   string
	Series string
	Number string
	Title  string
	Pages  int
	Status RetrofitStatus
	Err    error
}

// RetrofitLibrary walks root for .cbz archives and rebuilds every archive
// that lacks a ComicInfo.xml entry, injecting metadata derived from the
// archive's own filename (chapter number, optional title) and its series
// directory (Series). dryRun only inspects and reports; nothing is written.
// Existing image entries are preserved byte-for-byte (same names and
// compression methods) and each archive is replaced atomically in place.
func RetrofitLibrary(ctx context.Context, root string, dryRun bool) ([]RetrofitEntry, error) {
	var entries []RetrofitEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".cbz") {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		entry, err := retrofitFile(ctx, path, dryRun)
		if err != nil {
			// retrofitFile always returns the entry with Path set; surface
			// the failure through the entry's Err so the caller can report
			// it alongside the successful files.
			entry.Err = err
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return entries, fmt.Errorf("walking %s: %w", root, err)
	}
	return entries, nil
}

// retrofitFile inspects one archive and, unless dryRun, rebuilds it with the
// ComicInfo.xml entry it is missing.
func retrofitFile(ctx context.Context, path string, dryRun bool) (RetrofitEntry, error) {
	series := filepath.Base(filepath.Dir(path))
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	entry := RetrofitEntry{Path: path, Series: series}
	number, title, ok := splitChapterMetadata(stem)
	if !ok {
		entry.Status = RetrofitSkippedNoNumber
		return entry, nil
	}
	entry.Number, entry.Title = number, title

	reader, err := zip.OpenReader(path)
	if err != nil {
		return entry, fmt.Errorf("opening archive: %w", err)
	}
	defer reader.Close()

	if err := ctx.Err(); err != nil {
		return entry, err
	}

	for _, f := range reader.File {
		if f.Name == "ComicInfo.xml" {
			entry.Pages = pageCount(reader.File)
			entry.Status = RetrofitAlreadyHasMetadata
			return entry, nil
		}
	}

	entry.Pages = pageCount(reader.File)
	if dryRun {
		entry.Status = RetrofitWouldRebuild
		return entry, nil
	}

	if err := rebuildArchive(ctx, path, reader.File, ComicInfo{
		Series: series,
		Number: number,
		Title:  title,
	}); err != nil {
		return entry, err
	}
	entry.Status = RetrofitRebuilt
	return entry, nil
}

// pageCount counts the image entries of an archive: every entry except the
// ComicInfo.xml metadata file (directory entries too, defensively).
func pageCount(files []*zip.File) int {
	count := 0
	for _, f := range files {
		if f.Name != "ComicInfo.xml" && !strings.HasSuffix(f.Name, "/") {
			count++
		}
	}
	return count
}

// rebuildArchive rewrites path so the archive starts with a ComicInfo.xml
// entry, followed by every original entry streamed unchanged (same names,
// compression methods, and timestamps; byte-for-byte content). The archive
// is replaced atomically: the old file survives until assembly succeeds.
func rebuildArchive(ctx context.Context, path string, entries []*zip.File, metadata ComicInfo) error {
	imageCount := pageCount(entries)

	return publishFileAtomically(ctx, path, func(destination io.Writer) error {
		infoXML, err := marshalComicInfo(metadata, imageCount)
		if err != nil {
			return fmt.Errorf("marshaling ComicInfo.xml: %w", err)
		}
		zipWriter := zip.NewWriter(destination)
		if err := addBytesToZip(zipWriter, "ComicInfo.xml", infoXML); err != nil {
			_ = zipWriter.Close()
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				_ = zipWriter.Close()
				return err
			}
			if err := copyZipEntry(zipWriter, entry); err != nil {
				_ = zipWriter.Close()
				return err
			}
		}

		if err := zipWriter.Close(); err != nil {
			return fmt.Errorf("closing zip archive: %w", err)
		}
		return nil
	})
}

// copyZipEntry streams one source zip entry into the destination writer with
// its original header (name, compression method, timestamps) so the image
// bytes are preserved verbatim.
func copyZipEntry(zipWriter *zip.Writer, entry *zip.File) error {
	src, err := entry.Open()
	if err != nil {
		return fmt.Errorf("opening %s: %w", entry.Name, err)
	}
	defer src.Close()

	header := entry.FileHeader
	dst, err := zipWriter.CreateHeader(&header)
	if err != nil {
		return fmt.Errorf("creating zip entry: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("writing %s: %w", entry.Name, err)
	}
	return nil
}

var (
	// retrofitNumberPrefix matches a filename stem that begins with a
	// chapter number ("131", "112.5") and an optional " - <title>"
	// separator ("131 - Real Title", "131 - Chapter 131").
	retrofitNumberPrefix = regexp.MustCompile(`^(\d+(?:\.\d+)?)(?:\s*-\s*(.*))?$`)

	// retrofitChapterMarker matches the pre-rename naming form ("Ch. 361",
	// "Chapter 361 - ...") for archives the rename pass missed.
	retrofitChapterMarker = regexp.MustCompile(`(?:Ch\.?|Chapter)\s*(\d+(?:\.\d+)?)`)
)

// splitChapterMetadata extracts the chapter number and optional title from
// an archive's filename stem. Numbers must lead the stem; the old
// "Ch."/"Chapter" prefixed form is tolerated for leftover archives. A purely
// numeric title remainder is returned as-is ("131 - 131" yields title "131").
//
// When no title is recoverable from the filename, the caller echoes
// "Chapter {number}": Komga renders book titles as "{number} - {title}"
// (komga-webui BookItem.title), so an empty title would fall back to the
// filename and render the duplicated "131 - 131" that Komga issue #746
// calls confusing.
func splitChapterMetadata(stem string) (number, title string, ok bool) {
	if match := retrofitNumberPrefix.FindStringSubmatch(stem); match != nil {
		number = match[1]
		title = match[2]
		if title == "" {
			title = "Chapter " + number
		}
		return number, title, true
	}
	if match := retrofitChapterMarker.FindStringSubmatch(stem); match != nil {
		number = match[1]
		title = "Chapter " + number
		return number, title, true
	}
	return "", "", false
}
