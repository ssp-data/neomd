package ui

import (
	"testing"
	"time"

	"github.com/sspaeti/neomd/internal/imap"
)

func mkEmail(uid uint32, msgID, subject, from string, daysAgo int, seen bool) imap.Email {
	return imap.Email{
		UID: uid, MessageID: msgID, Subject: subject, From: from,
		Date: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC).AddDate(0, 0, -daysAgo),
		Seen: seen, Size: 100,
	}
}

func titleMap(m map[string]string) func(string) (string, bool) {
	return func(id string) (string, bool) { t, ok := m[id]; return t, ok }
}

func TestCollapseMerges_NilTitleOfIsNoop(t *testing.T) {
	rows := flatEmails([]imap.Email{mkEmail(1, "<a>", "x", "a@x", 0, true)}, "date", true)
	got := collapseMerges(rows, nil, "date", true)
	if len(got) != 1 || got[0].merge != nil {
		t.Fatalf("expected passthrough, got %+v", got)
	}
}

func TestCollapseMerges_Basic(t *testing.T) {
	emails := []imap.Email{
		mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 5, true),
		mkEmail(2, "<keep>", "Invoice", "acme@x", 3, true),
		mkEmail(3, "<b2>", "Undelivered", "mailer-daemon@x", 1, false),
	}
	rows := threadEmails(emails, "date", true) // newest first
	got := collapseMerges(rows, titleMap(map[string]string{"<b1>": "Bounces", "<b2>": "Bounces"}), "date", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (collapsed + Invoice); rows: %+v", len(got), got)
	}
	// Newest member (uid 3, 1 day ago) is newer than Invoice (3 days) → merged row first.
	if got[0].merge == nil || got[0].merge.title != "Bounces" {
		t.Fatalf("row 0 should be the merge, got %+v", got[0])
	}
	if got[0].email.UID != 3 {
		t.Errorf("representative UID = %d, want 3 (newest member)", got[0].email.UID)
	}
	if len(got[0].merge.members) != 2 || got[0].merge.members[0].UID != 3 || got[0].merge.members[1].UID != 1 {
		t.Errorf("members = %+v, want [uid3 uid1] newest first", got[0].merge.members)
	}
	if got[1].email.UID != 2 || got[1].merge != nil {
		t.Errorf("row 1 should be plain Invoice, got %+v", got[1])
	}
}

func TestCollapseMerges_SortPositionOldestFirst(t *testing.T) {
	emails := []imap.Email{
		mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 5, true),
		mkEmail(2, "<keep>", "Invoice", "acme@x", 3, true),
		mkEmail(3, "<b2>", "Undelivered", "mailer-daemon@x", 1, false),
	}
	rows := threadEmails(emails, "date", false) // oldest first
	got := collapseMerges(rows, titleMap(map[string]string{"<b1>": "Bounces", "<b2>": "Bounces"}), "date", false)
	// Representative is still the newest member (1 day ago) → sorts after Invoice (3 days ago).
	if got[0].email.UID != 2 || got[1].merge == nil {
		t.Errorf("want [Invoice, merge], got [%d, merge=%v]", got[0].email.UID, got[1].merge != nil)
	}
}

func TestCollapseMerges_SingleMemberStillCollapses(t *testing.T) {
	emails := []imap.Email{mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 0, true)}
	got := collapseMerges(flatEmails(emails, "date", true), titleMap(map[string]string{"<b1>": "Bounces"}), "date", true)
	if len(got) != 1 || got[0].merge == nil || len(got[0].merge.members) != 1 {
		t.Fatalf("single member should still collapse, got %+v", got)
	}
}

func TestCollapseMerges_AbsorbsAutomaticThread(t *testing.T) {
	// <b1> is a member; <reply> is an In-Reply-To child of <b1> → whole thread joins.
	emails := []imap.Email{
		mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 5, true),
		{UID: 2, MessageID: "<reply>", InReplyTo: "<b1>", Subject: "Re: Undelivered", From: "me@x",
			Date: time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC), Seen: true, Size: 10},
		mkEmail(3, "<keep>", "Invoice", "acme@x", 3, true),
	}
	rows := threadEmails(emails, "date", true)
	got := collapseMerges(rows, titleMap(map[string]string{"<b1>": "Bounces"}), "date", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2; rows %+v", len(got), got)
	}
	var mr *mergeRow
	for _, r := range got {
		if r.merge != nil {
			mr = r.merge
		}
	}
	if mr == nil || len(mr.members) != 2 {
		t.Fatalf("expected merge with 2 members (b1 + absorbed reply), got %+v", mr)
	}
	if mr.members[0].UID != 2 {
		t.Errorf("newest member should be the reply (uid 2), got %d", mr.members[0].UID)
	}
}

func TestCollapseMerges_TwoMergesInOneFolder(t *testing.T) {
	emails := []imap.Email{
		mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 4, true),
		mkEmail(2, "<n1>", "Digest", "news@x", 2, true),
		mkEmail(3, "<b2>", "Undelivered", "mailer-daemon@x", 1, true),
		mkEmail(4, "<n2>", "Digest", "news@x", 0, true),
	}
	got := collapseMerges(threadEmails(emails, "date", true),
		titleMap(map[string]string{"<b1>": "Bounces", "<b2>": "Bounces", "<n1>": "News", "<n2>": "News"}), "date", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].merge == nil || got[0].merge.title != "News" || got[1].merge == nil || got[1].merge.title != "Bounces" {
		t.Errorf("want [News, Bounces], got [%v, %v]", got[0].merge, got[1].merge)
	}
}

func TestCollapseMerges_ThreadingDisabledStillCollapses(t *testing.T) {
	emails := []imap.Email{
		mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 2, true),
		mkEmail(2, "<b2>", "Undelivered", "mailer-daemon@x", 1, true),
	}
	got := collapseMerges(flatEmails(emails, "date", true), titleMap(map[string]string{"<b1>": "Bounces", "<b2>": "Bounces"}), "date", true)
	if len(got) != 1 || got[0].merge == nil || len(got[0].merge.members) != 2 {
		t.Fatalf("flat rows should collapse too, got %+v", got)
	}
}
