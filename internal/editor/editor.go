// Package editor opens an external editor ($EDITOR, defaulting to nvim)
// for composing email bodies in Markdown.
package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// tempDir returns /tmp/neomd/, creating it if needed.
func tempDir() string {
	dir := filepath.Join(os.TempDir(), "neomd")
	os.MkdirAll(dir, 0700) //nolint
	return dir
}

var neomdHeaderRe = regexp.MustCompile(`^# \[neomd: (\w+): (.*)\]$`)

// Compose writes prelude to a temp .md file, opens $EDITOR, waits for it
// to close, reads and returns the file contents.
// The caller is responsible for suspending/resuming the bubbletea program
// around this call (via tea.ExecProcess or tea.Suspend/Resume).
func Compose(prelude string) (string, error) {
	f, err := os.CreateTemp(tempDir(), "neomd-*.md")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)

	if _, err := f.WriteString(prelude); err != nil {
		f.Close()
		return "", fmt.Errorf("write prelude: %w", err)
	}
	f.Close()

	editorBin := os.Getenv("EDITOR")
	if editorBin == "" {
		editorBin = "nvim"
	}

	cmd := exec.Command(editorBin, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor exited: %w", err)
	}

	content, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("read composed file: %w", err)
	}
	return string(content), nil
}

// View writes content to a temp .md file and opens it with nvim -R (read-only).
// The caller is responsible for suspending/resuming the bubbletea program via
// tea.ExecProcess. Returns the command and the temp file path (caller removes it).
func View(content string) (*exec.Cmd, string, error) {
	f, err := os.CreateTemp(tempDir(), "neomd-read-*.md")
	if err != nil {
		return nil, "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return nil, "", fmt.Errorf("write file: %w", err)
	}
	f.Close()

	editorBin := os.Getenv("EDITOR")
	if editorBin == "" {
		editorBin = "nvim"
	}
	cmd := exec.Command(editorBin, "-R", tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, tmpPath, nil
}

// Prelude builds the header shown at the top of a new compose buffer.
// cc, bcc, and from may be empty. If signature is non-empty it is appended
// after a blank line separator.
func Prelude(to, cc, bcc, from, subject, signature string) string {
	s := fmt.Sprintf("# [neomd: to: %s]\n", to)
	if cc != "" {
		s += fmt.Sprintf("# [neomd: cc: %s]\n", cc)
	}
	if bcc != "" {
		s += fmt.Sprintf("# [neomd: bcc: %s]\n", bcc)
	}
	if from != "" {
		s += fmt.Sprintf("# [neomd: from: %s]\n", from)
	}
	s += fmt.Sprintf("# [neomd: subject: %s]\n\n", subject)
	if signature != "" {
		s += "\n\n--  \n" + signature + "\n"
	}
	return s
}

// ReplyPrelude builds a quote block for replies. cc and from may be empty.
// buildQuotedReply builds the quoted "wrote:" section used in replies and reactions.
func buildQuotedReply(originalFrom, originalBody string) string {
	return fmt.Sprintf("---\n\n> **%s** wrote:\n>\n%s\n\n---\n\n",
		originalFrom, quoteLines(originalBody))
}

func ReplyPrelude(to, cc, subject, from, originalFrom, originalBody string) string {
	return Prelude(to, cc, "", from, subject, "") + buildQuotedReply(originalFrom, originalBody)
}

// ForwardPrelude builds a quoted forward block. The To field is left empty for
// the user to fill in.
func ForwardPrelude(subject, from, originalFrom, originalDate, originalTo, originalBody string) string {
	if !strings.HasPrefix(strings.ToLower(subject), "fwd:") {
		subject = "Fwd: " + subject
	}
	s := Prelude("", "", "", from, subject, "")
	s += "---------- Forwarded message ----------\n"
	s += fmt.Sprintf("From: %s\n", originalFrom)
	s += fmt.Sprintf("Date: %s\n", originalDate)
	s += fmt.Sprintf("Subject: %s\n", subject)
	s += fmt.Sprintf("To: %s\n\n", originalTo)
	s += quoteLines(originalBody) + "\n"
	return s
}

// ReactionBody builds the markdown body for an emoji reaction.
// Returns markdown that will be used for both text/plain and text/html parts (same as regular replies).
// Includes the quoted original message using the same quoting logic as regular replies.
func ReactionBody(emoji, signature, originalFrom, originalBody string) string {
	quoted := buildQuotedReply(originalFrom, originalBody)
	sig := ""
	if signature != "" {
		sig = "\n\n--  \n" + signature
	}
	return fmt.Sprintf("%s%s\n\n%s", emoji, sig, quoted)
}

// ParseHeaders scans raw editor content for # [neomd: key: value] lines and
// returns the extracted to, cc, bcc, from, subject values and the remaining body
// (with header lines stripped). Any field not found is returned as "".
func ParseHeaders(raw string) (to, cc, bcc, from, subject, body string) {
	lines := splitLines(raw)
	var kept []string
	for _, line := range lines {
		if m := neomdHeaderRe.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			switch strings.ToLower(m[1]) {
			case "to":
				to = strings.TrimSpace(m[2])
			case "cc":
				cc = strings.TrimSpace(m[2])
			case "bcc":
				bcc = strings.TrimSpace(m[2])
			case "from":
				from = strings.TrimSpace(m[2])
			case "subject":
				subject = strings.TrimSpace(m[2])
			}
			continue
		}
		kept = append(kept, line)
	}
	body = strings.Join(kept, "\n")
	return
}

func quoteLines(body string) string {
	lines := ""
	for _, line := range splitLines(body) {
		lines += "> " + line + "\n"
	}
	return lines
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
