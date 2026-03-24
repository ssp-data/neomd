// Package editor opens an external editor ($EDITOR, defaulting to nvim)
// for composing email bodies in Markdown.
package editor

import (
	"fmt"
	"os"
	"os/exec"
)

// Compose writes prelude to a temp .md file, opens $EDITOR, waits for it
// to close, reads and returns the file contents.
// The caller is responsible for suspending/resuming the bubbletea program
// around this call (via tea.ExecProcess or tea.Suspend/Resume).
func Compose(prelude string) (string, error) {
	f, err := os.CreateTemp("", "neomd-*.md")
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

// Prelude builds the comment header shown at the top of a new compose buffer.
func Prelude(to, subject string) string {
	return fmt.Sprintf("<!-- To: %s -->\n<!-- Subject: %s -->\n\n", to, subject)
}

// ReplyPrelude builds a quote block for replies.
func ReplyPrelude(to, subject, originalFrom, originalBody string) string {
	return fmt.Sprintf(
		"<!-- To: %s -->\n<!-- Subject: Re: %s -->\n\n---\n\n> **%s** wrote:\n>\n%s\n\n---\n\n",
		to, subject, originalFrom, quoteLines(originalBody),
	)
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
