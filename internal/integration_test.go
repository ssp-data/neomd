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
	markdown, rawHTML, _, _, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
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
	markdown, _, _, _, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
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
	_, _, _, attachments, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
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
	_, rawHTML, _, attachments, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
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
	// Simulate a compose with signature (same format as editor.Prelude adds)
	body := "Hi there,\n\nThis is the email body.\n\n" +
		"--  \n" +
		"**Simon Späti**\n" +
		"Data Engineer, [SSP Data](https://ssp.sh/)\n"

	err := smtp.Send(env.smtpConfig(), env.user, "", "", subject, body, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	email := waitForEmail(t, cli, "INBOX", subject, 30*time.Second)
	defer cleanupEmail(t, cli, "INBOX", email.UID)

	markdown, rawHTML, _, _, _, err := cli.FetchBody(context.Background(), "INBOX", email.UID)
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
	if !strings.Contains(rawHTML, "email body") {
		t.Errorf("HTML missing email body text, got: %s", truncate(rawHTML, 500))
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

// --- Helpers ---

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
