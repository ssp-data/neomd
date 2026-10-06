package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/sspaeti/neomd/internal/imap"
)

// quoteDepthOf returns the number of leading '>' markers on a markdown line.
func quoteDepthOf(line string) int {
	n := 0
	for {
		line = strings.TrimLeft(line, " ")
		if !strings.HasPrefix(line, ">") {
			return n
		}
		n++
		line = line[1:]
	}
}

func maxQuoteDepthOf(md string) int {
	max := 0
	for _, l := range strings.Split(md, "\n") {
		if d := quoteDepthOf(l); d > max {
			max = d
		}
	}
	return max
}

// A long reply chain quoted 33 levels deep (Gmail "> > > > …") made glamour
// take ~2.3 s to render a 50 KB body: its blockquote cost grows superlinearly
// with nesting. The reader caps the displayed depth; the markdown body used
// for reply/forward/editor is untouched.
func TestCapQuoteDepth_LimitsNesting(t *testing.T) {
	md := "top\n\n> one\n>\n> > two\n> >\n> > > three\n> > >\n> > > > four\n> > > >\n> > > > > five\n"
	got := capQuoteDepth(md, 3)
	if d := maxQuoteDepthOf(got); d != 3 {
		t.Fatalf("max depth after cap = %d, want 3:\n%s", d, got)
	}
	for _, want := range []string{"top", "one", "two", "three", "four", "five"} {
		if !strings.Contains(got, want) {
			t.Errorf("text %q lost after capping:\n%s", want, got)
		}
	}
}

func TestCapQuoteDepth_ShallowBodyUnchanged(t *testing.T) {
	md := "hello\n\n> quoted\n> > nested\n\n```\n> > > > > inside a fence stays\n```\n"
	if got := capQuoteDepth(md, 3); got != md {
		t.Fatalf("shallow body must round-trip unchanged:\n%s", got)
	}
}

// Two adjacent lines whose original depths both exceed the cap must not merge
// into one paragraph once they share the same capped prefix: a blank quoted
// line keeps them apart.
func TestCapQuoteDepth_DepthChangeKeepsParagraphBreak(t *testing.T) {
	md := "> > > > four\n> > > > > five\n"
	got := capQuoteDepth(md, 3)
	want := "> > > four\n> > >\n> > > five\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestCapQuoteDepth_FencedCodeUntouched(t *testing.T) {
	md := "```\n> > > > > > raw\n```\n> > > > > > quoted\n"
	got := capQuoteDepth(md, 3)
	if !strings.Contains(got, "> > > > > > raw") {
		t.Errorf("fenced code must be untouched:\n%s", got)
	}
	if strings.Contains(got, "> > > > > > quoted") {
		t.Errorf("quote outside fence must be capped:\n%s", got)
	}
}

// The reader renders a deep reply chain in well under a second: 650 lines
// nested up to 33 deep took 2.3 s uncapped on the reference machine.
func TestReader_DeepQuoteChainRendersFast(t *testing.T) {
	var b strings.Builder
	para := "Thanks, sounds good — let me check the calendar and get back to you with a few slots that work on our side.\n"
	for depth := 33; depth >= 0; depth-- {
		prefix := strings.Repeat("> ", depth)
		for i := 0; i < 20; i++ {
			b.WriteString(prefix + para)
		}
		b.WriteString(strings.TrimRight(prefix, " ") + "\n")
	}
	body := b.String()
	vp := newReader(180, 50)
	e := &imap.Email{Subject: "Re: deep", From: "a@example.com", To: "b@example.com"}
	start := time.Now()
	if err := loadEmailIntoReader(&vp, e, body, nil, imap.SpyPixelInfo{}, nil, "dark", 180); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > 1500*time.Millisecond {
		t.Fatalf("deep quote chain took %v to render; cap not applied?", el)
	}
	if !strings.Contains(vp.View(), "Thanks, sounds good") {
		t.Fatalf("rendered body lost its text")
	}
}
