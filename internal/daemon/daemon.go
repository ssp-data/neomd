// Package daemon provides a headless background mode for neomd that
// continuously screens emails without launching the TUI.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/screener"
)

// Daemon runs headless email screening in the background.
type Daemon struct {
	cfg      config.Config
	imapCli  *imap.Client
	screener *screener.Screener
	logger   *slog.Logger
}

// New creates a new daemon instance.
func New(cfg config.Config, imapCli *imap.Client, sc *screener.Screener) *Daemon {
	return &Daemon{
		cfg:      cfg,
		imapCli:  imapCli,
		screener: sc,
		logger:   slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
}

// Run starts the daemon and blocks until interrupted by a signal.
func (d *Daemon) Run(ctx context.Context) error {
	d.logger.Info("neomd daemon starting", "version", "headless")

	// Check bg_sync_interval
	intervalMins := d.cfg.UI.BgSyncInterval
	if intervalMins <= 0 {
		return fmt.Errorf("bg_sync_interval must be > 0 for daemon mode (got %d)", intervalMins)
	}
	interval := time.Duration(intervalMins) * time.Minute
	d.logger.Info("screening interval configured", "minutes", intervalMins)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Set up file watcher for screener list changes
	watcher, err := d.setupFileWatcher()
	if err != nil {
		return fmt.Errorf("setup file watcher: %w", err)
	}
	defer watcher.Close()

	// Create a context that gets cancelled on signal
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Run initial screening immediately
	d.logger.Info("running initial screening")
	if err := d.screenInbox(ctx); err != nil {
		d.logger.Error("initial screening failed", "error", err)
	}

	// Set up ticker for periodic screening
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	d.logger.Info("daemon running", "interval", interval.String())

	// Main event loop
	for {
		select {
		case <-ticker.C:
			d.logger.Info("running scheduled screening")
			if err := d.screenInbox(ctx); err != nil {
				d.logger.Error("screening failed", "error", err)
			}

		case event := <-watcher.Events:
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				d.logger.Info("screener list changed, reloading", "file", filepath.Base(event.Name))
				if err := d.reloadScreener(); err != nil {
					d.logger.Error("failed to reload screener", "error", err)
				}
			}

		case err := <-watcher.Errors:
			d.logger.Error("file watcher error", "error", err)

		case sig := <-sigChan:
			d.logger.Info("received signal, shutting down", "signal", sig.String())
			cancel()
			return nil
		}
	}
}

// setupFileWatcher creates a file watcher for all screener list files.
func (d *Daemon) setupFileWatcher() (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Watch all screener list files
	paths := []string{
		d.cfg.Screener.ScreenedIn,
		d.cfg.Screener.ScreenedOut,
		d.cfg.Screener.Feed,
		d.cfg.Screener.PaperTrail,
		d.cfg.Screener.Spam,
	}

	watchedDirs := make(map[string]bool)
	for _, path := range paths {
		if path == "" {
			continue
		}
		// Watch the directory containing the file, since some editors
		// create temp files and rename them (which breaks file watches)
		dir := filepath.Dir(path)
		if !watchedDirs[dir] {
			if err := watcher.Add(dir); err != nil {
				d.logger.Warn("failed to watch directory", "dir", dir, "error", err)
			} else {
				d.logger.Info("watching directory for changes", "dir", dir)
				watchedDirs[dir] = true
			}
		}
	}

	return watcher, nil
}

// reloadScreener reloads the screener from disk.
func (d *Daemon) reloadScreener() error {
	newScreener, err := screener.New(screener.Config{
		ScreenedIn:  d.cfg.Screener.ScreenedIn,
		ScreenedOut: d.cfg.Screener.ScreenedOut,
		Feed:        d.cfg.Screener.Feed,
		PaperTrail:  d.cfg.Screener.PaperTrail,
		Spam:        d.cfg.Screener.Spam,
	})
	if err != nil {
		return fmt.Errorf("reload screener: %w", err)
	}
	d.screener = newScreener
	d.logger.Info("screener reloaded successfully")
	return nil
}

// screenInbox fetches inbox emails and screens them.
func (d *Daemon) screenInbox(ctx context.Context) error {
	inboxFolder := d.cfg.Folders.Inbox

	// Fetch inbox headers (0 means fetch all)
	emails, err := d.imapCli.FetchHeaders(ctx, inboxFolder, 0)
	if err != nil {
		return fmt.Errorf("fetch inbox headers: %w", err)
	}

	if len(emails) == 0 {
		d.logger.Info("inbox is empty, nothing to screen")
		return nil
	}

	d.logger.Info("fetched inbox emails", "count", len(emails))

	// Classify emails using shared screener logic
	moves, err := screener.ClassifyForScreen(d.screener, emails, d.cfg.Folders)
	if err != nil {
		return fmt.Errorf("classify emails: %w", err)
	}

	if len(moves) == 0 {
		d.logger.Info("no emails need screening")
		return nil
	}

	d.logger.Info("emails to screen", "count", len(moves))

	// Execute moves
	movedCount := 0
	for i, mv := range moves {
		uid := mv.Email.UID
		from := mv.Email.From
		subject := mv.Email.Subject
		dst := mv.Dst

		if err := ctx.Err(); err != nil {
			d.logger.Warn("screening interrupted", "moved", movedCount, "total", len(moves))
			return err
		}

		_, err := d.imapCli.MoveMessage(ctx, inboxFolder, uid, dst)
		if err != nil {
			d.logger.Error("failed to move email",
				"index", i+1,
				"uid", uid,
				"from", from,
				"subject", subject,
				"dst", dst,
				"error", err)
			continue
		}

		movedCount++
		d.logger.Info("screened email",
			"index", i+1,
			"total", len(moves),
			"from", from,
			"subject", subject,
			"dst", dst)
	}

	d.logger.Info("screening complete", "moved", movedCount, "total", len(moves))
	return nil
}
