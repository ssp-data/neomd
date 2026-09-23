package imap

// Hardening suite: full compose→wire→read-back verification with no network.
//
// Every message neomd sends is built by internal/smtp and later read by two
// consumers: the recipient's mail client (modeled here by go-message, the
// same RFC 5322/2045 parser neomd uses) and neomd itself (parseBody — inbox,
// Sent, draft-continue). These tests build real messages and assert
// BYTE-EXACT fidelity of every business-critical field: From, To, Cc,
// Subject, body text, attachment names and contents, threading headers, and
// the absence of Bcc / internal X-Neomd-* headers.
//
// Run with: go test ./internal/imap -run Hardening
// If a severe refactor breaks anything a client would receive, it fails here.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-message/mail"
	"github.com/sspaeti/neomd/internal/schedule"
	"github.com/sspaeti/neomd/internal/smtp"
)

// parsedMessage is what a standards-compliant recipient client sees.
type parsedMessage struct {
	rawHead  string // raw header block (for absence checks: Bcc, X-Neomd-*)
	subject  string // RFC 2047 decoded
	from     string // first From as "Name <addr>" (addr only when no name)
	to       []string
	cc       []string
	inReply  string
	refs     string
	msgID    string
	parts    []string          // inline content types, in order
	plain    string            // decoded text/plain body
	html     string            // decoded text/html body
	attached map[string][]byte // decoded attachment name → bytes
	inline   map[string][]byte // decoded inline image cid (no brackets) → bytes
}

func fmtAddr(a *mail.Address) string {
	if a.Name != "" {
		return a.Name + " <" + a.Address + ">"
	}
	return a.Address
}

func parseBuilt(t *testing.T, raw []byte) parsedMessage {
	t.Helper()
	var pm parsedMessage
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		pm.rawHead = string(raw[:i])
	} else {
		t.Fatal("message has no header/body separator")
	}
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("go-message cannot parse built message: %v", err)
	}
	pm.subject, _ = r.Header.Subject()
	if froms, err := r.Header.AddressList("From"); err == nil && len(froms) > 0 {
		pm.from = fmtAddr(froms[0])
	}
	if tos, err := r.Header.AddressList("To"); err == nil {
		for _, a := range tos {
			pm.to = append(pm.to, fmtAddr(a))
		}
	}
	if ccs, err := r.Header.AddressList("Cc"); err == nil {
		for _, a := range ccs {
			pm.cc = append(pm.cc, fmtAddr(a))
		}
	}
	pm.inReply = r.Header.Get("In-Reply-To")
	pm.refs = r.Header.Get("References")
	pm.msgID = r.Header.Get("Message-ID")
	pm.attached = map[string][]byte{}
	pm.inline = map[string][]byte{}
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
			pm.parts = append(pm.parts, ct)
			body, _ := io.ReadAll(p.Body)
			switch ct {
			case "text/plain":
				pm.plain = string(body)
			case "text/html":
				pm.html = string(body)
			default:
				if strings.HasPrefix(ct, "image/") {
					cid := strings.Trim(h.Get("Content-Id"), "<>")
					pm.inline[cid] = body
				}
			}
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			body, _ := io.ReadAll(p.Body)
			pm.attached[name] = body
		}
	}
	return pm
}

// hasHeaderLine reports whether any raw header line starts with prefix
// (case-insensitive) — used to prove headers are ABSENT on the wire.
func hasHeaderLine(rawHead, prefix string) bool {
	for _, line := range strings.Split(rawHead, "\r\n") {
		if len(line) >= len(prefix) && strings.EqualFold(line[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// binaryFixture writes a file containing every byte value (0-255 repeated) —
// any encoding corruption in the attachment pipeline changes at least one byte.
func binaryFixture(t *testing.T, name string) (path string, content []byte) {
	t.Helper()
	content = make([]byte, 4096)
	for i := range content {
		content[i] = byte(i % 256)
	}
	path = filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, content
}

// ── The golden round trip ────────────────────────────────────────────────

func TestHardening_RoundTrip_HeadersAndBody(t *testing.T) {
	from := "Simon Späti <simu@sspaeti.com>"
	to := "Louise Nachname <lnachname@domain.io>, second@client.example"
	cc := "cc-person@client.example"
	subject := "Ängebot für Züri — Q3 (rev. 2) 🚀"
	body := "Sehr geehrte Frau Nachname,\n\nanbei das **Angebot** für Zürich.\n\nFreundliche Grüsse\nSimon"

	raw, err := smtp.BuildMessage(from, to, cc, subject, body, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	pm := parseBuilt(t, raw)

	if pm.from != from {
		t.Errorf("From = %q, want %q", pm.from, from)
	}
	wantTo := []string{"Louise Nachname <lnachname@domain.io>", "second@client.example"}
	if len(pm.to) != 2 || pm.to[0] != wantTo[0] || pm.to[1] != wantTo[1] {
		t.Errorf("To = %v, want %v", pm.to, wantTo)
	}
	if len(pm.cc) != 1 || pm.cc[0] != cc {
		t.Errorf("Cc = %v, want [%s]", pm.cc, cc)
	}
	if pm.subject != subject {
		t.Errorf("Subject decoded = %q, want %q", pm.subject, subject)
	}
	// Body text must arrive byte-exact (umlauts survive quoted-printable).
	for _, line := range strings.Split(body, "\n") {
		if line != "" && !strings.Contains(pm.plain, line) {
			t.Errorf("plain part lost line %q", line)
		}
	}
	if !strings.Contains(pm.html, "Zürich") || !strings.Contains(pm.html, "<strong>Angebot</strong>") {
		t.Error("HTML part lost umlauts or markdown rendering")
	}
	// Structural invariants.
	if len(pm.parts) < 2 || pm.parts[0] != "text/plain" || pm.parts[1] != "text/html" {
		t.Errorf("part order = %v, want [text/plain text/html]", pm.parts)
	}
	if !strings.Contains(pm.msgID, "@sspaeti.com>") {
		t.Errorf("Message-ID %q must use sender domain", pm.msgID)
	}
	for _, forbidden := range []string{"Bcc:", "X-Neomd"} {
		if hasHeaderLine(pm.rawHead, forbidden) {
			t.Errorf("outgoing message must not carry %q header", forbidden)
		}
	}
	if pm.inReply != "" || pm.refs != "" {
		t.Error("non-reply must not carry threading headers")
	}
}

func TestHardening_RoundTrip_AttachmentFidelity(t *testing.T) {
	names := []string{
		"All issues - Ssp.pdf", // spaces + dashes (the draft-rename bug)
		"Rechnung März.pdf",    // non-ASCII
	}
	var paths []string
	want := map[string][]byte{}
	for _, n := range names {
		p, content := binaryFixture(t, n)
		paths = append(paths, p)
		want[n] = content
	}

	raw, err := smtp.BuildMessage("Simon <simu@sspaeti.com>", "louise@client.example", "",
		"attachment fidelity", "see attached", paths, "")
	if err != nil {
		t.Fatal(err)
	}

	// As the recipient's client sees it.
	pm := parseBuilt(t, raw)
	for _, n := range names {
		got, ok := pm.attached[n]
		if !ok {
			t.Errorf("recipient view: attachment %q missing (got %v)", n, keysOf(pm.attached))
			continue
		}
		if !bytes.Equal(got, want[n]) {
			t.Errorf("recipient view: attachment %q content corrupted (%d vs %d bytes)", n, len(got), len(want[n]))
		}
	}

	// As neomd itself re-reads it (inbox view, Sent copy, forward source).
	_, _, _, atts, _, _ := parseBody(raw)
	if len(atts) != len(names) {
		t.Fatalf("parseBody found %d attachments, want %d", len(atts), len(names))
	}
	for _, a := range atts {
		if !bytes.Equal(a.Data, want[a.Filename]) {
			t.Errorf("parseBody: attachment %q content mismatch", a.Filename)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestHardening_RoundTrip_ThreadingHeaders(t *testing.T) {
	// Contract: callers pass the ORIGINAL message's References chain; the
	// builder appends In-Reply-To itself (and never duplicates it, even for
	// broken senders whose References already contain their own Message-ID).
	for _, origRefs := range []string{
		"<root@client.example>",
		"<root@client.example> <orig-id@client.example>", // broken sender
	} {
		raw, err := smtp.BuildMessageWithThreading("Simon <s@ssp.sh>", "a@b.io", "",
			"Re: deal", "reply body", nil, "", "<orig-id@client.example>", origRefs)
		if err != nil {
			t.Fatal(err)
		}
		pm := parseBuilt(t, raw)
		if pm.inReply != "<orig-id@client.example>" {
			t.Errorf("In-Reply-To = %q", pm.inReply)
		}
		if pm.refs != "<root@client.example> <orig-id@client.example>" {
			t.Errorf("refs(%q): References = %q, want single orig-id at end", origRefs, pm.refs)
		}
	}
}

// Drafts must keep Bcc and survive re-reading with body and attachments intact
// (a corrupted draft round-trip means the re-sent email differs from what the
// user reviewed).
func TestHardening_RoundTrip_DraftWithAttachment(t *testing.T) {
	body := "Draft body line 1\n\n**bold** stays literal\n"
	path, content := binaryFixture(t, "All issues - Ssp.pdf")

	raw, err := smtp.BuildDraftMessage("Simon <s@ssp.sh>", "louise@client.example",
		"cc@x.io", "hidden@x.io", "Draft Ängebot", body, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if !hasHeaderLine(string(raw[:bytes.Index(raw, []byte("\r\n\r\n"))]), "Bcc:") {
		t.Error("draft must keep Bcc (it is re-opened, not delivered)")
	}
	md, _, _, atts, _, _ := parseBody(raw)
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if line != "" && !strings.Contains(md, line) {
			t.Errorf("draft body lost line %q on re-open, got:\n%s", line, md)
		}
	}
	if len(atts) != 1 || atts[0].Filename != "All issues - Ssp.pdf" {
		t.Fatalf("draft attachment name mangled: %+v", attNames(atts))
	}
	if !bytes.Equal(atts[0].Data, content) {
		t.Error("draft attachment content corrupted")
	}
}

func attNames(atts []Attachment) []string {
	out := make([]string, len(atts))
	for i, a := range atts {
		out[i] = a.Filename
	}
	return out
}

// The send-later queue must deliver EXACTLY the bytes an immediate send would
// have produced — and never leak the X-Neomd-Rcpt header (it contains Bcc).
func TestHardening_RoundTrip_SendLaterDeliversIdenticalBytes(t *testing.T) {
	raw, err := smtp.BuildMessage("Simon <s@ssp.sh>", "louise@client.example", "",
		"scheduled offer", "body", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	queued := schedule.Inject(raw, time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC),
		[]string{"louise@client.example", "hidden-bcc@x.io"})
	job, cleaned, found, err := schedule.Extract(queued)
	if err != nil || !found {
		t.Fatalf("Extract: found=%v err=%v", found, err)
	}
	if !bytes.Equal(cleaned, raw) {
		t.Error("delivered bytes differ from immediate-send bytes")
	}
	if len(job.Rcpt) != 2 {
		t.Errorf("RCPT list = %v", job.Rcpt)
	}
	pm := parseBuilt(t, cleaned)
	if hasHeaderLine(pm.rawHead, "X-Neomd") {
		t.Error("delivered message leaks X-Neomd header (contains Bcc!)")
	}

	// At delivery the daemon stamps the ACTUAL send time into Date — and must
	// change nothing else (a build-time Date makes the recipient see the
	// moment the user queued the message, not when it was sent).
	deliveredAt := time.Date(2026, 8, 25, 9, 0, 3, 0, time.UTC)
	stamped := schedule.RewriteDate(cleaned, deliveredAt)
	pm2 := parseBuilt(t, stamped)
	if got, err := time.Parse(time.RFC1123Z, headerValue(pm2.rawHead, "Date")); err != nil || !got.Equal(deliveredAt) {
		t.Errorf("delivered Date = %q, want %v (err=%v)", headerValue(pm2.rawHead, "Date"), deliveredAt, err)
	}
	if !bytes.Equal(stripDateLine(stamped), stripDateLine(cleaned)) {
		t.Error("RewriteDate changed bytes other than the Date header")
	}
}

// stripDateLine removes the Date header line so before/after delivery-stamp
// messages can be compared byte-exact on everything else.
func stripDateLine(raw []byte) []byte {
	lines := bytes.Split(raw, []byte("\r\n"))
	out := make([][]byte, 0, len(lines))
	for _, l := range lines {
		if len(l) >= 5 && strings.EqualFold(string(l[:5]), "Date:") {
			continue
		}
		out = append(out, l)
	}
	return bytes.Join(out, []byte("\r\n"))
}

// A long non-ASCII subject is split across multiple RFC 2047 encoded-words —
// a splitting bug (mid-rune cut, lost space, wrong charset) garbles what every
// recipient sees first. Must decode back byte-exact.
func TestHardening_RoundTrip_SubjectExtremes(t *testing.T) {
	subjects := []string{
		strings.Repeat("Ängebot für Zürich — Überarbeitung ", 6) + "🚀 Ende", // ~220 bytes, forces multiple encoded-words
		"Re: [Invoice #4711] 100% done = paid? (Q3/2026)",                   // ASCII specials stay readable
		"emoji only 🚀🎉✅",
	}
	for _, subject := range subjects {
		raw, err := smtp.BuildMessage("Simon <s@ssp.sh>", "a@b.io", "", subject, "body", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if pm := parseBuilt(t, raw); pm.subject != subject {
			t.Errorf("subject round-trip failed:\ngot  %q\nwant %q", pm.subject, subject)
		}
	}
}

// Large recipient lists must survive with every address intact and in order —
// a lost or reordered recipient silently changes who receives a business email.
func TestHardening_RoundTrip_ManyRecipients(t *testing.T) {
	var tos, ccs []string
	for i := 0; i < 20; i++ {
		tos = append(tos, string(rune('a'+i))+"-to@client.example")
	}
	for i := 0; i < 10; i++ {
		ccs = append(ccs, string(rune('a'+i))+"-cc@client.example")
	}
	raw, err := smtp.BuildMessage("Simon <s@ssp.sh>",
		strings.Join(tos, ", "), strings.Join(ccs, ", "), "many recipients", "body", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	pm := parseBuilt(t, raw)
	if len(pm.to) != len(tos) {
		t.Fatalf("To count = %d, want %d", len(pm.to), len(tos))
	}
	for i, want := range tos {
		if pm.to[i] != want {
			t.Errorf("To[%d] = %q, want %q", i, pm.to[i], want)
		}
	}
	if len(pm.cc) != len(ccs) {
		t.Fatalf("Cc count = %d, want %d", len(pm.cc), len(ccs))
	}
	for i, want := range ccs {
		if pm.cc[i] != want {
			t.Errorf("Cc[%d] = %q, want %q", i, pm.cc[i], want)
		}
	}
}

// Body content that quoted-printable or SMTP transport classically mangles:
// Markdown two-space hard breaks, a lone "." line, "--" lines, lines that look
// like headers, and very long lines. All must arrive byte-exact.
func TestHardening_RoundTrip_BodyEdgeCases(t *testing.T) {
	longLine := strings.Repeat("a", 2000)
	body := "hard break line  \n" + // two trailing spaces = Markdown hard break
		"next line\n\n" +
		".\n\n" +
		"--\n\n" +
		"X-Evil: looks like a header but is body text\n\n" +
		"From Zurich with love\n\n" +
		longLine + "\n\n" +
		"Schluss mit Umlauten äöü"

	raw, err := smtp.BuildMessage("Simon <s@ssp.sh>", "a@b.io", "", "body edge cases", body, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	pm := parseBuilt(t, raw)

	if !strings.Contains(pm.plain, "hard break line  \r\n") {
		t.Error("Markdown two-space hard break lost its trailing spaces")
	}
	if !strings.Contains(pm.plain, "\r\n.\r\n") {
		t.Error("lone '.' line lost (dot-stuffing / QP interaction)")
	}
	if !strings.Contains(pm.plain, "\r\n--\r\n") {
		t.Error("'--' line lost")
	}
	if !strings.Contains(pm.plain, "X-Evil: looks like a header but is body text") {
		t.Error("header-lookalike body line lost")
	}
	if hasHeaderLine(pm.rawHead, "X-Evil") {
		t.Error("header-lookalike body line leaked into the headers")
	}
	if !strings.Contains(pm.plain, "From Zurich with love") {
		t.Error("'From ' line lost (mbox-escaping regression)")
	}
	if !strings.Contains(pm.plain, longLine) {
		t.Error("2000-char line corrupted by QP soft-break handling")
	}
	if !strings.Contains(pm.plain, "Schluss mit Umlauten äöü") {
		t.Error("umlauts corrupted")
	}

	// On the wire, no line may end in a literal space/tab — relays strip them,
	// which is exactly why trailing whitespace must be QP-encoded (=20).
	for _, line := range strings.Split(string(raw), "\r\n") {
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Errorf("wire line ends with literal whitespace: %q", line)
		}
	}

	// A body already using CRLF must not gain doubled blank lines.
	raw2, err := smtp.BuildMessage("Simon <s@ssp.sh>", "a@b.io", "", "crlf body", "line1\r\nline2", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if pm2 := parseBuilt(t, raw2); !strings.Contains(pm2.plain, "line1\r\nline2") {
		t.Errorf("CRLF input body mangled:\n%q", pm2.plain)
	}
}

// The combined shape — inline image AND file attachment — is the only MIME
// layout (mixed > related > alt+image, + file) not yet pinned end-to-end with
// byte fidelity. Both the recipient's client and neomd's own parser must
// recover both payloads exactly.
func TestHardening_RoundTrip_InlineImagePlusAttachment(t *testing.T) {
	imgPath, imgContent := binaryFixture(t, "diagram.png")
	pdfPath, pdfContent := binaryFixture(t, "Angebot Q3.pdf")

	body := "Grüezi,\n\n![diagram](" + imgPath + ")\n\nsee attached offer"
	raw, err := smtp.BuildMessage("Simon <s@ssp.sh>", "louise@client.example", "",
		"image plus attachment", body, []string{pdfPath}, "")
	if err != nil {
		t.Fatal(err)
	}

	pm := parseBuilt(t, raw)
	if !strings.Contains(pm.rawHead, "multipart/mixed") {
		t.Error("top-level Content-Type must be multipart/mixed for image+file")
	}
	if !bytes.Contains(raw, []byte("multipart/related")) {
		t.Error("inline image must live inside a multipart/related container")
	}
	// Content-IDs are unique per message (img0.<hex>@<sender domain>) so a
	// reply can never hijack the images quoted from an earlier neomd mail.
	var cid string
	for id := range pm.inline {
		cid = id
	}
	if len(pm.inline) != 1 || !strings.HasPrefix(cid, "img0.") || !strings.HasSuffix(cid, "@ssp.sh") {
		t.Fatalf("recipient view: want exactly one inline part with a unique img0.<hex>@ssp.sh id, got %v", pm.inline)
	}
	if !strings.Contains(pm.html, "cid:"+cid) {
		t.Error("HTML part lost the cid: reference to the inline image")
	}
	if !bytes.Equal(pm.inline[cid], imgContent) {
		t.Error("recipient view: inline image bytes corrupted")
	}
	if got, ok := pm.attached["Angebot Q3.pdf"]; !ok {
		t.Errorf("recipient view: file attachment missing (got %v)", keysOf(pm.attached))
	} else if !bytes.Equal(got, pdfContent) {
		t.Error("recipient view: file attachment bytes corrupted")
	}

	// neomd's own re-read (Sent copy, forward source) sees both payloads too.
	_, _, _, atts, _, _ := parseBody(raw)
	found := map[string][]byte{}
	for _, a := range atts {
		found[a.Filename] = a.Data
	}
	if !bytes.Equal(found["diagram.png"], imgContent) {
		t.Errorf("parseBody: inline image bytes mismatch (files seen: %v)", attNames(atts))
	}
	if !bytes.Equal(found["Angebot Q3.pdf"], pdfContent) {
		t.Errorf("parseBody: attachment bytes mismatch (files seen: %v)", attNames(atts))
	}
}

// Wire-format invariants no individual field test catches: strict CRLF line
// endings (a bare LF makes some servers reject or mangle the message), the
// RFC 5322 998-char line limit, a parseable Date, and Message-ID uniqueness
// (duplicate IDs make Gmail thread unrelated emails together).
func TestHardening_WireFormat(t *testing.T) {
	path, _ := binaryFixture(t, "wire.bin")
	raw, err := smtp.BuildMessage("Simon Späti <s@ssp.sh>", "a@b.io", "cc@b.io",
		"wire format ÄÖÜ", "body with umlauts äöü\n\n"+strings.Repeat("x", 1500), []string{path}, "")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.HasSuffix(raw, []byte("\r\n")) {
		t.Error("message must end with CRLF")
	}
	for i, line := range strings.Split(string(raw), "\r\n") {
		if strings.ContainsAny(line, "\r\n") {
			t.Errorf("line %d contains a bare CR or LF", i)
		}
		if len(line) > 998 {
			t.Errorf("line %d is %d chars, exceeds RFC 5322 limit of 998: %.60q...", i, len(line), line)
		}
	}

	pm := parseBuilt(t, raw)
	if _, err := time.Parse(time.RFC1123Z, headerValue(pm.rawHead, "Date")); err != nil {
		t.Errorf("Date header not RFC1123Z-parseable: %v", err)
	}
	if !hasHeaderLine(pm.rawHead, "MIME-Version") {
		t.Error("MIME-Version header missing")
	}

	// Two builds must never share a Message-ID.
	raw2, err := smtp.BuildMessage("Simon <s@ssp.sh>", "a@b.io", "", "second", "body", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	pm2 := parseBuilt(t, raw2)
	if pm.msgID == "" || pm.msgID == pm2.msgID {
		t.Errorf("Message-IDs must be unique and non-empty: %q vs %q", pm.msgID, pm2.msgID)
	}
}

// headerValue returns the value of the first raw header line named key.
func headerValue(rawHead, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(rawHead, "\r\n") {
		if len(line) > len(prefix) && strings.EqualFold(line[:len(prefix)], prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

// Emoji reactions (ctrl+e) are instant sends to real clients with no pre-send
// review — their recipients, threading, and body must be exactly right.
func TestHardening_RoundTrip_ReactionMessage(t *testing.T) {
	raw, err := smtp.BuildReactionMessage("Simon <s@ssp.sh>", "orig-sender@client.example", "",
		"Re: proposal", "👍\n\n> original quoted line", "<orig@client.example>", "<root@client.example>")
	if err != nil {
		t.Fatal(err)
	}
	pm := parseBuilt(t, raw)
	if len(pm.to) != 1 || pm.to[0] != "orig-sender@client.example" {
		t.Errorf("reaction To = %v", pm.to)
	}
	if pm.subject != "Re: proposal" {
		t.Errorf("reaction subject = %q", pm.subject)
	}
	if pm.inReply != "<orig@client.example>" {
		t.Errorf("reaction In-Reply-To = %q", pm.inReply)
	}
	if pm.refs != "<root@client.example> <orig@client.example>" {
		t.Errorf("reaction References = %q", pm.refs)
	}
	if !strings.Contains(pm.plain, "👍") || !strings.Contains(pm.plain, "> original quoted line") {
		t.Error("reaction body lost emoji or quoted history")
	}
}

// ── Header injection ─────────────────────────────────────────────────────
//
// Attacker-influenced strings (recipient fields parsed from the editor
// prelude, harvested contact names, forwarded attachment filenames) must
// never be able to smuggle extra headers into an outgoing message.

func TestHardening_HeaderInjection(t *testing.T) {
	const evil = "X-Injected"

	t.Run("subject CRLF", func(t *testing.T) {
		raw, err := smtp.BuildMessage("Simon <s@ssp.sh>", "a@b.io", "",
			"innocent\r\n"+evil+": 1", "body", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if pm := parseBuilt(t, raw); hasHeaderLine(pm.rawHead, evil) {
			t.Fatal("CRLF in subject injected a header")
		}
	})

	t.Run("to and cc CRLF", func(t *testing.T) {
		raw, err := smtp.BuildMessage("Simon <s@ssp.sh>",
			"victim@x.io\r\n"+evil+"-To: evil@x.io",
			"cc@x.io\r\n"+evil+"-Cc: evil@x.io",
			"s", "body", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if pm := parseBuilt(t, raw); hasHeaderLine(pm.rawHead, evil) {
			t.Fatal("CRLF in recipient field injected a header")
		}
	})

	t.Run("threading IDs CRLF", func(t *testing.T) {
		raw, err := smtp.BuildMessageWithThreading("Simon <s@ssp.sh>", "a@b.io", "",
			"s", "body", nil, "",
			"<id@x>\r\n"+evil+": 1", "<r@x>\r\n"+evil+": 2")
		if err != nil {
			t.Fatal(err)
		}
		if pm := parseBuilt(t, raw); hasHeaderLine(pm.rawHead, evil) {
			t.Fatal("CRLF in threading headers injected a header")
		}
	})

	t.Run("attachment filename quote and newline", func(t *testing.T) {
		// Linux allows quotes and newlines in filenames; a forwarded
		// attachment arrives under its sender-chosen name.
		dir := t.TempDir()
		path := filepath.Join(dir, "evil\"name\n"+evil+": 1.txt")
		if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
			t.Skip("filesystem rejects hostile filename")
		}
		raw, err := smtp.BuildMessage("Simon <s@ssp.sh>", "a@b.io", "", "s", "body", []string{path}, "")
		if err != nil {
			t.Fatal(err)
		}
		pm := parseBuilt(t, raw) // must stay parseable
		if hasHeaderLine(pm.rawHead, evil) {
			t.Fatal("hostile filename injected a top-level header")
		}
		for _, line := range strings.Split(string(raw), "\r\n") {
			if strings.HasPrefix(line, evil) {
				t.Fatalf("hostile filename injected a MIME part header: %q", line)
			}
		}
		if len(pm.attached) != 1 {
			t.Fatalf("attachment lost: %v", keysOf(pm.attached))
		}
	})
}

// Draft round trip with an inline image reference and a file attachment:
// saving (`d` on pre-send → BuildDraftMessage) and reopening (`E` → parseBody)
// must hand back the body byte-for-byte — the ![](path) stays at its spot and
// keeps the local path so the send pipeline can embed it later — and the
// attachment must come back with its name and bytes so continueDraft can
// re-list it as a # [attach] line.
func TestHardening_DraftRoundTrip_InlineImageAndAttachment(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "sub dir", "diagram v2.png")
	if err := os.MkdirAll(filepath.Dir(imgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imgPath, []byte("\x89PNG fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	pdfPath := filepath.Join(dir, "Offer Q4.pdf")
	pdfContent := []byte("%PDF-1.4 offer")
	if err := os.WriteFile(pdfPath, pdfContent, 0o600); err != nil {
		t.Fatal(err)
	}
	body := "Hallo Zoë,\n\nfirst paragraph\n\n![](<" + imgPath + ">)\n\nsecond paragraph after the image\n\n--\nZoë"

	raw, err := smtp.BuildDraftMessage("Zoë Example <zoe@example.org>", "rene@example.com", "", "", "Entwurf", body, []string{pdfPath})
	if err != nil {
		t.Fatal(err)
	}

	got, rawHTML, _, atts, _, _ := parseBody(raw)
	if got != body {
		t.Errorf("draft body mutated on reopen\ngot:\n%q\nwant:\n%q", got, body)
	}
	if rawHTML != "" {
		t.Errorf("draft must be plain text only, got HTML part:\n%s", rawHTML)
	}
	if len(atts) != 1 || atts[0].Filename != "Offer Q4.pdf" || !bytes.Equal(atts[0].Data, pdfContent) {
		t.Fatalf("attachment not restored: %+v", atts)
	}

	// Second cycle: re-save the reopened body with the restored attachment.
	raw2, err := smtp.BuildDraftMessage("Zoë Example <zoe@example.org>", "rene@example.com", "", "", "Entwurf", got, []string{pdfPath})
	if err != nil {
		t.Fatal(err)
	}
	got2, _, _, atts2, _, _ := parseBody(raw2)
	if got2 != body || len(atts2) != 1 {
		t.Errorf("second cycle drifted: body equal=%v attachments=%d", got2 == body, len(atts2))
	}
}
