package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"mangarr/internal/acquire"
	"mangarr/internal/buildinfo"
	"mangarr/internal/config"
	"mangarr/internal/domain"
	"mangarr/internal/files"
	"mangarr/internal/logger"
	"mangarr/internal/parse"
	"mangarr/internal/perf"
	"mangarr/internal/registry"
	"mangarr/internal/source"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

func newMonitorCommand(options *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:          "monitor",
		Short:        "Monitor a specified manga for new chapters",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runCtx, stop := signal.NotifyContext(
				cmd.Context(),
				syscall.SIGHUP,
				syscall.SIGINT,
				syscall.SIGQUIT,
				syscall.SIGTERM,
			)
			defer stop()

			cfg, err := config.Load(options.configPath, buildinfo.Version)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			snapshot := cfg.Snapshot()

			log := logger.New(&snapshot)

			if err := cfg.UpdateConfig(); err != nil {
				log.Error().Err(err).Msg("error updating config")
			}

			reloads, err := cfg.DynamicReload(log)
			if err != nil {
				log.Error().Err(err).Msg("dynamic config reload disabled")
				reloads = make(chan struct{})
			}

			if err := files.IsValidLocation(snapshot.DownloadLocation); err != nil {
				return fmt.Errorf("invalid download location: %w", err)
			}

			log.Info().Msg("Starting to monitor configured manga")
			log.Info().Msgf("Version: %s", buildinfo.Version)
			log.Info().Msgf("Commit: %s", buildinfo.Commit)
			log.Info().Msgf("Build date: %s", buildinfo.Date)
			log.Info().Msgf("Log-level: %s", snapshot.LogLevel)

			if snapshot.PprofEnabled {
				pprofAddress, err := perf.StartPprofServer(runCtx, log, snapshot.PprofAddress)
				if err != nil {
					log.Error().Err(err).Msg("error starting pprof endpoint")
				} else {
					log.Info().Msgf("pprof endpoint available at http://%s/debug/pprof/", pprofAddress)
				}
			}

			runMonitorCycle(runCtx, snapshot, log)

			timer := time.NewTimer(snapshot.CheckInterval * time.Minute)
			defer timer.Stop()
			for {
				select {
				case <-runCtx.Done():
					log.Info().Msg("stopping monitoring")
					return nil
				case <-reloads:
					snapshot = cfg.Snapshot()
					resetTimer(timer, snapshot.CheckInterval*time.Minute)
				case <-timer.C:
					snapshot = cfg.Snapshot()
					runMonitorCycle(runCtx, snapshot, log)
					timer.Reset(snapshot.CheckInterval * time.Minute)
				}
			}
		},
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

// decisionLogSuffix renders the [source=<key> group=<alias> decision=<outcome>]
// suffix for dry-run reports. An empty canonical id (unset profile, unknown
// native id, or a chapter without a group) yields a source-only suffix so the
// selection is still visible.
func decisionLogSuffix(sourceKey string, decision domain.Decision) string {
	suffix := fmt.Sprintf(" [source=%s", sourceKey)
	if decision.CanonicalID != "" {
		group := decision.GroupName
		if group == "" {
			group = decision.CanonicalID
		}
		suffix = suffix + fmt.Sprintf(" group=%s decision=%s", group, decision.Outcome)
	}
	return suffix + "]"
}

// runMonitorCycle checks every configured manga once. Groups and profiles are
// loaded fresh from the config directory each cycle, so registry edits are
// picked up without restarting monitor; a load failure aborts the cycle (the
// error is logged and the next interval retries).
func runMonitorCycle(ctx context.Context, cfg domain.Config, log *logger.DefaultLogger) {
	if err := files.IsValidLocation(cfg.DownloadLocation); err != nil {
		log.Error().Err(err).Msg("invalid download location")
		return
	}

	requestGroups, groupsErr := registry.LoadGroups(cfg.ConfigPath)
	if groupsErr != nil {
		log.Error().Err(groupsErr).Msg("loading groups registry")
		return
	}
	requestProfiles, profilesErr := registry.LoadProfiles(cfg.ConfigPath, &requestGroups)
	if profilesErr != nil {
		log.Error().Err(profilesErr).Msg("loading profiles registry")
		return
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(maxConcurrentSourceProcesses)
	for mangaTitle, monitoredManga := range cfg.MonitoredManga {
		group.Go(func() error {
			if err := monitorManga(groupCtx, cfg, &requestGroups, &requestProfiles, mangaTitle, *monitoredManga, log); err != nil {
				log.Error().Err(err).
					Str("manga", mangaTitle).
					Str("source", monitoredManga.Source).
					Msg("monitor check failed")
			}
			return nil
		})
	}
	_ = group.Wait()
}

func monitorManga(ctx context.Context, cfg domain.Config, groups *domain.GroupRegistry, profiles *domain.ProfileRegistry, mangaTitle string, monitoredManga domain.MonitoredManga, log *logger.DefaultLogger) error {
	mangaSource, err := source.Select(monitoredManga)
	if err != nil {
		return fmt.Errorf("selecting manga source: %w", err)
	}
	mLog := log.With().Str("manga", mangaTitle).Str("source", mangaSource.String()).Logger()

	if err := mangaSource.ValidateInput(); err != nil {
		return fmt.Errorf("validating input: %w", err)
	}

	selectedManga, err := mangaSource.Discover(ctx)
	if err != nil {
		return fmt.Errorf("getting manga from %s: %w", monitoredManga.Source, err)
	}

	_, latestChapterNr, err := parse.MinMaxChapterNumbers(selectedManga.Chapters)
	if err != nil {
		return fmt.Errorf("getting latest chapter number: %w", err)
	}
	selectedChapter, ok := selectedManga.Chapters[latestChapterNr]
	if !ok {
		return fmt.Errorf("finding chapter with number %s", latestChapterNr)
	}

	result, err := acquire.Chapter(ctx, mLog, acquire.Request{
		Source:             mangaSource,
		SourceKey:          monitoredManga.Source,
		Manga:              selectedManga,
		Chapter:            selectedChapter,
		DownloadDirectory:  cfg.DownloadLocation,
		NamingTemplate:     cfg.NamingTemplate,
		TitleOverride:      monitoredManga.Overwrite,
		DryRun:             cfg.DryRun,
		Groups:             groups,
		Profiles:           profiles,
		ProfileRef:         monitoredManga.QualityProfile,
		ResolveNativeGroup: source.NewNativeGroupResolver(monitoredManga.Source, mangaSource),
	})
	if err != nil {
		return err
	}
	if result.Status == acquire.DryRun {
		mLog.Info().Msgf("Would download %s -> %s%s", result.Name, result.Path, decisionLogSuffix(result.SourceKey, result.Decision))
		return nil
	}
	if result.Status == acquire.Skipped {
		mLog.Debug().Msgf("chapter has already been downloaded, skipping %s", result.Name)
		return nil
	}
	mLog.Info().Msgf("finished downloading %s", result.Name)

	return nil
}
