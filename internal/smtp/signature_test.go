package smtp

import (
	"strings"
	"testing"
)

const testSig = `<p class="sig">SIG</p>`

func htmlDoc(inner string) string {
	return "<html><head></head><body>\n" + inner + "\n</body>\n</html>"
}

func TestInjectHTMLSignature_AtPlaceholder(t *testing.T) {
	in := htmlDoc("<p>Hi</p>\n<p>" + HTMLSignaturePlaceholder + "</p>\n<hr>\n<blockquote><p>quote</p></blockquote>")
	out := InjectHTMLSignature(in, testSig)
	hi, sig, q := strings.Index(out, "Hi"), strings.Index(out, "SIG"), strings.Index(out, "quote")
	if !(hi < sig && sig < q) {
		t.Errorf("signature not between text and quote:\n%s", out)
	}
	if strings.Contains(out, HTMLSignaturePlaceholder) || strings.Count(out, "SIG") != 1 {
		t.Errorf("placeholder leaked or signature duplicated:\n%s", out)
	}
}

// No placeholder: identical to the previous behaviour (before </body>).
func TestInjectHTMLSignature_NoPlaceholderAppendsBeforeBody(t *testing.T) {
	in := htmlDoc("<p>Hi</p>")
	want := strings.Replace(in, "</body>", "\n"+testSig+"\n</body>", 1)
	if got := InjectHTMLSignature(in, testSig); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// Signature disabled or not configured: placeholder still removed, rest untouched.
func TestInjectHTMLSignature_EmptySigStripsPlaceholder(t *testing.T) {
	in := htmlDoc("<p>Hi</p>\n<p>" + HTMLSignaturePlaceholder + "</p>\n<p>Bye</p>")
	out := InjectHTMLSignature(in, "")
	if strings.Contains(out, HTMLSignaturePlaceholder) {
		t.Errorf("placeholder leaked:\n%s", out)
	}
	if out != htmlDoc("<p>Hi</p>\n<p>Bye</p>") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if plain := htmlDoc("<p>Hi</p>"); InjectHTMLSignature(plain, "") != plain {
		t.Error("no placeholder and no sig must be a no-op")
	}
}

func TestInjectHTMLSignature_OnlyFirstPlaceholderUsed(t *testing.T) {
	p := "<p>" + HTMLSignaturePlaceholder + "</p>\n"
	out := InjectHTMLSignature(htmlDoc("<p>A</p>\n"+p+"<p>B</p>\n"+p), testSig)
	if strings.Count(out, "SIG") != 1 || strings.Contains(out, HTMLSignaturePlaceholder) {
		t.Errorf("want one signature, no placeholder:\n%s", out)
	}
	if strings.Index(out, "SIG") > strings.Index(out, "<p>B</p>") {
		t.Errorf("signature should be at the first placeholder:\n%s", out)
	}
}

func TestStripHTMLSignaturePlaceholder(t *testing.T) {
	P := HTMLSignaturePlaceholder
	cases := map[string]string{
		"Hi\nBye":                       "Hi\nBye",
		"Hi\n\n" + P + "\n\nBye":        "Hi\nBye",
		"\n" + P + "\n\nHi":             "Hi",
		"Hi\n\n" + P + "\n":             "Hi",
		"Hi\n\n\n" + P + "\n\n\nBye":    "Hi\n\n\nBye", // user's own blank lines survive
		"Hi\n\n" + P + "\n\n---\n\n> q": "Hi\n---\n\n> q",
	}
	for in, want := range cases {
		if got := StripHTMLSignaturePlaceholder(in); got != want {
			t.Errorf("Strip(%q) = %q, want %q", in, got, want)
		}
	}
}

// End to end through BuildMessage: placeholder in markdown → signature at that
// spot in text/html, neither placeholder nor signature in text/plain.
func TestBuildMessage_SignatureAtPlaceholder(t *testing.T) {
	md := "Thanks!\n\n" + HTMLSignaturePlaceholder + "\n\n---\n\n> **You** wrote:\n> question\n"
	raw, err := BuildMessage("Me <me@example.com>", "you@example.com", "", "Re: x", md, nil, testSig)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, HTMLSignaturePlaceholder) {
		t.Fatal("placeholder leaked into the message")
	}
	if strings.Count(s, "SIG") != 1 {
		t.Errorf("signature should appear exactly once (HTML part only)")
	}
	thanks := strings.LastIndex(s, "Thanks!") // last = HTML part (plain comes first)
	sig, q := strings.Index(s, "SIG"), strings.LastIndex(s, "question")
	if !(thanks < sig && sig < q) {
		t.Errorf("signature not between reply and quote in HTML part: thanks=%d sig=%d quote=%d", thanks, sig, q)
	}
}

// No signature configured but placeholder present (marker typed, sig empty):
// nothing internal may be delivered.
func TestBuildMessage_PlaceholderWithoutSignature(t *testing.T) {
	raw, err := BuildMessage("me@example.com", "you@example.com", "", "x", "Hi\n\n"+HTMLSignaturePlaceholder+"\n\nBye", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), HTMLSignaturePlaceholder) {
		t.Fatal("placeholder leaked without a signature")
	}
}
