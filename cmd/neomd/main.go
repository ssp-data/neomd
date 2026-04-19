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
	"github.com/sspaeti/neomd/internal/daemon"
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
	headless := flag.Bool("headless", false, "run in headless daemon mode (no TUI)")
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
		// Determine TLS/STARTTLS: respect explicit user config, otherwise infer from port.
		// Security: non-standard ports default to TLS (e.g., Proton Mail Bridge on 1143).
		useTLS, useSTARTTLS := inferIMAPSecurity(p, acc.STARTTLS)
		imapCfg := goIMAP.Config{
			Host:        h,
			Port:        p,
			User:        acc.User,
			Password:    acc.Password,
			TLS:         useTLS,
			STARTTLS:    useSTARTTLS,
			TLSCertFile: acc.TLSCertFile,
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

	// Fork: run either headless daemon or TUI
	if *headless {
		// Headless daemon mode: run background screening loop
		d := daemon.New(*cfg, imapClients[0], sc)
		if err := d.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "neomd: daemon error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// TUI mode: run interactive interface
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
}

func splitAddr(addr string) (host, port string) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "993"
	}
	return addr[:i], addr[i+1:]
}

// inferIMAPSecurity determines TLS/STARTTLS settings based on port and user config.
// Returns (useTLS, useSTARTTLS).
//
// Logic:
//   - If userSTARTTLS is true: always use STARTTLS (user explicitly enabled it)
//   - Standard ports: 993 → TLS, 143 → STARTTLS
//   - Non-standard ports: default to TLS (e.g., Proton Mail Bridge on 1143)
func inferIMAPSecurity(port string, userSTARTTLS bool) (useTLS, useSTARTTLS bool) {
	if userSTARTTLS {
		// User explicitly set starttls=true in config — honor it.
		return false, true
	}
	switch port {
	case "993":
		return true, false // Standard IMAPS (implicit TLS)
	case "143":
		return false, true // Standard IMAP (STARTTLS upgrade)
	default:
		// Non-standard port (e.g., Proton Mail Bridge): default to TLS for security.
		return true, false
	}
}
