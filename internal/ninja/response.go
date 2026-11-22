package ninja

import (
	"runtime"
	"strings"

	"trestle/internal/plan"
)

func useArgumentFile(command plan.Command) bool {
	if command.Response == "" {
		return false
	}
	limit := 64 * 1024
	if runtime.GOOS == "windows" {
		limit = 7 * 1024
	}
	length := len(command.Exe) + 2
	for _, arg := range command.Args {
		length += len(arg) + 3 + strings.Count(arg, `"`)
	}
	return length >= limit
}

func responseContent(command plan.Command) string {
	quoted := make([]string, 0, len(command.Args))
	gnu := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	for _, arg := range command.Args {
		if command.Response == plan.ResponseMSVC {
			quoted = append(quoted, quoteMSVCResponse(arg))
		} else {
			quoted = append(quoted, `"`+gnu.Replace(arg)+`"`)
		}
	}
	// This is a Ninja variable value. Spaces and colons here are literal.
	return strings.ReplaceAll(strings.Join(quoted, " "), "$", "$$")
}

func quoteMSVCResponse(arg string) string {
	var result strings.Builder
	result.WriteByte('"')
	backslashes := 0
	for i := range len(arg) {
		switch arg[i] {
		case '\\':
			backslashes++
		case '"':
			result.WriteString(strings.Repeat(`\`, 2*backslashes+1))
			result.WriteByte('"')
			backslashes = 0
		default:
			result.WriteString(strings.Repeat(`\`, backslashes))
			result.WriteByte(arg[i])
			backslashes = 0
		}
	}
	result.WriteString(strings.Repeat(`\`, 2*backslashes))
	result.WriteByte('"')
	return result.String()
}
