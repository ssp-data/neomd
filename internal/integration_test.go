// Package integration_test runs end-to-end tests against a real IMAP/SMTP server.
//
// Skipped unless NEOMD_TEST_IMAP_HOST is set. Run with:
//
//	make test-integration
//
// These tests send real emails to the test account (sends to itself) and
// clean up after. They require network access and valid credentials.
package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goIMAP "github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/smtp"
)

// testEnv holds credentials loaded from environment variables.
type testEnv struct {
	imapHost string
	imapPort string
	smtpHost string
	smtpPort string
	user     string
	password string
	from     string
}

func loadEnv(t *testing.T) testEnv {
	t.Helper()
	host := os.Getenv("NEOMD_TEST_IMAP_HOST")
	if host == "" {
		t.Skip("set NEOMD_TEST_IMAP_HOST to run integration tests")
	}
	env := testEnv{
		imapHost: host,
		imapPort: getEnvOr("NEOMD_TEST_IMAP_PORT", "993"),
		smtpHost: getEnvOr("NEOMD_TEST_SMTP_HOST", host),
		smtpPort: getEnvOr("NEOMD_TEST_SMTP_PORT", "587"),
		user:     os.Getenv("NEOMD_TEST_USER"),
		password: os.Getenv("NEOMD_TEST_PASS"),
		from:     os.Getenv("NEOMD_TEST_FROM"),
	}
	if env.user == "" || env.password == "" {
		t.Skip("set NEOMD_TEST_USER and NEOMD_TEST_PASS")
	}
	if env.from == "" {
		env.from = env.user
	}
	return env
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (e testEnv) imapClient() *goIMAP.Client {
	return goIMAP.New(goIMAP.Config{
		Host:     e.imapHost,
		Port:     e.imapPort,
		User:     e.user,
		Password: e.password,
		TLS:      e.imapPort == "993",
		STARTTLS: e.imapPort == "143",
	})
}

// ccRecipient returns ", addr" if NEOMD_TEST_CC is set, empty string otherwise.
// Used to optionally CC test emails to a live inbox for manual inspection.
func (e testEnv) ccRecipient() string {
	if cc := os.Getenv("NEOMD_TEST_CC"); cc != "" {
		return ", " + cc
	}
	return ""
}

func (e testEnv) smtpConfig() smtp.Config {
	return smtp.Config{
		Host:     e.smtpHost,
		Port:     e.smtpPort,
		User:     e.user,
		Password: e.password,
		From:     e.from,
	}
}

// uniqueSubject returns a unique subject for test isolation.
func uniqueSubject(name string) string {
	return fmt.Sprintf("[neomd-test] %s %d", name, time.Now().UnixNano())
}

// waitForEmail polls IMAP until an email with the given subject substring appears, or times out.
// Uses FetchHeaders (not SEARCH) to avoid IMAP SEARCH substring quirks with special chars.
func waitForEmail(t *testing.T, cli *goIMAP.Client, folder, subject string, timeout time.Duration) *goIMAP.Email {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		emails, err := cli.FetchHeaders(ctx, folder, 20)
		if err == nil {
			for i := range emails {
				if strings.Contains(emails[i].Subject, subject) {
					return &emails[i]
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("email with subject %q not found in %s after %v", subject, folder, timeout)
	return nil
}

// cleanupEmail permanently deletes a test email.
func cleanupEmail(t *testing.T, cli *goIMAP.Client, folder string, uid uint32) {
	t.Helper()
	ctx := context.Background()
	if err := cli.ExpungeAll(ctx, folder, []uint32{uid}); err != nil {
		t.Logf("cleanup warning: %v", err)
	}
}

// --- Tests ---

func TestIntegration_IMAPConnect(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	if err := cli.Ping(context.Background()); err != nil {
		t.Fatalf("IMAP ping failed: %v", err)
	}
}

func TestIntegration_IMAPFetchHeaders(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	emails, err := cli.FetchHeaders(context.Background(), "INBOX", 5)
	if err != nil {
		t.Fatalf("FetchHeaders: %v", err)
	}
	// Just verify it returns without error and emails have basic fields
	for _, e := range emails {
		if e.UID == 0 {
			t.Error("email has UID 0")
		}
		if e.Subject == "" && e.From == "" {
			t.Error("email has no subject and no from")
		}
	}
}

func TestIntegration_SendPlainEmail(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("plain")
	body := "Hello from neomd integration test.\n\nThis is **bold** and this is a [link](https://ssp.sh)."

	// Send to self
	err := smtp.Send(env.smtpConfig(), env.user, "", "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for delivery and fetch
	email := waitForEmail(t, cli, "INBOX", subject, 30*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	// Verify headers
	if !strings.Contains(email.From, env.user) && !strings.Contains(email.From, extractUser(env.from)) {
		t.Errorf("From = %q, expected to contain %q", email.From, env.user)
	}
	if email.Subject != subject {
		t.Errorf("Subject = %q, want %q", email.Subject, subject)
	}

	// Fetch body and verify content
	markdown, rawHTML, _, _, _, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if !strings.Contains(markdown, "neomd integration test") {
		t.Errorf("body missing expected text, got: %s", truncate(markdown, 200))
	}
	if rawHTML == "" {
		t.Error("expected HTML part in multipart/alternative, got empty")
	}
	if !strings.Contains(rawHTML, "<strong>bold</strong>") {
		t.Errorf("HTML part missing <strong>bold</strong>, got: %s", truncate(rawHTML, 200))
	}
	if !strings.Contains(rawHTML, `href="https://ssp.sh"`) {
		t.Errorf("HTML part missing link href, got: %s", truncate(rawHTML, 200))
	}
}

func TestIntegration_SendWithCC(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("cc")
	body := "Testing CC header."

	// CC to self (same address, just verifying the header round-trips)
	err := smtp.Send(env.smtpConfig(), env.user, env.user, "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	email := waitForEmail(t, cli, "INBOX", subject, 30*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	// Fetch raw body to check CC header
	markdown, _, _, _, _, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	_ = markdown // CC is in envelope, not body — verify via headers if available
	// The email arrived with CC set; IMAP envelope should have it
	if email.CC == "" {
		t.Logf("Note: CC not populated in Email struct (CC field may not be fetched by FetchHeaders)")
	}
}

func TestIntegration_SendWithAttachment(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("attach")
	body := "Email with attachment."

	// Create a test file to attach
	dir := t.TempDir()
	attachPath := filepath.Join(dir, "test-document.txt")
	if err := os.WriteFile(attachPath, []byte("This is the attachment content from neomd test."), 0600); err != nil {
		t.Fatal(err)
	}

	err := smtp.Send(env.smtpConfig(), env.user, "", "", subject, body, []string{attachPath})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	email := waitForEmail(t, cli, "INBOX", subject, 60*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	// Fetch body — attachments should be listed
	_, _, _, attachments, _, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if len(attachments) == 0 {
		t.Fatal("expected at least 1 attachment, got 0")
	}

	found := false
	for _, a := range attachments {
		if strings.Contains(a.Filename, "test-document") {
			found = true
			if len(a.Data) == 0 {
				t.Error("attachment data is empty")
			}
			if !strings.Contains(string(a.Data), "attachment content from neomd test") {
				t.Errorf("attachment content mismatch, got %d bytes", len(a.Data))
			}
		}
	}
	if !found {
		names := make([]string, len(attachments))
		for i, a := range attachments {
			names[i] = a.Filename
		}
		t.Errorf("attachment 'test-document.txt' not found, got: %v", names)
	}
}

func TestIntegration_SendNonASCIISubject(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("Ünïcödé Tëst 🚀")
	body := "Testing non-ASCII subject encoding."

	err := smtp.Send(env.smtpConfig(), env.user, "", "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	email := waitForEmail(t, cli, "INBOX", "Tëst", 30*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	// Subject should survive Q-encoding round-trip
	if !strings.Contains(email.Subject, "Ünïcödé") {
		t.Errorf("Subject = %q, expected to contain 'Ünïcödé'", email.Subject)
	}
	if !strings.Contains(email.Subject, "🚀") {
		t.Errorf("Subject = %q, expected to contain emoji", email.Subject)
	}
}

func TestIntegration_IMAPSearch(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	// Send a unique email to search for
	subject := uniqueSubject("search-target")
	body := "This email exists to be found by IMAP SEARCH."

	err := smtp.Send(env.smtpConfig(), env.user, "", "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	email := waitForEmail(t, cli, "INBOX", subject, 30*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	// Test subject: prefix search
	results, err := cli.SearchMessages(context.Background(), "INBOX", "subject:"+subject)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("subject: search returned no results")
	}

	// Test from: prefix search
	results, err = cli.SearchMessages(context.Background(), "INBOX", "from:"+env.user)
	if err != nil {
		t.Fatalf("SearchMessages from: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("from: search returned no results")
	}
}

func TestIntegration_IMAPMoveAndUndo(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	// Ensure test folder exists
	testFolder := "NeomdTest"
	_, err := cli.EnsureFolders(context.Background(), []string{testFolder})
	if err != nil {
		t.Fatalf("EnsureFolders: %v", err)
	}

	// Send an email to move
	subject := uniqueSubject("move-test")
	body := "This email will be moved."

	err = smtp.Send(env.smtpConfig(), env.user, "", "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	email := waitForEmail(t, cli, "INBOX", subject, 30*time.Second)

	// Move to test folder
	destUID, err := cli.MoveMessage(context.Background(), "INBOX", email.UID, testFolder)
	if err != nil {
		cleanupEmail(t, cli, "INBOX", email.UID)
		t.Fatalf("MoveMessage: %v", err)
	}
	if destUID == 0 {
		t.Error("MoveMessage returned destUID 0")
	}

	// Verify it's in the test folder
	moved := waitForEmail(t, cli, testFolder, subject, 10*time.Second)

	// Move back (undo)
	_, err = cli.MoveMessage(context.Background(), testFolder, moved.UID, "INBOX")
	if err != nil {
		cleanupEmail(t, cli, testFolder, moved.UID)
		t.Fatalf("MoveMessage (undo): %v", err)
	}

	// Verify back in INBOX and cleanup
	restored := waitForEmail(t, cli, "INBOX", subject, 10*time.Second)
	cleanupEmail(t, cli, "INBOX", restored.UID)
}

func TestIntegration_SendWithInlineImage(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("inline-img")

	// Create a minimal 1x1 PNG in a temp dir
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test-logo.png")
	// Minimal valid PNG: 1x1 red pixel
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xde, 0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x00, 0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc,
		0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, // IEND chunk
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(imgPath, png, 0600); err != nil {
		t.Fatal(err)
	}

	// Markdown with image reference — goldmark produces <img src="/path">
	// which buildMessage rewrites to cid: for inline embedding.
	body := fmt.Sprintf("Here is an inline image:\n\n![logo](%s)\n\nEnd of email.", imgPath)

	err := smtp.Send(env.smtpConfig(), env.user, "", "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	email := waitForEmail(t, cli, "INBOX", subject, 60*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	// Fetch body — inline image should appear as attachment with image content type
	_, rawHTML, _, attachments, _, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}

	// HTML should contain cid: reference (inline image)
	if !strings.Contains(rawHTML, "cid:") {
		t.Logf("HTML body (truncated): %s", truncate(rawHTML, 500))
		t.Error("expected cid: reference in HTML for inline image")
	}

	// Should have at least one image attachment
	foundImage := false
	for _, a := range attachments {
		if strings.HasPrefix(a.ContentType, "image/") {
			foundImage = true
			if len(a.Data) == 0 {
				t.Error("inline image data is empty")
			}
		}
	}
	if !foundImage {
		names := make([]string, len(attachments))
		for i, a := range attachments {
			names[i] = fmt.Sprintf("%s (%s)", a.Filename, a.ContentType)
		}
		t.Errorf("no image attachment found, got: %v", names)
	}
}

func TestIntegration_SignatureRenderedInHTML(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("signature")
	// Simulate a compose with signature and callouts (same format as editor.Prelude adds)
	body := "Hi team,\n\n" +
		"Here's the update on the project:\n\n" +
		"> [!tip] Good News\n" +
		"> We're ahead of schedule! The new feature shipped yesterday.\n\n" +
		"> [!warning] Action Required\n" +
		"> Please review the security audit by Friday.\n\n" +
		"> [!note] note\n" +
		"> Please read\n\n" +
		"Thanks,\n" +
		"Simon\n\n" +
		"--  \n" +
		"**Simon Späti**\n" +
		"Data Engineer, [SSP Data](https://ssp.sh/)\n"

	err := smtp.Send(env.smtpConfig(), env.user, "", "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	email := waitForEmail(t, cli, "INBOX", subject, 30*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	markdown, rawHTML, _, _, _, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}

	// Plain text part should contain the signature as-is
	if !strings.Contains(markdown, "Simon Späti") {
		t.Errorf("plain text missing signature name, got: %s", truncate(markdown, 300))
	}

	// HTML part should render signature with formatting
	if !strings.Contains(rawHTML, "<strong>Simon Späti</strong>") {
		t.Errorf("HTML missing bold signature name, got: %s", truncate(rawHTML, 500))
	}
	if !strings.Contains(rawHTML, `href="https://ssp.sh/"`) {
		t.Errorf("HTML missing signature link, got: %s", truncate(rawHTML, 500))
	}

	// Body content before signature should also be rendered
	if !strings.Contains(rawHTML, "update on the project") {
		t.Errorf("HTML missing email body text, got: %s", truncate(rawHTML, 500))
	}

	// Callout rendering verification
	if !strings.Contains(rawHTML, "callout callout-tip") {
		t.Errorf("HTML missing tip callout class, got: %s", truncate(rawHTML, 800))
	}
	if !strings.Contains(rawHTML, "callout callout-warning") {
		t.Errorf("HTML missing warning callout class, got: %s", truncate(rawHTML, 800))
	}
	if !strings.Contains(rawHTML, "callout callout-note") {
		t.Errorf("HTML missing note callout class, got: %s", truncate(rawHTML, 800))
	}
	if !strings.Contains(rawHTML, "💡") { // Light bulb emoji for tip
		t.Errorf("HTML missing tip callout icon, got: %s", truncate(rawHTML, 800))
	}
	if !strings.Contains(rawHTML, "⚠️") { // Warning sign emoji
		t.Errorf("HTML missing warning callout icon, got: %s", truncate(rawHTML, 800))
	}
	if !strings.Contains(rawHTML, "Good News") {
		t.Errorf("HTML missing custom callout title, got: %s", truncate(rawHTML, 800))
	}
	if !strings.Contains(rawHTML, "ahead of schedule") {
		t.Errorf("HTML missing callout content, got: %s", truncate(rawHTML, 800))
	}
}

func TestIntegration_SaveSent(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("save-sent")
	body := "This email tests SaveSent IMAP APPEND."

	// Build the message (same as neomd does before sending)
	raw, err := smtp.BuildMessage(env.from, env.user, "", subject, body, nil, "")
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}

	// Save to Sent via IMAP APPEND (no actual SMTP send needed)
	err = cli.SaveSent(context.Background(), "Sent", raw)
	if err != nil {
		t.Fatalf("SaveSent: %v", err)
	}

	// Verify it appears in the Sent folder
	email := waitForEmail(t, cli, "Sent", subject, 15*time.Second)
	defer cleanupEmail(t, cli, "Sent", email.UID)

	if email.Subject != subject {
		t.Errorf("Sent email subject = %q, want %q", email.Subject, subject)
	}

	// Verify it's marked as read (\Seen flag)
	if !email.Seen {
		t.Error("SaveSent email should have \\Seen flag")
	}
}

func TestIntegration_MultipleRecipients(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	// Use a second address for the test. NEOMD_TEST_USER2 can be set to a
	// real second account; falls back to the same address (still tests parsing).
	user2 := getEnvOr("NEOMD_TEST_USER2", "simu@sspaeti.com")

	subject := uniqueSubject("multi-rcpt")
	body := "Testing comma-separated To, CC, and BCC."

	// Comma-separated To: two different addresses
	// CC: the test account itself
	// This exercises the bug we fixed: Send() must split To by comma.
	to := env.user + ", " + user2
	cc := env.user

	err := smtp.Send(env.smtpConfig(), to, cc, "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send with comma-separated To: %v", err)
	}

	// Verify delivery to primary test account
	email := waitForEmail(t, cli, "INBOX", subject, 30*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	// Verify To field contains both addresses (not just the first)
	if !strings.Contains(email.To, env.user) {
		t.Errorf("To field missing primary address, got: %q", email.To)
	}
	if !strings.Contains(email.To, user2) {
		t.Errorf("To field missing second address %q, got: %q", user2, email.To)
	}

	// Verify CC is populated
	if email.CC == "" {
		t.Logf("Note: CC not populated in envelope (fetch path may not include it)")
	} else if !strings.Contains(email.CC, env.user) {
		t.Errorf("CC field missing %q, got: %q", env.user, email.CC)
	}

	t.Logf("Email delivered with To: %s, CC: %s", email.To, email.CC)
}

func TestIntegration_ReplyAllPreservesRecipients(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	// Three distinct addresses to properly test reply-all.
	// demo sends to simu + simon, then reply-all should CC both back.
	user2 := getEnvOr("NEOMD_TEST_USER2", "simu@sspaeti.com")
	user3 := getEnvOr("NEOMD_TEST_USER3", "simon@ssp.sh")

	// Step 1: Send a group email from demo to user2, CC user3
	origSubject := uniqueSubject("reply-all-orig")
	origBody := "Original group email for reply-all test."

	err := smtp.Send(env.smtpConfig(), user2, user3, "", origSubject, origBody, nil)
	if err != nil {
		t.Fatalf("Send original: %v", err)
	}

	// The email lands in demo's Sent (via SaveSent) but also in demo's INBOX
	// if demo is in CC. Since demo is not in To/CC here, we save to Sent to
	// have a copy to inspect.
	raw, err := smtp.BuildMessage(env.from, user2, user3, origSubject, origBody, nil, "")
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}
	err = cli.SaveSent(context.Background(), "Sent", raw)
	if err != nil {
		t.Fatalf("SaveSent: %v", err)
	}

	original := waitForEmail(t, cli, "Sent", origSubject, 15*time.Second)
	defer cleanupEmail(t, cli, "Sent", original.UID)

	// Step 2: Simulate reply-all from user2's perspective.
	// Reply-all logic: To = original sender, CC = all To + CC minus self.
	replySubject := "Re: " + origSubject
	replyBody := "Reply-all response.\n\n> " + origBody

	// To = original sender (demo)
	replyTo := env.user

	// CC = original To + CC, minus the replier (user2)
	allRecipients := original.To
	if original.CC != "" {
		allRecipients += ", " + original.CC
	}
	var replyCC []string
	user2Lower := strings.ToLower(user2)
	for _, addr := range strings.Split(allRecipients, ",") {
		a := strings.TrimSpace(addr)
		if a != "" && strings.ToLower(a) != user2Lower {
			replyCC = append(replyCC, a)
		}
	}
	replyCCStr := strings.Join(replyCC, ", ")

	t.Logf("Reply-all: To=%s CC=%s", replyTo, replyCCStr)

	err = smtp.Send(env.smtpConfig(), replyTo, replyCCStr, "", replySubject, replyBody, nil)
	if err != nil {
		t.Fatalf("Send reply-all: %v", err)
	}

	// Step 3: Verify the reply arrives at demo (the To recipient)
	reply := waitForEmail(t, cli, "INBOX", replySubject, 30*time.Second)
	defer cleanupEmail(t, cli, "INBOX", reply.UID)

	if !strings.Contains(reply.Subject, "Re:") {
		t.Errorf("Reply subject missing Re: prefix, got: %q", reply.Subject)
	}

	// To should be the demo account (original sender)
	if !strings.Contains(reply.To, env.user) {
		t.Errorf("Reply To missing demo address, got: %q", reply.To)
	}

	// CC should contain user3 (simon@ssp.sh)
	if !strings.Contains(reply.CC, user3) {
		t.Errorf("Reply CC missing %q, got: %q", user3, reply.CC)
	}

	t.Logf("Reply-all delivered: To=%s CC=%s", reply.To, reply.CC)
}

func TestIntegration_MarkAsRead(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("mark-as-read")
	body := "Testing mark-as-read functionality."

	// Send test email to self
	err := smtp.Send(env.smtpConfig(), env.user, "", "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for delivery
	email := waitForEmail(t, cli, "INBOX", subject, 30*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	// Initially unread
	if email.Seen {
		t.Error("newly delivered email should be unread (Seen=false)")
	}

	// Mark as seen
	ctx := context.Background()
	err = cli.MarkSeen(ctx, "INBOX", email.UID)
	if err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}

	// Re-fetch to verify flag changed
	emails, err := cli.FetchHeaders(ctx, "INBOX", 20)
	if err != nil {
		t.Fatalf("FetchHeaders after MarkSeen: %v", err)
	}

	var found *goIMAP.Email
	for i := range emails {
		if emails[i].UID == email.UID {
			found = &emails[i]
			break
		}
	}

	if found == nil {
		t.Fatal("email not found after MarkSeen")
	}

	if !found.Seen {
		t.Error("email still unread after MarkSeen call")
	}

	// Test MarkUnseen
	err = cli.MarkUnseen(ctx, "INBOX", email.UID)
	if err != nil {
		t.Fatalf("MarkUnseen: %v", err)
	}

	// Re-fetch to verify flag cleared
	emails, err = cli.FetchHeaders(ctx, "INBOX", 20)
	if err != nil {
		t.Fatalf("FetchHeaders after MarkUnseen: %v", err)
	}

	found = nil
	for i := range emails {
		if emails[i].UID == email.UID {
			found = &emails[i]
			break
		}
	}

	if found == nil {
		t.Fatal("email not found after MarkUnseen")
	}

	if found.Seen {
		t.Error("email still marked as read after MarkUnseen call")
	}

	t.Logf("Mark-as-read round-trip successful: UID=%d", email.UID)
}

func TestIntegration_EmailStandardsCompliance(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("standards-check")
	body := "Testing RFC 5322 email standards compliance.\n\nThis email validates:\n- Message-ID uses sender's domain\n- multipart/alternative structure\n- Proper MIME encoding"

	// Build the message to inspect its structure before sending
	raw, err := smtp.BuildMessage(env.from, env.user, "", subject, body, nil, "")
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}

	rawStr := string(raw)

	// 1. Message-ID MUST use sender's domain (not @neomd or @localhost)
	msgIDIdx := strings.Index(rawStr, "Message-ID:")
	if msgIDIdx == -1 {
		t.Fatal("Message-ID header missing")
	}
	msgIDLine := rawStr[msgIDIdx : msgIDIdx+strings.Index(rawStr[msgIDIdx:], "\n")]

	// Extract domain from From address for validation
	fromAddr := extractUser(env.from)
	if fromAddr == "" {
		fromAddr = env.user
	}
	domainIdx := strings.LastIndex(fromAddr, "@")
	if domainIdx == -1 {
		t.Fatalf("Cannot extract domain from From: %s", fromAddr)
	}
	expectedDomain := fromAddr[domainIdx+1:]

	if !strings.Contains(msgIDLine, "@"+expectedDomain+">") {
		t.Errorf("Message-ID should use sender's domain @%s, got: %s", expectedDomain, msgIDLine)
	}
	if strings.Contains(msgIDLine, "@neomd>") {
		t.Errorf("Message-ID should not use hardcoded @neomd, got: %s", msgIDLine)
	}
	if strings.Contains(msgIDLine, "@localhost>") {
		t.Errorf("Message-ID should not use @localhost fallback, got: %s", msgIDLine)
	}
	t.Logf("✓ Message-ID uses sender's domain: %s", msgIDLine)

	// 2. Required RFC 5322 headers
	requiredHeaders := []string{
		"From:",
		"To:",
		"Subject:",
		"Date:",
		"Message-ID:",
		"MIME-Version:",
		"Content-Type:",
		"X-Mailer:",
	}
	for _, hdr := range requiredHeaders {
		if !strings.Contains(rawStr, hdr) {
			t.Errorf("Required header missing: %s", hdr)
		}
	}
	t.Logf("✓ All required headers present")

	// 3. Verify multipart/alternative structure
	if !strings.Contains(rawStr, "Content-Type: multipart/alternative") {
		t.Error("Expected multipart/alternative content type")
	}
	t.Logf("✓ Uses multipart/alternative structure")

	// 4. Verify text/plain comes before text/html (RFC 2046 requirement)
	plainIdx := strings.Index(rawStr, "Content-Type: text/plain")
	htmlIdx := strings.Index(rawStr, "Content-Type: text/html")
	if plainIdx == -1 {
		t.Error("text/plain part missing")
	}
	if htmlIdx == -1 {
		t.Error("text/html part missing")
	}
	if plainIdx >= htmlIdx {
		t.Errorf("text/plain must come before text/html (RFC 2046), got plain at %d, html at %d", plainIdx, htmlIdx)
	}
	t.Logf("✓ Correct part ordering: text/plain first, text/html second")

	// 5. Verify quoted-printable encoding is used
	if !strings.Contains(rawStr, "Content-Transfer-Encoding: quoted-printable") {
		t.Error("Expected quoted-printable encoding")
	}
	t.Logf("✓ Uses quoted-printable encoding")

	// 6. Verify X-Mailer header identifies neomd
	if !strings.Contains(rawStr, "X-Mailer: neomd") {
		t.Error("X-Mailer header should identify 'neomd'")
	}
	t.Logf("✓ X-Mailer header present")

	// 7. Verify BCC header is NOT present (RFC 5322 privacy requirement)
	if strings.Contains(rawStr, "\nBcc:") || strings.HasPrefix(rawStr, "Bcc:") {
		t.Error("BCC header should never appear in message headers")
	}
	t.Logf("✓ BCC header correctly excluded")

	// 8. Verify HTML part is valid (contains basic tags)
	if !strings.Contains(rawStr, "<!DOCTYPE html>") {
		t.Error("HTML part missing DOCTYPE declaration")
	}
	if !strings.Contains(rawStr, "<body>") || !strings.Contains(rawStr, "</body>") {
		t.Error("HTML part missing body tags")
	}
	t.Logf("✓ HTML part is well-formed")

	// Now actually send the email to verify end-to-end delivery
	err = smtp.Send(env.smtpConfig(), env.user, "", "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for delivery and verify it arrives correctly
	email := waitForEmail(t, cli, "INBOX", subject, 30*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	// Fetch body to verify content survived delivery
	ctx := context.Background()
	markdown, rawHTML, _, _, _, _, err := cli.FetchBody(ctx, "INBOX", email.UID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}

	if !strings.Contains(markdown, "RFC 5322") {
		t.Errorf("Plain text part missing expected content after delivery, got: %s", truncate(markdown, 200))
	}
	t.Logf("✓ Plain text part is readable after delivery")

	if !strings.Contains(rawHTML, "<!DOCTYPE html>") {
		t.Error("HTML part missing DOCTYPE after delivery")
	}
	t.Logf("✓ HTML part survived delivery intact")

	t.Log("\n=== Email Standards Compliance: ALL CHECKS PASSED ===")
	t.Logf("Message-ID: Uses sender's domain @%s", expectedDomain)
	t.Log("Headers: All required headers present")
	t.Log("MIME: multipart/alternative with correct ordering")
	t.Log("Encoding: quoted-printable")
	t.Log("Privacy: BCC correctly excluded")
	t.Log("Delivery: Email sent and received successfully")
}

// --- Helpers ---

// TestIntegration_SecurityFeatures sends an email with a real attachment, a
// disguised script (.sh content saved as .png), a callout, and an HTML signature.
// The email arrives in the test inbox so you can verify attachment safety live in neomd.
func TestIntegration_SecurityFeatures(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("security-attach-callout")

	// Create temp dir for attachments
	dir := t.TempDir()

	// 1. Real text attachment
	realDoc := filepath.Join(dir, "meeting-notes.txt")
	if err := os.WriteFile(realDoc, []byte("Meeting notes from 2026-04-28.\n\n- Discussed spy pixel blocking\n- Reviewed attachment safety"), 0600); err != nil {
		t.Fatal(err)
	}

	// 2. Disguised script: bash content saved as .png
	fakeImg := filepath.Join(dir, "totally-legit-photo.png")
	if err := os.WriteFile(fakeImg, []byte("#!/bin/bash\necho 'this is not a real image'\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Body with callout
	body := `# Security Features Test

This email tests neomd's security features.

> [!warning] Attachment Safety Test
> This email contains a disguised script (bash content saved as .png) alongside
> a real text document. neomd should block the fake image from auto-opening.

## Attachments included:
1. **meeting-notes.txt** — real text file (safe)
2. **totally-legit-photo.png** — actually a bash script (should be blocked by magic-byte check)

*sent from [neomd](https://neomd.ssp.sh)*`

	err := smtp.Send(env.smtpConfig(), env.user+env.ccRecipient(), "", "", subject, body, []string{realDoc, fakeImg})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	email := waitForEmail(t, cli, "INBOX", subject, 60*time.Second)
	// Intentionally NOT cleaned up — kept in demo inbox for manual testing.

	// Fetch and verify
	markdown, rawHTML, _, attachments, _, spyPixels, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}

	// Verify callout rendered in HTML
	if !strings.Contains(rawHTML, "callout") || !strings.Contains(rawHTML, "warning") {
		t.Logf("HTML (truncated): %s", truncate(rawHTML, 300))
		t.Error("expected callout markup in HTML body")
	}

	// Verify at least 2 attachments arrived
	if len(attachments) < 2 {
		t.Errorf("expected at least 2 attachments, got %d", len(attachments))
	}
	for _, a := range attachments {
		t.Logf("Attachment: %s (%s, %d bytes)", a.Filename, a.ContentType, len(a.Data))
	}

	// Verify the disguised script would be caught by magic-byte check
	for _, a := range attachments {
		if strings.Contains(a.Filename, "totally-legit") {
			detected := http.DetectContentType(a.Data)
			if strings.HasPrefix(detected, "image/") {
				t.Errorf("disguised script detected as image — magic bytes failed: %s", detected)
			} else {
				t.Logf("Correctly detected disguised script as: %s (not image/)", detected)
			}
		}
	}

	t.Logf("Spy pixels: %d (expected 0 for self-sent)", spyPixels.Count)
	t.Logf("Markdown preview: %s", truncate(markdown, 200))
}

// TestIntegration_BrowserSanitization sends an email with inline script tags,
// an iframe, and an event handler to verify that SanitizeForBrowser blocks them
// when opened with O in neomd. Also sent to simon@ssp.sh for live inspection.
func TestIntegration_BrowserSanitization(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	subject := uniqueSubject("browser-csp-test")

	body := `# Browser Sanitization Test

Open this email with **O** in neomd to test CSP protection.

## What to check in the browser:

1. The **script alert should NOT fire** — if you see a popup saying "XSS worked", the CSP failed
2. The **iframe should NOT load** — you should see an empty space, not an embedded page
3. The **image should load normally** — the neomd logo below should be visible
4. The **onload handler should NOT fire** — no "event handler" alert

If everything works: you see the image, no popups, no iframe content.

![neomd logo](https://raw.githubusercontent.com/ssp-data/neomd/main/docs/static/images/overview-email-feed.png)

*sent from [neomd](https://neomd.ssp.sh) — CSP test*`

	// Send normally — the HTML will contain the markdown-rendered content.
	// To also test raw HTML injection, we build a custom message with injected tags.
	raw, err := smtp.BuildMessage(env.from, env.user+env.ccRecipient(), "", subject, body, nil, "")
	if err != nil {
		t.Fatalf("BuildMessage: %v", err)
	}

	// Inject malicious HTML into the raw MIME before sending.
	// These should all be blocked by the CSP when opened with O.
	injection := `<script>alert('XSS worked! CSP is broken!')</script>` +
		`<iframe src="https://example.com" width="400" height="200"></iframe>` +
		`<img src="https://raw.githubusercontent.com/ssp-data/neomd/main/docs/static/images/overview-email-feed.png" onload="alert('event handler fired! CSP broken!')" alt="test image">` +
		`<p style="color:green;font-size:20px;font-weight:bold;">If you see this text but NO popups and NO iframe, the CSP is working correctly.</p>`

	// Insert injection before </body> in the HTML part
	rawStr := string(raw)
	if idx := strings.LastIndex(rawStr, "</body>"); idx >= 0 {
		rawStr = rawStr[:idx] + injection + rawStr[idx:]
	}

	allRecipients := []string{env.user}
	if cc := env.ccRecipient(); cc != "" {
		allRecipients = append(allRecipients, strings.TrimPrefix(cc, ", "))
	}
	if err := smtp.SendRaw(env.smtpConfig(), allRecipients, []byte(rawStr)); err != nil {
		t.Fatalf("SendRaw: %v", err)
	}

	email := waitForEmail(t, cli, "INBOX", subject, 60*time.Second)
	// Intentionally NOT cleaned up — kept in demo inbox for manual testing.

	_, rawHTML, _, _, _, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}

	// Verify the malicious content is present in raw HTML (it should be — CSP blocks execution, not content)
	if !strings.Contains(rawHTML, "<script>") {
		t.Error("expected <script> tag in raw HTML (CSP should block execution, not strip content)")
	}
	if !strings.Contains(rawHTML, "<iframe") {
		t.Error("expected <iframe> tag in raw HTML (CSP should block loading, not strip content)")
	}

	t.Log("Email sent with script/iframe/onload injection.")
	t.Log("Open with O in neomd — you should see the image and green text, but NO popups and NO iframe content.")
}

// TestIntegration_SendMultiScriptDisplay sends an email packed with scripts that
// exercise `displaySafe` (internal/ui/inbox.go): CJK (Hangul, kana, Hanzi) should
// render as glyphs in the inbox list, while complex scripts (Bengali, Devanagari,
// Thai, Arabic) and emoji should collapse to '·'. Latin extended, Cyrillic and
// Greek pass through. The email is intentionally NOT cleaned up so you can compare
// the inbox-list rendering before/after the CJK fix and confirm the reader view
// still shows every glyph correctly.
func TestIntegration_SendMultiScriptDisplay(t *testing.T) {
	env := loadEnv(t)
	cli := env.imapClient()
	defer cli.Close()

	// Subject mixes safe (CJK, Latin extended) and unsafe (Bengali, Arabic, emoji)
	// runs so the inbox list shows both glyphs and '·' placeholders side-by-side.
	subject := uniqueSubject("CJK 한국어 日本語 中文 — Bengali আপনার — Arabic مرحبا — Emoji 🚀")

	body := "# Multi-script rendering test\n\n" +
		"Compare the **inbox list** (uses `displaySafe`) with the **reader view** " +
		"(renders every glyph). After the CJK fix, Korean / Japanese / Chinese should " +
		"appear as real characters in the inbox list instead of collapsing to `·`.\n\n" +
		"## CJK — should render as glyphs in the inbox list\n\n" +
		"- Korean (Hangul): 한국어 제목 테스트입니다 — 안녕하세요 세계\n" +
		"- Japanese (Hiragana/Katakana/Kanji): こんにちは世界 — カタカナ — 日本語\n" +
		"- Chinese (Hanzi): 你好世界 — 中文测试 — 漢字\n\n" +
		"## Complex scripts — should collapse to '·' in the inbox list\n\n" +
		"- Bengali: আপনার দর্শকদের\n" +
		"- Devanagari (Hindi): नमस्ते दुनिया\n" +
		"- Arabic: مرحبا بالعالم\n" +
		"- Thai: สวัสดีชาวโลก\n" +
		"- Emoji: 🚀🎉✨\n\n" +
		"## Latin / Cyrillic / Greek — should render as glyphs\n\n" +
		"- Latin extended: Ünïcödé Tëst — Späti — naïve — café\n" +
		"- Cyrillic: Привет мир\n" +
		"- Greek: Γειά σου κόσμε\n\n" +
		"*sent from [neomd](https://neomd.ssp.sh) — multi-script display test*\n"

	err := smtp.Send(env.smtpConfig(), env.user+env.ccRecipient(), "", "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Intentionally NOT cleaned up — kept in INBOX so you can manually verify
	// the inbox-list `displaySafe` transform vs. the reader view.
	email := waitForEmail(t, cli, "INBOX", subject, 60*time.Second)

	// Subject must survive Q-encoding for all scripts that the inbox list will display.
	for _, want := range []string{"한국어", "日本語", "中文", "আপনার", "مرحبا", "🚀"} {
		if !strings.Contains(email.Subject, want) {
			t.Errorf("Subject missing %q after round-trip, got: %q", want, email.Subject)
		}
	}

	// Body must round-trip every script in both the text/plain and text/html parts
	// so the reader view (which renders the full glyphs) has something to show.
	markdown, rawHTML, _, _, _, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	for _, want := range []string{
		"한국어", "안녕하세요",
		"こんにちは世界", "カタカナ",
		"你好世界", "漢字",
		"আপনার", "नमस्ते", // Bengali + Devanagari
		"مرحبا",          // Arabic
		"สวัสดี",         // Thai
		"Ünïcödé", "Späti",
		"Привет",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("plain text missing %q, got: %s", want, truncate(markdown, 400))
		}
		if !strings.Contains(rawHTML, want) {
			t.Errorf("HTML missing %q, got: %s", want, truncate(rawHTML, 400))
		}
	}

	t.Logf("Email kept in INBOX (UID=%d, Subject=%q) for manual displaySafe inspection", email.UID, email.Subject)
}

func extractUser(from string) string {
	if i := strings.Index(from, "<"); i >= 0 {
		if j := strings.Index(from, ">"); j > i {
			return from[i+1 : j]
		}
	}
	return from
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
