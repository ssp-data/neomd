package smtp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emersion/go-message/mail"
)

func TestBuildRSVPMessage_Structure(t *testing.T) {
	calendarReply := []byte("BEGIN:VCALENDAR\nVERSION:2.0\nMETHOD:REPLY\nBEGIN:VEVENT\nUID:test@example.com\nDTSTAMP:20260506T120000Z\nATTENDEE;PARTSTAT=ACCEPTED;RSVP=TRUE:mailto:me@example.com\nEND:VEVENT\nEND:VCALENDAR\n")

	raw, err := BuildRSVPMessage(
		"Me <me@example.com>",
		"organizer@example.com",
		"Re: Q2 Planning Meeting",
		"ACCEPTED: Q2 Planning Meeting\nMon, 21 Apr 2026 14:00–15:00 at Conference Room A",
		calendarReply,
		"<original-msg-id@example.com>",
		"<original-msg-id@example.com>",
	)
	if err != nil {
		t.Fatalf("BuildRSVPMessage: %v", err)
	}

	// Outer envelope: parse top-level headers.
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("mail.CreateReader: %v", err)
	}
	defer r.Close()

	// Verify the threading headers survived.
	from := r.Header.Get("From")
	if !strings.Contains(from, "me@example.com") {
		t.Errorf("From header lost the address: %q", from)
	}
	if got := r.Header.Get("In-Reply-To"); got != "<original-msg-id@example.com>" {
		t.Errorf("In-Reply-To = %q", got)
	}
	if got := r.Header.Get("Subject"); !strings.Contains(got, "Q2 Planning Meeting") {
		t.Errorf("Subject lost: %q", got)
	}

	// Verify there's a text/calendar; method=REPLY part inline AND an
	// application/ics attachment — both required for max compatibility.
	var hasInlineCalendar, hasIcsAttachment bool
	for {
		p, err := r.NextPart()
		if err != nil {
			break
		}
		ct := p.Header.Get("Content-Type")
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ = h.ContentType()
			if strings.HasPrefix(ct, "text/calendar") {
				if !strings.Contains(p.Header.Get("Content-Type"), "method=REPLY") {
					t.Errorf("inline text/calendar missing method=REPLY: %q", p.Header.Get("Content-Type"))
				}
				hasInlineCalendar = true
			}
		case *mail.AttachmentHeader:
			fname, _ := h.Filename()
			if fname == "invite.ics" {
				hasIcsAttachment = true
			}
		}
	}
	if !hasInlineCalendar {
		t.Errorf("missing inline text/calendar; method=REPLY part. Raw:\n%s", string(raw))
	}
	if !hasIcsAttachment {
		t.Errorf("missing application/ics attachment named invite.ics")
	}
}

func TestBuildRSVPMessage_ThreadingHeadersBracketed(t *testing.T) {
	// Regression: Gmail's iMIP processor rejects RSVPs whose In-Reply-To
	// is missing the angle brackets RFC 5322 requires.
	calendarReply := []byte("BEGIN:VCALENDAR\nVERSION:2.0\nMETHOD:REPLY\nEND:VCALENDAR\n")
	cases := []struct {
		name             string
		inReplyToInput   string
		referencesInput  string
		wantInReplyTo    string
		wantReferences   string
	}{
		{
			name:            "bare ids get wrapped",
			inReplyToInput:  "calendar-abc@google.com",
			referencesInput: "calendar-abc@google.com",
			wantInReplyTo:   "<calendar-abc@google.com>",
			wantReferences:  "<calendar-abc@google.com>",
		},
		{
			name:            "already-bracketed ids stay unchanged",
			inReplyToInput:  "<already@example.com>",
			referencesInput: "<already@example.com>",
			wantInReplyTo:   "<already@example.com>",
			wantReferences:  "<already@example.com>",
		},
		{
			name:            "references chain wraps each token",
			inReplyToInput:  "newest@x.com",
			referencesInput: "oldest@x.com middle@x.com newest@x.com",
			wantInReplyTo:   "<newest@x.com>",
			wantReferences:  "<oldest@x.com> <middle@x.com> <newest@x.com>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := BuildRSVPMessage("me@example.com", "to@example.com", "Accepted: x", "ok\n", calendarReply, tc.inReplyToInput, tc.referencesInput)
			if err != nil {
				t.Fatalf("BuildRSVPMessage: %v", err)
			}
			s := string(raw)
			if !strings.Contains(s, "In-Reply-To: "+tc.wantInReplyTo+"\r\n") {
				t.Errorf("In-Reply-To wrong; want %q in:\n%s", tc.wantInReplyTo, s)
			}
			if !strings.Contains(s, "References: "+tc.wantReferences+"\r\n") {
				t.Errorf("References wrong; want %q in:\n%s", tc.wantReferences, s)
			}
		})
	}
}

func TestWrapMsgIDs(t *testing.T) {
	cases := map[string]string{
		"":                                  "",
		"foo@bar":                           "<foo@bar>",
		"<foo@bar>":                         "<foo@bar>",
		"a@x b@y c@z":                       "<a@x> <b@y> <c@z>",
		"<a@x> <b@y>":                       "<a@x> <b@y>",
		"<a@x> b@y <c@z>":                   "<a@x> <b@y> <c@z>",
	}
	for in, want := range cases {
		if got := wrapMsgIDs(in); got != want {
			t.Errorf("wrapMsgIDs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSenderEmail(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Me <me@example.com>", "me@example.com"},
		{"me@example.com", "me@example.com"},
		{`"Some Name" <other@example.com>`, "other@example.com"},
	}
	for _, tt := range tests {
		if got := senderEmail(tt.in); got != tt.want {
			t.Errorf("senderEmail(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
