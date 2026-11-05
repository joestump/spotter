// Governing: SPEC-0018 REQ "Tag Normalization"
package tags

import (
	"strings"
	"unicode"
)

// Normalize returns the canonical lowercase, whitespace-trimmed,
// whitespace-collapsed form of a tag name.
func Normalize(name string) string {
	fields := strings.FieldsFunc(strings.TrimSpace(name), unicode.IsSpace)
	return strings.ToLower(strings.Join(fields, " "))
}
