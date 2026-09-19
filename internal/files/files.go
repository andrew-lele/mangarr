package files

import (
	"archive/zip"
	"bufio"
	"cmp"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"slices"

	_ "github.com/gen2brain/avif" // needed for AVIF page dimensions
	"github.com/rs/zerolog"
	_ "golang.org/x/image/webp" // needed to decode webp
)

const (
	binSize       = 10
	maxWidthMulti = 1.25

	// errUnsupportedSubsamplingRatio indicates an unsupported luma/chroma subsampling ratio in JPEG images.
	errUnsupportedSubsamplingRatio = jpeg.UnsupportedError("luma/chroma subsampling ratio")
)

type imageMeta struct {
	path   string
	name   string
	width  int
	height int
}

// ComicInfo is the ComicRack metadata written into every archive as
// ComicInfo.xml, so readers (Komga, Mihon) display the real series,
// chapter number, and chapter title instead of deriving them from the
// filename. The fields mirror the tag subset of Komga's
// comicrack.dto.ComicInfo (Jackson @JsonProperty bindings).
type ComicInfo struct {
	// Series is the manga/series title.
	Series string

	// Number is the chapter number rendered exactly as discovered
	// (fractional chapters like "112.5" stay "112.5"; Komga parses the
	// string before converting to decimal).
	Number string

	// Title is the chapter title. When empty, the <Title> element is
	// omitted and readers fall back to their filename-derived title.
	Title string
}

func IsValidLocation(location string) error {
	info, err := os.Stat(location)
	if err != nil {
		return fmt.Errorf("stat location %s: %w", location, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("location %s is not a directory", location)
	}

	return nil
}

// CreateCbzArchive creates a zip (.cbz) archive from the images in sourceDir.
// A ComicInfo.xml metadata entry (see ComicInfo) is embedded first so
// comic readers show the real series/chapter title and number. It preserves
// the destination until assembly succeeds and cancellation is checked
// immediately before publication.
func CreateCbzArchive(ctx context.Context, log zerolog.Logger, sourceDir, cbzPath string, isManhwa bool, metadata ComicInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cbzPath), os.ModePerm); err != nil {
		return fmt.Errorf("creating destination dir: %w", err)
	}

	var (
		images      []imageMeta
		widthCount  = make(map[int]int)
		mostCommonW int
	)

	if walkErr := filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			return fmt.Errorf("opening %s: %w", path, openErr)
		}
		defer f.Close()

		img, _, decodeErr := image.DecodeConfig(bufio.NewReader(f))
		if decodeErr != nil {
			if errors.Is(decodeErr, errUnsupportedSubsamplingRatio) {
				log.Debug().Str("cbz", filepath.Base(cbzPath)).Str("name", d.Name()).
					Msg("skipping size check for image because it has an unsupported subsampling ratio")

				images = append(images, imageMeta{
					path: path,
					name: d.Name(),
				})

				return nil
			}

			return fmt.Errorf("decoding %s: %w", path, decodeErr)
		}

		bin := (img.Width / binSize) * binSize
		widthCount[bin]++

		images = append(images, imageMeta{
			path:   path,
			name:   d.Name(),
			width:  img.Width,
			height: img.Height,
		})
		return nil
	}); walkErr != nil {
		return fmt.Errorf("walking directory %s: %w", sourceDir, walkErr)
	}

	// Determine the most common width bin.
	for bin, count := range widthCount {
		if count > widthCount[mostCommonW] {
			mostCommonW = bin
		}
	}

	// Sort images lexicographically so they stay in page order.
	slices.SortFunc(images, func(a, b imageMeta) int { return cmp.Compare(a.name, b.name) })

	selectedImages := make([]imageMeta, 0, len(images))
	for _, img := range images {
		// Skip pages that are highly likely not a Manhwa page
		if isManhwa && isLikelyUnwanted(img, mostCommonW) {
			log.Debug().Str("cbz", filepath.Base(cbzPath)).Str("name", img.name).
				Int("width", img.width).Int("height", img.height).
				Msg("skipped image because it's likely not a Manhwa page")
			continue
		}
		selectedImages = append(selectedImages, img)
	}
	if len(selectedImages) == 0 {
		return fmt.Errorf("creating archive: no images to write")
	}

	comicInfoXML, err := marshalComicInfo(metadata, len(selectedImages))
	if err != nil {
		return fmt.Errorf("marshaling ComicInfo.xml: %w", err)
	}

	if err := publishFileAtomically(ctx, cbzPath, func(destination io.Writer) error {
		zipWriter := zip.NewWriter(destination)
		if err := addBytesToZip(zipWriter, "ComicInfo.xml", comicInfoXML); err != nil {
			_ = zipWriter.Close()
			return err
		}
		for _, img := range selectedImages {
			if err := ctx.Err(); err != nil {
				_ = zipWriter.Close()
				return err
			}
			if err := addFileToZip(zipWriter, img.path, img.name); err != nil {
				_ = zipWriter.Close()
				return err
			}
		}

		if err := zipWriter.Close(); err != nil {
			return fmt.Errorf("closing zip archive: %w", err)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("publishing %s: %w", cbzPath, err)
	}

	return nil
}

func publishFileAtomically(ctx context.Context, destinationPath string, write func(io.Writer) error) (err error) {
	destinationDir := filepath.Dir(destinationPath)
	tmpFile, err := os.CreateTemp(destinationDir, "."+filepath.Base(destinationPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary file: %w", err)
	}

	tmpPath := tmpFile.Name()
	published := false
	defer func() {
		if published {
			return
		}

		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	if err := tmpFile.Chmod(0o644); err != nil {
		return fmt.Errorf("setting temporary file permissions: %w", err)
	}
	if err := write(tmpFile); err != nil {
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("syncing temporary file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temporary file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destinationPath); err != nil {
		return fmt.Errorf("renaming temporary file: %w", err)
	}

	published = true
	return nil
}

func isLikelyUnwanted(img imageMeta, dominantW int) bool {
	// Skip pages that are not higher than wide
	if img.width < img.height {
		return false
	}

	// Skip pages that are wider than 1.25x the dominant width
	allowed := float64(dominantW) * maxWidthMulti
	if float64(img.width) < allowed {
		return false
	}

	return true
}

// comicInfoDocument is the ComicRack ComicInfo.xml schema subset mangarr
// embeds. Field names match Komga's comicrack.dto.ComicInfo Jackson
// bindings: Title/Series/Number are plain strings; a blank Title renders no
// <Title> element (omitempty) so readers fall back to the filename.
type comicInfoDocument struct {
	XMLName   xml.Name `xml:"ComicInfo"`
	Series    string   `xml:"Series"`
	Number    string   `xml:"Number"`
	Title     string   `xml:"Title,omitempty"`
	Genre     string   `xml:"Genre"`
	PageCount int      `xml:"PageCount"`
	Writer    string   `xml:"Writer"`
}

func marshalComicInfo(metadata ComicInfo, pageCount int) ([]byte, error) {
	doc, err := xml.Marshal(comicInfoDocument{
		Series:    metadata.Series,
		Number:    metadata.Number,
		Title:     metadata.Title,
		Genre:     "Manga",
		PageCount: pageCount,
		Writer:    "mangarr",
	})
	if err != nil {
		return nil, err
	}

	// xml.Marshal omits the declaration; prepend it so the document matches
	// the ComicRack XmlComicProvider convention. Readers ignore it either way.
	return append([]byte(xml.Header), doc...), nil
}

// addBytesToZip writes an in-memory file into an open zip archive. Used for
// the ComicInfo.xml metadata entry, which never exists on disk.
func addBytesToZip(zipWriter *zip.Writer, fileName string, data []byte) error {
	hdr := &zip.FileHeader{
		Name:   fileName,
		Method: zip.Store,
	}
	dst, err := zipWriter.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("creating zip entry: %w", err)
	}

	if _, err := dst.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", fileName, err)
	}

	return nil
}

// addFileToZip copies a single file into an open zip archive.
func addFileToZip(zipWriter *zip.Writer, filePath, fileName string) error {
	src, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", filePath, err)
	}
	defer src.Close()

	hdr := &zip.FileHeader{
		Name:   fileName,
		Method: zip.Store,
	}
	dst, err := zipWriter.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("creating zip entry: %w", err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("writing %s: %w", fileName, err)
	}

	return nil
}
