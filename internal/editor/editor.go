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
	"time"
)

// Command builds the $EDITOR invocation for args (usually the temp file).
// $EDITOR may carry arguments — Omarchy ships EDITOR="omarchy-launch-editor
// --inline", others use "code --wait" or "nvim -u ~/mail.lua" — so the value
// is split on whitespace. Values containing shell syntax (quotes, $, …) are
// handed to sh -c with the args appended, the way git runs GIT_EDITOR.
// Empty $EDITOR falls back to nvim. Stdio is wired to the terminal.
func Command(args ...string) *exec.Cmd {
	ed := strings.TrimSpace(os.Getenv("EDITOR"))
	if ed == "" {
		ed = "nvim"
	}
	var cmd *exec.Cmd
	if strings.ContainsAny(ed, "\"'\\$`|&;<>()*?[]#~") {
		cmd = exec.Command("sh", append([]string{"-c", ed + ` "$@"`, "editor"}, args...)...)
	} else {
		fields := strings.Fields(ed)
		cmd = exec.Command(fields[0], append(fields[1:], args...)...)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

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

	cmd := Command(tmpPath)
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

	cmd := Command("-R", tmpPath)
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

// buildQuotedReply builds the quoted "wrote:" section used in replies and
// reactions. When originalDate is non-zero the attribution reads
// "wrote on 28 Sep 2026 at 13:29:"; a zero date falls back to plain "wrote:".
func buildQuotedReply(originalFrom string, originalDate time.Time, originalBody string) string {
	wrote := "wrote:"
	if !originalDate.IsZero() {
		wrote = "wrote on " + originalDate.Format("2 Jan 2006 at 15:04") + ":"
	}
	return fmt.Sprintf("---\n\n> **%s** %s\n>\n%s\n\n---\n\n",
		originalFrom, wrote, quoteLines(originalBody))
}

// ReplyPrelude builds a quote block for replies. cc and from may be empty.
func ReplyPrelude(to, cc, subject, from, originalFrom string, originalDate time.Time, originalBody string) string {
	return Prelude(to, cc, "", from, subject, "") + buildQuotedReply(originalFrom, originalDate, originalBody)
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
func ReactionBody(emoji, fromName, originalFrom string, originalDate time.Time, originalBody string) string {
	quoted := buildQuotedReply(originalFrom, originalDate, originalBody)
	return fmt.Sprintf("%s\n\n_%s reacted via [neomd](https://neomd.ssp.sh)_\n\n%s", emoji, fromName, quoted)
}

// ParseHeaders scans raw editor content for # [neomd: key: value] lines and
// returns the extracted to, cc, bcc, from, subject values and the remaining body
// (with header lines stripped). Any field not found is returned as "".
func ParseHeaders(raw string) (to, cc, bcc, from, subject, body string) {
	lines := splitLines(raw)
	var kept []string
	inFence := false
	var fenceChar byte
	var fenceLen int
	for _, line := range lines {
		if inFence {
			kept = append(kept, line)
			if closesMarkdownFence(line, fenceChar, fenceLen) {
				inFence = false
			}
			continue
		}
		if c, n, ok := markdownFenceStart(line); ok {
			inFence = true
			fenceChar = c
			fenceLen = n
			kept = append(kept, line)
			continue
		}
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

// markdownFenceStart recognizes a CommonMark fenced code block opening: up to
// three leading spaces, then at least three backticks or tildes.
func markdownFenceStart(line string) (byte, int, bool) {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	if i > 3 || i == len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0, false
	}
	c := line[i]
	j := i
	for j < len(line) && line[j] == c {
		j++
	}
	return c, j - i, j-i >= 3
}

// closesMarkdownFence reports whether line is a closing fence for an open
// fence: the same character, at least as long as the opening fence, and no
// trailing content other than whitespace.
func closesMarkdownFence(line string, c byte, minLen int) bool {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	if i > 3 || i == len(line) || line[i] != c {
		return false
	}
	j := i
	for j < len(line) && line[j] == c {
		j++
	}
	return j-i >= minLen && strings.TrimSpace(line[j:]) == ""
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
