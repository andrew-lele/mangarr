package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"mangarr/internal/domain"

	"github.com/stretchr/testify/require"
)

func TestGroupsCommandPrintsNativeIDs(t *testing.T) {
	t.Parallel()

	lister := &mockGroupLister{
		groups: []domain.ScanlationGroup{
			{ID: "9641", Name: "Flame Comics", Slug: "flame-comics"},
			{ID: "8421", Name: "Asura Scans", Slug: ""},
		},
	}
	root := newRootCommand(dependencies{
		selectGroupLister: func(options domain.GroupSearchOptions) (domain.GroupLister, error) {
			require.Equal(t, "comix", options.Source)
			require.Equal(t, "http://127.0.0.1:8191", options.ImpersonationProxy)
			return lister, nil
		},
		versionClient: nil,
		releaseURL:    githubURL,
	})
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{
		"groups", "comix",
		"--search", "flame",
		"--impersonation-proxy", "http://127.0.0.1:8191",
	})

	require.NoError(t, root.Execute())
	require.Equal(t, "flame", lister.query)
	require.Equal(t, "9641\tFlame Comics\tflame-comics\n8421\tAsura Scans\n", stdout.String())
}

func TestGroupsCommandSourceWithoutGroupsSucceedsWithMessage(t *testing.T) {
	t.Parallel()

	root := newRootCommand(dependencies{
		selectGroupLister: func(_ domain.GroupSearchOptions) (domain.GroupLister, error) {
			return nil, errors.New("source mangaplus does not expose scanlation groups")
		},
		versionClient: nil,
		releaseURL:    githubURL,
	})
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"groups", "mangaplus"})

	require.NoError(t, root.Execute())
	require.Equal(t, "source mangaplus does not expose scanlation groups\n", stdout.String())
}

func TestGroupsCommandFailsOnListingError(t *testing.T) {
	t.Parallel()

	root := newRootCommand(dependencies{
		selectGroupLister: func(_ domain.GroupSearchOptions) (domain.GroupLister, error) {
			return &mockGroupLister{}, nil
		},
		versionClient: nil,
		releaseURL:    githubURL,
	})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"groups", "comix"})

	err := root.Execute()
	require.ErrorContains(t, err, "listing groups from comix")
}

type mockGroupLister struct {
	groups []domain.ScanlationGroup
	query  string
}

func (m *mockGroupLister) Groups(ctx context.Context, query string) ([]domain.ScanlationGroup, error) {
	m.query = query
	if len(m.groups) == 0 {
		return nil, errors.New("mock groups unavailable")
	}
	return m.groups, nil
}
