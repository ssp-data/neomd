package smtp

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
)

// BuildRSVPMessage builds the iMIP MIME body for an RSVP reply.
// Structure (matches matcha + Google Calendar / Outlook expectations):
//
//	multipart/mixed
//	  ├── multipart/alternative
//	  │     ├── text/plain;  charset=utf-8
//	  │     └── text/calendar;  method=REPLY;  charset=utf-8;  base64
//	  └── application/ics;  name="invite.ics";  base64  (also includes the calendar body)
//
// Some receivers prefer the inline text/calendar; others key off the
// .ics attachment — including both maximises compatibility.
//
// Args:
//   - from / to:    sender / recipient (organizer email).
//   - subject:      typically "Re: <event summary>".
//   - plainBody:    short human-readable confirmation (e.g. "ACCEPTED: …").
//   - calendarReply: the bytes returned by calendar.BuildRSVP — METHOD:REPLY .ics.
//   - inReplyTo / references: original Message-ID for threading.
func BuildRSVPMessage(from, to, subject, plainBody string, calendarReply []byte, inReplyTo, references string) ([]byte, error) {
	if len(calendarReply) == 0 {
		return nil, fmt.Errorf("rsvp: calendarReply is empty")
	}

	domain := "neomd.local"
	if d, ok := extractDomain(from); ok {
		domain = d
	}

	mixedBoundary, err := randomBoundary()
	if err != nil {
		return nil, err
	}
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

	hdr("From", from)
	hdr("To", to)
	hdr("Subject", mime.QEncoding.Encode("utf-8", subject))
	hdr("Date", time.Now().Format(time.RFC1123Z))
	hdr("Message-ID", "<"+msgID+"@"+domain+">")
	// RFC 5322 §3.6.4 requires angle brackets around msg-ids in In-Reply-To
	// and References. The IMAP envelope returns bare IDs, so wrap on the way
	// out — Gmail's iMIP processor refuses to match a REPLY to an event when
	// In-Reply-To isn't bracketed, silently treating it as ordinary mail.
	if inReplyTo != "" {
		hdr("In-Reply-To", wrapMsgIDs(inReplyTo))
	}
	if references != "" {
		hdr("References", wrapMsgIDs(references))
	}
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", `multipart/mixed; boundary="`+mixedBoundary+`"`)
	hdr("X-Mailer", "neomd")
	b.WriteString("\r\n")

	// multipart/alternative wrapper
	fmt.Fprintf(&b, "--%s\r\n", mixedBoundary)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", altBoundary)

	// text/plain part
	fmt.Fprintf(&b, "--%s\r\n", altBoundary)
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(plainBody)
	if !strings.HasSuffix(plainBody, "\n") {
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")

	// text/calendar (inline reply)
	fmt.Fprintf(&b, "--%s\r\n", altBoundary)
	b.WriteString("Content-Type: text/calendar; charset=utf-8; method=REPLY\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	writeBase64(&b, calendarReply)
	b.WriteString("\r\n")

	// close alt
	fmt.Fprintf(&b, "--%s--\r\n", altBoundary)

	// .ics attachment (mirrors the inline payload byte-for-byte)
	fmt.Fprintf(&b, "--%s\r\n", mixedBoundary)
	b.WriteString("Content-Type: application/ics; name=\"invite.ics\"\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"invite.ics\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	writeBase64(&b, calendarReply)
	b.WriteString("\r\n")

	// close mixed
	fmt.Fprintf(&b, "--%s--\r\n", mixedBoundary)

	return b.Bytes(), nil
}

// writeBase64 emits the data as base64 in 76-char lines (RFC 2045).
func writeBase64(b *bytes.Buffer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	const lineLen = 76
	for i := 0; i < len(enc); i += lineLen {
		end := i + lineLen
		if end > len(enc) {
			end = len(enc)
		}
		b.WriteString(enc[i:end])
		b.WriteString("\r\n")
	}
}

// senderEmail strips the display-name part of a From header so we can use
// the bare email as the responder address in the RSVP ATTENDEE line.
// Returns the original string on parse failure.
func senderEmail(from string) string {
	if addr, err := mail.ParseAddress(from); err == nil {
		return addr.Address
	}
	return from
}

// wrapMsgIDs ensures every whitespace-separated message-id token in the
// input is wrapped in angle brackets. Tokens that are already bracketed are
// left alone. Used for In-Reply-To (single token) and References (chain).
func wrapMsgIDs(s string) string {
	parts := strings.Fields(s)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if !strings.HasPrefix(p, "<") {
			p = "<" + p
		}
		if !strings.HasSuffix(p, ">") {
			p = p + ">"
		}
		out = append(out, p)
	}
	return strings.Join(out, " ")
}
