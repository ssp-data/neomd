// Package smtp handles outgoing email via SMTP.
// Sends multipart/alternative (text/plain + text/html) so recipients
// get clickable links and formatted output while you write pure Markdown.
package smtp

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sspaeti/neomd/internal/mailtls"
	"github.com/sspaeti/neomd/internal/render"
)

// imgSrcRe matches <img src="/absolute/path"> produced by goldmark for local paths.
var imgSrcRe = regexp.MustCompile(`<img\s[^>]*src="(/[^"]+)"`)

// Config holds outgoing mail settings.
type Config struct {
	Host        string // e.g. "smtp.example.com"
	Port        string // e.g. "587" (STARTTLS) or "465" (TLS)
	User        string
	Password    string
	From        string // "Name <email>"
	STARTTLS    bool   // User's explicit starttls config preference
	TLSCertFile string // optional PEM CA/cert for self-signed local bridges

	// TokenSource is used for OAuth2 accounts instead of Password.
	TokenSource func() (string, error)
}

func (c Config) auth(host string) (smtp.Auth, error) {
	if c.TokenSource != nil {
		token, err := c.TokenSource()
		if err != nil {
			return nil, fmt.Errorf("get OAuth2 token: %w", err)
		}
		return &xoauth2Auth{user: c.User, token: token}, nil
	}
	return smtp.PlainAuth("", c.User, c.Password, host), nil
}

type xoauth2Auth struct {
	user  string
	token string
}

func (a *xoauth2Auth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	ir := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.user, a.token)
	return "XOAUTH2", []byte(ir), nil
}

func (a *xoauth2Auth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return []byte{}, nil
	}
	return nil, nil
}

// Send composes and sends an email.
// markdownBody is sent as text/plain (raw) and text/html (goldmark-rendered).
// cc and bcc may be empty. BCC recipients receive the email but are not visible
// in the message headers (standard BCC privacy behaviour).
// attachments is a list of local file paths (may be nil).
func Send(cfg Config, to, cc, bcc, subject, markdownBody string, attachments []string) error {
	htmlBody, err := render.ToHTML(markdownBody)
	if err != nil {
		return fmt.Errorf("markdown to html: %w", err)
	}

	// BCC is intentionally NOT passed to buildMessage — it must not appear in headers.
	raw, err := buildMessage(cfg.From, to, cc, subject, markdownBody, htmlBody, attachments)
	if err != nil {
		return fmt.Errorf("build message: %w", err)
	}

	seen := make(map[string]bool)
	var toAddrs []string
	for _, field := range []string{to, cc, bcc} {
		for _, addr := range strings.Split(field, ",") {
			if a := extractAddr(strings.TrimSpace(addr)); a != "" && !seen[a] {
				seen[a] = true
				toAddrs = append(toAddrs, a)
			}
		}
	}
	fromAddr := extractAddr(cfg.From)

	auth, err := cfg.auth(cfg.Host)
	if err != nil {
		return err
	}
	addr := cfg.Host + ":" + cfg.Port
	tlsCfg, err := mailtls.Config(cfg.Host, cfg.TLSCertFile)
	if err != nil {
		return err
	}
	if inferSMTPUseTLS(cfg.Port, cfg.STARTTLS) {
		return sendTLS(addr, cfg, tlsCfg, auth, fromAddr, toAddrs, raw)
	}
	return sendSTARTTLS(addr, cfg, tlsCfg, auth, fromAddr, toAddrs, raw)
}

// SendRaw delivers a pre-built raw MIME message (e.g. from BuildMessage).
// toAddrs must include all RCPT TO recipients (To + CC + BCC combined).
// This lets the caller build the message once and reuse it (e.g. to save to Sent).
func SendRaw(cfg Config, toAddrs []string, raw []byte) error {
	fromAddr := extractAddr(cfg.From)
	auth, err := cfg.auth(cfg.Host)
	if err != nil {
		return err
	}
	addr := cfg.Host + ":" + cfg.Port
	tlsCfg, err := mailtls.Config(cfg.Host, cfg.TLSCertFile)
	if err != nil {
		return err
	}
	if inferSMTPUseTLS(cfg.Port, cfg.STARTTLS) {
		return sendTLS(addr, cfg, tlsCfg, auth, fromAddr, toAddrs, raw)
	}
	return sendSTARTTLS(addr, cfg, tlsCfg, auth, fromAddr, toAddrs, raw)
}

// inferSMTPUseTLS determines whether to use implicit TLS or STARTTLS based on
// port and user config. Returns true for TLS, false for STARTTLS.
//
// Logic:
//   - If userSTARTTLS is true: always use STARTTLS (user explicitly enabled it)
//   - Standard ports: 465 → TLS, 587 → STARTTLS
//   - Non-standard ports: default to TLS (e.g., Proton Mail Bridge on 1025 uses STARTTLS,
//     but user must set starttls=true for that)
func inferSMTPUseTLS(port string, userSTARTTLS bool) bool {
	if userSTARTTLS {
		// User explicitly set starttls=true in config — use STARTTLS.
		return false
	}
	switch port {
	case "465":
		return true // SMTPS (implicit TLS)
	case "587":
		return false // Submission with STARTTLS (modern standard)
	default:
		// Non-standard port: default to TLS for security.
		// User must explicitly set starttls=true if their provider uses STARTTLS.
		return true
	}
}

// sendSTARTTLS sends via STARTTLS upgrade (port 587).
func sendSTARTTLS(addr string, cfg Config, tlsCfg *tls.Config, auth smtp.Auth, from string, to []string, msg []byte) error {
	err := sendSTARTTLSWithConfig(addr, cfg.Host, tlsCfg, auth, from, to, msg)
	if err != nil && mailtls.ShouldRetryInsecureLocalhost(cfg.Host, cfg.TLSCertFile, err) {
		return sendSTARTTLSWithConfig(addr, cfg.Host, mailtls.InsecureLocalhostConfig(cfg.Host), auth, from, to, msg)
	}
	return err
}

// sendTLS sends via implicit TLS (port 465 / SMTPS).
func sendTLS(addr string, cfg Config, tlsCfg *tls.Config, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		if mailtls.ShouldRetryInsecureLocalhost(cfg.Host, cfg.TLSCertFile, err) {
			conn, err = tls.Dial("tcp", addr, mailtls.InsecureLocalhostConfig(cfg.Host))
		}
		if err != nil {
			return fmt.Errorf("TLS dial %s: %w", addr, err)
		}
	}

	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("SMTP new client: %w", err)
	}
	defer c.Close()

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

func sendSTARTTLSWithConfig(addr, host string, tlsCfg *tls.Config, auth smtp.Auth, from string, to []string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("SMTP dial %s: %w", addr, err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); !ok {
		return fmt.Errorf("SMTP server %s does not support STARTTLS", addr)
	}
	if err := c.StartTLS(tlsCfg); err != nil {
		return fmt.Errorf("SMTP STARTTLS %s: %w", addr, err)
	}
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("SMTP auth: %w", err)
			}
		}
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

// BuildMessage constructs a raw MIME message suitable for SMTP DATA or IMAP APPEND.
// BCC must not be passed — it must never appear in message headers.
// When attachments is non-empty the message is wrapped in multipart/mixed;
// otherwise the structure is unchanged (multipart/alternative only).
// htmlSignature, if non-empty, is injected before the closing </body> tag in the HTML part.
func BuildMessage(from, to, cc, subject, markdownBody string, attachments []string, htmlSignature string) ([]byte, error) {
	return BuildMessageWithThreading(from, to, cc, subject, markdownBody, attachments, htmlSignature, "", "")
}

// BuildMessageWithThreading builds a MIME message with optional threading headers (In-Reply-To, References).
// Used for replies and forwards to maintain proper email conversation threading.
func BuildMessageWithThreading(from, to, cc, subject, markdownBody string, attachments []string, htmlSignature, inReplyTo, references string) ([]byte, error) {
	htmlBody, err := render.ToHTML(markdownBody)
	if err != nil {
		return nil, fmt.Errorf("markdown to html: %w", err)
	}
	// Inject HTML signature before </body> tag if provided
	if htmlSignature != "" {
		// Replace the last occurrence of </body> with signature + </body>
		// This ensures the signature is inside the HTML document structure
		idx := strings.LastIndex(htmlBody, "</body>")
		if idx >= 0 {
			htmlBody = htmlBody[:idx] + "\n" + htmlSignature + "\n" + htmlBody[idx:]
		}
	}
	// Build References chain: append inReplyTo to existing references
	refChain := references
	if inReplyTo != "" {
		if refChain != "" {
			refChain = refChain + " " + inReplyTo
		} else {
			refChain = inReplyTo
		}
	}
	return buildMessageWithBCC(from, to, cc, "", subject, markdownBody, htmlBody, attachments, inReplyTo, refChain)
}

// BuildDraftMessage constructs a raw MIME draft for IMAP APPEND.
// Unlike SMTP delivery, drafts should retain the Bcc header so the user's
// intent survives round-tripping through Drafts.
// Drafts are stored as plain text only (no HTML conversion) to preserve the
// original markdown formatting exactly during save/load cycles.
func BuildDraftMessage(from, to, cc, bcc, subject, markdownBody string, attachments []string) ([]byte, error) {
	// Pass empty htmlBody to store plain text only; no threading headers for drafts
	return buildMessageWithBCC(from, to, cc, bcc, subject, markdownBody, "", attachments, "", "")
}

// BuildReactionMessage constructs a minimal reaction email with threading headers.
// Used for emoji reactions sent as replies to emails.
// markdownBody is used for both text/plain and text/html parts (same as BuildMessageWithThreading).
// inReplyTo is the Message-ID of the original email.
// references is the References chain from the original email (may be empty).
func BuildReactionMessage(from, to, cc, subject, markdownBody, inReplyTo, references string) ([]byte, error) {
	// Convert markdown to HTML (same as regular replies)
	htmlBody, err := render.ToHTML(markdownBody)
	if err != nil {
		return nil, fmt.Errorf("markdown to html: %w", err)
	}

	// Build References chain: append inReplyTo to existing references
	refChain := references
	if inReplyTo != "" {
		if refChain != "" {
			refChain = refChain + " " + inReplyTo
		} else {
			refChain = inReplyTo
		}
	}
	// No attachments for reactions
	// Use markdown for text/plain part, rendered HTML for text/html part (same as regular replies)
	return buildMessageWithBCC(from, to, cc, "", subject, markdownBody, htmlBody, nil, inReplyTo, refChain)
}

// inlineImage holds a local image path and its assigned Content-ID.
type inlineImage struct {
	path string
	cid  string // without angle brackets, e.g. "img0@neomd"
}

// buildMessage constructs a MIME message.
// Images referenced as <img src="/abs/path"> in htmlBody are embedded inline
// via multipart/related with Content-ID headers (standard inline image embedding).
// File attachments (non-image) are wrapped in multipart/mixed.
// MIME structure:
//   - no images, no files  → multipart/alternative
//   - files only           → multipart/mixed > multipart/alternative
//   - images only          → multipart/related > (multipart/alternative + inline images)
//   - images + files       → multipart/mixed > (multipart/related > alt+images) + files
func buildMessage(from, to, cc, subject, plainText, htmlBody string, attachments []string) ([]byte, error) {
	return buildMessageWithBCC(from, to, cc, "", subject, plainText, htmlBody, attachments, "", "")
}

func buildMessageWithBCC(from, to, cc, bcc, subject, plainText, htmlBody string, attachments []string, inReplyTo, references string) ([]byte, error) {
	// Find local image paths in htmlBody (<img src="/abs/path">), assign CIDs.
	var inlines []inlineImage
	processedHTML := imgSrcRe.ReplaceAllStringFunc(htmlBody, func(match string) string {
		m := imgSrcRe.FindStringSubmatch(match)
		if len(m) < 2 {
			return match
		}
		localPath := m[1]
		cid := fmt.Sprintf("img%d@neomd", len(inlines))
		inlines = append(inlines, inlineImage{path: localPath, cid: cid})
		return strings.Replace(match, `"`+localPath+`"`, `"cid:`+cid+`"`, 1)
	})

	altBoundary, err := randomBoundary()
	if err != nil {
		return nil, err
	}
	msgID, err := randomMsgID()
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	hdr := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }

	writeHeaders := func(contentType string) {
		hdr("From", from)
		hdr("To", to)
		if cc != "" {
			hdr("Cc", cc)
		}
		if bcc != "" {
			hdr("Bcc", bcc)
		}
		hdr("Subject", mime.QEncoding.Encode("utf-8", subject))
		hdr("Date", time.Now().Format(time.RFC1123Z))
		hdr("Message-ID", "<"+msgID+"@neomd>")
		// Threading headers for replies
		if inReplyTo != "" {
			hdr("In-Reply-To", inReplyTo)
		}
		if references != "" {
			hdr("References", references)
		}
		hdr("MIME-Version", "1.0")
		hdr("Content-Type", contentType)
		hdr("X-Mailer", "neomd")
		b.WriteString("\r\n")
	}

	hasFiles := len(attachments) > 0
	hasImages := len(inlines) > 0
	hasHTML := htmlBody != ""

	switch {
	case !hasHTML && !hasFiles:
		// Plain text only (drafts): simple text/plain message
		hdr("From", from)
		hdr("To", to)
		if cc != "" {
			hdr("Cc", cc)
		}
		if bcc != "" {
			hdr("Bcc", bcc)
		}
		hdr("Subject", mime.QEncoding.Encode("utf-8", subject))
		hdr("Date", time.Now().Format(time.RFC1123Z))
		hdr("Message-ID", "<"+msgID+"@neomd>")
		// Threading headers for replies
		if inReplyTo != "" {
			hdr("In-Reply-To", inReplyTo)
		}
		if references != "" {
			hdr("References", references)
		}
		hdr("MIME-Version", "1.0")
		hdr("Content-Type", "text/plain; charset=utf-8")
		hdr("Content-Transfer-Encoding", "quoted-printable")
		hdr("X-Mailer", "neomd")
		hdr("X-Neomd-Draft", "true")
		b.WriteString("\r\n")
		writeQP(&b, plainText)

	case !hasHTML && hasFiles:
		// Plain text + attachments (drafts with files): multipart/mixed
		mixedBoundary, err := randomBoundary()
		if err != nil {
			return nil, err
		}
		hdr("From", from)
		hdr("To", to)
		if cc != "" {
			hdr("Cc", cc)
		}
		if bcc != "" {
			hdr("Bcc", bcc)
		}
		hdr("Subject", mime.QEncoding.Encode("utf-8", subject))
		hdr("Date", time.Now().Format(time.RFC1123Z))
		hdr("Message-ID", "<"+msgID+"@neomd>")
		hdr("MIME-Version", "1.0")
		hdr("Content-Type", `multipart/mixed; boundary="`+mixedBoundary+`"`)
		hdr("X-Mailer", "neomd")
		hdr("X-Neomd-Draft", "true")
		b.WriteString("\r\n")
		fmt.Fprintf(&b, "--%s\r\n", mixedBoundary)
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		writeQP(&b, plainText)
		b.WriteString("\r\n")
		for _, path := range attachments {
			if err := writeAttachment(&b, mixedBoundary, path); err != nil {
				return nil, fmt.Errorf("attachment %s: %w", path, err)
			}
		}
		fmt.Fprintf(&b, "--%s--\r\n", mixedBoundary)

	case !hasImages && !hasFiles:
		// Simple: multipart/alternative only
		writeHeaders(`multipart/alternative; boundary="` + altBoundary + `"`)
		writeAltParts(&b, altBoundary, plainText, processedHTML)

	case hasFiles && !hasImages:
		// multipart/mixed > multipart/alternative + file attachments
		mixedBoundary, err := randomBoundary()
		if err != nil {
			return nil, err
		}
		writeHeaders(`multipart/mixed; boundary="` + mixedBoundary + `"`)
		fmt.Fprintf(&b, "--%s\r\n", mixedBoundary)
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", altBoundary)
		writeAltParts(&b, altBoundary, plainText, processedHTML)
		for _, path := range attachments {
			if err := writeAttachment(&b, mixedBoundary, path); err != nil {
				return nil, fmt.Errorf("attachment %s: %w", path, err)
			}
		}
		fmt.Fprintf(&b, "--%s--\r\n", mixedBoundary)

	case hasImages && !hasFiles:
		// multipart/related > multipart/alternative + inline images
		relBoundary, err := randomBoundary()
		if err != nil {
			return nil, err
		}
		writeHeaders(`multipart/related; boundary="` + relBoundary + `"`)
		fmt.Fprintf(&b, "--%s\r\n", relBoundary)
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", altBoundary)
		writeAltParts(&b, altBoundary, plainText, processedHTML)
		for _, img := range inlines {
			if err := writeInlineImage(&b, relBoundary, img); err != nil {
				return nil, fmt.Errorf("inline image %s: %w", img.path, err)
			}
		}
		fmt.Fprintf(&b, "--%s--\r\n", relBoundary)

	default:
		// multipart/mixed > multipart/related > (alt + images) + file attachments
		mixedBoundary, err := randomBoundary()
		if err != nil {
			return nil, err
		}
		relBoundary, err := randomBoundary()
		if err != nil {
			return nil, err
		}
		writeHeaders(`multipart/mixed; boundary="` + mixedBoundary + `"`)
		// related part
		fmt.Fprintf(&b, "--%s\r\n", mixedBoundary)
		fmt.Fprintf(&b, "Content-Type: multipart/related; boundary=\"%s\"\r\n\r\n", relBoundary)
		fmt.Fprintf(&b, "--%s\r\n", relBoundary)
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", altBoundary)
		writeAltParts(&b, altBoundary, plainText, processedHTML)
		for _, img := range inlines {
			if err := writeInlineImage(&b, relBoundary, img); err != nil {
				return nil, fmt.Errorf("inline image %s: %w", img.path, err)
			}
		}
		fmt.Fprintf(&b, "--%s--\r\n", relBoundary)
		// file attachments
		for _, path := range attachments {
			if err := writeAttachment(&b, mixedBoundary, path); err != nil {
				return nil, fmt.Errorf("attachment %s: %w", path, err)
			}
		}
		fmt.Fprintf(&b, "--%s--\r\n", mixedBoundary)
	}

	return b.Bytes(), nil
}

// writeInlineImage appends a base64-encoded image part with Content-ID for inline display.
func writeInlineImage(b *bytes.Buffer, boundary string, img inlineImage) error {
	data, err := os.ReadFile(img.path)
	if err != nil {
		return err
	}
	filename := filepath.Base(img.path)
	mimeType := mime.TypeByExtension(filepath.Ext(img.path))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	fmt.Fprintf(b, "--%s\r\n", boundary)
	fmt.Fprintf(b, "Content-Type: %s; name=\"%s\"\r\n", mimeType, filename)
	fmt.Fprintf(b, "Content-ID: <%s>\r\n", img.cid)
	b.WriteString("Content-Disposition: inline\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")

	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		b.WriteString(enc[:76])
		b.WriteString("\r\n")
		enc = enc[76:]
	}
	if len(enc) > 0 {
		b.WriteString(enc)
		b.WriteString("\r\n")
	}
	return nil
}

// writeAltParts writes the text/plain and text/html parts into b using boundary.
func writeAltParts(b *bytes.Buffer, boundary, plainText, htmlBody string) {
	fmt.Fprintf(b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	writeQP(b, plainText)
	b.WriteString("\r\n")

	fmt.Fprintf(b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	writeQP(b, htmlBody)
	b.WriteString("\r\n")

	fmt.Fprintf(b, "--%s--\r\n", boundary)
}

// writeAttachment appends a single file as a base64-encoded MIME part.
func writeAttachment(b *bytes.Buffer, boundary, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	filename := filepath.Base(path)
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	fmt.Fprintf(b, "--%s\r\n", boundary)
	fmt.Fprintf(b, "Content-Type: %s; name=\"%s\"\r\n", mimeType, filename)
	fmt.Fprintf(b, "Content-Disposition: attachment; filename=\"%s\"\r\n", filename)
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")

	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		b.WriteString(enc[:76])
		b.WriteString("\r\n")
		enc = enc[76:]
	}
	if len(enc) > 0 {
		b.WriteString(enc)
		b.WriteString("\r\n")
	}
	return nil
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
