package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sspaeti/neomd/internal/contacts"
)

// Typing a person's NAME in To/Cc/Bcc must suggest their address as
// "Name <addr>" — the contacts store (harvested + [contacts] file) is a
// suggestion source, not just the bare screener-list addresses.
func TestComposeSuggestions_MatchContactName(t *testing.T) {
	s := contacts.Load(filepath.Join(t.TempDir(), "contacts"))
	s.HarvestField("Max Muster <max@muster.example>")

	c := newComposeModel()
	c.contacts = s
	c.knownAddrs = []string{"max@muster.example", "other@x.io"} // screener list: bare, no names
	c.step = stepTo

	for _, query := range []string{"max", "muster", "Max Mus", "muster.example"} {
		c.to.SetValue(query)
		c.updateSuggestions()
		want := "Max Muster <max@muster.example>"
		found := false
		for _, sug := range c.suggestions {
			if sug == want {
				found = true
			}
			if sug == "max@muster.example" {
				t.Errorf("query %q: bare duplicate of a named contact suggested: %v", query, c.suggestions)
			}
		}
		if !found {
			t.Errorf("query %q: want suggestion %q, got %v", query, want, c.suggestions)
		}
	}

	// Unknown-name screener addresses still suggested bare.
	c.to.SetValue("other")
	c.updateSuggestions()
	if len(c.suggestions) != 1 || c.suggestions[0] != "other@x.io" {
		t.Errorf("bare screener suggestion broken: %v", c.suggestions)
	}
}

// Nil contacts store (fresh install, empty cache) must not panic and must
// keep plain screener-list completion working.
func TestComposeSuggestions_NilStoreSafe(t *testing.T) {
	c := newComposeModel()
	c.contacts = nil
	c.knownAddrs = []string{"louise@client.example"}
	c.step = stepTo
	c.to.SetValue("louise")
	c.updateSuggestions()
	if len(c.suggestions) != 1 || c.suggestions[0] != "louise@client.example" {
		t.Errorf("suggestions = %v", c.suggestions)
	}
}

// Accepting a suggestion after a comma must keep the earlier recipients and
// insert the decorated form — which the send path already handles (Decorate
// skips parts containing '<', collectRcptTo extracts the bare address).
func TestComposeSuggestions_MultiRecipientInsert(t *testing.T) {
	s := contacts.Load(filepath.Join(t.TempDir(), "contacts"))
	s.HarvestField("Max Muster <max@muster.example>")

	c := newComposeModel()
	c.contacts = s
	c.step = stepTo
	c.to.SetValue("first@x.io, muster")
	c.updateSuggestions()
	if len(c.suggestions) == 0 {
		t.Fatal("no suggestion for name after comma")
	}
	c.suggestI = 0
	c.applySuggestion()
	got := c.to.Value()
	if !strings.HasPrefix(got, "first@x.io,") || !strings.Contains(got, "Max Muster <max@muster.example>") {
		t.Errorf("after applySuggestion: %q", got)
	}
}

// Sending must persist user-typed "Name <addr>" recipients into the contacts
// store so autocomplete knows them next time (no contacts file needed).
func TestHarvestTypedRecipients(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "contacts")
	s := contacts.Load(cachePath)
	m := Model{contacts: s}
	m.harvestTypedRecipients(
		"Max Muster <max@muster.example>, bare@x.io",
		"Cc Person <cc@x.io>",
		"Hidden Name <hidden@x.io>",
	)
	if got := s.Name("max@muster.example"); got != "Max Muster" {
		t.Errorf("To name not harvested: %q", got)
	}
	if got := s.Name("cc@x.io"); got != "Cc Person" {
		t.Errorf("Cc name not harvested: %q", got)
	}
	if got := s.Name("hidden@x.io"); got != "Hidden Name" {
		t.Errorf("Bcc name not harvested (cache is local-only): %q", got)
	}
	if got := s.Name("bare@x.io"); got != "" {
		t.Errorf("bare address must not gain a name: %q", got)
	}

	// Nil store: must be a no-op, not a panic.
	nilModel := Model{}
	nilModel.harvestTypedRecipients("A <a@b.io>")

	// harvestTypedRecipients persists via safeGo(SaveIfDirty) — wait for
	// the write before TempDir cleanup runs.
	waitForFile(t, cachePath)
}
