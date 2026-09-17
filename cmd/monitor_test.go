package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mangarr/internal/domain"
	"mangarr/internal/logger"
	"mangarr/internal/registry"

	"github.com/stretchr/testify/require"
)

// runCycleCapturingLog swaps stderr for a pipe so the test can decode the
// zerolog JSON entry written by a single cycle. Serial only: os.Stderr is
// process-global.
func runCycleCapturingLog(t *testing.T, cfg domain.Config) map[string]any {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	originalStderr := os.Stderr
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = originalStderr
		_ = reader.Close()
		_ = writer.Close()
	})

	runMonitorCycle(t.Context(), cfg, logger.New(&cfg))
	os.Stderr = originalStderr
	if err := writer.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}

	contents, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read monitor log: %v", err)
	}

	var entry map[string]any
	if err := json.Unmarshal(contents, &entry); err != nil {
		t.Fatalf("decode monitor log %q: %v", contents, err)
	}
	return entry
}

func TestRunMonitorCycleErrorIncludesConfiguredSource(t *testing.T) {
	cfg := domain.Config{
		Version:          "test",
		DownloadLocation: t.TempDir(),
		LogLevel:         "DEBUG",
		MonitoredManga: map[string]*domain.MonitoredManga{
			"Broken Manga": {
				Source: "asurascans",
			},
		},
	}

	entry := runCycleCapturingLog(t, cfg)
	if got := entry["message"]; got != "monitor check failed" {
		t.Fatalf("message = %v, want monitor check failed", got)
	}
	if got := entry["manga"]; got != "Broken Manga" {
		t.Fatalf("manga = %v, want Broken Manga", got)
	}
	if got := entry["source"]; got != "asurascans" {
		t.Fatalf("source = %v, want asurascans", got)
	}
}

func TestDecisionSuffix(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", decisionLogSuffix(domain.Decision{}))
	require.Equal(t, "", decisionLogSuffix(domain.Decision{Outcome: domain.OutcomeUnknown}))
	require.Equal(t, " [group=a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b decision=preferred]", decisionLogSuffix(domain.Decision{Outcome: domain.OutcomePreferred, PreferredIndex: 0, CanonicalID: "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b"}))
	require.Equal(t, " [group=b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c decision=ignored]", decisionLogSuffix(domain.Decision{Outcome: domain.OutcomeIgnored, PreferredIndex: -1, CanonicalID: "b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c"}))
}

func TestMonitorDryRunReportsWouldBeDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"title":"Fixture","chapters":{"1":{"groups":{"test":["http://%s/page"]}},"2":{"groups":{"test":["http://%s/page"]}}}}`, r.Host, r.Host)
	}))
	defer server.Close()

	cfg := domain.Config{
		Version:          "test",
		ConfigPath:       t.TempDir(),
		DownloadLocation: t.TempDir(),
		NamingTemplate:   "{manga:<.>} Ch. {num}{title: - <.>}",
		DryRun:           true,
		LogLevel:         "DEBUG",
		MonitoredManga: map[string]*domain.MonitoredManga{
			"Fixture": {
				Source: "cubari",
				Manga:  server.URL + "/gist",
				Group:  "test",
			},
		},
	}

	entry := runCycleCapturingLog(t, cfg)
	expected := "Would download Fixture Ch. 2 -> " + filepath.Join(cfg.DownloadLocation, "Fixture", "Fixture Ch. 2.cbz")
	if got := entry["message"]; got != expected {
		t.Fatalf("message = %v, want %s", got, expected)
	}
}

func TestRunMonitorCycleAbortsWhenRegistryInvalid(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, registry.GroupsFileName), []byte("groups: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := domain.Config{
		Version:          "test",
		ConfigPath:       configDir,
		DownloadLocation: t.TempDir(),
		LogLevel:         "DEBUG",
		MonitoredManga: map[string]*domain.MonitoredManga{
			"Broken Manga": {
				Source: "asurascans",
			},
		},
	}

	entry := runCycleCapturingLog(t, cfg)
	if got := entry["message"]; got != "loading groups registry" {
		t.Fatalf("message = %v, want loading groups registry", got)
	}
}
