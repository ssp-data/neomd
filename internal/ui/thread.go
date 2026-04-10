package ui

import (
	"regexp"
	"sort"
	"strings"

	"github.com/sspaeti/neomd/internal/imap"
)

// replyPrefixRe matches common reply/forward prefixes.
var replyPrefixRe = regexp.MustCompile(`(?i)^(re|fwd?|fw|aw|sv|vs|ref|rif)\s*(\[\d+\])?\s*:\s*`)

// compareEmails returns -1 if a < b, 0 if a == b, 1 if a > b.
// Comparison uses the specified sortField with deterministic tie-breakers:
// 1. Primary sort field (from/subject/size/date)
// 2. Date (newest first) if primary keys match and sortField != "date"
// 3. UID for fully deterministic ordering
func compareEmails(a, b imap.Email, sortField string) int {
	// Primary sort comparison
	var cmp int // -1 = a < b, 0 = equal, 1 = a > b
	switch sortField {
	case "from":
		aFrom, bFrom := strings.ToLower(a.From), strings.ToLower(b.From)
		if aFrom < bFrom {
			cmp = -1
		} else if aFrom > bFrom {
			cmp = 1
		}
	case "subject":
		aSubj, bSubj := strings.ToLower(a.Subject), strings.ToLower(b.Subject)
		if aSubj < bSubj {
			cmp = -1
		} else if aSubj > bSubj {
			cmp = 1
		}
	case "size":
		if a.Size < b.Size {
			cmp = -1
		} else if a.Size > b.Size {
			cmp = 1
		}
	default: // "date"
		if a.Date.Before(b.Date) {
			cmp = -1
		} else if a.Date.After(b.Date) {
			cmp = 1
		}
	}

	// Tie-breaker 1: date (newest first) if primary keys are equal
	if cmp == 0 && sortField != "date" {
		if a.Date.After(b.Date) {
			cmp = -1
		} else if a.Date.Before(b.Date) {
			cmp = 1
		}
	}

	// Tie-breaker 2: UID for deterministic ordering
	if cmp == 0 {
		if a.UID < b.UID {
			cmp = -1
		} else if a.UID > b.UID {
			cmp = 1
		}
	}

	return cmp
}

// hasReplyPrefix returns true if the subject starts with a reply/forward prefix.
func hasReplyPrefix(subject string) bool {
	return replyPrefixRe.MatchString(strings.TrimSpace(subject))
}

// normalizeSubject strips all reply/forward prefixes and lowercases.
func normalizeSubject(subject string) string {
	s := strings.TrimSpace(subject)
	for {
		stripped := replyPrefixRe.ReplaceAllString(s, "")
		stripped = strings.TrimSpace(stripped)
		if stripped == s {
			break
		}
		s = stripped
	}
	return strings.ToLower(s)
}

// threadedEmail pairs an email with its tree-drawing prefix for the inbox list.
type threadedEmail struct {
	email        imap.Email
	threadPrefix string // "│" = continuation, "╰" = root, "" = not threaded
}

// threadEmails groups and reorders emails into threaded display order.
//
// Threading uses two strategies:
//  1. InReplyTo/MessageID chain — direct header links (most reliable)
//  2. Subject fallback — ONLY for emails whose subject has a reply prefix
//     (Re:, AW:, Fwd:, etc.). Emails without a prefix are never grouped by
//     subject, so recurring notifications/invoices with identical subjects
//     stay separate.
//
// Each thread is sorted internally by date ascending (oldest = root at bottom,
// newest replies on top). Threads are sorted by the user's chosen sort field
// and order (sortField: "date", "from", "subject", "size"; sortReverse: true/false).
func threadEmails(emails []imap.Email, sortField string, sortReverse bool) []threadedEmail {
	if len(emails) == 0 {
		return nil
	}

	// Union-find for grouping connected emails.
	parent := make([]int, len(emails))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	// Phase 1: Connect by InReplyTo -> MessageID chain.
	byMsgID := make(map[string]int, len(emails))
	for i := range emails {
		if id := emails[i].MessageID; id != "" {
			byMsgID[id] = i
		}
	}
	for i := range emails {
		if replyTo := emails[i].InReplyTo; replyTo != "" {
			if j, ok := byMsgID[replyTo]; ok {
				union(i, j)
			}
		}
	}

	// Phase 2: Subject fallback — only for emails with a reply/forward prefix.
	// This catches threads where the InReplyTo points to an email in another
	// folder (e.g. your reply in Sent). We group by normalized subject, but
	// ONLY if at least one email in the pair has a reply prefix.
	byNormSubj := make(map[string][]int) // normalized subject -> indices
	for i := range emails {
		subj := normalizeSubject(emails[i].Subject)
		if subj == "" {
			continue
		}
		byNormSubj[subj] = append(byNormSubj[subj], i)
	}
	for _, indices := range byNormSubj {
		if len(indices) < 2 {
			continue
		}
		// Only group if at least one email has a reply prefix.
		hasReply := false
		for _, idx := range indices {
			if hasReplyPrefix(emails[idx].Subject) {
				hasReply = true
				break
			}
		}
		if !hasReply {
			continue
		}
		// Connect all emails with this normalized subject.
		first := indices[0]
		for _, idx := range indices[1:] {
			union(first, idx)
		}
	}

	// Collect threads.
	threadMap := make(map[int][]int) // root -> indices
	for i := range emails {
		root := find(i)
		threadMap[root] = append(threadMap[root], i)
	}

	// Sort each thread internally by date ascending (oldest first = root).
	type thread struct {
		indices   []int
		newestIdx int
	}
	var threads []thread
	for _, indices := range threadMap {
		sort.Slice(indices, func(a, b int) bool {
			return emails[indices[a]].Date.Before(emails[indices[b]].Date)
		})
		newest := indices[len(indices)-1]
		threads = append(threads, thread{indices: indices, newestIdx: newest})
	}

	// Sort threads by user's chosen sort field and order.
	// We use the newest email in each thread as the representative for sorting.
	sort.SliceStable(threads, func(i, j int) bool {
		a := emails[threads[i].newestIdx]
		b := emails[threads[j].newestIdx]
		cmp := compareEmails(a, b, sortField)

		// Apply sort direction
		if sortReverse {
			return cmp > 0 // descending: a > b means a comes first
		}
		return cmp < 0 // ascending: a < b means a comes first
	})

	// Build output with thread connector lines.
	// │ = continuation (more thread below), ╰ = root/last in thread.
	result := make([]threadedEmail, 0, len(emails))
	for _, t := range threads {
		n := len(t.indices)
		if n == 1 {
			result = append(result, threadedEmail{email: emails[t.indices[0]]})
			continue
		}
		// Reverse order: newest first, oldest (root) last.
		for k := n - 1; k >= 0; k-- {
			prefix := "│"
			if k == 0 {
				prefix = "╰" // root = bottom of thread
			}
			result = append(result, threadedEmail{
				email:        emails[t.indices[k]],
				threadPrefix: prefix,
			})
		}
	}

	return result
}
