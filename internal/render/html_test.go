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
	expected := "💡 Good News\nWe're ahead of schedule!\n"
	got := FormatCalloutsForPlainText(input)
	if got != expected {
		t.Errorf("FormatCalloutsForPlainText with title:\nwant: %q\ngot:  %q", expected, got)
	}
}

func TestFormatCalloutsForPlainText_NoTitle(t *testing.T) {
	input := "> [!note]\n> This is a note\n"
	expected := "📘 Note\nThis is a note\n"
	got := FormatCalloutsForPlainText(input)
	if got != expected {
		t.Errorf("FormatCalloutsForPlainText without title:\nwant: %q\ngot:  %q", expected, got)
	}
}

func TestFormatCalloutsForPlainText_MultipleCallouts(t *testing.T) {
	input := "> [!warning] Action Required\n> Please review by Friday.\n\n> [!note]\n> Please read\n"
	expected := "⚠️ Action Required\nPlease review by Friday.\n\n📘 Note\nPlease read\n"
	got := FormatCalloutsForPlainText(input)
	if got != expected {
		t.Errorf("FormatCalloutsForPlainText with multiple callouts:\nwant: %q\ngot:  %q", expected, got)
	}
}

func TestFormatCalloutsForPlainText_NoSpaceAfterArrow(t *testing.T) {
	input := ">[!tip] Title\n>Content here\n"
	got := FormatCalloutsForPlainText(input)
	if !strings.Contains(got, "💡 Title") {
		t.Errorf("FormatCalloutsForPlainText should handle >[!type] without space:\ngot: %q", got)
	}
	if !strings.Contains(got, "Content here") {
		t.Errorf("FormatCalloutsForPlainText should unquote content:\ngot: %q", got)
	}
	// Should NOT contain > markers
	if strings.Contains(got, ">") {
		t.Errorf("FormatCalloutsForPlainText should remove blockquote markers:\ngot: %q", got)
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

	// Should preserve regular text and non-callout blockquotes
	if !strings.Contains(got, "Regular text") {
		t.Error("should preserve regular text")
	}
	if !strings.Contains(got, "> Regular blockquote") {
		t.Error("should preserve regular blockquotes")
	}
	// Should format the callout (no blockquote marker)
	if !strings.Contains(got, "📘 Callout") {
		t.Error("should format callout syntax")
	}
	if !strings.Contains(got, "With content") {
		t.Error("should include callout content")
	}
}

func TestFormatCalloutsForPlainText_MultiParagraphCallout(t *testing.T) {
	input := "> [!tip] Title\n> First paragraph\n> \n> Second paragraph\n"
	got := FormatCalloutsForPlainText(input)

	if !strings.Contains(got, "💡 Title") {
		t.Errorf("should have emoji title, got: %q", got)
	}
	if !strings.Contains(got, "First paragraph") {
		t.Errorf("should have first paragraph, got: %q", got)
	}
	if !strings.Contains(got, "Second paragraph") {
		t.Errorf("should have second paragraph, got: %q", got)
	}
	// Should not have > markers
	lines := strings.Split(got, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) != "" && strings.HasPrefix(strings.TrimSpace(line), ">") {
			t.Errorf("should not have > markers in callout content, got line: %q", line)
		}
	}
}

// The plain-text alternative must never carry local file paths (they expose
// the sender's home directory and are meaningless to the recipient) nor raw
// cid: markdown. Images become a short placeholder built from the alt text
// or, failing that, the file name.
func TestImagePlaceholdersForPlainText(t *testing.T) {
	in := "Hi\n\n![](</home/someone/secret dir/pic.png>)\n\n![diagram](/home/someone/d.png)\n\n> ![shot.png](cid:abc@example)\n\n![](https://example.com/a/b.png?x=1)\n\nbye"
	got := ImagePlaceholdersForPlainText(in)
	for _, want := range []string{"[Image: pic.png]", "[Image: diagram]", "> [Image: shot.png]", "[Image: b.png]", "Hi", "bye"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, leak := range []string{"/home/", "cid:", "https://", "![", "secret"} {
		if strings.Contains(got, leak) {
			t.Errorf("plain text leaks %q:\n%s", leak, got)
		}
	}
	if got := ImagePlaceholdersForPlainText("no images here"); got != "no images here" {
		t.Errorf("text without images changed: %q", got)
	}
}

// Received HTML is transcoded to UTF-8 before it reaches the browser, but the
// original <meta charset> (Outlook: Windows-1252) still sits in the document,
// so the browser decoded UTF-8 bytes as Windows-1252 ("Späti" → "SpÃ¤ti").
// SanitizeForBrowser must replace any charset declaration with UTF-8.
func TestSanitizeForBrowser_ForcesUTF8Charset(t *testing.T) {
	in := `<html><head><meta http-equiv="Content-Type" content="text/html; charset=Windows-1252"><meta charset="iso-8859-1"><title>x</title></head><body>Zoë Späti</body></html>`
	got := SanitizeForBrowser(in)
	lower := strings.ToLower(got)
	if strings.Count(lower, `<meta charset="utf-8">`) != 1 {
		t.Errorf("want exactly one utf-8 charset meta, got:\n%s", got)
	}
	if strings.Contains(lower, "windows-1252") || strings.Contains(lower, "iso-8859-1") {
		t.Errorf("stale charset declaration survived:\n%s", got)
	}
	if !strings.Contains(got, browserCSP) || !strings.Contains(got, "Zoë Späti") {
		t.Errorf("CSP or body lost:\n%s", got)
	}
	// The utf-8 meta must come before any other head content so it wins.
	if strings.Index(lower, `<meta charset="utf-8">`) > strings.Index(lower, "<title>") {
		t.Errorf("utf-8 meta must precede other head elements:\n%s", got)
	}

	// No <head>: still declares UTF-8 and the CSP.
	got = SanitizeForBrowser("<p>plain ascii</p>")
	if !strings.Contains(strings.ToLower(got), `<meta charset="utf-8">`) || !strings.Contains(got, browserCSP) || !strings.Contains(got, "<p>plain ascii</p>") {
		t.Errorf("headless html not handled:\n%s", got)
	}

	// Our own template (already UTF-8 + CSP) is returned untouched.
	own, _ := ToHTML("hi")
	if SanitizeForBrowser(own) != own {
		t.Error("own template should pass through unchanged")
	}
}

func TestToHTML_ImageSizeTitleBecomesWidthHeight(t *testing.T) {
	out, err := ToHTML(`![Logo](https://example.org/logo.png "70x70")` + "\n\n" +
		`![H](https://example.org/h.png "x30")` + "\n\n" +
		`![Plain](https://example.org/plain.png)` + "\n\n" +
		`![Real](https://example.org/t.png "a real title")`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`src="https://example.org/logo.png" alt="Logo" width="70" height="70"`,
		`src="https://example.org/h.png" alt="H" height="30"`,
		`src="https://example.org/plain.png" alt="Plain">`,
		`title="a real title"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `title="70x70"`) || strings.Contains(out, `title="x30"`) {
		t.Errorf("size marker title leaked into HTML:\n%s", out)
	}
	if got := ImagePlaceholdersForPlainText(`![](https://example.org/a.png "70x70")`); got != "[Image: a.png]" {
		t.Errorf("plain placeholder with size title = %q", got)
	}
}
