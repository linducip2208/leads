package components

import (
	"strconv"
	"strings"
)

func itoaLib(n int) string { return strconv.Itoa(n) }

func capitalizeLib(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
