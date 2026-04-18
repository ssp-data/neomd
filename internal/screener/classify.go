package screener

import (
	"fmt"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
)

// ScreenMove represents a planned (not yet executed) IMAP move for auto-screening.
type ScreenMove struct {
	Email *imap.Email
	Dst   string
}

// ClassifyForScreen classifies a slice of inbox emails in-memory (O(1) map
// lookups) and returns planned moves. emails must live at least as long as the
// returned moves (pointers into the slice are stored).
func ClassifyForScreen(screener *Screener, emails []imap.Email, folderCfg config.FoldersConfig) ([]ScreenMove, error) {
	// Validate screener safety (check that no screening folder points to Trash)
	if err := ValidateScreenerSafety(folderCfg); err != nil {
		return nil, err
	}

	inboxFolder := folderCfg.Inbox
	var moves []ScreenMove
	for i := range emails {
		e := &emails[i]
		cat := screener.Classify(e.From)
		var dst string
		switch cat {
		case CategorySpam:
			dst = folderCfg.Spam
		case CategoryScreenedOut:
			dst = folderCfg.ScreenedOut
		case CategoryFeed:
			dst = folderCfg.Feed
		case CategoryPaperTrail:
			dst = folderCfg.PaperTrail
		case CategoryToScreen:
			dst = folderCfg.ToScreen
		}
		if dst != "" && dst != inboxFolder {
			moves = append(moves, ScreenMove{Email: e, Dst: dst})
		}
	}
	return moves, nil
}

// ValidateScreenerSafety ensures that no screener destination folder points to Trash.
func ValidateScreenerSafety(folderCfg config.FoldersConfig) error {
	dests := map[string]string{
		"ToScreen":    folderCfg.ToScreen,
		"ScreenedOut": folderCfg.ScreenedOut,
		"Feed":        folderCfg.Feed,
		"PaperTrail":  folderCfg.PaperTrail,
		"Spam":        folderCfg.Spam,
	}
	for name, folder := range dests {
		if folder != "" && folder == folderCfg.Trash {
			return fmt.Errorf("unsafe folder config: %s points to Trash (%s); refusing to screen until config is fixed", name, folder)
		}
	}
	return nil
}
