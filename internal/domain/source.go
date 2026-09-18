package domain

import (
	"context"
	"io"
	"net/http"
)

type Source interface {
	String() string
	ValidateInput() error
	Discover(context.Context) (Manga, error)
	Pages(context.Context, Chapter) ([]ImageInfo, error)
}

type Manga struct {
	ID       string
	URL      string
	Title    string
	Chapters map[ChapterNumber]Chapter
	IsManhwa bool
}

type Chapter struct {
	ID     string
	URL    string
	Number ChapterNumber
	Title  string
	// Group is the source-specific scanlation group id carried by the
	// chapter (comix numeric GroupID, atsumaru ScanID). It feeds the
	// quality-profile resolver; empty means the source could not identify
	// the group.
	Group     string
	ImageInfo []ImageInfo
}

// ScanlationGroup is one scanlation group as discovered from a source: the
// source-native id (comix numeric GroupID, atsumaru ScanID) plus the
// human-readable name and optional slug. Group discovery feeds groups.yaml
// authoring, which maps these native ids to canonical UUIDs and aliases.
type ScanlationGroup struct {
	ID   string
	Name string
	Slug string
}

// GroupSearchOptions carries the per-source inputs a group lister needs
// beyond the source identifier. atsumaru discovers per-manga scanlators from
// its manga URL; comix can reuse the impersonation relay origin that mints
// Cloudflare clearance cookies (see internal/sharedhttp/impersonate.go).
type GroupSearchOptions struct {
	Source             string
	Manga              string
	ImpersonationProxy string
}

// GroupLister is an OPTIONAL capability layered on top of domain.Source:
// sources that model scanlation groups can list/search them. query is an
// empty string to list (possibly paged) groups or a case-insensitive name
// search; each adapter owns how search is performed (server-side keyword for
// comix, local filter for atsumaru). Sources without a group model implement
// only Source, and the groups command reports them as such.
type GroupLister interface {
	Groups(ctx context.Context, query string) ([]ScanlationGroup, error)
}

// SearchResult is one candidate series from a source's title search. URL is
// the series identifier in the form the source's Discover accepts (atsumaru /
// weebcentral full series URL, mangadex UUID); Title is the source-side
// display name used for exact-title matching against the tracked entry.
type SearchResult struct {
	Title string
	URL   string
}

// Searcher is an OPTIONAL capability layered on top of domain.Source: sources
// that can look up a series by title implement it so title+qualityProfile
// tracked entries can find the series URL without a source-pinned config
// entry. Sources without search support report an error from
// source.NewSearcher and the caller skips that source.
type Searcher interface {
	Search(ctx context.Context, title string) ([]SearchResult, error)
}

// ImageInfo is one page transport description for a chapter.
type ImageInfo struct {
	ImageURL       string
	EncryptionKey  string
	RequestHeaders map[string]string
	Processor      ImageProcessor
}

type ImageProcessor interface {
	Extension() string
	Process(http.Header, io.Reader, io.Writer) error
}
