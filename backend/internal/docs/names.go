package docs

import (
	"strings"
	"unicode"
)

const maxNameLen = 120

// SanitizeName makes a client-supplied file name safe to use as the last segment of an S3 key
// (backend-spec 8.3). Only ASCII letters, digits, dot, dash, underscore and space survive;
// anything else becomes "_". Path components are dropped, leading dots and spaces are
// stripped, the result is at most 120 characters with the extension kept, and an empty
// result becomes "file".
func SanitizeName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		if allowedNameRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := strings.TrimLeft(b.String(), ". ")
	s = strings.TrimRight(s, " ")
	if len(s) > maxNameLen {
		ext := ""
		if i := strings.LastIndexByte(s, '.'); i > 0 && len(s)-i <= 10 {
			ext = s[i:]
		}
		s = strings.TrimRight(s[:maxNameLen-len(ext)], ". ") + ext
	}
	if s == "" {
		return "file"
	}
	return s
}

func allowedNameRune(r rune) bool {
	return r == '.' || r == '-' || r == '_' || r == ' ' ||
		r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// DisplayName is the name shown to users: trimmed, control characters removed, capped at
// 120 runes, "file" if nothing is left. Unlike SanitizeName it keeps Unicode; it is never
// used in an S3 key.
func DisplayName(name string) string {
	s := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if r := []rune(s); len(r) > maxNameLen {
		s = strings.TrimSpace(string(r[:maxNameLen]))
	}
	if s == "" {
		return "file"
	}
	return s
}
