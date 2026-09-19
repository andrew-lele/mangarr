package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"mangarr/internal/files"

	"github.com/spf13/cobra"
)

type cbzRetrofitOptions struct {
	apply   bool
	logPath string
}

func newCbzRetrofitCommand() *cobra.Command {
	options := &cbzRetrofitOptions{}
	command := &cobra.Command{
		Use:   "cbz-retrofit <library-root>",
		Short: "Inject ComicInfo.xml into existing bare .cbz archives",
		Example: `  mangarr cbz-retrofit /data/library/Manga            # dry run: report only
  mangarr cbz-retrofit /data/library/Manga --apply   # rebuild archives in place`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCbzRetrofit(cmd.Context(), cmd.OutOrStdout(), options, args[0])
		},
	}

	command.Flags().BoolVar(
		&options.apply,
		"apply",
		false,
		"rebuild archives in place (default is a dry run that only reports)",
	)
	command.Flags().StringVar(
		&options.logPath,
		"log",
		"cbz-retrofit.log",
		"append one line per archive to this file",
	)

	return command
}

func runCbzRetrofit(ctx context.Context, out io.Writer, options *cbzRetrofitOptions, root string) error {
	entries, err := files.RetrofitLibrary(ctx, root, !options.apply)
	if err != nil {
		return err
	}

	logFile, err := os.OpenFile(options.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening log file %s: %w", options.logPath, err)
	}
	defer logFile.Close()

	for _, entry := range entries {
		line := entryLine(entry)
		if _, err := fmt.Fprintln(logFile, line); err != nil {
			return fmt.Errorf("writing log file: %w", err)
		}
	}

	// Tab-separated summary, one line per status, mirroring the log format.
	for _, status := range []files.RetrofitStatus{
		files.RetrofitRebuilt,
		files.RetrofitWouldRebuild,
		files.RetrofitAlreadyHasMetadata,
		files.RetrofitSkippedNoNumber,
	} {
		count := 0
		for _, entry := range entries {
			if entry.Status == status {
				count++
			}
		}
		fmt.Fprintf(out, "%s\t%d\n", status, count)
	}

	errorCount := 0
	for _, entry := range entries {
		if entry.Err != nil {
			errorCount++
			fmt.Fprintf(out, "error\t%s\t%v\n", entry.Path, entry.Err)
		}
	}
	if errorCount > 0 {
		fmt.Fprintf(out, "errors\t%d\n", errorCount)
	}

	return nil
}

// entryLine renders one archive result as a tab-separated line:
// path\tstatus\tseries\tnumber\ttitle\tpages. Errors carry the message
// instead of the metadata columns.
func entryLine(entry files.RetrofitEntry) string {
	if entry.Err != nil {
		return fmt.Sprintf("%s\terror\t%v", entry.Path, entry.Err)
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%d",
		entry.Path, entry.Status, entry.Series, entry.Number, entry.Title, entry.Pages)
}
