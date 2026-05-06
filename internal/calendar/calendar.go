// Package calendar parses iCalendar (.ics) attachments and builds RFC
// 5546/6047 (iMIP) RSVP replies. Used to render meeting-invite cards in
// the reader and to send accept/decline/tentative responses with one
// keystroke.
//
// We deliberately handle only the common single-event REQUEST case: the
// first VEVENT is parsed, recurring rules (RRULE) are reported as-is, and
// METHOD=COUNTER / METHOD=CANCEL are out of scope.
package calendar

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
)

// Status is the responder's RSVP choice.
type Status string

const (
	StatusAccepted  Status = "ACCEPTED"
	StatusDeclined  Status = "DECLINED"
	StatusTentative Status = "TENTATIVE"
)

// Event is the subset of VEVENT data the UI needs to render a card.
type Event struct {
	Summary   string
	Location  string
	Start     time.Time
	End       time.Time
	AllDay    bool
	Organizer string   // bare email address, mailto: prefix stripped
	Attendees []string // bare email addresses
}

// Parse reads a single VCALENDAR/VEVENT body and returns the event metadata.
// If the body has multiple VEVENTs (e.g. recurring rule expanded by the
// sender), only the first is returned — matches matcha's behaviour and
// keeps the reader card simple.
func Parse(data []byte) (*Event, error) {
	cal, err := ics.ParseCalendar(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse vcalendar: %w", err)
	}
	events := cal.Events()
	if len(events) == 0 {
		return nil, fmt.Errorf("vcalendar has no VEVENT")
	}
	v := events[0]

	e := &Event{}
	if p := v.GetProperty(ics.ComponentPropertySummary); p != nil {
		e.Summary = p.Value
	}
	if p := v.GetProperty(ics.ComponentPropertyLocation); p != nil {
		e.Location = p.Value
	}
	if p := v.GetProperty(ics.ComponentPropertyOrganizer); p != nil {
		e.Organizer = stripMailto(p.Value)
	}
	for _, a := range v.Attendees() {
		e.Attendees = append(e.Attendees, a.Email())
	}

	// All-day events use VALUE=DATE on DTSTART/DTEND. Detect that explicitly
	// because GetStartAt happily parses a DATE-only value as midnight UTC.
	if dtStart := v.GetProperty(ics.ComponentPropertyDtStart); dtStart != nil {
		if vals := dtStart.ICalParameters[string(ics.ParameterValue)]; len(vals) > 0 && strings.EqualFold(vals[0], "DATE") {
			e.AllDay = true
			if t, err := v.GetAllDayStartAt(); err == nil {
				e.Start = t
			}
		}
	}
	if !e.AllDay {
		if t, err := v.GetStartAt(); err == nil {
			e.Start = t
		}
	}
	if t, err := v.GetEndAt(); err == nil {
		e.End = t
	}

	return e, nil
}

// BuildRSVP constructs a METHOD:REPLY iCalendar body suitable for the
// text/calendar part of an iMIP response. It:
//   - parses the original .ics (preserves UID, DTSTART, DTEND, SEQUENCE)
//   - sets METHOD:REPLY at calendar level
//   - refreshes DTSTAMP to now (UTC)
//   - removes every ATTENDEE on the first VEVENT and re-adds only the
//     responder with PARTSTAT=<status> and RSVP=TRUE
//
// responderEmail must match (case-insensitively) one of the original
// ATTENDEEs for Google Calendar / Outlook to credit the response. If it
// doesn't match the function still succeeds — some servers accept it,
// others ignore it.
func BuildRSVP(originalICS []byte, responderEmail string, status Status) ([]byte, error) {
	cal, err := ics.ParseCalendar(bytes.NewReader(originalICS))
	if err != nil {
		return nil, fmt.Errorf("parse original vcalendar: %w", err)
	}
	events := cal.Events()
	if len(events) == 0 {
		return nil, fmt.Errorf("vcalendar has no VEVENT")
	}

	cal.SetMethod(ics.MethodReply)

	v := events[0]
	v.SetDtStampTime(time.Now().UTC())

	// Map our public Status to the library's PartStat parameter.
	var partStat ics.ParticipationStatus
	switch status {
	case StatusAccepted:
		partStat = ics.ParticipationStatusAccepted
	case StatusDeclined:
		partStat = ics.ParticipationStatusDeclined
	case StatusTentative:
		partStat = ics.ParticipationStatusTentative
	default:
		return nil, fmt.Errorf("unknown rsvp status: %q", status)
	}

	// Remove ALL existing ATTENDEE lines, then re-add only the responder.
	// Google Calendar / Outlook require this — leaving other attendees in
	// the reply causes the response to be rejected or attributed wrong.
	v.RemoveProperty(ics.ComponentPropertyAttendee)
	v.AddProperty(
		ics.ComponentPropertyAttendee,
		"mailto:"+responderEmail,
		partStat,
		ics.WithRSVP(true),
	)

	var buf bytes.Buffer
	if err := cal.SerializeTo(&buf); err != nil {
		return nil, fmt.Errorf("serialize reply vcalendar: %w", err)
	}
	return buf.Bytes(), nil
}

// FormatTime renders the event's start (and optionally end) for display in
// the reader card. Examples:
//
//	"Mon, 21 Apr 2026 14:00–15:00"
//	"Mon, 21 Apr 2026 14:00"
//	"Mon, 21 Apr 2026  (all day)"
//
// Empty strings indicate the event has no start time set.
func (e *Event) FormatTime() string {
	if e.Start.IsZero() {
		return ""
	}
	loc := e.Start.Local()
	day := loc.Format("Mon, 02 Jan 2006")
	if e.AllDay {
		return day + "  (all day)"
	}
	startTime := loc.Format("15:04")
	if !e.End.IsZero() {
		endTime := e.End.Local().Format("15:04")
		return fmt.Sprintf("%s %s–%s", day, startTime, endTime)
	}
	return fmt.Sprintf("%s %s", day, startTime)
}

func stripMailto(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "mailto:")
}
