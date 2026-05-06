package calendar

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

const fixtureRequest = `BEGIN:VCALENDAR
PRODID:-//Test//Test//EN
VERSION:2.0
METHOD:REQUEST
BEGIN:VEVENT
UID:test-event-12345@example.com
DTSTAMP:20260101T120000Z
DTSTART:20260421T140000Z
DTEND:20260421T150000Z
SUMMARY:Q2 Planning Meeting
LOCATION:Conference Room A
ORGANIZER:mailto:boss@example.com
ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:me@example.com
ATTENDEE;PARTSTAT=NEEDS-ACTION;RSVP=TRUE:mailto:other@example.com
SEQUENCE:0
STATUS:CONFIRMED
END:VEVENT
END:VCALENDAR
`

const fixtureAllDay = `BEGIN:VCALENDAR
PRODID:-//Test//Test//EN
VERSION:2.0
METHOD:REQUEST
BEGIN:VEVENT
UID:allday@example.com
DTSTAMP:20260101T120000Z
DTSTART;VALUE=DATE:20260501
DTEND;VALUE=DATE:20260502
SUMMARY:Holiday
ORGANIZER:mailto:hr@example.com
ATTENDEE;PARTSTAT=NEEDS-ACTION:mailto:me@example.com
END:VEVENT
END:VCALENDAR
`

func TestParse_BasicRequest(t *testing.T) {
	e, err := Parse([]byte(fixtureRequest))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Summary != "Q2 Planning Meeting" {
		t.Errorf("Summary = %q, want %q", e.Summary, "Q2 Planning Meeting")
	}
	if e.Location != "Conference Room A" {
		t.Errorf("Location = %q, want %q", e.Location, "Conference Room A")
	}
	if e.Organizer != "boss@example.com" {
		t.Errorf("Organizer = %q, want bare email", e.Organizer)
	}
	if len(e.Attendees) != 2 {
		t.Errorf("Attendees count = %d, want 2", len(e.Attendees))
	}
	wantStart := time.Date(2026, 4, 21, 14, 0, 0, 0, time.UTC)
	if !e.Start.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", e.Start, wantStart)
	}
	if e.AllDay {
		t.Error("expected AllDay = false for time-bound event")
	}
}

func TestParse_AllDay(t *testing.T) {
	e, err := Parse([]byte(fixtureAllDay))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !e.AllDay {
		t.Error("expected AllDay = true for VALUE=DATE event")
	}
	if e.Summary != "Holiday" {
		t.Errorf("Summary = %q", e.Summary)
	}
}

func TestParse_ErrorOnEmpty(t *testing.T) {
	_, err := Parse([]byte("not a calendar"))
	if err == nil {
		t.Error("expected error on garbage input")
	}
}

func TestBuildRSVP_AcceptedStripsOtherAttendees(t *testing.T) {
	reply, err := BuildRSVP([]byte(fixtureRequest), "me@example.com", StatusAccepted)
	if err != nil {
		t.Fatalf("BuildRSVP: %v", err)
	}
	s := string(reply)

	if !strings.Contains(s, "METHOD:REPLY") {
		t.Errorf("reply missing METHOD:REPLY:\n%s", s)
	}
	if !strings.Contains(s, "PARTSTAT=ACCEPTED") {
		t.Errorf("reply missing PARTSTAT=ACCEPTED:\n%s", s)
	}
	if strings.Contains(s, "other@example.com") {
		t.Error("reply must NOT contain non-responding attendees (Google rejects RSVPs that include others)")
	}
	if strings.Count(s, "ATTENDEE") != 1 {
		t.Errorf("reply must have exactly one ATTENDEE line, got %d", strings.Count(s, "ATTENDEE"))
	}
	if !strings.Contains(s, "mailto:me@example.com") {
		t.Errorf("reply must include responder email:\n%s", s)
	}
	// UID/DTSTART/DTEND must survive the round-trip so the server can
	// match the reply to the original event.
	for _, mustHave := range []string{"UID:test-event-12345@example.com", "DTSTART:20260421T140000Z", "DTEND:20260421T150000Z", "SUMMARY:Q2 Planning Meeting"} {
		if !strings.Contains(s, mustHave) {
			t.Errorf("reply missing %q:\n%s", mustHave, s)
		}
	}
}

func TestBuildRSVP_DeclinedAndTentative(t *testing.T) {
	for _, st := range []Status{StatusDeclined, StatusTentative} {
		reply, err := BuildRSVP([]byte(fixtureRequest), "me@example.com", st)
		if err != nil {
			t.Fatalf("BuildRSVP(%s): %v", st, err)
		}
		want := "PARTSTAT=" + string(st)
		if !bytes.Contains(reply, []byte(want)) {
			t.Errorf("BuildRSVP(%s) missing %q", st, want)
		}
	}
}

func TestBuildRSVP_DTSTAMPUpdated(t *testing.T) {
	// Original DTSTAMP is 20260101; reply's DTSTAMP must be more recent.
	reply, err := BuildRSVP([]byte(fixtureRequest), "me@example.com", StatusAccepted)
	if err != nil {
		t.Fatalf("BuildRSVP: %v", err)
	}
	s := string(reply)
	if strings.Contains(s, "DTSTAMP:20260101T120000Z") {
		t.Error("DTSTAMP should be refreshed to now, not preserved from original")
	}
	if !strings.Contains(s, "DTSTAMP:") {
		t.Error("DTSTAMP line missing")
	}
}

func TestEvent_FormatTime(t *testing.T) {
	e, err := Parse([]byte(fixtureRequest))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := e.FormatTime()
	if out == "" {
		t.Error("FormatTime returned empty for an event with start/end")
	}
	if !strings.Contains(out, "2026") {
		t.Errorf("FormatTime missing year: %q", out)
	}

	allDay, _ := Parse([]byte(fixtureAllDay))
	if !strings.Contains(allDay.FormatTime(), "all day") {
		t.Errorf("FormatTime should mark all-day events: %q", allDay.FormatTime())
	}
}
