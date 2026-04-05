// Command neomd is a minimal Neovim-flavored Markdown email client.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sspaeti/neomd/internal/config"
	goIMAP "github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/oauth2"
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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

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
		h, p := splitAddr(acc.IMAP)
		imapCfg := goIMAP.Config{
			Host:     h,
			Port:     p,
			User:     acc.User,
			Password: acc.Password,
			TLS:      p == "993",
			STARTTLS: p == "143",
		}
		if acc.IsOAuth2() {
			if acc.OAuth2ClientID == "" {
				fmt.Fprintf(os.Stderr, "neomd: account %q: oauth2_client_id is required\n", acc.Name)
				os.Exit(1)
			}
			if acc.OAuth2IssuerURL == "" && (acc.OAuth2AuthURL == "" || acc.OAuth2TokenURL == "") {
				fmt.Fprintf(os.Stderr, "neomd: account %q: set oauth2_issuer_url or both oauth2_auth_url and oauth2_token_url\n", acc.Name)
				os.Exit(1)
			}
			tokenFile, err := config.TokenFilePath(acc.Name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "neomd: account %q: %v\n", acc.Name, err)
				os.Exit(1)
			}
			ts, err := oauth2.TokenSource(ctx, oauth2.Config{
				ClientID:     acc.OAuth2ClientID,
				ClientSecret: acc.OAuth2ClientSecret,
				IssuerURL:    acc.OAuth2IssuerURL,
				AuthURL:      acc.OAuth2AuthURL,
				TokenURL:     acc.OAuth2TokenURL,
				Scopes:       acc.OAuth2Scopes,
				RedirectPort: acc.OAuth2RedirectPort,
				TokenFile:    tokenFile,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "neomd: account %q: oauth2: %v\n", acc.Name, err)
				os.Exit(1)
			}
			imapCfg.TokenSource = ts
		} else if acc.User == "" || acc.Password == "" {
			fmt.Fprintf(os.Stderr, "neomd: account %q: user/password not set\n", acc.Name)
			os.Exit(1)
		}
		imapClients = append(imapClients, goIMAP.New(imapCfg))
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
