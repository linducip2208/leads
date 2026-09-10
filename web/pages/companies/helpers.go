package companies

import "strings"

func trimPrefixLib(s, p string) string { return strings.TrimPrefix(s, p) }

func indexOfLib(s, sub string) int { return strings.Index(s, sub) }

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
