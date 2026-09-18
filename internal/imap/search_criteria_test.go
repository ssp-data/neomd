package imap

import (
	"testing"

	"github.com/emersion/go-imap/v2"
)

// collectHeaders walks an OR tree and returns every Key:Value leaf.
func collectHeaders(c *imap.SearchCriteria) []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, h := range c.Header {
		out = append(out, h.Key+":"+h.Value)
	}
	for _, pair := range c.Or {
		out = append(out, collectHeaders(&pair[0])...)
		out = append(out, collectHeaders(&pair[1])...)
	}
	return out
}

func TestOrCriteria(t *testing.T) {
	if orCriteria(nil) != nil {
		t.Error("orCriteria(nil) should be nil")
	}
	single := imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "A", Value: "1"}}}
	if got := orCriteria([]imap.SearchCriteria{single}); len(got.Or) != 0 || len(got.Header) != 1 {
		t.Errorf("single criterion should be returned as-is, got %+v", got)
	}
	three := []imap.SearchCriteria{
		{Header: []imap.SearchCriteriaHeaderField{{Key: "A", Value: "1"}}},
		{Header: []imap.SearchCriteriaHeaderField{{Key: "B", Value: "2"}}},
		{Header: []imap.SearchCriteriaHeaderField{{Key: "C", Value: "3"}}},
	}
	got := collectHeaders(orCriteria(three))
	if len(got) != 3 {
		t.Errorf("OR of 3 should keep 3 leaves, got %v", got)
	}
}

func TestMessageIDCriteria(t *testing.T) {
	if messageIDCriteria(nil) != nil {
		t.Error("empty ids should yield nil")
	}
	got := collectHeaders(messageIDCriteria([]string{"<a@x>", "b@y"}))
	want := map[string]bool{
		"Message-ID:a@x": true, "In-Reply-To:a@x": true,
		"Message-ID:b@y": true, "In-Reply-To:b@y": true,
	}
	if len(got) != 4 {
		t.Fatalf("want 4 leaves, got %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected leaf %q (angle brackets must be stripped)", g)
		}
	}
}
