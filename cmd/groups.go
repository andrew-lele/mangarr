package cmd

import (
	"fmt"

	"mangarr/internal/domain"

	"github.com/spf13/cobra"
)

type groupsOptions struct {
	search             string
	manga              string
	impersonationProxy string
}

func newGroupsCommand(selectGroupLister func(domain.GroupSearchOptions) (domain.GroupLister, error)) *cobra.Command {
	options := &groupsOptions{}
	command := &cobra.Command{
		Use:   "groups <source>",
		Short: "List scanlation groups with their native IDs for a source",
		Example: `  mangarr groups comix --search "flame"
  mangarr groups atsumaru -m https://atsu.moe/manga/Q5Mqy`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			lister, err := selectGroupLister(domain.GroupSearchOptions{
				Source:             args[0],
				Manga:              options.manga,
				ImpersonationProxy: options.impersonationProxy,
			})
			if err != nil {
				// "no group model for this source" is a successful answer,
				// not a command failure: print it plainly and exit 0 so
				// scripts can iterate over sources.
				fmt.Fprintln(cmd.OutOrStdout(), err)
				return nil
			}

			groups, err := lister.Groups(cmd.Context(), options.search)
			if err != nil {
				return fmt.Errorf("listing groups from %s: %w", args[0], err)
			}

			for _, group := range groups {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s", group.ID, group.Name)
				if group.Slug != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "\t%s", group.Slug)
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}

			return nil
		},
	}

	command.Flags().StringVar(
		&options.search,
		"search",
		"",
		"list groups whose name contains the query (case-insensitive); empty lists the source's first page of groups",
	)
	command.Flags().StringVarP(
		&options.manga,
		"manga",
		"m",
		"",
		"atsumaru: manga URL whose scanlation groups are listed",
	)
	command.Flags().StringVar(
		&options.impersonationProxy,
		"impersonation-proxy",
		"",
		"comix: FlareSolverr-compatible relay origin that solves comix.to's Cloudflare challenge",
	)

	return command
}
