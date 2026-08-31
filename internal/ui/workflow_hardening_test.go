package ui

// Workflow hardening: end-to-end tests of the COMPOSITION of the send
// pipeline, not individual functions. They drive the real bubbletea Update
// handlers (reply-all → editor file → editorDoneMsg → pre-send → enter) and
// catch the outgoing mail on a fake in-process SMTP server, then assert on
// what an external observer sees: the SMTP envelope (MAIL FROM / RCPT TO /
// auth user) and the delivered wire bytes parsed back with go-message.
//
// Because only observable output is asserted, any refactor of the internals
// is free — these tests fail only when a real recipient (or the SMTP server)
// would see something different. Run with: go test ./internal/ui -run Hardening
//
// The only simulated seam is the editor round trip: the tests write/edit the
// same neomd-*.md temp file the real flow uses and build editorDoneMsg via
// editor.ParseHeaders — byte-for-byte what the tea.ExecProcess callback in
// launchReplyWithCC / launchEditorCmd does after $EDITOR exits.

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/emersion/go-message/mail"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/editor"
	"github.com/sspaeti/neomd/internal/imap"
)

// ── Fake SMTP server ─────────────────────────────────────────────────────

// smtpRecord captures what one delivery looked like to the server.
type smtpRecord struct {
	mu       sync.Mutex
	authUser string
	mailFrom string
	rcpt     []string
	data     []byte
}

func (r *smtpRecord) snapshot() (authUser, mailFrom string, rcpt []string, data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authUser, r.mailFrom, append([]string(nil), r.rcpt...), append([]byte(nil), r.data...)
}

// startFakeSMTP starts a TLS SMTP server on 127.0.0.1 with a self-signed
// certificate. neomd's send path retries loopback hosts with insecure
// verification (mailtls.ShouldRetryInsecureLocalhost), so no production code
// changes are needed. Returns "host:port".
func startFakeSMTP(t *testing.T) (string, *smtpRecord) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fake-smtp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	rec := &smtpRecord{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSMTPConn(conn, rec)
		}
	}()
	return ln.Addr().String(), rec
}

func handleSMTPConn(conn net.Conn, rec *smtpRecord) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second)) //nolint:errcheck
	br := bufio.NewReader(conn)
	write := func(s string) bool {
		_, err := io.WriteString(conn, s+"\r\n")
		return err == nil
	}
	// The first client attempt fails the TLS handshake (self-signed cert);
	// the write error below ends that session and the client retries insecure.
	if !write("220 fake ESMTP") {
		return
	}
	var authUser, mailFrom string
	var rcpt []string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			if _, err := io.WriteString(conn, "250-fake\r\n250-AUTH PLAIN LOGIN\r\n250 8BITMIME\r\n"); err != nil {
				return
			}
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):])); err == nil {
				if parts := strings.Split(string(dec), "\x00"); len(parts) == 3 {
					authUser = parts[1]
				}
			}
			write("235 ok")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			mailFrom = angleAddr(line[len("MAIL FROM:"):])
			write("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			rcpt = append(rcpt, angleAddr(line[len("RCPT TO:"):]))
			write("250 ok")
		case upper == "DATA":
			write("354 go")
			var b bytes.Buffer
			for {
				dl, err := br.ReadString('\n')
				if err != nil {
					return
				}
				trimmed := strings.TrimRight(dl, "\r\n")
				if trimmed == "." {
					break
				}
				if strings.HasPrefix(trimmed, "..") {
					trimmed = trimmed[1:] // SMTP dot-unstuffing
				}
				b.WriteString(trimmed + "\r\n")
			}
			rec.mu.Lock()
			rec.authUser, rec.mailFrom, rec.rcpt, rec.data = authUser, mailFrom, rcpt, b.Bytes()
			rec.mu.Unlock()
			write("250 accepted")
		case upper == "QUIT":
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

// angleAddr extracts the address from an SMTP envelope argument like
// "<a@b.io> BODY=8BITMIME" (ESMTP parameters follow the angle-bracket path).
func angleAddr(s string) string {
	if i := strings.IndexByte(s, '<'); i >= 0 {
		if j := strings.IndexByte(s, '>'); j > i {
			return s[i+1 : j]
		}
	}
	return strings.TrimSpace(s)
}

// ── Wire parse-back (what the recipient's client sees) ───────────────────

type wireMsg struct {
	rawHead  string
	subject  string
	from     string
	to, cc   []string
	inReply  string
	refs     string
	plain    string
	html     string
	attached map[string][]byte
}

func parseWire(t *testing.T, raw []byte) wireMsg {
	t.Helper()
	var w wireMsg
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		w.rawHead = string(raw[:i])
	} else {
		t.Fatal("delivered message has no header/body separator")
	}
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("recipient client cannot parse delivered message: %v", err)
	}
	w.subject, _ = r.Header.Subject()
	addrList := func(key string) []string {
		var out []string
		if addrs, err := r.Header.AddressList(key); err == nil {
			for _, a := range addrs {
				if a.Name != "" {
					out = append(out, a.Name+" <"+a.Address+">")
				} else {
					out = append(out, a.Address)
				}
			}
		}
		return out
	}
	if f := addrList("From"); len(f) > 0 {
		w.from = f[0]
	}
	w.to = addrList("To")
	w.cc = addrList("Cc")
	w.inReply = r.Header.Get("In-Reply-To")
	w.refs = r.Header.Get("References")
	w.attached = map[string][]byte{}
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			body, _ := io.ReadAll(p.Body)
			switch ct {
			case "text/plain":
				w.plain = string(body)
			case "text/html":
				w.html = string(body)
			}
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			body, _ := io.ReadAll(p.Body)
			w.attached[name] = body
		}
	}
	return w
}

func wireHasHeader(rawHead, prefix string) bool {
	for _, line := range strings.Split(rawHead, "\r\n") {
		if len(line) >= len(prefix) && strings.EqualFold(line[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// ── Test plumbing ────────────────────────────────────────────────────────

// runCmds executes a tea.Cmd tree (unwrapping tea.Batch) and returns every
// resulting message — this is what the bubbletea runtime would do.
func runCmds(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmds(t, c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// findSendDone extracts the sendDoneMsg from executed commands and fails on a
// send error.
func findSendDone(t *testing.T, msgs []tea.Msg) sendDoneMsg {
	t.Helper()
	for _, m := range msgs {
		if sd, ok := m.(sendDoneMsg); ok {
			if sd.err != nil {
				t.Fatalf("send failed: %v", sd.err)
			}
			return sd
		}
	}
	t.Fatal("no sendDoneMsg produced — send path never ran")
	return sendDoneMsg{}
}

// editorDone builds the editorDoneMsg the real tea.ExecProcess callback
// produces from the edited file content (see launchReplyWithCC /
// launchEditorCmd: editor.ParseHeaders + full raw content as body).
func editorDone(content string) editorDoneMsg {
	to, cc, bcc, from, subject, _ := editor.ParseHeaders(content)
	return editorDoneMsg{to: to, cc: cc, bcc: bcc, from: from, subject: subject, body: content}
}

// newestComposeFile returns the neomd-*.md file in the compose temp dir that
// is not in the before set — the file launchReplyWithCC just wrote.
func newestComposeFile(t *testing.T, before map[string]bool) string {
	t.Helper()
	entries, err := os.ReadDir(neomdTempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !before[e.Name()] && strings.HasPrefix(e.Name(), "neomd-") && strings.HasSuffix(e.Name(), ".md") {
			return filepath.Join(neomdTempDir(), e.Name())
		}
	}
	t.Fatal("compose temp file not found")
	return ""
}

func listComposeFiles(t *testing.T) map[string]bool {
	t.Helper()
	before := map[string]bool{}
	if entries, err := os.ReadDir(neomdTempDir()); err == nil {
		for _, e := range entries {
			before[e.Name()] = true
		}
	}
	return before
}

func pressEnter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

// ── The workflow tests ───────────────────────────────────────────────────

// Reply-all, multi-account: the full real chain — launchReplyWithCC builds
// the compose file, the "user" edits it, editorDoneMsg and the pre-send
// enter key run through the real Update handlers, and the mail is delivered
// to a fake SMTP server. Asserts the invariants that individual unit tests
// can't protect against wiring mistakes:
//   - the reply goes out through the ACCOUNT whose address received the email
//     (auth user, MAIL FROM, and From header all follow the Work identity)
//   - reply-all CC contains external recipients only — every own address
//     (IMAP logins, account Froms, sender aliases) is excluded
//   - auto_bcc reaches RCPT TO but never the headers
//   - threading headers point at the original message
//   - the · reply indicator gets the data it needs (sendDoneMsg carries the
//     original UID/folder)
func TestHardening_Workflow_ReplyAllUsesReceivingAccount(t *testing.T) {
	personalAddr, personalRec := startFakeSMTP(t)
	workAddr, workRec := startFakeSMTP(t)

	cfg := &config.Config{
		Accounts: []config.AccountConfig{
			{Name: "Personal", User: "personal-login@provider.example", Password: "pw1",
				From: "Simon Späti <simu@sspaeti.com>", SMTP: personalAddr,
				Signature: config.SignatureConfig{HTML: `<div class="personal-signature">Personal signature</div>`}},
			{Name: "Work", User: "work-login@provider.example", Password: "pw2",
				From: "Work Persona <work@company.example>", SMTP: workAddr,
				Signature: config.SignatureConfig{HTML: `<div class="work-signature">Work signature</div>`}},
		},
		Senders: []config.SenderConfig{
			{Name: "Support", From: "Support <support@sspaeti.com>", Account: "Work"},
		},
		AutoBCC: "archive@sspaeti.com",
		Folders: config.FoldersConfig{Sent: "Sent"},
	}
	m := Model{
		cfg:      cfg,
		accounts: cfg.ActiveAccounts(),
		accountI: 0,
		openEmail: &imap.Email{
			UID:        42,
			Folder:     "INBOX",
			From:       "Louise Nachname <louise@client.example>",
			To:         "Work Persona <work@company.example>, peter@client.example",
			CC:         "cc-extern@client.example, Support <support@sspaeti.com>, personal-login@provider.example",
			Subject:    "Ängebot Züri",
			MessageID:  "<orig-123@client.example>",
			References: "<root-1@client.example>",
		},
	}
	m.openBody = "Grüezi Simon,\n\nkönnen wir das Angebot besprechen?"

	// Step 1: user presses reply-all — real recipient/From computation, real
	// compose temp file with the real prelude.
	before := listComposeFiles(t)
	mm, _ := m.launchReplyWithCC("", true)
	m = mm.(Model)
	composePath := newestComposeFile(t, before)
	defer os.Remove(composePath)

	// Step 2: the "user" edits the file in their editor.
	content, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	replyText := "Grüezi Louise,\n\nGerne — der Termin **passt**.\n\n--  \n[html-signature]\n\n"
	headerEnd := strings.Index(string(content), "\n\n")
	if headerEnd < 0 {
		t.Fatal("reply compose file has no header/body separator")
	}
	edited := string(content[:headerEnd+2]) + replyText + string(content[headerEnd+2:])
	if err := os.WriteFile(composePath, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	// Step 3: editor exits → editorDoneMsg through the real handler.
	mm2, _ := m.Update(editorDone(edited))
	m = mm2.(Model)
	if m.state != statePresend {
		t.Fatalf("after editor: state = %v, want presend", m.state)
	}
	if m.pendingSend == nil {
		t.Fatal("no pendingSend after editor")
	}

	// Step 4: user confirms on pre-send → real SMTP delivery to fake server.
	mm3, cmd := m.Update(pressEnter())
	m = mm3.(Model)
	sd := findSendDone(t, runCmds(t, cmd))
	if sd.replyToUID != 42 || sd.replyToFolder != "INBOX" {
		t.Errorf("· indicator data lost: sendDoneMsg uid=%d folder=%q", sd.replyToUID, sd.replyToFolder)
	}

	// ── Assertions: what the outside world saw ──
	authUser, mailFrom, rcpt, data := workRec.snapshot()
	if _, pFrom, _, _ := personalRec.snapshot(); pFrom != "" {
		t.Fatalf("reply went through the PERSONAL account's SMTP server (MAIL FROM %q) — wrong identity", pFrom)
	}
	if authUser != "work-login@provider.example" {
		t.Errorf("SMTP auth user = %q, want Work login", authUser)
	}
	if mailFrom != "work@company.example" {
		t.Errorf("MAIL FROM = %q, want work@company.example", mailFrom)
	}

	wantRcpt := map[string]bool{
		"louise@client.example":    true, // To (reply target)
		"peter@client.example":     true, // reply-all CC
		"cc-extern@client.example": true, // reply-all CC
		"archive@sspaeti.com":      true, // auto_bcc
	}
	if len(rcpt) != len(wantRcpt) {
		t.Errorf("RCPT TO = %v, want exactly %d recipients", rcpt, len(wantRcpt))
	}
	for _, r := range rcpt {
		if !wantRcpt[r] {
			t.Errorf("unexpected RCPT recipient %q (own address leaked into reply-all?)", r)
		}
	}

	w := parseWire(t, data)
	if w.from != "Work Persona <work@company.example>" {
		t.Errorf("From header = %q, must follow the receiving account", w.from)
	}
	if len(w.to) != 1 || w.to[0] != "Louise Nachname <louise@client.example>" {
		t.Errorf("To = %v, want the original sender", w.to)
	}
	ccJoined := strings.Join(w.cc, ", ")
	for _, own := range []string{"work@company.example", "support@sspaeti.com", "personal-login@provider.example", "simu@sspaeti.com"} {
		if strings.Contains(ccJoined, own) {
			t.Errorf("own address %q leaked into reply-all Cc: %v", own, w.cc)
		}
	}
	for _, ext := range []string{"peter@client.example", "cc-extern@client.example"} {
		if !strings.Contains(ccJoined, ext) {
			t.Errorf("external recipient %q missing from reply-all Cc: %v", ext, w.cc)
		}
	}
	if wireHasHeader(w.rawHead, "Bcc") {
		t.Error("Bcc header leaked on the wire")
	}
	if w.subject != "Re: Ängebot Züri" {
		t.Errorf("subject = %q, want %q", w.subject, "Re: Ängebot Züri")
	}
	if w.inReply != "<orig-123@client.example>" {
		t.Errorf("In-Reply-To = %q", w.inReply)
	}
	if !strings.Contains(w.refs, "<root-1@client.example>") || !strings.Contains(w.refs, "<orig-123@client.example>") {
		t.Errorf("References = %q, want root + original", w.refs)
	}
	if !strings.Contains(w.plain, "Gerne — der Termin **passt**.") {
		t.Error("plain part lost the user's reply text (markdown must stay literal)")
	}
	if !strings.Contains(w.html, "<strong>passt</strong>") {
		t.Error("HTML part lost markdown rendering of the reply text")
	}
	replyAt := strings.Index(w.html, "<strong>passt</strong>")
	separatorAt := strings.Index(w.html, "<p>--</p>")
	workSignatureAt := strings.Index(w.html, "work-signature")
	quoteAt := strings.Index(w.html, "<blockquote>")
	if strings.Count(w.html, "work-signature") != 1 || replyAt < 0 || separatorAt < 0 || workSignatureAt < 0 || quoteAt < 0 || !(replyAt < separatorAt && separatorAt < workSignatureAt && workSignatureAt < quoteAt) {
		t.Errorf("Work HTML signature ordering must be reply < separator < signature < quote: reply=%d separator=%d signature=%d quote=%d\n%s", replyAt, separatorAt, workSignatureAt, quoteAt, w.html)
	}
	if strings.Contains(w.html, "personal-signature") {
		t.Error("Personal account HTML signature was used instead of the receiving Work account")
	}
	if strings.Contains(w.plain, "[html-signature]") || strings.Contains(w.html, "[html-signature]") {
		t.Error("HTML signature marker leaked to the recipient")
	}
	if !strings.Contains(w.plain, "> Grüezi Simon,") {
		t.Error("quoted original message missing from the reply")
	}
}

// The Markdown-file-to-wire test: everything neomd is about — a plain
// neomd-*.md file with # [neomd: ...] headers, markdown body, [attach] line,
// signatures — must arrive at the recipient with every field intact:
// To/Cc/Bcc routing, umlaut subject, literal-markdown plain part, rendered
// HTML part (bold/link/callout), text signature in both parts, HTML signature
// in the HTML part only, attachment bytes identical, [html-signature] marker
// never delivered.
func TestHardening_Workflow_MarkdownFileToWire(t *testing.T) {
	addr, rec := startFakeSMTP(t)

	pdfContent := make([]byte, 2048)
	for i := range pdfContent {
		pdfContent[i] = byte(i % 256)
	}
	pdfPath := filepath.Join(t.TempDir(), "Angebot Q3 - Ssp.pdf")
	if err := os.WriteFile(pdfPath, pdfContent, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Accounts: []config.AccountConfig{
			{Name: "Personal", User: "login@provider.example", Password: "pw",
				From: "Simon Späti <simu@sspaeti.com>", SMTP: addr,
				Signature: config.SignatureConfig{
					Text: "Freundliche Grüsse\nSimon Späti",
					HTML: `<p class="sig">Simon Späti — <a href="https://ssp.sh">ssp.sh</a></p>`,
				}},
		},
		Folders: config.FoldersConfig{Sent: "Sent"},
	}
	m := Model{cfg: cfg, accounts: cfg.ActiveAccounts(), accountI: 0}

	// The compose file exactly as it exists in nvim: prelude headers built by
	// the real editor package, user-written markdown, [attach] marker,
	// [html-signature] marker, text signature block.
	to := "Louise Nachname <louise@client.example>, peter@client.example"
	cc := "cc-extern@client.example"
	bcc := "hidden@archive.example"
	subject := "Ängebot für Züri — Q3 🚀"
	content := editor.Prelude(to, cc, bcc, "Simon Späti <simu@sspaeti.com>", subject, "")
	content += "Grüezi Louise,\n\n" +
		"anbei das **Angebot** mit allen Details: [ssp.sh](https://ssp.sh)\n\n" +
		"> [!note]\n> Gilt bis Ende Monat.\n\n" +
		"[attach] " + pdfPath + "\n\n" +
		"[html-signature]\n\n" +
		"--  \nFreundliche Grüsse\nSimon Späti\n"

	// Write and re-read through a real neomd-*.md file — same as the editor flow.
	f, err := os.CreateTemp(neomdTempDir(), "neomd-*.md")
	if err != nil {
		t.Fatal(err)
	}
	composePath := f.Name()
	defer os.Remove(composePath)
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	raw, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}

	// Editor exits → real handler → pre-send.
	mm, _ := m.Update(editorDone(string(raw)))
	m = mm.(Model)
	if m.state != statePresend {
		t.Fatalf("state = %v, want presend", m.state)
	}
	if len(m.attachments) != 1 || m.attachments[0] != pdfPath {
		t.Fatalf("[attach] line not extracted: %v", m.attachments)
	}

	// Pre-send enter → delivery.
	mm2, cmd := m.Update(pressEnter())
	m = mm2.(Model)
	findSendDone(t, runCmds(t, cmd))

	_, mailFrom, rcpt, data := rec.snapshot()
	if mailFrom != "simu@sspaeti.com" {
		t.Errorf("MAIL FROM = %q", mailFrom)
	}
	wantRcpt := []string{"louise@client.example", "peter@client.example", "cc-extern@client.example", "hidden@archive.example"}
	if len(rcpt) != len(wantRcpt) {
		t.Fatalf("RCPT TO = %v, want %v", rcpt, wantRcpt)
	}
	for i, want := range wantRcpt {
		if rcpt[i] != want {
			t.Errorf("RCPT[%d] = %q, want %q", i, rcpt[i], want)
		}
	}

	w := parseWire(t, data)
	if w.subject != subject {
		t.Errorf("subject = %q, want %q", w.subject, subject)
	}
	if w.from != "Simon Späti <simu@sspaeti.com>" {
		t.Errorf("From = %q", w.from)
	}
	if len(w.to) != 2 || w.to[0] != "Louise Nachname <louise@client.example>" || w.to[1] != "peter@client.example" {
		t.Errorf("To = %v", w.to)
	}
	if len(w.cc) != 1 || w.cc[0] != "cc-extern@client.example" {
		t.Errorf("Cc = %v", w.cc)
	}
	if wireHasHeader(w.rawHead, "Bcc") {
		t.Error("Bcc leaked into delivered headers")
	}

	// Plain part: literal markdown + text signature, never HTML.
	for _, want := range []string{
		"Grüezi Louise,",
		"**Angebot**",
		"[ssp.sh](https://ssp.sh)",
		"Gilt bis Ende Monat.",
		"Freundliche Grüsse",
	} {
		if !strings.Contains(w.plain, want) {
			t.Errorf("plain part lost %q:\n%s", want, w.plain)
		}
	}
	if strings.Contains(w.plain, "class=\"sig\"") {
		t.Error("HTML signature leaked into the plain part")
	}

	// HTML part: rendered markdown + HTML signature.
	for _, want := range []string{
		"<strong>Angebot</strong>",
		`href="https://ssp.sh"`,
		"Gilt bis Ende Monat.",
		`class="sig"`,
	} {
		if !strings.Contains(w.html, want) {
			t.Errorf("HTML part lost %q", want)
		}
	}

	// Internal markers must never reach the recipient.
	for _, forbidden := range []string{"[html-signature]", "[attach]", "[neomd:"} {
		if strings.Contains(w.plain, forbidden) || strings.Contains(w.html, forbidden) {
			t.Errorf("internal marker %q delivered to recipient", forbidden)
		}
	}

	// Attachment: original name, identical bytes.
	got, ok := w.attached["Angebot Q3 - Ssp.pdf"]
	if !ok {
		t.Fatalf("attachment missing, got %v", attachedNames(w.attached))
	}
	if !bytes.Equal(got, pdfContent) {
		t.Error("attachment bytes corrupted on the wire")
	}
	if !strings.Contains(w.rawHead, "multipart/mixed") {
		t.Error("top-level Content-Type must be multipart/mixed with an attachment")
	}
}

func attachedNames(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
