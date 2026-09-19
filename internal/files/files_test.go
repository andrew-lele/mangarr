package files

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestCreateCbzArchiveFlushesOutput(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	imgPath := filepath.Join(sourceDir, "001.png")
	f, err := os.Create(imgPath)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode png: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close image: %v", err)
	}

	outPath := filepath.Join(tmpDir, "out.cbz")
	if err := CreateCbzArchive(t.Context(), zerolog.Nop(), sourceDir, outPath, false, ComicInfo{}); err != nil {
		t.Fatalf("create cbz: %v", err)
	}

	r, err := zip.OpenReader(outPath)
	if err != nil {
		t.Fatalf("open cbz: %v", err)
	}
	defer r.Close()

	if len(r.File) != 2 {
		t.Fatalf("expected ComicInfo.xml + 1 image in cbz, got %d entries", len(r.File))
	}
	if r.File[0].Name != "ComicInfo.xml" {
		t.Errorf("ComicInfo.xml must be the first entry, got %q", r.File[0].Name)
	}
}

func writeTestImage(t *testing.T, dir, name string) {
	t.Helper()

	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("create image: %v", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode png: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close image: %v", err)
	}
}

// readZipEntries reads every entry of a cbz so structure and content are
// validated end to end (entry CRCs included).
func readZipEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()

	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open cbz: %v", err)
	}

	entries := make(map[string][]byte, len(r.File))
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			_ = r.Close()
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			_ = r.Close()
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		entries[f.Name] = data
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close cbz: %v", err)
	}

	return entries
}

func TestCreateCbzArchiveEmbedsComicInfo(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	writeTestImage(t, sourceDir, "001.png")
	writeTestImage(t, sourceDir, "002.png")

	outPath := filepath.Join(tmpDir, "out.cbz")
	if err := CreateCbzArchive(t.Context(), zerolog.Nop(), sourceDir, outPath, false, ComicInfo{
		Series: "Blue Lock",
		Number: "112.5",
		Title:  "Chapter 112.5",
	}); err != nil {
		t.Fatalf("create cbz: %v", err)
	}

	entries := readZipEntries(t, outPath)
	infoXML, ok := entries["ComicInfo.xml"]
	if !ok {
		t.Fatalf("archive is missing ComicInfo.xml; entries: %v", entryNames(entries))
	}
	if !bytes.HasPrefix(infoXML, []byte(xml.Header)) {
		t.Errorf("ComicInfo.xml must start with the XML declaration, got %q", infoXML)
	}

	var info comicInfoDocument
	if err := xml.Unmarshal(infoXML, &info); err != nil {
		t.Fatalf("parse ComicInfo.xml: %v", err)
	}
	if info.Series != "Blue Lock" {
		t.Errorf("Series = %q, want %q", info.Series, "Blue Lock")
	}
	if info.Number != "112.5" {
		t.Errorf("Number = %q, want %q (decimal must round-trip)", info.Number, "112.5")
	}
	if info.Title != "Chapter 112.5" {
		t.Errorf("Title = %q, want %q", info.Title, "Chapter 112.5")
	}
	if info.Genre != "Manga" {
		t.Errorf("Genre = %q, want %q", info.Genre, "Manga")
	}
	if info.PageCount != 2 {
		t.Errorf("PageCount = %d, want 2", info.PageCount)
	}
	if info.Writer != "mangarr" {
		t.Errorf("Writer = %q, want %q", info.Writer, "mangarr")
	}

	// Both pages must survive alongside the metadata entry.
	for _, name := range []string{"001.png", "002.png"} {
		if _, ok := entries[name]; !ok {
			t.Errorf("%s missing after ComicInfo.xml embedded", name)
		}
	}
}

func TestCreateCbzArchiveOmitsEmptyChapterTitle(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	writeTestImage(t, sourceDir, "001.png")

	outPath := filepath.Join(tmpDir, "out.cbz")
	if err := CreateCbzArchive(t.Context(), zerolog.Nop(), sourceDir, outPath, false, ComicInfo{
		Series: "Blue Lock",
		Number: "361",
	}); err != nil {
		t.Fatalf("create cbz: %v", err)
	}

	entries := readZipEntries(t, outPath)
	infoXML, ok := entries["ComicInfo.xml"]
	if !ok {
		t.Fatalf("archive is missing ComicInfo.xml")
	}

	var info comicInfoDocument
	if err := xml.Unmarshal(infoXML, &info); err != nil {
		t.Fatalf("parse ComicInfo.xml: %v", err)
	}
	if info.Title != "" {
		t.Errorf("Title = %q, want empty (readers fall back to the filename)", info.Title)
	}
	if strings.Contains(string(infoXML), "<Title>") {
		t.Errorf("ComicInfo.xml must not contain a <Title> element, got:\n%s", infoXML)
	}
}

func entryNames(entries map[string][]byte) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	return names
}

func TestCreateCbzArchiveDoesNotPublishEmptyArchive(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "empty")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir empty source: %v", err)
	}
	outPath := filepath.Join(tmpDir, "out.cbz")
	err := CreateCbzArchive(t.Context(), zerolog.Nop(), sourceDir, outPath, false, ComicInfo{})

	if err == nil {
		t.Fatal("expected empty source directory to fail")
	}
	if _, statErr := os.Stat(outPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("final archive exists after failure: %v", statErr)
	}
}

func TestPublishFileAtomicallyRemovesPartialOutput(t *testing.T) {
	t.Parallel()

	destination := filepath.Join(t.TempDir(), "chapter.cbz")
	wantErr := errors.New("simulated write failure")
	err := publishFileAtomically(t.Context(), destination, func(writer io.Writer) error {
		if _, err := writer.Write([]byte("partial archive")); err != nil {
			return err
		}
		return wantErr
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("publish error = %v, want %v", err, wantErr)
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("final file exists after failure: %v", statErr)
	}

	matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(destination), ".chapter.cbz.tmp-*"))
	if globErr != nil {
		t.Fatalf("glob temporary files: %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain after failure: %v", matches)
	}
}

func TestAtomicReplacementPreservesOriginalUntilPublication(t *testing.T) {
	for _, outcome := range []string{"success", "write failure", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "chapter.cbz")
			if err := os.WriteFile(destination, []byte("original"), 0o644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			writeErr := errors.New("assembly failed")
			err := publishFileAtomically(ctx, destination, func(writer io.Writer) error {
				if _, err := writer.Write([]byte("replacement")); err != nil {
					return err
				}
				data, err := os.ReadFile(destination)
				if err != nil || string(data) != "original" {
					t.Fatalf("original changed during assembly: %q, %v", data, err)
				}
				if outcome == "write failure" {
					return writeErr
				}
				if outcome == "cancelled" {
					cancel()
				}
				return nil
			})
			want := "original"
			switch outcome {
			case "success":
				want = "replacement"
				if err != nil {
					t.Fatal(err)
				}
			case "write failure":
				if !errors.Is(err, writeErr) {
					t.Fatalf("error = %v", err)
				}
			case "cancelled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v", err)
				}
			}
			data, err := os.ReadFile(destination)
			if err != nil || string(data) != want {
				t.Fatalf("archive = %q, %v; want %q", data, err, want)
			}
			entries, err := os.ReadDir(filepath.Dir(destination))
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary archives remain: %v, %v", entries, err)
			}
		})
	}
}

func TestIsValidLocationRejectsFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("file"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := IsValidLocation(path); err == nil {
		t.Fatal("expected file location to fail validation")
	}
}
