package ninja

import "strings"

// EscapePath escapes the characters Ninja treats specially in a path, doubling
// "$" and escaping spaces and colons.
func EscapePath(value string) string {
	value = strings.ReplaceAll(value, "$", "$$")
	value = strings.ReplaceAll(value, " ", "$ ")
	value = strings.ReplaceAll(value, ":", "$:")
	return value
}

// Unescape reverses EscapePath by dropping the "$" that introduces each escaped
// character.
func Unescape(value string) string {
	var result strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '$' && i+1 < len(value) {
			i++
			result.WriteByte(value[i])
		} else {
			result.WriteByte(value[i])
		}
	}
	return result.String()
}
