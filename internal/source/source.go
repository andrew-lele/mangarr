package source

import (
	"fmt"
	"slices"

	"mangarr/internal/domain"
)

type constructor func(domain.MonitoredManga) domain.Source

var registry = map[string]constructor{
	"asurascans": func(m domain.MonitoredManga) domain.Source { return NewAsurascans(m.Manga) },
	"atsumaru":   func(m domain.MonitoredManga) domain.Source { return NewAtsumaru(m.Manga, m.Group, m.QualityProfile) },
	"comix":      func(m domain.MonitoredManga) domain.Source { return NewComix(m.Manga, m.Group, m.ImpersonationProxy) },
	"cubari":     func(m domain.MonitoredManga) domain.Source { return NewCubari(m.Manga, m.Group) },
	"flamecomics": func(m domain.MonitoredManga) domain.Source {
		return NewFlamecomics(m.Manga)
	},
	"mangadex": func(m domain.MonitoredManga) domain.Source {
		return NewMangadex(m.Manga, m.Group, m.Language)
	},
	"mangaplus":   func(m domain.MonitoredManga) domain.Source { return NewMangaPlus(m.Manga) },
	"tcbscans":    func(m domain.MonitoredManga) domain.Source { return NewTCBScans(m.Manga) },
	"weebcentral": func(m domain.MonitoredManga) domain.Source { return NewWeebCentral(m.Manga) },
}

func Select(monitoredManga domain.MonitoredManga) (domain.Source, error) {
	newSource, ok := registry[monitoredManga.Source]
	if !ok {
		return nil, fmt.Errorf("unknown monitored manga source %s", monitoredManga.Source)
	}

	return newSource(monitoredManga), nil
}

// Keys returns the supported source keys in deterministic order, for
// validating and intersecting profile scan-source sets.
func Keys() []string {
	keys := make([]string, 0, len(registry))
	for key := range registry {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// nativeResolvers maps source names to factories that close a per-source
// native-id resolver extension over the concrete source adapter instance.
// Sources whose native group ids are GLOBAL (comix numeric GroupID, mangadex
// scanlation UUID) have no entry and keep the default NativeIndex lookup in
// internal/resolve. atsumaru's ids are scoped per-manga, so its resolver
// bridges the chapter ScanID through the adapter's scanlator cache.
var nativeResolvers = map[string]func(domain.Source) domain.NativeResolver{
	"atsumaru": func(s domain.Source) domain.NativeResolver {
		return func(groups *domain.GroupRegistry, profile *domain.Profile, nativeGroup string) string {
			return s.(*atsumaru).ResolveNativeGroup(groups, profile, nativeGroup)
		}
	},
}

// NewNativeGroupResolver returns the per-source native-id resolver extension
// for sourceKey closed over the adapter instance s, or nil when the source
// has no extension (the caller then uses the default NativeIndex lookup).
// Pass the same source instance that is used for the chapter acquisition so
// per-manga data (atsumaru scanlators) is available to the resolver.
func NewNativeGroupResolver(sourceKey string, s domain.Source) domain.NativeResolver {
	factory, ok := nativeResolvers[sourceKey]
	if !ok {
		return nil
	}

	return factory(s)
}
