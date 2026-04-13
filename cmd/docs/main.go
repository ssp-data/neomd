// docs regenerates the keybindings section in docs/content/docs/keybindings.md from the
// single source of truth in internal/ui/keys.go.
//
// Usage: go run ./cmd/docs   (or: make docs)
//
// It replaces the block between:
//   <!-- keybindings-start -->
//   <!-- keybindings-end -->
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/sspaeti/neomd/internal/ui"
)

const (
	readmePath = "docs/content/docs/keybindings.md"
	startTag   = "<!-- keybindings-start -->"
	endTag     = "<!-- keybindings-end -->"
)

func main() {
	md := buildMarkdown()

	readme, err := os.ReadFile(readmePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", readmePath, err)
		os.Exit(1)
	}

	content := string(readme)
	start := strings.Index(content, startTag)
	end := strings.Index(content, endTag)
	if start == -1 || end == -1 || end <= start {
		fmt.Fprintf(os.Stderr, "markers %q / %q not found in %s\n", startTag, endTag, readmePath)
		os.Exit(1)
	}

	updated := content[:start+len(startTag)] + "\n" + md + content[end:]
	if err := os.WriteFile(readmePath, []byte(updated), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", readmePath, err)
		os.Exit(1)
	}
	fmt.Printf("Updated keybindings in %s (%d sections)\n", readmePath, len(ui.HelpSections))
}

func buildMarkdown() string {
	var b strings.Builder
	for _, sec := range ui.HelpSections {
		b.WriteString("\n### " + sec.Title + "\n\n")
		b.WriteString("| Key | Action |\n|-----|--------|\n")
		for _, row := range sec.Rows {
			b.WriteString("| `" + row[0] + "` | " + row[1] + " |\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
