// Command neomd is a minimal Neovim-flavored Markdown email client.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sspaeti/neomd/internal/config"
	goIMAP "github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/screener"
	"github.com/sspaeti/neomd/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=v0.1.0"
var version = "dev"

func main() {
	cfgPath := flag.String("config", "", "path to config.toml (default: ~/.config/neomd/config.toml)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("neomd", version)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		if strings.Contains(err.Error(), "please fill in") {
			fmt.Fprintln(os.Stderr, "neomd:", err)
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "neomd: config error: %v\n", err)
		os.Exit(1)
	}

	accounts := cfg.ActiveAccounts()
	if len(accounts) == 0 {
		fmt.Fprintln(os.Stderr, "neomd: no accounts configured in config.toml")
		os.Exit(1)
	}

	// Build one IMAP client per account.
	imapClients := make([]*goIMAP.Client, 0, len(accounts))
	for _, acc := range accounts {
		if acc.User == "" || acc.Password == "" {
			fmt.Fprintf(os.Stderr, "neomd: account %q: user/password not set\n", acc.Name)
			os.Exit(1)
		}
		h, p := splitAddr(acc.IMAP)
		imapClients = append(imapClients, goIMAP.New(goIMAP.Config{
			Host:     h,
			Port:     p,
			User:     acc.User,
			Password: acc.Password,
			TLS:      p == "993",
			STARTTLS: p == "143",
		}))
	}
	defer func() {
		for _, c := range imapClients {
			c.Close()
		}
	}()

	// Screener (shared across accounts — same allowlist files).
	sc, err := screener.New(screener.Config{
		ScreenedIn:  cfg.Screener.ScreenedIn,
		ScreenedOut: cfg.Screener.ScreenedOut,
		Feed:        cfg.Screener.Feed,
		PaperTrail:  cfg.Screener.PaperTrail,
		Spam:        cfg.Screener.Spam,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "neomd: screener error: %v\n", err)
		os.Exit(1)
	}

	ui.Version = version
	model := ui.New(cfg, imapClients, sc)

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "neomd: %v\n", err)
		os.Exit(1)
	}
}

func splitAddr(addr string) (host, port string) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "993"
	}
	return addr[:i], addr[i+1:]
}
