package cmd

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeBareCbzTestArchive writes a bare image zip (no ComicInfo.xml) with the
// given entry payloads, like the archives mangarr produced before the
// ComicInfo.xml feature.
func writeBareCbzTestArchive(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()

	f, err := os.Create(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	zw := zip.NewWriter(f)
	for name, data := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		require.NoError(t, err)
		_, err = w.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
}

func readArchiveEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()

	r, err := zip.OpenReader(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	entries := make(map[string][]byte, len(r.File))
	for _, f := range r.File {
		rc, err := f.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(rc)
		require.NoError(t, rc.Close())
		require.NoError(t, err)
		entries[f.Name] = data
	}
	return entries
}

// TestCbzRetrofitCommandDryRunThenApply exercises the full command against a
// tiny library: bare archives (with and without titles, decimal chapter),
// an archive that already carries ComicInfo.xml, and an unparseable file.
func TestCbzRetrofitCommandDryRunThenApply(t *testing.T) {
	lib := t.TempDir()
	series := filepath.Join(lib, "Blue Lock")
	require.NoError(t, os.MkdirAll(series, 0o755))

	// Bare archives from the pre-metadata era.
	plain := filepath.Join(series, "131.cbz")
	writeBareCbzTestArchive(t, plain, map[string][]byte{"001.png": []byte("page-one")})
	decimal := filepath.Join(series, "112.5 - Chapter 112.5.cbz")
	writeBareCbzTestArchive(t, decimal, map[string][]byte{"001.png": []byte("page-one")})
	// Covered + unparseable skip candidates.
	cover := filepath.Join(series, "cover.cbz")
	writeBareCbzTestArchive(t, cover, map[string][]byte{"0001.png": []byte("cover")})

	run := func(apply bool) string {
		t.Helper()
		root := NewRootCommand()
		out := &bytes.Buffer{}
		root.SetOut(out)
		root.SetErr(out)
		args := []string{"cbz-retrofit", lib, "--log", filepath.Join(t.TempDir(), "retrofit.log")}
		if apply {
			args = append(args, "--apply")
		}
		root.SetArgs(args)
		require.NoError(t, root.ExecuteContext(t.Context()))
		return out.String()
	}

	// Dry run: report only, nothing written.
	dryReport := run(false)
	require.Contains(t, dryReport, "would-rebuild\t2")
	require.Contains(t, dryReport, "already-has-comicinfo\t0")
	require.Contains(t, dryReport, "skipped-no-chapter-number\t1")
	require.Contains(t, dryReport, "rebuilt\t0")
	_, err := os.Stat(plain)
	require.NoError(t, err)
	entries := readArchiveEntries(t, plain)
	require.Len(t, entries, 1, "dry run must not modify archives")

	// Apply: rebuild in place.
	report := run(true)
	require.Contains(t, report, "rebuilt\t2")
	require.Contains(t, report, "already-has-comicinfo\t0")
	require.Contains(t, report, "skipped-no-chapter-number\t1")

	assertRetrofitted(t, plain, "Blue Lock", "131", "Chapter 131", "page-one")
	assertRetrofitted(t, decimal, "Blue Lock", "112.5", "Chapter 112.5", "page-one")
	// Unparseable filename stays untouched.
	coverEntries := readArchiveEntries(t, cover)
	require.Len(t, coverEntries, 1)
	require.NotContains(t, coverEntries, "ComicInfo.xml")

	// Second apply is a no-op (idempotent).
	second := run(true)
	require.Contains(t, second, "rebuilt\t0")
	require.Contains(t, second, "already-has-comicinfo\t2")
	require.Contains(t, second, "skipped-no-chapter-number\t1")
}

// TestCbzRetrofitCommandSkipsArchivesWithMetadata covers a series directory
// with an already-retrofitted archive (as produced by the download flow).
func TestCbzRetrofitCommandSkipsArchivesWithMetadata(t *testing.T) {
	lib := t.TempDir()
	series := filepath.Join(lib, "Blue Lock")
	require.NoError(t, os.MkdirAll(series, 0o755))

	// Build an archive through the download-equivalent path: a bare zip
	// rebuilt by RetrofitLibrary once, then run the command again.
	path := filepath.Join(series, "7.cbz")
	writeBareCbzTestArchive(t, path, map[string][]byte{"001.png": []byte("page-one")})

	root := NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"cbz-retrofit", lib, "--apply", "--log", filepath.Join(t.TempDir(), "retrofit.log")})
	require.NoError(t, root.ExecuteContext(t.Context()))

	assertRetrofitted(t, path, "Blue Lock", "7", "Chapter 7", "page-one")

	root = NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"cbz-retrofit", lib, "--log", filepath.Join(t.TempDir(), "retrofit.log")})
	require.NoError(t, root.ExecuteContext(t.Context()))
}

// assertRetrofitted verifies an archive carries a ComicInfo.xml entry with
// the file-derived metadata, and that its images survived byte-for-byte.
func assertRetrofitted(t *testing.T, path, series, number, title, wantImage string) {
	t.Helper()

	entries := readArchiveEntries(t, path)
	info, ok := entries["ComicInfo.xml"]
	require.True(t, ok, "archive %s must carry ComicInfo.xml", path)
	require.Contains(t, string(info), "<Series>"+series+"</Series>")
	require.Contains(t, string(info), "<Number>"+number+"</Number>")
	if title == "" {
		require.NotContains(t, string(info), "<Title>")
	} else {
		require.Contains(t, string(info), "<Title>"+title+"</Title>")
	}
	require.Contains(t, string(info), "<Genre>Manga</Genre>")
	require.Contains(t, string(info), "<Writer>mangarr</Writer>")

	found := false
	for name, data := range entries {
		if name == "ComicInfo.xml" {
			continue
		}
		require.Equal(t, wantImage, string(data), "image %s must survive retrofit unchanged", name)
		found = true
	}
	require.True(t, found, "archive %s must retain its images", path)
}

func TestCbzRetrofitCommandRejectsMissingArgument(t *testing.T) {
	root := NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"cbz-retrofit"})
	err := root.ExecuteContext(t.Context())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "accepts 1 arg"), "error = %q", err)
}
