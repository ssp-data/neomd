package listmonk

import (
	"strings"
)

// Trigger maps a virtual email address to Listmonk list IDs.
// TemplateID, when non-zero, overrides Listmonk's default template for
// campaigns created from this trigger.
type Trigger struct {
	Address    string
	ListIDs    []int
	TemplateID int
}

// ResolveListIDs returns the combined list IDs for all trigger addresses
// that match any recipient in the To field. Returns nil if no match.
func ResolveListIDs(triggers []Trigger, toField string) []int {
	seen := make(map[int]bool)
	var ids []int
	for _, addr := range splitAddrs(toField) {
		for _, t := range triggers {
			if strings.EqualFold(addr, t.Address) {
				for _, id := range t.ListIDs {
					if !seen[id] {
						seen[id] = true
						ids = append(ids, id)
					}
				}
			}
		}
	}
	return ids
}

// ResolveTemplateID returns the first non-zero template ID from triggers
// matching any recipient in the To field. Returns 0 if no override.
func ResolveTemplateID(triggers []Trigger, toField string) int {
	for _, addr := range splitAddrs(toField) {
		for _, t := range triggers {
			if strings.EqualFold(addr, t.Address) && t.TemplateID != 0 {
				return t.TemplateID
			}
		}
	}
	return 0
}

// IsTriggerAddress returns true if any address in toField matches a trigger.
func IsTriggerAddress(triggers []Trigger, toField string) bool {
	return len(ResolveListIDs(triggers, toField)) > 0
}

// splitAddrs splits a comma-separated To field and extracts bare email addresses.
func splitAddrs(field string) []string {
	var addrs []string
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Handle "Name <addr>" format
		if idx := strings.LastIndex(part, "<"); idx >= 0 {
			if end := strings.Index(part[idx:], ">"); end >= 0 {
				part = part[idx+1 : idx+end]
			}
		}
		addrs = append(addrs, strings.TrimSpace(part))
	}
	return addrs
}
