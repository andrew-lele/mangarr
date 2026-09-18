package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"mangarr/internal/domain"
	"mangarr/internal/registry"
	"mangarr/internal/resolve"
	"mangarr/internal/source"
)

// profileSourceManga is one scanned source's discovered manga (chapters with
// per-row native group ids) for a title+qualityProfile tracked entry.
type profileSourceManga struct {
	SourceKey string
	Source    domain.Source
	Manga     domain.Manga
}

// trackSkip records a scanned source that produced no usable manga; callers
// log it as a soft skip (search unsupported, no match, discovery failure).
type trackSkip struct {
	SourceKey string
	Err       error
}

// trackTitle resolves a title+qualityProfile entry: it searches every source
// in the profile's STRICT scan set for the title, discovers the best-matching
// series per source, and merges the candidates into one manga whose chapter
// map holds the highest-profile-preferred non-ignored winner per number.
//
// The profile's scan set is derived from the preferred groups' per-source
// native-id maps only (registry.ProfileScanSources); sources the profile does
// not map are never searched, so fallback:any cannot trigger for off-profile
// sources. Search-unavailable or failing sources are reported via skips and
// do not fail the entry.
func trackTitle(ctx context.Context, groups *domain.GroupRegistry, profiles *domain.ProfileRegistry, profileRef, title string) (domain.Manga, map[domain.ChapterNumber]profileChapter, []trackSkip, error) {
	scanned := registry.ScannableSources(groups, profiles, profileRef, source.Keys())
	if len(scanned) == 0 {
		return domain.Manga{}, nil, nil, fmt.Errorf("profile %q maps no supported scan sources", profileRef)
	}

	var perSource []profileSourceManga
	var skips []trackSkip
	for _, sourceKey := range scanned {
		searcher, err := source.NewSearcher(sourceKey)
		if err != nil {
			skips = append(skips, trackSkip{SourceKey: sourceKey, Err: err})
			continue
		}
		results, err := searcher.Search(ctx, title)
		if err != nil {
			skips = append(skips, trackSkip{SourceKey: sourceKey, Err: fmt.Errorf("searching: %w", err)})
			continue
		}
		best := pickSeries(title, results)
		if best == nil {
			skips = append(skips, trackSkip{SourceKey: sourceKey, Err: fmt.Errorf("no search match for %q", title)})
			continue
		}

		adapter, err := source.Select(domain.MonitoredManga{
			Source:         sourceKey,
			Manga:          best.URL,
			QualityProfile: profileRef,
		})
		if err != nil {
			skips = append(skips, trackSkip{SourceKey: sourceKey, Err: err})
			continue
		}
		if err := adapter.ValidateInput(); err != nil {
			skips = append(skips, trackSkip{SourceKey: sourceKey, Err: fmt.Errorf("invalid series %q: %w", best.URL, err)})
			continue
		}

		manga, err := adapter.Discover(ctx)
		if err != nil {
			skips = append(skips, trackSkip{SourceKey: sourceKey, Err: fmt.Errorf("discovering %q: %w", best.URL, err)})
			continue
		}
		perSource = append(perSource, profileSourceManga{SourceKey: sourceKey, Source: adapter, Manga: manga})
	}

	if len(perSource) == 0 {
		return domain.Manga{}, nil, skips, fmt.Errorf("title %q: no scanned source matched the series", title)
	}

	best, err := bestPerNumber(groups, profiles, profileRef, perSource)
	if err != nil {
		return domain.Manga{}, nil, skips, err
	}

	merged := domain.Manga{
		Title:    title,
		Chapters: make(map[domain.ChapterNumber]domain.Chapter, len(best)),
	}
	for number, chapter := range best {
		merged.Chapters[number] = chapter.chapter
	}

	return merged, best, skips, nil
}

// profileChapter is the winning (source, chapter) for one chapter number
// together with its profile decision.
type profileChapter struct {
	from     profileSourceManga
	chapter  domain.Chapter
	decision domain.Decision
}

// bestPerNumber resolves every (source, chapter) across the scanned sources
// against the profile and picks the highest-preferred non-ignored candidate
// per chapter number (resolve.BestCandidate).
func bestPerNumber(groups *domain.GroupRegistry, profiles *domain.ProfileRegistry, profileRef string, perSource []profileSourceManga) (map[domain.ChapterNumber]profileChapter, error) {
	profile := resolve.FindProfile(profiles, profileRef)
	if profile == nil {
		return nil, fmt.Errorf("profile %q not found", profileRef)
	}

	byKey := make(map[string]profileSourceManga, len(perSource))
	for _, ps := range perSource {
		byKey[ps.SourceKey] = ps
	}

	// Candidates per chapter number, in deterministic source order.
	candidates := make(map[domain.ChapterNumber][]resolve.ChapterCandidate)
	for _, ps := range perSource {
		resolver := source.NewNativeGroupResolver(ps.SourceKey, ps.Source)
		for number, chapter := range ps.Manga.Chapters {
			decision := resolve.ResolveWithResolver(groups, profiles, ps.SourceKey, chapter.Group, profileRef, resolver)
			candidates[number] = append(candidates[number], resolve.ChapterCandidate{
				SourceKey: ps.SourceKey,
				Chapter:   chapter,
				Decision:  decision,
			})
		}
	}

	best := make(map[domain.ChapterNumber]profileChapter, len(candidates))
	for number, list := range candidates {
		winner, ok := resolve.BestCandidate(profile, list)
		if !ok {
			continue
		}
		from, ok := byKey[winner.SourceKey]
		if !ok {
			continue
		}
		best[number] = profileChapter{from: from, chapter: winner.Chapter, decision: winner.Decision}
	}

	return best, nil
}

// pickSeries chooses the best series for a tracked title from search results:
// an exact (case-insensitive) title match, else the first result, following
// Sonarr's ranking convention (first hit wins when no exact match).
func pickSeries(title string, results []domain.SearchResult) *domain.SearchResult {
	if len(results) == 0 {
		return nil
	}
	for i := range results {
		if strings.EqualFold(strings.TrimSpace(results[i].Title), strings.TrimSpace(title)) {
			return &results[i]
		}
	}
	return &results[0]
}

// logTrackSkips logs soft per-source skips deterministically.
func logTrackSkips(log func(format string, args ...any), skips []trackSkip) {
	sort.Slice(skips, func(i, j int) bool { return skips[i].SourceKey < skips[j].SourceKey })
	for _, skip := range skips {
		log("skipping source %s: %v", skip.SourceKey, skip.Err)
	}
}
