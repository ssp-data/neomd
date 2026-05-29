// Package notify fires desktop notifications (via notify-send or compatible
// CLI) for newly arrived emails whose sender is on the screener notify list.
//
// TUI-only: the headless daemon does not invoke this package, so notifications
// never fire on a server / NAS where no one would see them.
package notify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/screener"
)

// sendTimeout caps how long we'll wait for the notification command to
// return.  MaybeNotify runs inside the bubbletea Update loop, so a hung
// notify-send (broken DBus, mako restarting, …) would otherwise freeze the
// TUI.  notify-send normally returns in single-digit milliseconds; 2 s is a
// generous ceiling that still keeps the UI responsive.
const sendTimeout = 2 * time.Second

// Notifier wraps a notification command (notify-send by default) and a
// resolved-defaults config snapshot.
type Notifier struct {
	cfg config.NotificationsConfig
}

// New returns a Notifier. Send is a no-op when cfg.Enabled is false.
func New(cfg config.NotificationsConfig) *Notifier {
	return &Notifier{cfg: cfg.Resolved()}
}

// Enabled reports whether notifications would be sent. Useful so callers can
// skip building dstByUID maps when the feature is off.
func (n *Notifier) Enabled() bool {
	return n != nil && n.cfg.Enabled
}

// Send fires a single notification with a hard deadline so it can never
// freeze the bubbletea Update loop.  Safe to call when disabled (returns
// nil).  Returned error wraps the command's stderr so callers can surface
// useful diagnostics in the TUI status bar.
func (n *Notifier) Send(title, body string) error {
	if !n.cfg.Enabled {
		return nil
	}
	args := []string{
		"-i", n.cfg.Icon,
		"-t", strconv.Itoa(n.cfg.ExpireMs),
		"-a", "neomd",
		title,
		truncate(body, 200),
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, n.cfg.Command, args...)
	// WaitDelay ensures Wait() returns shortly after the process is killed
	// even if its stdout/stderr pipes are still being held open by a child
	// process — without this, a hung notifier could keep cmd.Run() blocked
	// long after the deadline expired.
	cmd.WaitDelay = 500 * time.Millisecond
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s: timed out after %s", n.cfg.Command, sendTimeout)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s: %s", n.cfg.Command, msg)
	}
	return nil
}

// Result summarises a MaybeNotify pass. The counters help diagnose why a
// notification did or did not fire.
type Result struct {
	Sent           int    // notifications dispatched without error
	Failed         int    // notifications attempted but the command returned an error
	Err            string // last error message (if any)
	NewSinceBase   int    // emails whose UID was strictly above the recorded baseline
	MatchedNotify  int    // of those, how many had a sender on the notify list
	FolderAllowed  int    // of those, how many landed in a folder in the allowlist
	HadBaseline    bool   // false → first-run pass, no notifications fire by design
	Baseline       uint32 // baseline used for the comparison
	MaxUIDObserved uint32 // highest UID seen in this batch
}

// MaybeNotify processes a freshly fetched batch of emails from a folder.
// For each email with UID > the per-(account, folder) baseline whose sender
// is in the screener notify list and whose post-screening destination is in
// the configured Folders allowlist, a notification fires.
//
// First-run behaviour: when no baseline exists yet, MaybeNotify silently
// records the highest UID it saw and fires *no* notifications — this prevents
// the entire current Inbox from notifying the first time the feature is
// enabled.
//
// sourceIMAP is the raw IMAP folder name (e.g. "INBOX", "HEY/Paper Trail")
// and is used as the persisted state key — keeping that stable means a user
// upgrading from an older neomd build never gets re-baselined.
//
// sourceLabel is the UI label for sourceIMAP (e.g. "Inbox", "PaperTrail")
// and is what the allowlist check compares against. When sourceIMAP and
// sourceLabel are identical (default folder names), the two arguments can
// be the same string.
//
// dstByUID maps a UID to the destination folder *label* (not IMAP name)
// where the email is about to live after auto-screening. UIDs missing from
// dstByUID are assumed to stay in sourceLabel for the allowlist check.
//
// Returns a Result describing how many notifications fired and the last
// error encountered (if any).
func (n *Notifier) MaybeNotify(account, sourceIMAP, sourceLabel string, emails []imap.Email, dstByUID map[uint32]string, sc *screener.Screener, state *State) Result {
	var res Result
	if !n.Enabled() || sc == nil || state == nil || len(emails) == 0 {
		return res
	}
	key := stateKey(account, sourceIMAP)
	baseline, hadBaseline := state.Get(key)
	res.HadBaseline = hadBaseline
	res.Baseline = baseline

	var maxUID uint32
	for i := range emails {
		e := &emails[i]
		if e.UID > maxUID {
			maxUID = e.UID
		}
		if !hadBaseline || e.UID <= baseline {
			continue
		}
		res.NewSinceBase++
		if !sc.ShouldNotify(e.From) {
			continue
		}
		res.MatchedNotify++
		dst, ok := dstByUID[e.UID]
		if !ok {
			dst = sourceLabel
		}
		if !n.cfg.FolderAllowed(dst) {
			continue
		}
		res.FolderAllowed++
		title := "󱡯: :" + truncate(e.From, 80)
		body := e.Subject
		if body == "" {
			body = "(no subject)"
		}
		if err := n.Send(title, body); err != nil {
			res.Failed++
			res.Err = err.Error()
			continue
		}
		res.Sent++
	}
	res.MaxUIDObserved = maxUID

	if maxUID > baseline {
		state.Set(key, maxUID)
		_ = state.Save()
	}
	return res
}

func stateKey(account, folder string) string {
	return account + "|" + folder
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
