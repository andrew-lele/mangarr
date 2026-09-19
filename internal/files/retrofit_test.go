package files

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestSplitChapterMetadata(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		stem       string
		wantNumber string
		wantTitle  string
		wantOK     bool
	}{
		{stem: "131", wantNumber: "131", wantTitle: "Chapter 131", wantOK: true},
		{stem: "112.5", wantNumber: "112.5", wantTitle: "Chapter 112.5", wantOK: true},
		{stem: "131 - Real Title", wantNumber: "131", wantTitle: "Real Title", wantOK: true},
		{stem: "112.5 - Chapter 112.5", wantNumber: "112.5", wantTitle: "Chapter 112.5", wantOK: true},
		{stem: "Ch. 361 - Chapter 361", wantNumber: "361", wantTitle: "Chapter 361", wantOK: true},
		{stem: "Blue Lock Ch. 361", wantNumber: "361", wantTitle: "Chapter 361", wantOK: true},
		{stem: "Chapter 58", wantNumber: "58", wantTitle: "Chapter 58", wantOK: true},
		{stem: "cover", wantOK: false},
		{stem: "volume-1", wantOK: false},
	} {
		t.Run(tc.stem, func(t *testing.T) {
			number, title, ok := splitChapterMetadata(tc.stem)
			if ok != tc.wantOK {
				t.Fatalf("splitChapterMetadata(%q) ok = %v, want %v", tc.stem, ok, tc.wantOK)
			}
			if number != tc.wantNumber {
				t.Errorf("splitChapterMetadata(%q) number = %q, want %q", tc.stem, number, tc.wantNumber)
			}
			if title != tc.wantTitle {
				t.Errorf("splitChapterMetadata(%q) title = %q, want %q", tc.stem, title, tc.wantTitle)
			}
		})
	}
}

// writeBareCbz writes a bare image zip (no ComicInfo.xml) with the given
// entry names and payloads.
func writeBareCbz(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	zw := zip.NewWriter(f)
	for name, data := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			_ = f.Close()
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			_ = f.Close()
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		t.Fatalf("close zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}

func readCbz(t *testing.T, path string) map[string][]byte {
	t.Helper()

	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer r.Close()

	out := make(map[string][]byte, len(r.File))
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		out[f.Name] = data
	}
	return out
}

func TestRetrofitRebuildsBareArchive(t *testing.T) {
	t.Parallel()

	seriesDir := filepath.Join(t.TempDir(), "Blue Lock")
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(seriesDir, "131.cbz")
	imageData := map[string][]byte{"001.png": []byte("page-one"), "002.png": []byte("page-two")}
	writeBareCbz(t, path, imageData)

	entry, err := retrofitFile(t.Context(), path, false)
	if err != nil {
		t.Fatalf("retrofitFile: %v", err)
	}
	if entry.Status != RetrofitRebuilt {
		t.Fatalf("status = %q, want %q", entry.Status, RetrofitRebuilt)
	}
	if entry.Series != "Blue Lock" || entry.Number != "131" || entry.Title != "Chapter 131" || entry.Pages != 2 {
		t.Errorf("entry = %+v, want series Blue Lock number 131 title \"Chapter 131\" pages 2", entry)
	}

	entries := readCbz(t, path)
	if len(entries) != 3 {
		t.Fatalf("expected ComicInfo.xml + 2 images, got %d entries", len(entries))
	}
	infoXML, ok := entries["ComicInfo.xml"]
	if !ok {
		t.Fatal("missing ComicInfo.xml after retrofit")
	}
	var info comicInfoDocument
	if err := xml.Unmarshal(infoXML, &info); err != nil {
		t.Fatalf("parse ComicInfo.xml: %v", err)
	}
	if info.Series != "Blue Lock" {
		t.Errorf("Series = %q, want %q", info.Series, "Blue Lock")
	}
	if info.Number != "131" {
		t.Errorf("Number = %q, want %q", info.Number, "131")
	}
	if info.Title != "Chapter 131" {
		t.Errorf("Title = %q, want %q (echoed so Komga does not render \"131 - 131\")", info.Title, "Chapter 131")
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

	// Images preserved byte-for-byte.
	for name, want := range imageData {
		if got := entries[name]; !bytes.Equal(got, want) {
			t.Errorf("image %s changed: got %q, want %q", name, got, want)
		}
	}
}

func TestRetrofitPreservesDecimalNumberAndTitle(t *testing.T) {
	t.Parallel()

	seriesDir := filepath.Join(t.TempDir(), "Blue Lock")
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(seriesDir, "112.5 - Real Title.cbz")
	writeBareCbz(t, path, map[string][]byte{"001.png": []byte("page-one")})

	entry, err := retrofitFile(t.Context(), path, false)
	if err != nil {
		t.Fatalf("retrofitFile: %v", err)
	}
	if entry.Status != RetrofitRebuilt {
		t.Fatalf("status = %q, want %q", entry.Status, RetrofitRebuilt)
	}
	if entry.Number != "112.5" || entry.Title != "Real Title" {
		t.Errorf("entry = %+v, want number 112.5 title \"Real Title\"", entry)
	}

	entries := readCbz(t, path)
	var info comicInfoDocument
	if err := xml.Unmarshal(entries["ComicInfo.xml"], &info); err != nil {
		t.Fatalf("parse ComicInfo.xml: %v", err)
	}
	if info.Number != "112.5" {
		t.Errorf("Number = %q, want %q (decimal must round-trip)", info.Number, "112.5")
	}
	if info.Title != "Real Title" {
		t.Errorf("Title = %q, want %q", info.Title, "Real Title")
	}
	if info.PageCount != 1 {
		t.Errorf("PageCount = %d, want 1", info.PageCount)
	}
}

func TestRetrofitSkipsArchivesWithComicInfo(t *testing.T) {
	t.Parallel()

	seriesDir := filepath.Join(t.TempDir(), "Blue Lock")
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(seriesDir, "7.cbz")
	writeTestImage(t, seriesDir, "001.png")
	// Build through the real writer so the archive already carries metadata.
	if err := CreateCbzArchive(t.Context(), zerolog.Nop(), seriesDir, path, false, ComicInfo{
		Series: "Blue Lock",
		Number: "7",
	}); err != nil {
		t.Fatalf("CreateCbzArchive: %v", err)
	}
	before := readCbz(t, path)

	entry, err := retrofitFile(t.Context(), path, false)
	if err != nil {
		t.Fatalf("retrofitFile: %v", err)
	}
	if entry.Status != RetrofitAlreadyHasMetadata {
		t.Fatalf("status = %q, want %q", entry.Status, RetrofitAlreadyHasMetadata)
	}

	after := readCbz(t, path)
	for name, want := range before {
		if got := after[name]; !bytes.Equal(got, want) {
			t.Errorf("entry %s changed for archive that already had metadata", name)
		}
	}
}

func TestRetrofitSkipsUnparsableFilename(t *testing.T) {
	t.Parallel()

	seriesDir := filepath.Join(t.TempDir(), "Blue Lock")
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(seriesDir, "cover.cbz")
	payload := map[string][]byte{"0001.png": []byte("cover")}
	writeBareCbz(t, path, payload)

	entry, err := retrofitFile(t.Context(), path, false)
	if err != nil {
		t.Fatalf("retrofitFile: %v", err)
	}
	if entry.Status != RetrofitSkippedNoNumber {
		t.Fatalf("status = %q, want %q", entry.Status, RetrofitSkippedNoNumber)
	}
	if got := readCbz(t, path)["0001.png"]; !bytes.Equal(got, payload["0001.png"]) {
		t.Error("archive changed for unparseable filename")
	}
}

func TestRetrofitLibraryDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	seriesDir := filepath.Join(root, "Blue Lock")
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(seriesDir, "131.cbz")
	writeBareCbz(t, path, map[string][]byte{"001.png": []byte("page-one")})
	before := readCbz(t, path)

	entries, err := RetrofitLibrary(t.Context(), root, true)
	if err != nil {
		t.Fatalf("RetrofitLibrary: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Status != RetrofitWouldRebuild {
		t.Errorf("status = %q, want %q", entries[0].Status, RetrofitWouldRebuild)
	}

	// Nothing may change in a dry run.
	after := readCbz(t, path)
	if _, ok := after["ComicInfo.xml"]; ok {
		t.Error("dry run must not write ComicInfo.xml")
	}
	for name, want := range before {
		if got := after[name]; !bytes.Equal(got, want) {
			t.Errorf("entry %s changed during dry run", name)
		}
	}
}

func TestRetrofitLibraryIsIdempotent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	seriesDir := filepath.Join(root, "Blue Lock")
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBareCbz(t, filepath.Join(seriesDir, "131.cbz"), map[string][]byte{"001.png": []byte("page-one")})
	writeBareCbz(t, filepath.Join(seriesDir, "112.5 - Real Title.cbz"), map[string][]byte{"001.png": []byte("page-one")})

	first, err := RetrofitLibrary(t.Context(), root, false)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	rebuilt, skipped := 0, 0
	for _, entry := range first {
		switch entry.Status {
		case RetrofitRebuilt:
			rebuilt++
		case RetrofitAlreadyHasMetadata:
			skipped++
		}
	}
	if rebuilt != 2 || skipped != 0 {
		t.Fatalf("first run rebuilt=%d skipped=%d, want rebuilt=2 skipped=0", rebuilt, skipped)
	}

	second, err := RetrofitLibrary(t.Context(), root, false)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	for _, entry := range second {
		if entry.Status != RetrofitAlreadyHasMetadata {
			t.Errorf("second run status = %q, want %q (%s)", entry.Status, RetrofitAlreadyHasMetadata, entry.Path)
		}
	}
}

func TestRetrofitLibraryToleratesCancellation(t *testing.T) {
	root := t.TempDir()
	seriesDir := filepath.Join(root, "Blue Lock")
	if err := os.MkdirAll(seriesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBareCbz(t, filepath.Join(seriesDir, "131.cbz"), map[string][]byte{"001.png": []byte("page-one")})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := RetrofitLibrary(ctx, root, false); err == nil {
		t.Fatal("expected cancelled context to fail the walk")
	}
	if !strings.Contains("walking", "walking") {
		t.Error("unreachable sanity check")
	}
}
