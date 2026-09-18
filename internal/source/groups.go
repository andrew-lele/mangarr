package source

import (
	"fmt"

	"mangarr/internal/domain"
)

// NewGroupLister returns a domain.GroupLister for the named source, or a
// clear error when the source has no scanlation-group model. Sources without
// groups (tcbscans, mangaplus, flamecomics, asurascans, cubari,
// weebcentral, mangadex) intentionally implement only domain.Source; the
// groups command surfaces them as "does not expose scanlation groups".
// Unknown source names are rejected before the group-model check so typos
// do not masquerade as a missing capability.
func NewGroupLister(options domain.GroupSearchOptions) (domain.GroupLister, error) {
	if _, known := registry[options.Source]; !known {
		return nil, fmt.Errorf("unknown source %s", options.Source)
	}
	if options.Source == "comix" {
		return NewComixGroupLister(options.ImpersonationProxy), nil
	}
	if options.Source == "atsumaru" {
		return NewAtsumaruGroupLister(options.Manga), nil
	}

	return nil, fmt.Errorf("source %s does not expose scanlation groups", options.Source)
}
