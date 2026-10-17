package vcpkg

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PcFile is the parsed content of a pkg-config .pc file, holding the include and
// link flags as well as the public and private requirements it declares.
type PcFile struct {
	Name            string
	Cflags          []string
	Libs            []string
	LibsPrivate     []string
	Requires        []string
	RequiresPrivate []string
}

// ParsePC reads and parses a .pc file, expanding ${variable} references. It fails
// when a variable cannot be resolved rather than emitting a broken flag.
func ParsePC(path string) (PcFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return PcFile{}, err
	}
	defer file.Close()
	absolute, err := filepath.Abs(path)
	if err != nil {
		return PcFile{}, err
	}
	values := map[string]string{"pcfiledir": filepath.ToSlash(filepath.Dir(absolute))}
	fields := map[string]string{}
	var result PcFile
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, isVariable, ok := parsePCLine(scanner.Text())
		if !ok {
			continue
		}
		if isVariable {
			values[key] = value
			continue
		}
		fields[key] = value
	}
	if err := scanner.Err(); err != nil {
		return PcFile{}, err
	}
	for key, raw := range fields {
		value := expandPC(raw, values)
		if strings.Contains(value, "${") {
			return PcFile{}, fmt.Errorf("unresolved pkg-config variable in %s: %s", key, value)
		}
		applyPCField(&result, key, value)
	}
	if err := scanner.Err(); err != nil {
		return PcFile{}, err
	}
	return result, nil
}

// parsePCLine splits one .pc line into a key and value. A '=' assignment defines
// a variable and reports isVariable true; a ':' assignment defines a field.
// Blank lines, comments and lines without a separator report ok false.
func parsePCLine(line string) (key, value string, isVariable, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, false
	}
	separator := strings.IndexAny(line, "=:")
	if separator < 0 {
		return "", "", false, false
	}
	key = strings.TrimSpace(line[:separator])
	value = strings.TrimSpace(line[separator+1:])
	return key, value, line[separator] == '=', true
}

// applyPCField copies one expanded field value into the matching slot of a
// PcFile. Fields it does not recognise are ignored.
func applyPCField(result *PcFile, key, value string) {
	switch key {
	case "Name":
		result.Name = value
	case "Cflags":
		result.Cflags = splitPC(value)
	case "Libs":
		result.Libs = splitPC(value)
	case "Libs.private":
		result.LibsPrivate = splitPC(value)
	case "Requires":
		result.Requires = splitRequires(value)
	case "Requires.private":
		result.RequiresPrivate = splitRequires(value)
	}
}

func expandPC(value string, values map[string]string) string {
	for pass := 0; pass < 16; pass++ {
		changed := false
		for name, replacement := range values {
			token := "${" + name + "}"
			if strings.Contains(value, token) {
				value = strings.ReplaceAll(value, token, replacement)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return value
}

// splitPC splits a pkg-config flag value into fields, honouring single and
// double quotes, and rejoins a separated -I, -L or -D flag with its argument.
func splitPC(value string) []string {
	return joinPCFlagArguments(tokenizePC(value))
}

// tokenizePC splits a whitespace-separated pkg-config value into tokens,
// treating single and double quotes as grouping and dropping the quote
// characters. Both spaces and tabs separate tokens.
func tokenizePC(value string) []string {
	var fields []string
	var token strings.Builder
	var quote rune
	for _, char := range value {
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				token.WriteRune(char)
			}
		} else if char == '\'' || char == '"' {
			quote = char
		} else if char == ' ' || char == '\t' {
			if token.Len() > 0 {
				fields = append(fields, token.String())
				token.Reset()
			}
		} else {
			token.WriteRune(char)
		}
	}
	if token.Len() > 0 {
		fields = append(fields, token.String())
	}
	return fields
}

// joinPCFlagArguments attaches a separated argument to a preceding -I, -L or -D
// flag, so each returned field is a self-contained compiler or linker option.
func joinPCFlagArguments(fields []string) []string {
	var result []string
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if (field == "-I" || field == "-L" || field == "-D") && index+1 < len(fields) {
			index++
			field += fields[index]
		}
		result = append(result, field)
	}
	return result
}

func splitRequires(value string) []string {
	var result []string
	for _, part := range strings.Split(value, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) > 0 {
			result = append(result, fields[0])
		}
	}
	return result
}

// FindPC searches a layout's pkg-config directories for name.pc and returns the
// first match, or an error when the package has no metadata there.
func FindPC(layout Layout, name string) (string, error) {
	for _, dir := range layout.PkgConfigDirs {
		candidate := filepath.Join(dir, name+".pc")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("E_PACKAGE_METADATA_UNSUPPORTED: no pkg-config metadata for %s", name)
}
