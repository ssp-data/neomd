// Package smtp handles outgoing email via SMTP.
// Sends multipart/alternative (text/plain + text/html) so recipients
// get clickable links and formatted output while you write pure Markdown.
package smtp

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/sspaeti/neomd/internal/render"
)

// Config holds outgoing mail settings.
type Config struct {
	Host     string // e.g. "smtp.example.com"
	Port     string // e.g. "587" (STARTTLS) or "465" (TLS)
	User     string
	Password string
	From     string // "Name <email>"
}

// Send composes and sends an email.
// markdownBody is sent as text/plain (raw) and text/html (goldmark-rendered).
func Send(cfg Config, to, subject, markdownBody string) error {
	htmlBody, err := render.ToHTML(markdownBody)
	if err != nil {
		return fmt.Errorf("markdown to html: %w", err)
	}

	raw, err := buildMessage(cfg.From, to, subject, markdownBody, htmlBody)
	if err != nil {
		return fmt.Errorf("build message: %w", err)
	}

	toAddrs := []string{extractAddr(to)}
	fromAddr := extractAddr(cfg.From)

	addr := cfg.Host + ":" + cfg.Port
	switch cfg.Port {
	case "465": // Implicit TLS (SMTPS)
		return sendTLS(addr, cfg.Host, cfg.User, cfg.Password, fromAddr, toAddrs, raw)
	default: // STARTTLS (587) or plain (25)
		return sendSTARTTLS(addr, cfg.Host, cfg.User, cfg.Password, fromAddr, toAddrs, raw)
	}
}

// sendSTARTTLS sends via STARTTLS upgrade (port 587).
func sendSTARTTLS(addr, host, user, password, from string, to []string, msg []byte) error {
	auth := smtp.PlainAuth("", user, password, host)
	return smtp.SendMail(addr, auth, from, to, msg)
}

// sendTLS sends via implicit TLS (port 465 / SMTPS).
func sendTLS(addr, host, user, password, from string, to []string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: host}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("TLS dial %s: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("SMTP new client: %w", err)
	}
	defer c.Close()

	auth := smtp.PlainAuth("", user, password, host)
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("SMTP auth: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("SMTP RCPT TO %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	return w.Close()
}

// buildMessage constructs a multipart/alternative MIME message.
func buildMessage(from, to, subject, plainText, htmlBody string) ([]byte, error) {
	boundary, err := randomBoundary()
	if err != nil {
		return nil, err
	}
	msgID, err := randomMsgID()
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer

	// Headers
	hdr := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	hdr("From", from)
	hdr("To", to)
	hdr("Subject", mime.QEncoding.Encode("utf-8", subject))
	hdr("Date", time.Now().Format(time.RFC1123Z))
	hdr("Message-ID", "<"+msgID+"@neomd>")
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	hdr("X-Mailer", "neomd")
	b.WriteString("\r\n")

	// text/plain part (raw markdown — readable as-is in any client)
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	writeQP(&b, plainText)
	b.WriteString("\r\n")

	// text/html part (goldmark rendered)
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	writeQP(&b, htmlBody)
	b.WriteString("\r\n")

	// Closing boundary
	fmt.Fprintf(&b, "--%s--\r\n", boundary)

	return b.Bytes(), nil
}

// writeQP writes s as simplified quoted-printable (ASCII passthrough,
// encodes only non-ASCII and special chars). Good enough for UTF-8 prose.
func writeQP(b *bytes.Buffer, s string) {
	lineLen := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' {
			b.WriteString("\r\n")
			lineLen = 0
			continue
		}
		if c == '\r' {
			continue // CRLF handled above
		}
		if (c >= 33 && c <= 126 && c != '=') || c == '\t' || c == ' ' {
			if lineLen >= 75 {
				b.WriteString("=\r\n")
				lineLen = 0
			}
			b.WriteByte(c)
			lineLen++
		} else {
			enc := fmt.Sprintf("=%02X", c)
			if lineLen+3 > 75 {
				b.WriteString("=\r\n")
				lineLen = 0
			}
			b.WriteString(enc)
			lineLen += 3
		}
	}
}

func randomBoundary() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "neomd-" + hex.EncodeToString(b), nil
}

func randomMsgID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// extractAddr pulls the bare email address from "Name <addr>" or "addr".
func extractAddr(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '<'); i >= 0 {
		j := strings.IndexByte(s, '>')
		if j > i {
			h, _, _ := strings.Cut(s[i+1:j], "@")
			_ = h
			// validate it looks like an address
			addr := s[i+1 : j]
			if _, err := net.LookupHost(strings.SplitN(addr, "@", 2)[len(strings.SplitN(addr, "@", 2))-1]); err == nil || strings.Contains(addr, "@") {
				return addr
			}
		}
	}
	return s
}
