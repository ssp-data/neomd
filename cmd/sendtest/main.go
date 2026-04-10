// sendtest sends a test email using the neomd config to verify HTML rendering.
// Recipients are read from NEOMD_TEST_RECIPIENTS in .env (comma-separated).
// Usage: go run ./cmd/sendtest [recipient...]
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/smtp"
)

// backtick cannot appear inside a Go raw string literal, so we concatenate.
var testBody = `This email is from Neomd. Great I can add links such as [this](https://ssp.sh) with plain Markdown.

E.g. **bold** or _italic_.

## Does headers work too?

this is a text before a h3.

here's an image in-line:
[attach] /home/sspaeti/git/email/neomd/images/overview-email-feed.png

### H3 header

Code block here:
` + "```python\nclass A:\n    a = 42\n    b = list(a + i for i in range(10))\n```" + `

or just a ` + "`inline code block`" + ` looks like this.

how does that look in an email?
Best regards`

func main() {
	loadDotEnv(".env")

	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sendtest: load config: %v\n", err)
		os.Exit(1)
	}

	recipients := os.Args[1:]
	if len(recipients) == 0 {
		raw := os.Getenv("NEOMD_TEST_RECIPIENTS")
		for _, r := range strings.Split(raw, ",") {
			if r = strings.TrimSpace(r); r != "" {
				recipients = append(recipients, r)
			}
		}
	}
	if len(recipients) == 0 {
		fmt.Fprintln(os.Stderr, "sendtest: no recipients — set NEOMD_TEST_RECIPIENTS in .env")
		os.Exit(1)
	}

	accounts := cfg.ActiveAccounts()
	if len(accounts) == 0 {
		fmt.Fprintln(os.Stderr, "sendtest: no accounts configured")
		os.Exit(1)
	}
	acc := accounts[0]

	h, p := splitAddr(acc.SMTP)
	smtpCfg := smtp.Config{
		Host:        h,
		Port:        p,
		User:        acc.User,
		Password:    acc.Password,
		From:        acc.From,
		STARTTLS:    acc.STARTTLS,
		TLSCertFile: acc.TLSCertFile,
	}

	for _, to := range recipients {
		fmt.Printf("sending to %s via %s...\n", to, acc.SMTP)
		attachments := []string{"images/overview-email-feed.png"}
		if err := smtp.Send(smtpCfg, to, "", "", "test neomd HTML rendering", testBody, attachments); err != nil {
			fmt.Fprintf(os.Stderr, "sendtest: %s: %v\n", to, err)
			os.Exit(1)
		}
		fmt.Printf("sent to %s\n", to)
	}
}

// loadDotEnv reads key=value pairs from path and sets them as env vars.
// Lines starting with # and empty lines are ignored. Silently no-ops if the
// file does not exist (e.g. in CI or other environments).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		_ = os.Setenv(strings.TrimSpace(k), strings.TrimSpace(v))
	}
}

func splitAddr(addr string) (host, port string) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "587"
	}
	return addr[:i], addr[i+1:]
}
