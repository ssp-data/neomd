package render

import (
	"strings"
	"testing"
)

func TestToHTML_Bold(t *testing.T) {
	out, err := ToHTML("**bold** text")
	if err != nil {
		t.Fatalf("ToHTML returned error: %v", err)
	}
	if !strings.Contains(out, "<strong>bold</strong>") {
		t.Errorf("expected <strong>bold</strong> in output, got:\n%s", out)
	}
}

func TestToHTML_HTMLWrapper(t *testing.T) {
	out, err := ToHTML("hello")
	if err != nil {
		t.Fatalf("ToHTML returned error: %v", err)
	}
	if !strings.HasPrefix(out, "<!DOCTYPE html>") {
		t.Errorf("expected output to start with <!DOCTYPE html>, got:\n%.80s...", out)
	}
	if !strings.Contains(out, "<body>") {
		t.Errorf("expected <body> in output")
	}
}

func TestToHTML_GFMTable(t *testing.T) {
	md := "| A | B |\n|---|---|\n| 1 | 2 |\n"
	out, err := ToHTML(md)
	if err != nil {
		t.Fatalf("ToHTML returned error: %v", err)
	}
	if !strings.Contains(out, "<table>") {
		t.Errorf("expected <table> in output, got:\n%s", out)
	}
}

func TestToHTML_CodeBlock(t *testing.T) {
	md := "```go\nfmt.Println(\"hi\")\n```\n"
	out, err := ToHTML(md)
	if err != nil {
		t.Fatalf("ToHTML returned error: %v", err)
	}
	if !strings.Contains(out, "<pre>") {
		t.Errorf("expected <pre> in output, got:\n%s", out)
	}
}

func TestToHTML_Empty(t *testing.T) {
	out, err := ToHTML("")
	if err != nil {
		t.Fatalf("ToHTML returned error for empty input: %v", err)
	}
	if !strings.HasPrefix(out, "<!DOCTYPE html>") {
		t.Errorf("expected DOCTYPE even for empty input, got:\n%.80s...", out)
	}
}

func TestToANSI_Smoke(t *testing.T) {
	_, err := ToANSI("# Hello\n\nSome **bold** text.", "dark", 80)
	if err != nil {
		t.Fatalf("ToANSI returned error: %v", err)
	}
}

func TestToHTML_Callout_Note(t *testing.T) {
	md := "> [!note]\n> This is a note callout\n"
	out, err := ToHTML(md)
	if err != nil {
		t.Fatalf("ToHTML returned error: %v", err)
	}
	// Print actual HTML for debugging
	t.Logf("Actual HTML output:\n%s", out)
	if !strings.Contains(out, "callout") {
		t.Errorf("expected 'callout' class in output, got:\n%s", out)
	}
	if !strings.Contains(out, "callout-note") {
		t.Errorf("expected 'callout-note' class in output, got:\n%s", out)
	}
	if !strings.Contains(out, "This is a note callout") {
		t.Errorf("expected callout content in output, got:\n%s", out)
	}
}

func TestToHTML_Callout_WithTitle(t *testing.T) {
	md := "> [!warning] Custom Warning Title\n> This is a warning\n"
	out, err := ToHTML(md)
	if err != nil {
		t.Fatalf("ToHTML returned error: %v", err)
	}
	if !strings.Contains(out, "callout-warning") {
		t.Errorf("expected 'callout-warning' class in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Custom Warning Title") {
		t.Errorf("expected custom title in output, got:\n%s", out)
	}
	if !strings.Contains(out, "This is a warning") {
		t.Errorf("expected callout content in output, got:\n%s", out)
	}
}

func TestToHTML_Callout_MultiParagraph(t *testing.T) {
	md := "> [!tip]\n> First paragraph\n> \n> Second paragraph\n"
	out, err := ToHTML(md)
	if err != nil {
		t.Fatalf("ToHTML returned error: %v", err)
	}
	if !strings.Contains(out, "callout-tip") {
		t.Errorf("expected 'callout-tip' class in output, got:\n%s", out)
	}
	if !strings.Contains(out, "First paragraph") {
		t.Errorf("expected first paragraph in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Second paragraph") {
		t.Errorf("expected second paragraph in output, got:\n%s", out)
	}
}

func TestToHTML_Callout_Types(t *testing.T) {
	tests := []struct {
		name      string
		callType  string
		wantClass string
	}{
		{"note", "note", "callout-note"},
		{"tip", "tip", "callout-tip"},
		{"important", "important", "callout-important"},
		{"warning", "warning", "callout-warning"},
		{"caution", "caution", "callout-caution"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := "> [!" + tt.callType + "]\n> Test content\n"
			out, err := ToHTML(md)
			if err != nil {
				t.Fatalf("ToHTML returned error: %v", err)
			}
			if !strings.Contains(out, tt.wantClass) {
				t.Errorf("expected '%s' class in output, got:\n%s", tt.wantClass, out)
			}
		})
	}
}

func TestToHTML_Callout_NoSpaceSyntax(t *testing.T) {
	// Test if >[!note] works without space after >
	md := ">[!note] No Space Test\n>This tests the syntax without space\n"
	out, err := ToHTML(md)
	if err != nil {
		t.Fatalf("ToHTML returned error: %v", err)
	}
	// Check if it rendered as callout or as regular blockquote
	if strings.Contains(out, "callout-note") {
		// Success: >[!note] (no space) DOES work as callout
		if !strings.Contains(out, "No Space Test") {
			t.Errorf("expected title in callout output, got:\n%s", out)
		}
		if !strings.Contains(out, "This tests the syntax without space") {
			t.Errorf("expected content in callout output, got:\n%s", out)
		}
	} else {
		// Failure: rendered as blockquote instead
		t.Errorf(">[!note] without space did not render as callout. Use '> [!note]' (with space) instead. Got:\n%s", out)
	}
}

func TestFormatCalloutsForPlainText_WithTitle(t *testing.T) {
	input := "> [!tip] Good News\n> We're ahead of schedule!\n"
	expected := "> 💡 Good News\n> We're ahead of schedule!\n"
	got := FormatCalloutsForPlainText(input)
	if got != expected {
		t.Errorf("FormatCalloutsForPlainText with title:\nwant: %q\ngot:  %q", expected, got)
	}
}

func TestFormatCalloutsForPlainText_NoTitle(t *testing.T) {
	input := "> [!note]\n> This is a note\n"
	expected := "> 📘 Note\n> This is a note\n"
	got := FormatCalloutsForPlainText(input)
	if got != expected {
		t.Errorf("FormatCalloutsForPlainText without title:\nwant: %q\ngot:  %q", expected, got)
	}
}

func TestFormatCalloutsForPlainText_MultipleCallouts(t *testing.T) {
	input := "> [!warning] Action Required\n> Please review by Friday.\n\n> [!note]\n> Please read\n"
	expected := "> ⚠️ Action Required\n> Please review by Friday.\n\n> 📘 Note\n> Please read\n"
	got := FormatCalloutsForPlainText(input)
	if got != expected {
		t.Errorf("FormatCalloutsForPlainText with multiple callouts:\nwant: %q\ngot:  %q", expected, got)
	}
}

func TestFormatCalloutsForPlainText_NoSpaceAfterArrow(t *testing.T) {
	input := ">[!tip] Title\n>Content here\n"
	// Should still match because regex handles both "> " and ">"
	got := FormatCalloutsForPlainText(input)
	if !strings.Contains(got, "💡 Title") {
		t.Errorf("FormatCalloutsForPlainText should handle >[!type] without space:\ngot: %q", got)
	}
}

func TestFormatCalloutsForPlainText_AllTypes(t *testing.T) {
	tests := []struct {
		callType string
		wantIcon string
	}{
		{"note", "📘"},
		{"tip", "💡"},
		{"warning", "⚠️"},
		{"danger", "🚨"},
		{"success", "✅"},
		{"info", "ℹ️"},
		{"question", "❓"},
		{"bug", "🐛"},
		{"example", "📝"},
	}

	for _, tt := range tests {
		t.Run(tt.callType, func(t *testing.T) {
			input := "> [!" + tt.callType + "] Title\n> Content\n"
			got := FormatCalloutsForPlainText(input)
			if !strings.Contains(got, tt.wantIcon) {
				t.Errorf("expected icon %s for type %s, got: %q", tt.wantIcon, tt.callType, got)
			}
		})
	}
}

func TestFormatCalloutsForPlainText_PreservesNonCallouts(t *testing.T) {
	input := "Regular text\n\n> Regular blockquote\n> without callout\n\n> [!note] Callout\n> With content\n"
	got := FormatCalloutsForPlainText(input)

	// Should preserve regular text and blockquotes
	if !strings.Contains(got, "Regular text") {
		t.Error("should preserve regular text")
	}
	if !strings.Contains(got, "> Regular blockquote") {
		t.Error("should preserve regular blockquotes")
	}
	// Should format the callout
	if !strings.Contains(got, "📘 Callout") {
		t.Error("should format callout syntax")
	}
}
