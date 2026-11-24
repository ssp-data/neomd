package smtp

import (
	"regexp"
	"strings"
)

// HTMLSignaturePlaceholder marks where the HTML signature goes. The compose
// layer replaces the first [html-signature] / [sig] marker line with this
// token on its own paragraph (blank line before and after); BuildMessage then
// swaps the rendered paragraph for the signature and strips the token from the
// plain-text part. Letters only, so goldmark renders it verbatim as <p>TOKEN</p>.
const HTMLSignaturePlaceholder = "NEOMDHTMLSIGNATUREPLACEHOLDER"

var placeholderParaRe = regexp.MustCompile(`<p[^>]*>\s*` + HTMLSignaturePlaceholder + `\s*</p>\n?`)

// InjectHTMLSignature puts sig into htmlBody. If the body carries the
// placeholder paragraph, the first one is replaced by sig (the signature lands
// where the user put the marker, e.g. above the quoted history of a reply).
// Otherwise sig goes before </body> as before. Any leftover placeholder is
// always removed, also when sig is empty, so the token never reaches a
// recipient.
func InjectHTMLSignature(htmlBody, sig string) string {
	placed := false
	if sig != "" {
		if loc := placeholderParaRe.FindStringIndex(htmlBody); loc != nil {
			htmlBody = htmlBody[:loc[0]] + sig + "\n" + htmlBody[loc[1]:]
			placed = true
		}
	}
	htmlBody = placeholderParaRe.ReplaceAllString(htmlBody, "")
	htmlBody = strings.ReplaceAll(htmlBody, HTMLSignaturePlaceholder, "")
	if sig != "" && !placed {
		if idx := strings.LastIndex(htmlBody, "</body>"); idx >= 0 {
			htmlBody = htmlBody[:idx] + "\n" + sig + "\n" + htmlBody[idx:]
		}
	}
	return htmlBody
}

// StripHTMLSignaturePlaceholder removes placeholder lines from text together
// with the one blank line the compose layer inserted on each side, restoring
// the text exactly as typed without the marker line.
func StripHTMLSignaturePlaceholder(text string) string {
	if !strings.Contains(text, HTMLSignaturePlaceholder) {
		return text
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != HTMLSignaturePlaceholder {
			out = append(out, lines[i])
			continue
		}
		if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) == "" {
			out = out[:n-1]
		}
		if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
			i++
		}
	}
	return strings.ReplaceAll(strings.Join(out, "\n"), HTMLSignaturePlaceholder, "")
}
